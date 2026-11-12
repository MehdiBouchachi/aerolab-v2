#include "board_profile.h"

#ifndef CONFIG_AEROLAB_WIRING_VERIFIED
#define CONFIG_AEROLAB_WIRING_VERIFIED 0
#endif

/* UNCONFIGURED on purpose. Replace with the real, verified profile. */
static const board_profile_t kProfile = {
    .name = "UNCONFIGURED",
    .pump_gpio = BOARD_PIN_UNASSIGNED,
    .pump_active_high = true,
    .level_gpio = BOARD_PIN_UNASSIGNED,
    .level_ok_is_high = true,
    .hard_max_on_seconds = 0,
    .essential_level_sensor = true,
};

const board_profile_t *board_profile(void) { return &kProfile; }

bool board_outputs_permitted(void) {
    const board_profile_t *p = board_profile();
    return CONFIG_AEROLAB_WIRING_VERIFIED && p->pump_gpio != BOARD_PIN_UNASSIGNED && p->level_gpio != BOARD_PIN_UNASSIGNED &&
           p->hard_max_on_seconds > 0;
}
