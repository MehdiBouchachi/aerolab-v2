#pragma once
/*
 * Board profile: pin map, output polarity and hardware limits.
 *
 * NOTHING HERE IS VERIFIED HARDWARE FACT. The pin numbers are -1 (unassigned)
 * and the outputs are DISABLED until a human fills in the real wiring AND sets
 * CONFIG_AEROLAB_WIRING_VERIFIED=y (see main/Kconfig.projbuild). With either
 * missing the actuator layer refuses to energise any output.
 *
 * Unknown facts the project owner must supply (spec section 22): ESP32 model,
 * relay polarity, pump power requirements, level/pH/EC/temperature sensor
 * models and wiring, which interlocks are essential, and the maximum pump ON
 * time that is safe for the real pump.
 */
#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define BOARD_PIN_UNASSIGNED (-1)

typedef struct {
    const char *name;
    int pump_gpio;               /* relay/driver input */
    bool pump_active_high;       /* polarity: UNKNOWN until verified */
    int level_gpio;              /* reservoir level switch */
    bool level_ok_is_high;       /* polarity: UNKNOWN until verified */
    /* Absolute pump ON cap enforced by firmware regardless of programme,
     * network or user. 0 = not set => outputs stay disabled. */
    uint32_t hard_max_on_seconds;
    bool essential_level_sensor; /* a level fault must stop the pump */
} board_profile_t;

/* The compiled-in profile (defaults are the SAFE, unconfigured one). */
const board_profile_t *board_profile(void);

/* True only when pins, polarity and hard cap are filled in AND the build was
 * explicitly marked as wiring-verified. */
bool board_outputs_permitted(void);

#ifdef __cplusplus
}
#endif
