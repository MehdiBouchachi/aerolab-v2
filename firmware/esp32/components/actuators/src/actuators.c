#include "actuators.h"
#include "board_profile.h"
#include "driver/gpio.h"
#include "esp_log.h"

static const char *TAG = "act";
static bool s_on, s_enabled;
static uint64_t s_on_since;

static void drive(bool on) {
    const board_profile_t *p = board_profile();
    gpio_set_level((gpio_num_t)p->pump_gpio, (on == p->pump_active_high) ? 1 : 0);
}

void act_init(void) {
    if (!board_outputs_permitted()) {
        ESP_LOGW(TAG, "outputs DISABLED: board unconfigured or wiring not verified (CONFIG_AEROLAB_WIRING_VERIFIED). The pump pin is not driven.");
        s_enabled = false;
        return;
    }
    const board_profile_t *p = board_profile();
    gpio_config_t io = {.pin_bit_mask = 1ULL << p->pump_gpio, .mode = GPIO_MODE_OUTPUT, .pull_up_en = GPIO_PULLUP_DISABLE, .pull_down_en = GPIO_PULLDOWN_DISABLE, .intr_type = GPIO_INTR_DISABLE};
    drive(false); /* OFF level is latched before the pin becomes an output where the hardware allows */
    gpio_config(&io);
    drive(false);
    s_enabled = true;
}

void act_set_pump(bool on, uint64_t now_ms) {
    if (!s_enabled) { s_on = false; return; }
    if (s_on && now_ms - s_on_since >= (uint64_t)board_profile()->hard_max_on_seconds * 1000u) on = false; /* hard cap */
    if (on && !s_on) s_on_since = now_ms;
    s_on = on;
    drive(on);
}
bool act_pump_is_on(void) { return s_on; }
