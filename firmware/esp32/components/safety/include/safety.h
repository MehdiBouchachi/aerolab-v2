#pragma once
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Raw interlock inputs, sampled by the sensors layer. */
typedef struct {
    bool level_ok;            /* reservoir has water */
    bool level_sensor_valid;  /* the level feedback itself is trustworthy */
    bool ph_valid;            /* non-essential */
    bool ec_valid;            /* non-essential */
} safety_inputs_t;

/* ESSENTIAL protection: low water, or no trustworthy level feedback (fail safe:
 * an unreadable level sensor stops the pump). Pure function, no I/O. */
static inline bool safety_essential_fault(const safety_inputs_t *in) { return !in->level_ok || !in->level_sensor_valid; }

/* Non-essential faults degrade but never stop a safe cycle. */
static inline bool safety_degraded(const safety_inputs_t *in) { return !in->ph_valid || !in->ec_valid; }

#ifdef __cplusplus
}
#endif
