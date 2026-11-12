/*
 * AeroLab ESP32 controller - application entry.
 *
 * STATUS: ESP-IDF 5.x code that has NEVER been compiled or flashed in this repository. The portable
 * logic it calls (controller, safety, protocol, journal, config store, command handler) is compiled and
 * tested on the host (firmware/esp32/test/host).
 *
 * Architecture rules enforced here:
 *  - The control task owns the pump. It reads inputs, ticks the controller, drives the output, and
 *    feeds the task watchdog. It NEVER waits on MQTT, Wi-Fi, flash, or a mutex (try-lock only).
 *  - Networking and the journal live in the net task. If it dies, irrigation continues.
 *  - Boot: outputs OFF -> self-check -> load atomic programme -> run only if valid.
 */
#include <stdio.h>
#include <string.h>
#include "actuators.h"
#include "board_profile.h"
#include "clock.h"
#include "command_handler.h"
#include "config_store.h"
#include "controller.h"
#include "esp_log.h"
#include "esp_random.h"
#include "esp_task_wdt.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "freertos/task.h"
#include "message_buffer.h"
#include "net.h"
#include "nvs_flash.h"
#include "nvs.h"
#include "protocol.h"
#include "sensors.h"

extern "C" {
extern const cs_backend_t CS_NVS_BACKEND;
extern const mb_backend_t MB_FS_BACKEND;
int journal_fs_mount(void);
}

static const char *TAG = "app";
static controller_t g_ctl;
static cs_t g_cs;
static ch_t g_ch;
static identity_t g_id;
static mb_t g_mb;
static mb_meta_t g_meta[400]; /* 360 snapshots (6 h) + headroom for critical events */
static SemaphoreHandle_t g_lock;
static int64_t g_seq;
static char g_last_cmd[CS_CMD_ID_LEN];

static void lock_fn(void *) { xSemaphoreTake(g_lock, portMAX_DELAY); }
static void unlock_fn(void *) { xSemaphoreGive(g_lock); }

static void snapshot_fn(state_report_t *out) {
    xSemaphoreTake(g_lock, pdMS_TO_TICKS(20));
    out->uptime_ms = (int64_t)clock_now_ms(); out->state = g_ctl.state; out->pump_on = g_ctl.pump_on;
    out->prog_version = g_ctl.has_program ? g_ctl.program_version : 0; out->faults = g_ctl.faults;
    out->interlocks_ok = !(g_ctl.faults & (FAULT_LOW_WATER | FAULT_LEVEL_SENSOR)); out->last_command_id = g_ch.last_command_id[0] ? g_ch.last_command_id : nullptr;
    xSemaphoreGive(g_lock);
}

static void emit_event(const ctl_event_t &e) {
    const char *type = "STATE_CHANGE";
    switch (e.type) {
    case CEV_FAULT_RAISED: type = "FAULT_RAISED"; break; case CEV_FAULT_CLEARED: type = "FAULT_CLEARED"; break;
    case CEV_RECOVERY_CONFIRMED: type = "RECOVERY_CONFIRMED"; break; case CEV_PROGRAM_APPLIED: type = "PROGRAM_APPLIED"; break; default: break;
    }
    char b[512]; int64_t seq = ++g_seq;
    int n = proto_encode_event(b, sizeof b, &g_id, seq, (int64_t)clock_now_ms(), type, e.severity, e.code);
    if (n > 0) net_enqueue_record("events", seq, e.severity == SEV_CRITICAL, b, (size_t)n); /* non-blocking; a full queue loses HISTORY, never control */
}

static void control_task(void *) {
    esp_task_wdt_add(nullptr);
    safety_inputs_t in; uint64_t last_tel = 0;
    for (;;) {
        sensors_read_safety(&in);
        uint64_t now = clock_now_ms();
        if (xSemaphoreTake(g_lock, 0) == pdTRUE) { /* try-lock: a command swap in progress just delays one 50 ms tick */
            ctl_tick(&g_ctl, now, &in);
            bool pump = ctl_pump_requested(&g_ctl);
            ctl_event_t ev; while (ctl_pop_event(&g_ctl, &ev)) emit_event(ev);
            xSemaphoreGive(g_lock);
            act_set_pump(pump, now);
        }
        if (now - last_tel >= 60000 || last_tel == 0) {
            reading_t r[8]; int n = sensors_snapshot(r, 8); char b[1024]; int64_t seq = ++g_seq;
            int pv = g_ctl.has_program ? g_ctl.program_version : 0;
            int len = proto_encode_telemetry(b, sizeof b, &g_id, seq, (int64_t)now, pv, r, n);
            if (len > 0) net_enqueue_record("telemetry", seq, false, b, (size_t)len);
            last_tel = now;
        }
        esp_task_wdt_reset();
        vTaskDelay(pdMS_TO_TICKS(50));
    }
}

static void random_boot_id(char *out) {
    uint8_t r[8]; esp_fill_random(r, sizeof r); static const char *hex = "0123456789abcdef"; strcpy(out, "boot-");
    for (int i = 0; i < 8; i++) { out[5 + 2 * i] = hex[r[i] >> 4]; out[6 + 2 * i] = hex[r[i] & 15]; } out[21] = 0;
}

extern "C" void app_main(void) {
    ESP_ERROR_CHECK(nvs_flash_init());
    act_init(); /* pump pin is driven only if the board is verified; otherwise never */
    sensors_init();
    g_lock = xSemaphoreCreateMutex();

    nvs_handle_t h; size_t l;
    memset(&g_id, 0, sizeof g_id);
    if (nvs_open("aero", NVS_READONLY, &h) == ESP_OK) {
        l = sizeof g_id.site_id; nvs_get_str(h, "site_id", g_id.site_id, &l);
        l = sizeof g_id.device_id; nvs_get_str(h, "device_id", g_id.device_id, &l);
        l = sizeof g_id.unit_id; g_id.has_unit = nvs_get_str(h, "unit_id", g_id.unit_id, &l) == ESP_OK;
        nvs_close(h);
    }
#ifdef CONFIG_AEROLAB_SIMULATED_DATA
    g_id.simulated = true;
#endif
    random_boot_id(g_id.boot_id);

    const board_profile_t *bp = board_profile();
    ctl_init(&g_ctl, bp->hard_max_on_seconds, 2000);
    ctl_boot(&g_ctl, clock_now_ms());
    cs_init(&g_cs, CS_NVS_BACKEND, nullptr, bp->hard_max_on_seconds);
    cs_record_t rec;
    if (cs_load(&g_cs, &rec) && rec.has_program) { /* atomic, CRC-checked, re-validated against the board cap */
        ctl_set_program(&g_ctl, &rec.program, rec.version, clock_now_ms());
        strncpy(g_last_cmd, rec.last_command_id, sizeof g_last_cmd - 1);
    } else ESP_LOGW(TAG, "no valid stored programme: staying in NEEDS_SETUP, no irrigation");

    journal_fs_mount();
    mb_init(&g_mb, g_meta, 400, MB_FS_BACKEND, nullptr);
    ch_init(&g_ch, &g_ctl, &g_cs, &g_id);
    g_ch.lock = lock_fn; g_ch.unlock = unlock_fn;
    if (g_last_cmd[0]) ch_set_last_applied(&g_ch, g_last_cmd);

    xTaskCreate(control_task, "control", 4096, nullptr, 10, nullptr); /* highest app priority */
    net_cfg_t nc = {&g_id, &g_ch, &g_mb, &g_ctl, CONFIG_AEROLAB_FIRMWARE_VERSION, snapshot_fn};
    net_start(&nc); /* Wi-Fi bring-up omitted in this skeleton: see README (needs provisioning decisions) */
}
