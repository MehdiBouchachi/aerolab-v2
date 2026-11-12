#include "protocol.h"
#include <ctype.h>
#include <math.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "jsonscan.h"

/* ------------------------------------------------------------------ writer */
typedef struct { char *b; size_t cap, len; bool ovf; } wb_t;
static void wf(wb_t *w, const char *fmt, ...) {
    if (w->ovf) return;
    va_list ap; va_start(ap, fmt);
    int n = vsnprintf(w->b + w->len, w->cap - w->len, fmt, ap);
    va_end(ap);
    if (n < 0 || (size_t)n >= w->cap - w->len) { w->ovf = true; return; }
    w->len += (size_t)n;
}
static int wdone(wb_t *w) { return w->ovf ? -1 : (int)w->len; }

static void wnum(wb_t *w, double v) {
    char t[40];
    snprintf(t, sizeof t, "%.4f", v);
    size_t n = strlen(t);
    while (n > 1 && t[n - 1] == '0') t[--n] = 0;
    if (n > 1 && t[n - 1] == '.') t[--n] = 0;
    wf(w, "%s", t);
}

static void header(wb_t *w, const identity_t *id, bool with_schema_open) {
    (void)with_schema_open;
    wf(w, "{\"schema_version\":1,\"site_id\":\"%s\",\"device_id\":\"%s\",\"unit_id\":", id->site_id, id->device_id);
    if (id->has_unit) wf(w, "\"%s\"", id->unit_id); else wf(w, "null");
    wf(w, ",\"boot_id\":\"%s\",\"data_source\":\"%s\"", id->boot_id, id->simulated ? "SIMULATOR" : "DEVICE");
}

/* Honest clock: this firmware has a monotonic clock only unless an RTC driver says otherwise
 * (clock component). It NEVER invents sampled_at_utc. */
static void clockfields(wb_t *w) { wf(w, ",\"sampled_at_utc\":null,\"clock_quality\":\"monotonic_only\""); }

int proto_encode_telemetry(char *buf, size_t cap, const identity_t *id, int64_t seq, int64_t uptime_ms, int pv, const reading_t *r, int n) {
    wb_t w = {buf, cap, 0, false};
    header(&w, id, true);
    wf(&w, ",\"type\":\"telemetry\",\"seq\":%lld,\"uptime_ms\":%lld", (long long)seq, (long long)uptime_ms);
    clockfields(&w);
    if (pv > 0) wf(&w, ",\"program_version_applied\":%d", pv); else wf(&w, ",\"program_version_applied\":null");
    wf(&w, ",\"readings\":{");
    static const char *qn[] = {"valid", "invalid", "missing", "outdated"};
    for (int i = 0; i < n; i++) {
        wf(&w, "%s\"%s\":{\"value\":", i ? "," : "", r[i].name);
        /* a missing reading is null, NEVER zero; an invalid one carries no value either */
        if (r[i].q == Q_VALID) { if (r[i].is_bool) wf(&w, r[i].value != 0 ? "true" : "false"); else wnum(&w, r[i].value); }
        else wf(&w, "null");
        wf(&w, ",\"quality\":\"%s\"", qn[r[i].q]);
        if (r[i].unit && *r[i].unit) wf(&w, ",\"unit\":\"%s\"", r[i].unit);
        wf(&w, "}");
    }
    wf(&w, "}}");
    return wdone(&w);
}

int proto_encode_event(char *buf, size_t cap, const identity_t *id, int64_t seq, int64_t uptime_ms, const char *event_type, severity_t sev, const char *code) {
    static const char *sn[] = {"info", "warning", "critical"};
    wb_t w = {buf, cap, 0, false};
    header(&w, id, true);
    wf(&w, ",\"type\":\"event\",\"seq\":%lld,\"uptime_ms\":%lld", (long long)seq, (long long)uptime_ms);
    clockfields(&w);
    wf(&w, ",\"event_type\":\"%s\",\"severity\":\"%s\",\"code\":\"%s\"}", event_type, sn[sev], code);
    return wdone(&w);
}

static const struct { uint32_t bit; const char *name; } kFaults[] = {
    {FAULT_LOW_WATER, "LOW_WATER"}, {FAULT_LEVEL_SENSOR, "LEVEL_SENSOR_INVALID"}, {FAULT_PH_INVALID, "PH_INVALID"}, {FAULT_EC_INVALID, "EC_INVALID"}};

int proto_encode_state(char *buf, size_t cap, const identity_t *id, const state_report_t *s) {
    wb_t w = {buf, cap, 0, false};
    header(&w, id, true);
    wf(&w, ",\"uptime_ms\":%lld,\"control_state\":\"%s\",\"pump_commanded\":\"%s\",\"water_delivery\":\"NOT_VERIFIED\"", (long long)s->uptime_ms,
       ctl_state_name(s->state), s->pump_on ? "ON" : "OFF");
    if (s->prog_version > 0) wf(&w, ",\"program_version_applied\":%d", s->prog_version); else wf(&w, ",\"program_version_applied\":null");
    if (s->last_command_id) wf(&w, ",\"last_command_id\":\"%s\"", s->last_command_id); else wf(&w, ",\"last_command_id\":null");
    wf(&w, ",\"essential_interlocks_ok\":%s,\"fault_codes\":[", s->interlocks_ok ? "true" : "false");
    bool first = true;
    for (unsigned i = 0; i < sizeof kFaults / sizeof kFaults[0]; i++)
        if (s->faults & kFaults[i].bit) { wf(&w, "%s\"%s\"", first ? "" : ",", kFaults[i].name); first = false; }
    if (s->state == CS_NEEDS_SETUP) wf(&w, "%s\"NO_VALID_PROGRAM\"", first ? "" : ",");
    wf(&w, "],\"free_heap_bytes\":%lld,\"wifi_rssi_dbm\":%d,\"firmware_version\":\"%s\",\"session_nonce\":\"%s\",", (long long)s->free_heap, s->rssi_dbm,
       s->firmware, s->session_nonce);
    wf(&w, "\"buffer\":{\"occupancy\":%d,\"capacity\":%d,\"unreceipted\":%d,\"overflow_count\":%lld}}", s->buf_occupancy, s->buf_capacity, s->buf_unreceipted,
       (long long)s->buf_overflow);
    return wdone(&w);
}

int proto_encode_presence(char *buf, size_t cap, const identity_t *id, bool online, const char *fw, const char *nonce) {
    wb_t w = {buf, cap, 0, false};
    header(&w, id, true);
    wf(&w, ",\"status\":\"%s\",\"firmware_version\":\"%s\",\"session_nonce\":\"%s\"}", online ? "ONLINE" : "OFFLINE", fw, nonce);
    return wdone(&w);
}

const char *result_status_name(result_status_t s) {
    static const char *n[] = {"APPLIED", "REJECTED", "ALREADY_PROCESSED", "UNKNOWN"};
    return n[s];
}

int proto_encode_result(char *buf, size_t cap, const identity_t *id, const char *cid, result_status_t st, bool has_orig, result_status_t orig, int cur, const char *reason) {
    wb_t w = {buf, cap, 0, false};
    wf(&w, "{\"schema_version\":1,\"command_id\":\"%s\",\"site_id\":\"%s\",\"device_id\":\"%s\",\"boot_id\":\"%s\",\"data_source\":\"%s\",\"status\":\"%s\"", cid,
       id->site_id, id->device_id, id->boot_id, id->simulated ? "SIMULATOR" : "DEVICE", result_status_name(st));
    if (has_orig) wf(&w, ",\"original_status\":\"%s\"", result_status_name(orig));
    if (cur > 0) wf(&w, ",\"current_program_version\":%d", cur); else wf(&w, ",\"current_program_version\":null");
    if (reason) wf(&w, ",\"reason_code\":\"%s\"}", reason); else wf(&w, ",\"reason_code\":null}");
    return wdone(&w);
}

/* ------------------------------------------------------------------ validators */
bool proto_valid_id(const char *s) {
    size_t n = strlen(s);
    if (n < 3 || n > 40) return false;
    if (!(islower((unsigned char)s[0]) || isdigit((unsigned char)s[0]))) return false;
    for (size_t i = 1; i < n; i++)
        if (!(islower((unsigned char)s[i]) || isdigit((unsigned char)s[i]) || s[i] == '-')) return false;
    return true;
}
bool proto_valid_token(const char *s) {
    size_t n = strlen(s);
    if (n < 8 || n > 64) return false;
    for (size_t i = 0; i < n; i++)
        if (!(isalnum((unsigned char)s[i]) || s[i] == '-')) return false;
    return true;
}

const char *proto_err_name(proto_err_t e) {
    switch (e) {
    case PERR_OK: return "OK"; case PERR_EMPTY: return "EMPTY_PAYLOAD"; case PERR_TOO_LARGE: return "PAYLOAD_TOO_LARGE"; case PERR_MALFORMED: return "MALFORMED_JSON";
    case PERR_TOO_DEEP: return "PAYLOAD_TOO_DEEP"; case PERR_SCHEMA_VERSION: return "UNSUPPORTED_SCHEMA_VERSION"; case PERR_ID: return "INVALID_ID";
    case PERR_NONCE: return "INVALID_SESSION_NONCE"; case PERR_TTL: return "INVALID_TTL"; case PERR_TIME: return "INVALID_TIMESTAMP";
    case PERR_EXPIRY: return "INVALID_EXPIRY"; case PERR_VERSION: return "INVALID_PROGRAM_VERSION"; case PERR_TYPE: return "INVALID_COMMAND_TYPE";
    case PERR_PROGRAM: return "INVALID_PROGRAM"; case PERR_UNEXPECTED_PROGRAM: return "UNEXPECTED_PROGRAM";
    }
    return "UNKNOWN";
}

/* RFC3339 -> epoch seconds. Accepts Z or +-hh:mm and optional fractional seconds. */
static bool two(const char *p, int *o) { if (!isdigit((unsigned char)p[0]) || !isdigit((unsigned char)p[1])) return false; *o = (p[0]-'0')*10 + (p[1]-'0'); return true; }
static bool parse_rfc3339(const char *s, int64_t *out) {
    size_t n = strlen(s);
    if (n < 20) return false;
    int Y, mo, d, h, mi, se;
    if (!isdigit((unsigned char)s[0]) || !isdigit((unsigned char)s[1]) || !isdigit((unsigned char)s[2]) || !isdigit((unsigned char)s[3])) return false;
    Y = (s[0]-'0')*1000 + (s[1]-'0')*100 + (s[2]-'0')*10 + (s[3]-'0');
    if (s[4] != '-' || !two(s + 5, &mo) || s[7] != '-' || !two(s + 8, &d) || s[10] != 'T' || !two(s + 11, &h) || s[13] != ':' || !two(s + 14, &mi) || s[16] != ':' || !two(s + 17, &se)) return false;
    if (mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || se > 59) return false;
    size_t i = 19;
    if (s[i] == '.') { i++; size_t st = i; while (isdigit((unsigned char)s[i])) i++; if (i == st) return false; }
    int64_t off = 0;
    if (s[i] == 'Z' && s[i + 1] == 0) { /* UTC */ }
    else if ((s[i] == '+' || s[i] == '-') && strlen(s + i) == 6 && s[i + 3] == ':') {
        int oh, om; if (!two(s + i + 1, &oh) || !two(s + i + 4, &om)) return false;
        off = (oh * 3600 + om * 60) * (s[i] == '+' ? 1 : -1);
    } else return false;
    /* days from civil (Howard Hinnant) */
    int y = Y - (mo <= 2);
    int era = (y >= 0 ? y : y - 399) / 400;
    unsigned yoe = (unsigned)(y - era * 400);
    unsigned doy = (153u * (unsigned)(mo + (mo > 2 ? -3 : 9)) + 2u) / 5u + (unsigned)d - 1u;
    unsigned doe = yoe * 365u + yoe / 4u - yoe / 100u + doy;
    int64_t days = (int64_t)era * 146097 + (int64_t)doe - 719468;
    *out = days * 86400 + h * 3600 + mi * 60 + se - off;
    return true;
}

static bool get_s(const char *j, size_t n, const char *k, char *dst, size_t cap) { jval_t v; return jscan_get(j, n, k, &v) && jscan_str(&v, dst, cap); }
static bool get_int(const char *j, size_t n, const char *k, int *o) {
    jval_t v;
    if (!jscan_get(j, n, k, &v) || v.t != J_NUMBER || v.num != floor(v.num) || v.num < -2e9 || v.num > 2e9) return false;
    *o = (int)v.num; return true;
}

proto_err_t proto_decode_command(const char *j, size_t n, command_t *c) {
    memset(c, 0, sizeof *c);
    if (n == 0) return PERR_EMPTY;
    if (n > PROTO_MAX_PAYLOAD) return PERR_TOO_LARGE;
    if (!jscan_valid(j, n, 8)) {
        /* distinguish depth from syntax for the same stable codes the Go side reports */
        return jscan_valid(j, n, 64) ? PERR_TOO_DEEP : PERR_MALFORMED;
    }
    int sv;
    if (!get_int(j, n, "schema_version", &sv) || sv != 1) return PERR_SCHEMA_VERSION;
    if (!get_s(j, n, "command_id", c->command_id, sizeof c->command_id) || !proto_valid_token(c->command_id)) return PERR_ID;
    if (!get_s(j, n, "site_id", c->site_id, sizeof c->site_id) || !proto_valid_id(c->site_id) ||
        !get_s(j, n, "device_id", c->device_id, sizeof c->device_id) || !proto_valid_id(c->device_id) ||
        !get_s(j, n, "unit_id", c->unit_id, sizeof c->unit_id) || !proto_valid_id(c->unit_id)) return PERR_ID;
    if (!get_s(j, n, "session_nonce", c->session_nonce, sizeof c->session_nonce) || !proto_valid_token(c->session_nonce)) return PERR_NONCE;
    if (!get_int(j, n, "ttl_seconds", &c->ttl_s) || c->ttl_s < 1 || c->ttl_s > 300) return PERR_TTL;
    char ia[40], ex[40]; int64_t ti, te;
    if (!get_s(j, n, "issued_at_utc", ia, sizeof ia) || !get_s(j, n, "expires_at_utc", ex, sizeof ex) || !parse_rfc3339(ia, &ti) || !parse_rfc3339(ex, &te)) return PERR_TIME;
    if (te <= ti || te - ti > 300) return PERR_EXPIRY;
    if (!get_int(j, n, "expected_program_version", &c->expected_version) || c->expected_version < 0) return PERR_VERSION;
    char ty[24];
    if (!get_s(j, n, "command_type", ty, sizeof ty)) return PERR_TYPE;
    jval_t pj; bool has_p = jscan_get(j, n, "program", &pj);
    if (!strcmp(ty, "APPLY_PROGRAM")) {
        c->type = CMD_APPLY_PROGRAM;
        if (!has_p || pj.t != J_OBJECT) return PERR_PROGRAM;
        if (!get_int(j, n, "new_program_version", &c->new_version) || c->new_version != c->expected_version + 1) return PERR_VERSION;
        char mode[24]; int on, off, mx; jval_t rr;
        if (!get_s(pj.p, pj.n, "mode", mode, sizeof mode) || strcmp(mode, "TIMED_CYCLE")) return PERR_PROGRAM;
        if (!get_int(pj.p, pj.n, "on_duration_seconds", &on) || !get_int(pj.p, pj.n, "off_duration_seconds", &off) || !get_int(pj.p, pj.n, "max_continuous_on_seconds", &mx)) return PERR_PROGRAM;
        if (!jscan_get(pj.p, pj.n, "require_reservoir_ok", &rr) || rr.t != J_BOOL) return PERR_PROGRAM;
        if (on < 0 || off < 0 || mx < 0) return PERR_PROGRAM;
        c->program = (program_t){(uint32_t)on, (uint32_t)off, (uint32_t)mx, rr.b};
        if (!program_valid(&c->program)) return PERR_PROGRAM;
        c->has_program = true;
    } else if (!strcmp(ty, "QUERY_STATE")) {
        c->type = CMD_QUERY_STATE;
        if (has_p) return PERR_UNEXPECTED_PROGRAM;
    } else return PERR_TYPE; /* e.g. a "PUMP_TEST": manual tests are never a network command */
    return PERR_OK;
}

proto_err_t proto_decode_receipt(const char *j, size_t n, receipt_t *r) {
    memset(r, 0, sizeof *r);
    if (n == 0) return PERR_EMPTY;
    if (n > PROTO_MAX_PAYLOAD) return PERR_TOO_LARGE;
    if (!jscan_valid(j, n, 8)) return PERR_MALFORMED;
    int sv; char st[16];
    if (!get_int(j, n, "schema_version", &sv) || sv != 1) return PERR_SCHEMA_VERSION;
    if (!get_s(j, n, "site_id", r->site_id, sizeof r->site_id) || !get_s(j, n, "device_id", r->device_id, sizeof r->device_id) || !get_s(j, n, "boot_id", r->boot_id, sizeof r->boot_id)) return PERR_ID;
    if (!proto_valid_id(r->site_id) || !proto_valid_id(r->device_id) || !proto_valid_token(r->boot_id)) return PERR_ID;
    if (!get_s(j, n, "status", st, sizeof st) || strcmp(st, "PERSISTED")) return PERR_TYPE;
    for (int pass = 0; pass < 2; pass++) {
        jval_t a; const char *key = pass ? "rejected_sequences" : "acked_sequences";
        if (!jscan_get(j, n, key, &a) || a.t != J_ARRAY) return PERR_MALFORMED;
        const char *p = a.p + 1, *e = a.p + a.n - 1; int cnt = 0;
        while (p < e) {
            while (p < e && (*p == ' ' || *p == ',' || *p == '\n' || *p == '\r' || *p == '\t')) p++;
            if (p >= e) break;
            char *end; double d = strtod(p, &end);
            if (end == p || d < 1 || d != floor(d) || cnt >= 100) return PERR_MALFORMED;
            (pass ? r->rejected : r->acked)[cnt++] = (int64_t)d;
            p = end;
        }
        if (pass) r->n_rejected = cnt; else r->n_acked = cnt;
    }
    if (r->n_acked + r->n_rejected == 0) return PERR_MALFORMED;
    return PERR_OK;
}
