#pragma once
/*
 * Autonomous irrigation controller. The ONLY authority over the pump output.
 * It never blocks, never touches the network or flash, and runs on a monotonic
 * millisecond clock passed in by the caller. Pump ON time is bounded three ways:
 * programme on_duration, programme max_continuous_on, and the board hard cap.
 */
#include <stdbool.h>
#include <stdint.h>
#include "safety.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef enum { CS_BOOT_SAFE, CS_SELF_CHECK, CS_NEEDS_SETUP, CS_RUNNING, CS_DEGRADED, CS_PROTECTED_STOP } ctl_state_t;

typedef struct {
    uint32_t on_s, off_s, max_continuous_on_s;
    bool require_reservoir_ok; /* must be true; false programmes are invalid */
} program_t;

/* Mirrors the wire contract bounds (contracts/mqtt). */
#define PROGRAM_MAX_ON_S 3600u
#define PROGRAM_MAX_OFF_S 86400u
bool program_valid(const program_t *p);

typedef enum { CEV_NONE, CEV_STATE_CHANGE, CEV_FAULT_RAISED, CEV_FAULT_CLEARED, CEV_RECOVERY_CONFIRMED, CEV_PROGRAM_APPLIED } ctl_event_type_t;
typedef enum { SEV_INFO, SEV_WARNING, SEV_CRITICAL } severity_t;
typedef struct {
    ctl_event_type_t type;
    severity_t severity;
    char code[40];
} ctl_event_t;

#define CTL_EVENTS_MAX 8
#define FAULT_LOW_WATER (1u << 0)
#define FAULT_LEVEL_SENSOR (1u << 1)
#define FAULT_PH_INVALID (1u << 2)
#define FAULT_EC_INVALID (1u << 3)

typedef struct {
    ctl_state_t state;
    bool pump_on;
    bool has_program;
    program_t program;
    int program_version;
    uint32_t hard_max_on_s;       /* board cap, never 0 when outputs permitted */
    uint32_t self_check_ms;
    uint64_t check_until_ms, phase_start_ms, on_since_ms;
    uint32_t faults;              /* currently raised */
    uint32_t raised_reported;     /* for edge detection */
    ctl_event_t ev[CTL_EVENTS_MAX];
    int ev_n;
} controller_t;

void ctl_init(controller_t *c, uint32_t hard_max_on_s, uint32_t self_check_ms);
/* Boot: outputs OFF first, then self-check. Programme (if any) is loaded by the caller beforehand. */
void ctl_boot(controller_t *c, uint64_t now_ms);
/* Replace the programme atomically; the cycle restarts in the OFF phase. */
bool ctl_set_program(controller_t *c, const program_t *p, int version, uint64_t now_ms);
void ctl_tick(controller_t *c, uint64_t now_ms, const safety_inputs_t *in);
/* Local physical confirmation (NOT reachable from the network). */
bool ctl_confirm_recovery(controller_t *c, uint64_t now_ms, const safety_inputs_t *in);
/* The single value the GPIO layer may drive. */
static inline bool ctl_pump_requested(const controller_t *c) { return c->pump_on; }
/* Pop one pending event; false when none. */
bool ctl_pop_event(controller_t *c, ctl_event_t *out);
const char *ctl_state_name(ctl_state_t s);

#ifdef __cplusplus
}
#endif
