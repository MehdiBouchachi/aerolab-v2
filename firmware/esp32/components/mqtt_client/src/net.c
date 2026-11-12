/*
 * Network task: Wi-Fi + MQTT, reconnect with exponential backoff + jitter, a NEW session nonce per
 * MQTT connection, Last Will, command/receipt handling, journal replay in bounded batches.
 *
 * STATUS: written against ESP-IDF 5.x (esp-mqtt) but NEVER BUILT or run on a device in this
 * repository. Behaviour is specified by simulator/esp32 (tested) and by the host tests of the
 * portable core. TLS is supported by configuration but unverified.
 */
#include "net.h"
#include <string.h>
#include "clock.h"
#include "diagnostics.h"
#include "esp_log.h"
#include "esp_random.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"
#include "mqtt_client.h"
#include "nvs.h"

static const char *TAG = "net";
#define BACKOFF_MIN_MS 1000
#define BACKOFF_MAX_MS 30000
#define MSG_MAX 2048

typedef struct { char leaf[12]; int64_t seq; bool critical; uint16_t len; char data[1024]; } rec_msg_t;
typedef struct { char topic[96]; uint16_t len; char data[MSG_MAX]; } rx_msg_t;

static net_cfg_t s_cfg;
static esp_mqtt_client_handle_t s_mqtt;
static QueueHandle_t s_rec_q, s_rx_q;
static volatile bool s_connected;
static char s_nonce[PROTO_MAX_TOKEN];
static char s_topic_cmd[96], s_topic_rcpt[96], s_topic_state[96], s_topic_pres[96], s_topic_res[96];
static char s_uri[128], s_user[64], s_pass[96], s_lwt[512];

bool net_connected(void) { return s_connected; }

bool net_enqueue_record(const char *leaf, int64_t seq, bool critical, const char *json, size_t len) {
    if (len >= sizeof(((rec_msg_t *)0)->data)) return false;
    rec_msg_t m = {0}; strncpy(m.leaf, leaf, sizeof m.leaf - 1); m.seq = seq; m.critical = critical; m.len = (uint16_t)len; memcpy(m.data, json, len);
    return xQueueSend(s_rec_q, &m, 0) == pdTRUE; /* never blocks the caller: a full queue is reported, control goes on */
}

static void new_nonce(void) {
    uint8_t r[8]; esp_fill_random(r, sizeof r);
    static const char *hex = "0123456789abcdef"; strcpy(s_nonce, "nonce-");
    for (int i = 0; i < 8; i++) { s_nonce[6 + 2 * i] = hex[r[i] >> 4]; s_nonce[7 + 2 * i] = hex[r[i] & 15]; }
    s_nonce[22] = 0;
    ch_set_session(s_cfg.ch, s_nonce); /* commands parked in the broker across a reconnect now carry a stale nonce */
}

static void publish_state(void) {
    state_report_t st; s_cfg.snapshot(&st); st.session_nonce = s_nonce; st.firmware = s_cfg.firmware;
    st.free_heap = diag_free_heap(); st.rssi_dbm = diag_rssi_dbm();
    st.buf_occupancy = mb_occupancy(s_cfg.mb); st.buf_capacity = s_cfg.mb->cap; st.buf_unreceipted = st.buf_occupancy; st.buf_overflow = s_cfg.mb->overflow;
    char b[1024]; int n = proto_encode_state(b, sizeof b, s_cfg.id, &st);
    if (n > 0) esp_mqtt_client_publish(s_mqtt, s_topic_state, b, n, 0, 0);
}

static void on_connected(void) {
    new_nonce();
    esp_mqtt_client_subscribe(s_mqtt, s_topic_cmd, 1);
    esp_mqtt_client_subscribe(s_mqtt, s_topic_rcpt, 1);
    char b[512]; int n = proto_encode_presence(b, sizeof b, s_cfg.id, true, s_cfg.firmware, s_nonce);
    if (n > 0) esp_mqtt_client_publish(s_mqtt, s_topic_pres, b, n, 1, 1);
    publish_state(); /* reconnect order: presence + state FIRST, then the backlog */
    s_connected = true;
}

static void mqtt_event(void *arg, esp_event_base_t base, int32_t id, void *data) {
    (void)arg; (void)base; esp_mqtt_event_handle_t e = (esp_mqtt_event_handle_t)data;
    switch ((esp_mqtt_event_id_t)id) {
    case MQTT_EVENT_CONNECTED: on_connected(); break;
    case MQTT_EVENT_DISCONNECTED: s_connected = false; break;
    case MQTT_EVENT_DATA: {
        if (e->total_data_len != e->data_len || e->data_len >= MSG_MAX || e->topic_len >= 96) break; /* fragments / oversize: refuse */
        rx_msg_t m; memcpy(m.topic, e->topic, (size_t)e->topic_len); m.topic[e->topic_len] = 0; m.len = (uint16_t)e->data_len; memcpy(m.data, e->data, (size_t)e->data_len);
        xQueueSend(s_rx_q, &m, 0);
        break;
    }
    default: break;
    }
}

static void handle_rx(const rx_msg_t *m) {
    if (!strcmp(m->topic, s_topic_cmd)) {
        char rep[1024];
        ch_action_t a = ch_handle(s_cfg.ch, m->data, m->len, clock_now_ms(), rep, sizeof rep);
        if (a != CH_IGNORE) esp_mqtt_client_publish(s_mqtt, s_topic_res, rep, (int)strlen(rep), 1, 0);
        if (a == CH_REPLY_AND_STATE) publish_state();
    } else if (!strcmp(m->topic, s_topic_rcpt)) {
        receipt_t r;
        if (proto_decode_receipt(m->data, m->len, &r) == PERR_OK && !strcmp(r.device_id, s_cfg.id->device_id)) {
            mb_reclaim(s_cfg.mb, r.boot_id, r.acked, r.n_acked);
            mb_reclaim(s_cfg.mb, r.boot_id, r.rejected, r.n_rejected);
        }
    }
}

static void flush_journal(void) {
    int slots[20]; char b[1100];
    int n = mb_next_batch(s_cfg.mb, clock_now_ms(), 15000, 20, 40, slots);
    for (int i = 0; i < n; i++) {
        int len = mb_read(s_cfg.mb, slots[i], b, sizeof b);
        if (len <= 0) continue;
        const char *leaf = strstr(b, "\"type\":\"event\"") ? "events" : "telemetry";
        char topic[96]; snprintf(topic, sizeof topic, "aerolab/v1/devices/%s/%s", s_cfg.id->device_id, leaf);
        esp_mqtt_client_publish(s_mqtt, topic, b, len, 1, 0);
    }
}

static void net_task(void *arg) {
    (void)arg; uint32_t backoff = BACKOFF_MIN_MS; uint64_t last_state = 0, last_flush = 0; bool started = false;
    for (;;) {
        rec_msg_t rm; while (xQueueReceive(s_rec_q, &rm, 0) == pdTRUE) mb_append(s_cfg.mb, s_cfg.id->boot_id, rm.seq, rm.critical, rm.data, rm.len);
        if (!s_connected) {
            if (!started) { esp_mqtt_client_start(s_mqtt); started = true; }
            else { vTaskDelay(pdMS_TO_TICKS(backoff / 2 + (esp_random() % (backoff / 2 + 1)))); esp_mqtt_client_reconnect(s_mqtt); if ((backoff *= 2) > BACKOFF_MAX_MS) backoff = BACKOFF_MAX_MS; }
            vTaskDelay(pdMS_TO_TICKS(500));
            if (s_connected) backoff = BACKOFF_MIN_MS;
            continue;
        }
        rx_msg_t rx; while (xQueueReceive(s_rx_q, &rx, 0) == pdTRUE) handle_rx(&rx);
        uint64_t now = clock_now_ms();
        if (now - last_state >= 5000) { publish_state(); last_state = now; }
        if (now - last_flush >= 250) { flush_journal(); last_flush = now; }
        vTaskDelay(pdMS_TO_TICKS(50));
    }
}

static bool nvs_str(nvs_handle_t h, const char *k, char *dst, size_t cap) { size_t l = cap; return nvs_get_str(h, k, dst, &l) == ESP_OK; }

void net_start(const net_cfg_t *cfg) {
    s_cfg = *cfg;
    s_rec_q = xQueueCreate(32, sizeof(rec_msg_t)); s_rx_q = xQueueCreate(8, sizeof(rx_msg_t));
    nvs_handle_t h;
    if (nvs_open("aero", NVS_READONLY, &h) != ESP_OK || !nvs_str(h, "mqtt_uri", s_uri, sizeof s_uri) || !nvs_str(h, "mqtt_user", s_user, sizeof s_user) || !nvs_str(h, "mqtt_pass", s_pass, sizeof s_pass)) {
        ESP_LOGE(TAG, "UNPROVISIONED: no MQTT credentials in NVS. Not connecting. Control is unaffected."); return;
    }
    nvs_close(h);
    snprintf(s_topic_cmd, sizeof s_topic_cmd, "aerolab/v1/devices/%s/commands", cfg->id->device_id);
    snprintf(s_topic_rcpt, sizeof s_topic_rcpt, "aerolab/v1/devices/%s/receipts", cfg->id->device_id);
    snprintf(s_topic_state, sizeof s_topic_state, "aerolab/v1/devices/%s/state", cfg->id->device_id);
    snprintf(s_topic_pres, sizeof s_topic_pres, "aerolab/v1/devices/%s/presence", cfg->id->device_id);
    snprintf(s_topic_res, sizeof s_topic_res, "aerolab/v1/devices/%s/command-results", cfg->id->device_id);
    new_nonce();
    proto_encode_presence(s_lwt, sizeof s_lwt, cfg->id, false, cfg->firmware, s_nonce);
    esp_mqtt_client_config_t mc = {.broker.address.uri = s_uri, .credentials.username = s_user, .credentials.client_id = cfg->id->device_id,
        .credentials.authentication.password = s_pass, .session.keepalive = 10, .session.disable_clean_session = false, .network.disable_auto_reconnect = true,
        .session.last_will = {.topic = s_topic_pres, .msg = s_lwt, .msg_len = (int)strlen(s_lwt), .qos = 1, .retain = 1}};
    s_mqtt = esp_mqtt_client_init(&mc);
    esp_mqtt_client_register_event(s_mqtt, MQTT_EVENT_ANY, mqtt_event, NULL);
    xTaskCreate(net_task, "net", 8192, NULL, 4, NULL); /* lower priority than the control task */
}
