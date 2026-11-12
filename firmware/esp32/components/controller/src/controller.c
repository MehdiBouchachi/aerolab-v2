#include "controller.h"
#include <string.h>

bool program_valid(const program_t *p) {
    if (!p || !p->require_reservoir_ok) return false; /* protection cannot be disabled */
    if (p->on_s < 1 || p->on_s > PROGRAM_MAX_ON_S) return false;
    if (p->off_s < 1 || p->off_s > PROGRAM_MAX_OFF_S) return false;
    if (p->max_continuous_on_s < p->on_s || p->max_continuous_on_s > PROGRAM_MAX_ON_S) return false;
    return true;
}

const char *ctl_state_name(ctl_state_t s) {
    switch (s) {
    case CS_BOOT_SAFE: return "BOOT_SAFE";
    case CS_SELF_CHECK: return "SELF_CHECK";
    case CS_NEEDS_SETUP: return "NEEDS_SETUP";
    case CS_RUNNING: return "RUNNING";
    case CS_DEGRADED: return "DEGRADED";
    case CS_PROTECTED_STOP: return "PROTECTED_STOP";
    }
    return "UNKNOWN";
}

static void push(controller_t *c, ctl_event_type_t t, severity_t s, const char *code) {
    if (c->ev_n >= CTL_EVENTS_MAX) return; /* bounded: the journal layer records overflow */
    ctl_event_t *e = &c->ev[c->ev_n++];
    e->type = t;
    e->severity = s;
    strncpy(e->code, code, sizeof e->code - 1);
    e->code[sizeof e->code - 1] = 0;
}

bool ctl_pop_event(controller_t *c, ctl_event_t *out) {
    if (c->ev_n == 0) return false;
    *out = c->ev[0];
    memmove(&c->ev[0], &c->ev[1], sizeof(ctl_event_t) * (size_t)(c->ev_n - 1));
    c->ev_n--;
    return true;
}

static void pump(controller_t *c, bool on, uint64_t now) {
    if (on == c->pump_on) return;
    if (on) c->on_since_ms = now;
    c->pump_on = on;
}

static void set_state(controller_t *c, ctl_state_t s) {
    if (c->state == s) return;
    c->state = s;
    push(c, CEV_STATE_CHANGE, SEV_INFO, ctl_state_name(s));
}

void ctl_init(controller_t *c, uint32_t hard_max_on_s, uint32_t self_check_ms) {
    memset(c, 0, sizeof *c);
    c->hard_max_on_s = hard_max_on_s;
    c->self_check_ms = self_check_ms;
}

void ctl_boot(controller_t *c, uint64_t now) {
    c->pump_on = false; /* outputs OFF before anything else */
    c->faults = c->raised_reported = 0;
    c->ev_n = 0;
    c->state = CS_BOOT_SAFE;
    c->check_until_ms = now + c->self_check_ms;
    c->phase_start_ms = now;
    set_state(c, CS_SELF_CHECK);
}

bool ctl_set_program(controller_t *c, const program_t *p, int version, uint64_t now) {
    if (!program_valid(p) || version < 1) return false;
    /* A programme that could exceed the board's cap is refused, not clamped silently. */
    if (c->hard_max_on_s == 0 || p->on_s > c->hard_max_on_s || p->max_continuous_on_s > c->hard_max_on_s) return false;
    c->program = *p;
    c->program_version = version;
    c->has_program = true;
    pump(c, false, now); /* a new programme never jumps the pump ON */
    c->phase_start_ms = now;
    if (c->state == CS_NEEDS_SETUP) {
        c->check_until_ms = now + c->self_check_ms;
        set_state(c, CS_SELF_CHECK);
    }
    push(c, CEV_PROGRAM_APPLIED, SEV_INFO, "PROGRAM_APPLIED");
    return true;
}

static uint32_t fault_bits(const safety_inputs_t *in) {
    uint32_t f = 0;
    if (!in->level_ok) f |= FAULT_LOW_WATER;
    if (!in->level_sensor_valid) f |= FAULT_LEVEL_SENSOR;
    if (!in->ph_valid) f |= FAULT_PH_INVALID;
    if (!in->ec_valid) f |= FAULT_EC_INVALID;
    return f;
}

static void edge_events(controller_t *c, uint32_t now_f) {
    static const struct { uint32_t bit; const char *code; severity_t sev; } t[] = {
        {FAULT_LOW_WATER, "LOW_WATER", SEV_CRITICAL}, {FAULT_LEVEL_SENSOR, "LEVEL_SENSOR_INVALID", SEV_CRITICAL},
        {FAULT_PH_INVALID, "PH_INVALID", SEV_WARNING}, {FAULT_EC_INVALID, "EC_INVALID", SEV_WARNING}};
    for (unsigned i = 0; i < sizeof t / sizeof t[0]; i++) {
        bool was = c->raised_reported & t[i].bit, is = now_f & t[i].bit;
        if (is && !was) push(c, CEV_FAULT_RAISED, t[i].sev, t[i].code);
        if (!is && was) push(c, CEV_FAULT_CLEARED, SEV_INFO, t[i].code);
    }
    c->raised_reported = now_f;
}

static uint32_t on_limit_s(const controller_t *c) {
    uint32_t n = c->program.on_s;
    if (c->program.max_continuous_on_s < n) n = c->program.max_continuous_on_s;
    if (c->hard_max_on_s < n) n = c->hard_max_on_s;
    return n;
}

void ctl_tick(controller_t *c, uint64_t now, const safety_inputs_t *in) {
    uint32_t f = fault_bits(in);
    c->faults = f;
    edge_events(c, f);
    bool essential = safety_essential_fault(in);

    /* Essential protection overrides everything, in every state, immediately. */
    if (essential && c->state != CS_PROTECTED_STOP && c->state != CS_BOOT_SAFE) {
        pump(c, false, now);
        set_state(c, CS_PROTECTED_STOP);
        return;
    }
    switch (c->state) {
    case CS_BOOT_SAFE: pump(c, false, now); break;
    case CS_SELF_CHECK:
        pump(c, false, now);
        if (now < c->check_until_ms) return;
        if (essential) set_state(c, CS_PROTECTED_STOP);
        else if (!c->has_program) set_state(c, CS_NEEDS_SETUP);
        else {
            c->phase_start_ms = now; /* cycles always start in the OFF phase */
            set_state(c, CS_RUNNING);
        }
        break;
    case CS_NEEDS_SETUP: pump(c, false, now); break; /* no valid programme => no irrigation */
    case CS_PROTECTED_STOP: pump(c, false, now); break; /* leaves only via ctl_confirm_recovery */
    case CS_RUNNING:
    case CS_DEGRADED: {
        if (!c->has_program) { pump(c, false, now); set_state(c, CS_NEEDS_SETUP); break; }
        uint64_t lim = (uint64_t)on_limit_s(c) * 1000u;
        if (c->pump_on) {
            if (now - c->on_since_ms >= lim) { pump(c, false, now); c->phase_start_ms = now; }
        } else if (now - c->phase_start_ms >= (uint64_t)c->program.off_s * 1000u) {
            pump(c, true, now);
        }
        set_state(c, safety_degraded(in) ? CS_DEGRADED : CS_RUNNING);
        break;
    }
    }
}

bool ctl_confirm_recovery(controller_t *c, uint64_t now, const safety_inputs_t *in) {
    if (c->state != CS_PROTECTED_STOP || safety_essential_fault(in)) return false;
    push(c, CEV_RECOVERY_CONFIRMED, SEV_INFO, "RECOVERY_CONFIRMED");
    c->check_until_ms = now + c->self_check_ms;
    set_state(c, CS_SELF_CHECK);
    return true;
}
