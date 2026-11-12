#include "sensors.h"
#include "board_profile.h"
#include "driver/gpio.h"

/*
 * Only the reservoir level input is wired in this skeleton, and only when the board is verified.
 * Air/water temperature, pH, EC and light drivers are NOT written: the sensor models are unknown
 * (spec section 22). They are reported as MISSING, which is the truth, and they do not degrade the
 * controller because they are not fitted. Add real drivers behind these same functions.
 */
void sensors_init(void) {
    if (!board_outputs_permitted()) return;
    const board_profile_t *p = board_profile();
    gpio_config_t io = {.pin_bit_mask = 1ULL << p->level_gpio, .mode = GPIO_MODE_INPUT, .pull_up_en = GPIO_PULLUP_ENABLE, .intr_type = GPIO_INTR_DISABLE};
    gpio_config(&io);
}

void sensors_read_safety(safety_inputs_t *in) {
    if (!board_outputs_permitted()) {
        /* FAIL SAFE: no verified level input => treat the level feedback as invalid => PROTECTED_STOP */
        in->level_ok = false; in->level_sensor_valid = false; in->ph_valid = true; in->ec_valid = true;
        return;
    }
    const board_profile_t *p = board_profile();
    int raw = gpio_get_level((gpio_num_t)p->level_gpio);
    in->level_sensor_valid = true; /* TODO(hardware): add plausibility checks once the real sensor is known */
    in->level_ok = (raw != 0) == p->level_ok_is_high;
    in->ph_valid = true; in->ec_valid = true; /* not fitted: not a fault */
}

int sensors_snapshot(reading_t *r, int n) {
    static const char *missing[] = {"air_temp_c", "relative_humidity_pct", "water_temp_c", "ph", "ec_ms_cm", "light_lux"};
    int k = 0;
    safety_inputs_t in; sensors_read_safety(&in);
    if (k < n) r[k++] = (reading_t){"reservoir_level_ok", in.level_ok ? 1 : 0, true, in.level_sensor_valid ? Q_VALID : Q_INVALID, NULL};
    for (unsigned i = 0; i < sizeof missing / sizeof missing[0] && k < n; i++) r[k++] = (reading_t){missing[i], 0, false, Q_MISSING, NULL};
    return k;
}
