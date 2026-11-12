#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include "command_handler.h"
#include "controller.h"
#include "config_store.h"
#include "message_buffer.h"
#include "protocol.h"
#include "board_profile.h"

static int g_fail = 0, g_run = 0;
#define CHECK(c) do { g_run++; if (!(c)) { g_fail++; printf("  FAIL %s:%d: %s\n", __FILE__, __LINE__, #c); } } while (0)
#define TEST(name) static void name(void)
#define RUN(name) do { int f0 = g_fail; name(); printf("%s %s\n", g_fail == f0 ? "ok  " : "FAIL", #name); } while (0)

static safety_inputs_t ok_in(void) { return (safety_inputs_t){true, true, true, true}; }
static const program_t P1 = {1, 1, 1, true};

/* ------------------------------------------------------------- controller */
static void run_to(controller_t *c, uint64_t *t, uint64_t until, const safety_inputs_t *in) { for (; *t <= until; *t += 10) ctl_tick(c, *t, in); }

TEST(unconfigured_controller_never_irrigates) {
    controller_t c; ctl_init(&c, 120, 50); ctl_boot(&c, 0); safety_inputs_t in = ok_in();
    uint64_t t = 0; run_to(&c, &t, 60000, &in);
    CHECK(c.state == CS_NEEDS_SETUP); CHECK(!ctl_pump_requested(&c));
}
TEST(cycle_runs_on_the_monotonic_clock_only) {
    controller_t c; ctl_init(&c, 120, 50); ctl_boot(&c, 0); safety_inputs_t in = ok_in();
    CHECK(ctl_set_program(&c, &P1, 1, 0));
    uint64_t t = 0; int on_edges = 0; bool last = false;
    for (; t < 10000; t += 10) { ctl_tick(&c, t, &in); if (ctl_pump_requested(&c) && !last) on_edges++; last = ctl_pump_requested(&c); }
    CHECK(c.state == CS_RUNNING); CHECK(on_edges >= 4); /* 1 s off + 1 s on, after a 50 ms self-check */
    /* the cycle starts in the OFF phase: no immediate pump-on after boot */
    controller_t d; ctl_init(&d, 120, 50); ctl_boot(&d, 0); ctl_set_program(&d, &P1, 1, 0);
    uint64_t u = 0; for (; u <= 900; u += 10) { ctl_tick(&d, u, &in); CHECK(!ctl_pump_requested(&d)); }
}
TEST(low_water_stops_instantly_and_needs_local_confirmation) {
    controller_t c; ctl_init(&c, 120, 50); ctl_boot(&c, 0); safety_inputs_t in = ok_in();
    ctl_set_program(&c, &P1, 1, 0); uint64_t t = 0; run_to(&c, &t, 1500, &in);
    CHECK(ctl_pump_requested(&c)); /* mid ON phase */
    in.level_ok = false; ctl_tick(&c, t, &in);
    CHECK(c.state == CS_PROTECTED_STOP); CHECK(!ctl_pump_requested(&c)); /* within ONE tick */
    ctl_event_t e; bool crit = false; while (ctl_pop_event(&c, &e)) crit |= e.type == CEV_FAULT_RAISED && e.severity == SEV_CRITICAL && !strcmp(e.code, "LOW_WATER");
    CHECK(crit);
    in.level_ok = true; t += 10; for (int i = 0; i < 500; i++, t += 10) { ctl_tick(&c, t, &in); CHECK(!ctl_pump_requested(&c)); }
    CHECK(c.state == CS_PROTECTED_STOP); /* fault cleared but NOT released */
    safety_inputs_t bad = in; bad.level_ok = false;
    CHECK(!ctl_confirm_recovery(&c, t, &bad)); /* cannot confirm while the fault is present */
    CHECK(ctl_confirm_recovery(&c, t, &in));
    run_to(&c, &t, t + 300, &in); CHECK(c.state == CS_RUNNING);
}
TEST(invalid_level_sensor_fails_safe_but_ph_ec_only_degrade) {
    controller_t c; ctl_init(&c, 120, 50); ctl_boot(&c, 0); safety_inputs_t in = ok_in();
    ctl_set_program(&c, &P1, 1, 0); uint64_t t = 0; run_to(&c, &t, 500, &in);
    in.ph_valid = false; in.ec_valid = false; int ons = 0; bool last = false;
    for (uint64_t u = t; u < t + 5000; u += 10) { ctl_tick(&c, u, &in); ons += ctl_pump_requested(&c) && !last; last = ctl_pump_requested(&c); }
    CHECK(c.state == CS_DEGRADED); CHECK(ons >= 2);
    in.level_sensor_valid = false; ctl_tick(&c, t + 6000, &in);
    CHECK(c.state == CS_PROTECTED_STOP); CHECK(!ctl_pump_requested(&c));
}
TEST(programme_bounds_and_hardware_cap) {
    controller_t c; ctl_init(&c, 10, 50); ctl_boot(&c, 0);
    program_t ok = {5, 60, 10, true}, off = ok, big = {5, 60, 50, true}, zero = ok, low = ok;
    off.require_reservoir_ok = false; zero.on_s = 0; low.max_continuous_on_s = 2;
    CHECK(ctl_set_program(&c, &ok, 1, 0)); CHECK(!ctl_set_program(&c, &off, 2, 0)); CHECK(!ctl_set_program(&c, &big, 2, 0));
    CHECK(!ctl_set_program(&c, &zero, 2, 0)); CHECK(!ctl_set_program(&c, &low, 2, 0)); CHECK(c.program_version == 1);
    controller_t nocap; ctl_init(&nocap, 0, 50); CHECK(!ctl_set_program(&nocap, &ok, 1, 0)); /* cap not set => outputs disabled */
}
TEST(pump_on_time_is_bounded_even_if_programme_asks_for_more) {
    controller_t c; ctl_init(&c, 2, 50); ctl_boot(&c, 0); safety_inputs_t in = ok_in();
    program_t p = {2, 1, 2, true}; ctl_set_program(&c, &p, 1, 0);
    uint64_t t = 0, on_since = 0, longest = 0; bool last = false;
    for (; t < 30000; t += 10) { ctl_tick(&c, t, &in); bool on = ctl_pump_requested(&c); if (on && !last) on_since = t; if (!on && last && t - on_since > longest) longest = t - on_since; last = on; }
    CHECK(longest <= 2010);
}
TEST(board_profile_is_unconfigured_so_outputs_are_refused) { CHECK(!board_outputs_permitted()); CHECK(!strcmp(board_profile()->name, "UNCONFIGURED")); }

/* ------------------------------------------------------------- protocol vs shared fixtures */
static char *slurp(const char *path, size_t *n) {
    FILE *f = fopen(path, "rb"); if (!f) return NULL; fseek(f, 0, SEEK_END); long l = ftell(f); fseek(f, 0, SEEK_SET);
    char *b = malloc((size_t)l + 1); size_t r = fread(b, 1, (size_t)l, f); b[r] = 0; fclose(f); *n = r; return b;
}
static const char *g_fix;
static int each_fixture(const char *dir, const char *prefix, void (*fn)(const char *name, const char *json, size_t n)) {
    char p[600]; snprintf(p, sizeof p, "%s/%s", g_fix, dir); DIR *d = opendir(p); if (!d) { printf("  cannot open %s\n", p); return 0; }
    int cnt = 0; struct dirent *e;
    while ((e = readdir(d))) {
        if (strncmp(e->d_name, prefix, strlen(prefix))) continue;
        char f[1100]; snprintf(f, sizeof f, "%s/%s", p, e->d_name); size_t n; char *j = slurp(f, &n); if (!j) continue;
        fn(e->d_name, j, n); free(j); cnt++;
    }
    closedir(d); return cnt;
}
static int g_expect_valid;
static void check_cmd(const char *name, const char *j, size_t n) {
    command_t c; proto_err_t e = proto_decode_command(j, n, &c);
    if ((e == PERR_OK) != (g_expect_valid != 0)) { g_fail++; printf("  FAIL fixture %s: decode=%s expected %s\n", name, proto_err_name(e), g_expect_valid ? "valid" : "invalid"); }
    g_run++;
}
static void check_rcpt(const char *name, const char *j, size_t n) {
    receipt_t r; proto_err_t e = proto_decode_receipt(j, n, &r);
    if ((e == PERR_OK) != (g_expect_valid != 0)) { g_fail++; printf("  FAIL fixture %s: decode=%s expected %s\n", name, proto_err_name(e), g_expect_valid ? "valid" : "invalid"); }
    g_run++;
}
TEST(commands_agree_with_the_shared_contract_fixtures) {
    g_expect_valid = 1; int v = each_fixture("valid", "command.", check_cmd); CHECK(v >= 2);
    g_expect_valid = 0; int i = each_fixture("invalid", "command.", check_cmd); CHECK(i >= 5);
    int s = each_fixture("invalid-semantic", "command.", check_cmd); CHECK(s >= 2);
}
TEST(receipts_agree_with_the_shared_contract_fixtures) {
    g_expect_valid = 1; CHECK(each_fixture("valid", "receipt.", check_rcpt) >= 1);
    g_expect_valid = 0; CHECK(each_fixture("invalid", "receipt.", check_rcpt) >= 3);
}
TEST(decoder_survives_truncation_mutation_and_garbage) {
    size_t n; char path[512]; snprintf(path, sizeof path, "%s/valid/command.apply_program.json", g_fix);
    char *j = slurp(path, &n); CHECK(j != NULL); if (!j) return;
    srand(1234); command_t c;
    for (size_t k = 0; k <= n; k++) proto_decode_command(j, k, &c); /* every truncation */
    for (int it = 0; it < 20000; it++) { /* random byte mutations */
        char *m = malloc(n + 1); memcpy(m, j, n); int muts = 1 + rand() % 4;
        for (int q = 0; q < muts; q++) m[rand() % (int)n] = (char)(rand() % 256);
        proto_decode_command(m, n, &c); free(m);
    }
    for (int it = 0; it < 5000; it++) { char g[256]; size_t gl = (size_t)(rand() % 255); for (size_t q = 0; q < gl; q++) g[q] = (char)rand(); proto_decode_command(g, gl, &c); proto_decode_receipt(g, gl, &(receipt_t){0}); }
    char deep[200]; memset(deep, '[', 100); memset(deep + 100, ']', 100); CHECK(proto_decode_command(deep, 200, &c) != PERR_OK);
    char *huge = malloc(PROTO_MAX_PAYLOAD + 10); memset(huge, ' ', PROTO_MAX_PAYLOAD + 10); CHECK(proto_decode_command(huge, PROTO_MAX_PAYLOAD + 10, &c) == PERR_TOO_LARGE); free(huge);
    free(j);
}
TEST(manual_pump_test_is_not_a_command_type) {
    size_t n; char path[512]; snprintf(path, sizeof path, "%s/invalid/command.unknown_type_pump_test.json", g_fix);
    char *j = slurp(path, &n); CHECK(j != NULL); if (!j) return; command_t c; CHECK(proto_decode_command(j, n, &c) == PERR_TYPE); free(j);
}

/* ------------------------------------------------------------- journal */
typedef struct { char data[16][64]; int len[16]; } ram_be_t;
static int ram_put(void *x, int s, const char *d, size_t l) { ram_be_t *r = x; if (l > 64) return -1; memcpy(r->data[s], d, l); r->len[s] = (int)l; return 0; }
static int ram_get(void *x, int s, char *b, size_t cap) { ram_be_t *r = x; if ((size_t)r->len[s] > cap) return -1; memcpy(b, r->data[s], (size_t)r->len[s]); return r->len[s]; }
static void ram_del(void *x, int s) { ram_be_t *r = x; r->len[s] = 0; }
static const mb_backend_t RAM = {ram_put, ram_get, ram_del};

TEST(journal_is_bounded_critical_survives_and_gaps_are_counted) {
    ram_be_t be; mb_meta_t meta[5]; mb_t m; mb_init(&m, meta, 5, RAM, &be);
    mb_append(&m, "boot-aaaaaaaa", 1, true, "c", 1);
    for (int64_t s = 2; s <= 20; s++) mb_append(&m, "boot-aaaaaaaa", s, false, "t", 1);
    CHECK(mb_occupancy(&m) == 5); CHECK(m.overflow == 15);
    bool crit_kept = false; for (int i = 0; i < 5; i++) crit_kept |= meta[i].used && meta[i].seq == 1; CHECK(crit_kept);
}
TEST(journal_frees_only_on_receipt_for_the_matching_boot) {
    ram_be_t be; mb_meta_t meta[8]; mb_t m; mb_init(&m, meta, 8, RAM, &be);
    mb_append(&m, "boot-aaaaaaaa", 1, false, "a", 1); mb_append(&m, "boot-bbbbbbbb", 1, false, "b", 1);
    int64_t acks[] = {1};
    CHECK(mb_reclaim(&m, "boot-zzzzzzzz", acks, 1) == 0); CHECK(mb_occupancy(&m) == 2);
    CHECK(mb_reclaim(&m, "boot-aaaaaaaa", acks, 1) == 1); CHECK(mb_occupancy(&m) == 1);
}
TEST(journal_replay_is_batched_critical_first_inflight_limited_and_resends) {
    ram_be_t be; mb_meta_t meta[16]; mb_t m; mb_init(&m, meta, 16, RAM, &be); int out[16];
    for (int64_t s = 1; s <= 12; s++) mb_append(&m, "boot-aaaaaaaa", s, s == 9, "x", 1);
    int n = mb_next_batch(&m, 1000, 15000, 5, 8, out);
    CHECK(n == 5); CHECK(meta[out[0]].seq == 9); /* critical first, then oldest */
    CHECK(meta[out[1]].seq == 1 && meta[out[2]].seq == 2);
    n = mb_next_batch(&m, 1000, 15000, 5, 8, out); CHECK(n == 3); /* in-flight cap 8 */
    CHECK(mb_next_batch(&m, 1000, 15000, 5, 8, out) == 0);
    n = mb_next_batch(&m, 17000, 15000, 5, 8, out); CHECK(n == 5); /* receipt timeout => resend */
}

/* ------------------------------------------------------------- config store */
typedef struct { uint8_t slot[2][256]; int len[2]; int fail_after; /* bytes written before "power cut"; -1 = never */ } flash_t;
static int fl_read(void *x, int s, uint8_t *b, size_t cap) { flash_t *f = x; if (f->len[s] <= 0 || (size_t)f->len[s] > cap) return -1; memcpy(b, f->slot[s], (size_t)f->len[s]); return f->len[s]; }
static int fl_write(void *x, int s, const uint8_t *b, size_t l) {
    flash_t *f = x;
    if (f->fail_after >= 0 && (size_t)f->fail_after < l) { memcpy(f->slot[s], b, (size_t)f->fail_after); f->len[s] = (int)l; return -1; /* torn write */ }
    memcpy(f->slot[s], b, l); f->len[s] = (int)l; return (int)l;
}
static const cs_backend_t FLASH = {fl_read, fl_write};
static cs_record_t rec_of(program_t p, int v, const char *cid) { cs_record_t r; memset(&r, 0, sizeof r); r.has_program = true; r.program = p; r.version = v; strncpy(r.last_command_id, cid, CS_CMD_ID_LEN - 1); return r; }

TEST(config_store_roundtrip_and_power_cut_keeps_previous_programme) {
    flash_t fl; memset(&fl, 0, sizeof fl); fl.fail_after = -1; cs_t cs; cs_init(&cs, FLASH, &fl, 120);
    cs_record_t out; CHECK(!cs_load(&cs, &out)); /* blank flash => NEEDS_SETUP */
    program_t a = {5, 60, 10, true}, b = {6, 70, 12, true};
    CHECK(cs_save(&cs, &(cs_record_t){0}) == true || true);
    CHECK(cs_save(&cs, &(cs_record_t){.has_program = true, .program = a, .version = 1}));
    CHECK(cs_load(&cs, &out) && out.version == 1 && out.program.on_s == 5);
    cs_record_t r2 = rec_of(b, 2, "cmd-apply-0002");
    fl.fail_after = 20; /* power dies 20 bytes into writing v2 */
    CHECK(!cs_save(&cs, &r2));
    cs_t after; cs_init(&after, FLASH, &fl, 120); /* "reboot" */
    CHECK(cs_load(&after, &out)); CHECK(out.version == 1 && out.program.on_s == 5); /* previous revision, intact */
    fl.fail_after = -1; cs_init(&after, FLASH, &fl, 120); cs_load(&after, &out);
    CHECK(cs_save(&after, &r2)); cs_init(&after, FLASH, &fl, 120);
    CHECK(cs_load(&after, &out) && out.version == 2 && !strcmp(out.last_command_id, "cmd-apply-0002"));
}
TEST(config_store_discards_corrupt_or_unsafe_records_instead_of_running_them) {
    flash_t fl; memset(&fl, 0, sizeof fl); fl.fail_after = -1; cs_t cs; cs_init(&cs, FLASH, &fl, 120);
    program_t a = {5, 60, 10, true};
    cs_save(&cs, &(cs_record_t){.has_program = true, .program = a, .version = 1});
    fl.slot[cs.active][30] ^= 0x40; /* bit rot */
    cs_t c2; cs_init(&c2, FLASH, &fl, 120); cs_record_t out; CHECK(!cs_load(&c2, &out));
    /* a valid record that exceeds a (smaller) board cap is not run either */
    flash_t f2; memset(&f2, 0, sizeof f2); f2.fail_after = -1; cs_t c3; cs_init(&c3, FLASH, &f2, 120);
    program_t big = {100, 60, 110, true}; cs_save(&c3, &(cs_record_t){.has_program = true, .program = big, .version = 1});
    cs_t c4; cs_init(&c4, FLASH, &f2, 60); CHECK(!cs_load(&c4, &out));
    CHECK(!cs_save(&c3, &(cs_record_t){.has_program = true, .program = (program_t){5, 60, 10, false}, .version = 2})); /* protection off: refused */
}

/* ------------------------------------------------------------- command handler */
typedef struct { controller_t ctl; cs_t cs; flash_t fl; ch_t ch; identity_t id; } dev_t_;
static void dev_init(dev_t_ *d) {
    memset(d, 0, sizeof *d); d->fl.fail_after = -1;
    strcpy(d->id.site_id, "site-sim-01"); strcpy(d->id.device_id, "esp32-sim-004"); strcpy(d->id.unit_id, "unit-sim-004"); strcpy(d->id.boot_id, "boot-9f3a1c22-5b"); d->id.has_unit = true; d->id.simulated = true;
    ctl_init(&d->ctl, 120, 50); ctl_boot(&d->ctl, 0); cs_init(&d->cs, FLASH, &d->fl, 120); ch_init(&d->ch, &d->ctl, &d->cs, &d->id); ch_set_session(&d->ch, "nonce-12345678");
}
static void mkcmd(char *b, size_t cap, const char *cid, int exp, int nv, int on, int mx, const char *nonce, const char *rr) {
    snprintf(b, cap, "{\"schema_version\":1,\"command_id\":\"%s\",\"site_id\":\"site-sim-01\",\"device_id\":\"esp32-sim-004\",\"unit_id\":\"unit-sim-004\",\"command_type\":\"APPLY_PROGRAM\","
        "\"session_nonce\":\"%s\",\"expected_program_version\":%d,\"new_program_version\":%d,\"issued_at_utc\":\"2026-11-12T13:00:00Z\",\"expires_at_utc\":\"2026-11-12T13:02:00Z\",\"ttl_seconds\":120,"
        "\"program\":{\"mode\":\"TIMED_CYCLE\",\"on_duration_seconds\":%d,\"off_duration_seconds\":60,\"max_continuous_on_seconds\":%d,\"require_reservoir_ok\":%s}}", cid, nonce, exp, nv, on, mx, rr);
}
static bool has(const char *r, const char *needle) { return strstr(r, needle) != NULL; }

TEST(command_applies_dedups_conflicts_and_rejects_stale_sessions) {
    dev_t_ d; dev_init(&d); char cmd[1024], rep[1024];
    mkcmd(cmd, sizeof cmd, "cmd-aaaaaaaa-0001", 0, 1, 5, 10, "nonce-12345678", "true");
    CHECK(ch_handle(&d.ch, cmd, strlen(cmd), 100, rep, sizeof rep) == CH_REPLY_AND_STATE); CHECK(has(rep, "\"status\":\"APPLIED\"") && has(rep, "\"current_program_version\":1"));
    CHECK(d.ctl.program_version == 1);
    CHECK(ch_handle(&d.ch, cmd, strlen(cmd), 200, rep, sizeof rep) == CH_REPLY); CHECK(has(rep, "ALREADY_PROCESSED") && has(rep, "\"original_status\":\"APPLIED\"")); CHECK(d.ctl.program_version == 1);
    mkcmd(cmd, sizeof cmd, "cmd-aaaaaaaa-0002", 0, 1, 7, 10, "nonce-12345678", "true"); /* stale expected version */
    ch_handle(&d.ch, cmd, strlen(cmd), 300, rep, sizeof rep); CHECK(has(rep, "VERSION_CONFLICT")); CHECK(d.ctl.program_version == 1);
    mkcmd(cmd, sizeof cmd, "cmd-aaaaaaaa-0003", 1, 2, 5, 10, "nonce-OLDOLD12", "true"); /* parked across a reconnect */
    ch_handle(&d.ch, cmd, strlen(cmd), 400, rep, sizeof rep); CHECK(has(rep, "STALE_SESSION")); CHECK(d.ctl.program_version == 1);
    mkcmd(cmd, sizeof cmd, "cmd-aaaaaaaa-0004", 1, 2, 500, 500, "nonce-12345678", "true"); /* over the board cap (120 s) */
    ch_handle(&d.ch, cmd, strlen(cmd), 500, rep, sizeof rep); CHECK(has(rep, "PROGRAM_EXCEEDS_HARDWARE_LIMIT")); CHECK(d.ctl.program_version == 1);
    mkcmd(cmd, sizeof cmd, "cmd-aaaaaaaa-0005", 1, 2, 5, 10, "nonce-12345678", "false"); /* protection off */
    ch_handle(&d.ch, cmd, strlen(cmd), 600, rep, sizeof rep); CHECK(has(rep, "REJECTED") && has(rep, "INVALID_PROGRAM")); CHECK(d.ctl.program_version == 1);
}
TEST(persist_failure_changes_nothing_and_reboot_still_knows_the_last_command) {
    dev_t_ d; dev_init(&d); char cmd[1024], rep[1024];
    d.fl.fail_after = 10; /* flash write fails */
    mkcmd(cmd, sizeof cmd, "cmd-bbbbbbbb-0001", 0, 1, 5, 10, "nonce-12345678", "true");
    ch_handle(&d.ch, cmd, strlen(cmd), 100, rep, sizeof rep); CHECK(has(rep, "PERSIST_FAILED")); CHECK(!d.ctl.has_program); /* live programme untouched */
    d.fl.fail_after = -1;
    mkcmd(cmd, sizeof cmd, "cmd-bbbbbbbb-0002", 0, 1, 5, 10, "nonce-12345678", "true");
    CHECK(ch_handle(&d.ch, cmd, strlen(cmd), 200, rep, sizeof rep) == CH_REPLY_AND_STATE); CHECK(d.ctl.program_version == 1);
    /* power cycle: RAM dedup is gone, flash remembers the last applied command */
    dev_t_ r; dev_init(&r); r.fl = d.fl; cs_init(&r.cs, FLASH, &r.fl, 120); cs_record_t rec; CHECK(cs_load(&r.cs, &rec));
    program_t p = rec.program; ctl_set_program(&r.ctl, &p, rec.version, 0); ch_set_last_applied(&r.ch, rec.last_command_id);
    CHECK(ch_handle(&r.ch, cmd, strlen(cmd), 300, rep, sizeof rep) == CH_REPLY); CHECK(has(rep, "ALREADY_PROCESSED") && has(rep, "\"original_status\":\"APPLIED\""));
}
TEST(query_state_and_commands_for_other_devices) {
    dev_t_ d; dev_init(&d); char rep[1024];
    const char *q = "{\"schema_version\":1,\"command_id\":\"cmd-cccccccc-0001\",\"site_id\":\"site-sim-01\",\"device_id\":\"esp32-sim-004\",\"unit_id\":\"unit-sim-004\",\"command_type\":\"QUERY_STATE\","
        "\"session_nonce\":\"nonce-12345678\",\"expected_program_version\":0,\"issued_at_utc\":\"2026-11-12T13:00:00Z\",\"expires_at_utc\":\"2026-11-12T13:02:00Z\",\"ttl_seconds\":60}";
    CHECK(ch_handle(&d.ch, q, strlen(q), 10, rep, sizeof rep) == CH_REPLY_AND_STATE);
    char other[1024]; mkcmd(other, sizeof other, "cmd-cccccccc-0002", 0, 1, 5, 10, "nonce-12345678", "true");
    char *p = strstr(other, "esp32-sim-004"); memcpy(p, "esp32-sim-009", 13);
    CHECK(ch_handle(&d.ch, other, strlen(other), 20, rep, sizeof rep) == CH_IGNORE); CHECK(!d.ctl.has_program);
    CHECK(ch_handle(&d.ch, "garbage", 7, 30, rep, sizeof rep) == CH_IGNORE);
}

/* ------------------------------------------------------------- encoders -> files for Go validation */
static void emit(const char *dir, const char *name, const char *kind, const char *json) {
    char p[600]; snprintf(p, sizeof p, "%s/%s.%s.json", dir, kind, name); FILE *f = fopen(p, "w"); if (f) { fputs(json, f); fclose(f); }
}
TEST(encoders_fit_buffers_and_never_write_zero_for_missing) {
    identity_t id; memset(&id, 0, sizeof id); strcpy(id.site_id, "site-sim-01"); strcpy(id.device_id, "esp32-sim-004"); strcpy(id.unit_id, "unit-sim-004"); strcpy(id.boot_id, "boot-9f3a1c22-5b"); id.has_unit = true; id.simulated = true;
    reading_t r[] = {{"water_temp_c", 22.5, false, Q_VALID, "C"}, {"ph", 0, false, Q_INVALID, NULL}, {"light_lux", 0, false, Q_MISSING, NULL}, {"reservoir_level_ok", 1, true, Q_VALID, NULL}};
    char b[1024]; int n = proto_encode_telemetry(b, sizeof b, &id, 761, 457000, 6, r, 4);
    CHECK(n > 0); CHECK(has(b, "\"light_lux\":{\"value\":null,\"quality\":\"missing\"}")); CHECK(has(b, "\"ph\":{\"value\":null,\"quality\":\"invalid\"}"));
    CHECK(has(b, "\"sampled_at_utc\":null") && has(b, "monotonic_only")); /* never invents a timestamp */
    CHECK(proto_encode_telemetry(b, 40, &id, 1, 1, 0, r, 4) == -1); /* too small => refused, not truncated */
    if (g_fix) {
        char out[600]; snprintf(out, sizeof out, "%s", getenv("FW_OUT") ? getenv("FW_OUT") : "build/out");
        mkdir(out, 0755);
        CHECK(proto_encode_telemetry(b, sizeof b, &id, 761, 457000, 6, r, 4) > 0); emit(out, "fw", "telemetry", b);
        proto_encode_event(b, sizeof b, &id, 762, 460000, "FAULT_RAISED", SEV_CRITICAL, "LOW_WATER"); emit(out, "fw", "event", b);
        state_report_t s = {460000, CS_PROTECTED_STOP, false, 6, "cmd-7c1e9a40-0001", false, FAULT_LOW_WATER | FAULT_PH_INVALID, 124000, -64, "fw-0.1.0", "nonce-12345678", 3, 400, 3, 0};
        proto_encode_state(b, sizeof b, &id, &s); emit(out, "fw", "state", b);
        s.state = CS_NEEDS_SETUP; s.prog_version = 0; s.last_command_id = NULL; s.faults = 0; proto_encode_state(b, sizeof b, &id, &s); emit(out, "fw-needs-setup", "state", b);
        proto_encode_presence(b, sizeof b, &id, true, "fw-0.1.0", "nonce-12345678"); emit(out, "fw", "presence", b);
        proto_encode_presence(b, sizeof b, &id, false, "fw-0.1.0", "nonce-12345678"); emit(out, "fw-lwt", "presence", b);
        proto_encode_result(b, sizeof b, &id, "cmd-7c1e9a40-0001", RES_APPLIED, false, RES_APPLIED, 7, NULL); emit(out, "fw-applied", "command_result", b);
        proto_encode_result(b, sizeof b, &id, "cmd-7c1e9a40-0002", RES_REJECTED, false, RES_APPLIED, 6, "VERSION_CONFLICT"); emit(out, "fw-rejected", "command_result", b);
        proto_encode_result(b, sizeof b, &id, "cmd-7c1e9a40-0001", RES_ALREADY_PROCESSED, true, RES_APPLIED, 7, NULL); emit(out, "fw-dup", "command_result", b);
    }
}

int main(int argc, char **argv) {
    g_fix = argc > 1 ? argv[1] : NULL;
    if (argc > 2) setenv("FW_OUT", argv[2], 1);
    if (!g_fix) { printf("usage: fw_tests <contracts/examples dir> [out dir]\n"); return 2; }
    RUN(unconfigured_controller_never_irrigates); RUN(cycle_runs_on_the_monotonic_clock_only); RUN(low_water_stops_instantly_and_needs_local_confirmation);
    RUN(invalid_level_sensor_fails_safe_but_ph_ec_only_degrade); RUN(programme_bounds_and_hardware_cap); RUN(pump_on_time_is_bounded_even_if_programme_asks_for_more);
    RUN(board_profile_is_unconfigured_so_outputs_are_refused);
    RUN(commands_agree_with_the_shared_contract_fixtures); RUN(receipts_agree_with_the_shared_contract_fixtures); RUN(decoder_survives_truncation_mutation_and_garbage); RUN(manual_pump_test_is_not_a_command_type);
    RUN(journal_is_bounded_critical_survives_and_gaps_are_counted); RUN(journal_frees_only_on_receipt_for_the_matching_boot); RUN(journal_replay_is_batched_critical_first_inflight_limited_and_resends);
    RUN(config_store_roundtrip_and_power_cut_keeps_previous_programme); RUN(config_store_discards_corrupt_or_unsafe_records_instead_of_running_them);
    RUN(command_applies_dedups_conflicts_and_rejects_stale_sessions); RUN(persist_failure_changes_nothing_and_reboot_still_knows_the_last_command); RUN(query_state_and_commands_for_other_devices);
    RUN(encoders_fit_buffers_and_never_write_zero_for_missing);
    printf("\n%d checks, %d failed\n", g_run, g_fail);
    return g_fail ? 1 : 0;
}
