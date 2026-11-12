#include "command_handler.h"
#include "jsonscan.h"
#include <string.h>

void ch_init(ch_t *h, controller_t *c, cs_t *s, const identity_t *id) { memset(h, 0, sizeof *h); h->ctl = c; h->store = s; h->id = id; }
void ch_set_session(ch_t *h, const char *n) { strncpy(h->nonce, n, sizeof h->nonce - 1); }
void ch_set_last_applied(ch_t *h, const char *id) { strncpy(h->last_command_id, id, sizeof h->last_command_id - 1); }

static const ch_done_t *find(const ch_t *h, const char *id) {
    for (int i = 0; i < h->done_n; i++) if (!strcmp(h->done[i].id, id)) return &h->done[i];
    return NULL;
}
static void remember(ch_t *h, const char *id, result_status_t st, const char *reason) {
    ch_done_t *d = &h->done[h->done_next];
    strncpy(d->id, id, sizeof d->id - 1); d->id[sizeof d->id - 1] = 0;
    d->status = st;
    strncpy(d->reason, reason ? reason : "", sizeof d->reason - 1); d->reason[sizeof d->reason - 1] = 0;
    h->done_next = (h->done_next + 1) % CH_PROCESSED_MAX;
    if (h->done_n < CH_PROCESSED_MAX) h->done_n++;
}

static ch_action_t reply(ch_t *h, const char *cid, result_status_t st, const char *reason, bool remember_it, bool state_too, char *out, size_t cap) {
    if (remember_it) remember(h, cid, st, reason);
    int v = h->ctl->has_program ? h->ctl->program_version : 0;
    if (proto_encode_result(out, cap, h->id, cid, st, false, RES_APPLIED, v, reason) < 0) return CH_IGNORE;
    return state_too ? CH_REPLY_AND_STATE : CH_REPLY;
}

ch_action_t ch_handle(ch_t *h, const char *json, size_t len, uint64_t now, char *out, size_t cap) {
    command_t c;
    proto_err_t e = proto_decode_command(json, len, &c);
    if (e != PERR_OK) {
        /* answer REJECTED only if we can still tell WHICH command it was */
        jval_t v; char cid[PROTO_MAX_TOKEN];
        if (len > 0 && len <= PROTO_MAX_PAYLOAD && jscan_get(json, len, "command_id", &v) && jscan_str(&v, cid, sizeof cid) && proto_valid_token(cid)) {
            const ch_done_t *p = find(h, cid);
            if (p) return CH_IGNORE;
            return reply(h, cid, RES_REJECTED, proto_err_name(e), false, false, out, cap);
        }
        return CH_IGNORE;
    }
    if (strcmp(c.device_id, h->id->device_id) || strcmp(c.site_id, h->id->site_id)) return CH_IGNORE; /* not for me */

    const ch_done_t *prev = find(h, c.command_id);
    if (prev) { /* idempotency: original outcome, never re-applied */
        int v = h->ctl->has_program ? h->ctl->program_version : 0;
        if (proto_encode_result(out, cap, h->id, c.command_id, RES_ALREADY_PROCESSED, true, prev->status, v, prev->reason[0] ? prev->reason : NULL) < 0) return CH_IGNORE;
        return CH_REPLY;
    }
    if (!strcmp(c.command_id, h->last_command_id)) { /* known only from flash after a reboot: it WAS applied */
        int v = h->ctl->has_program ? h->ctl->program_version : 0;
        if (proto_encode_result(out, cap, h->id, c.command_id, RES_ALREADY_PROCESSED, true, RES_APPLIED, v, NULL) < 0) return CH_IGNORE;
        return CH_REPLY;
    }
    /* a command parked in the broker across a reconnect carries an old nonce */
    if (strcmp(c.session_nonce, h->nonce)) return reply(h, c.command_id, RES_REJECTED, "STALE_SESSION", true, false, out, cap);

    if (c.type == CMD_QUERY_STATE) return reply(h, c.command_id, RES_APPLIED, NULL, true, true, out, cap);

    int cur = h->ctl->has_program ? h->ctl->program_version : 0;
    if (c.expected_version != cur) return reply(h, c.command_id, RES_REJECTED, "VERSION_CONFLICT", true, false, out, cap);
    if (h->ctl->hard_max_on_s == 0 || c.program.on_s > h->ctl->hard_max_on_s || c.program.max_continuous_on_s > h->ctl->hard_max_on_s)
        return reply(h, c.command_id, RES_REJECTED, "PROGRAM_EXCEEDS_HARDWARE_LIMIT", true, false, out, cap);

    /* persist FIRST, atomically; only then switch the live programme */
    cs_record_t rec; memset(&rec, 0, sizeof rec);
    rec.has_program = true; rec.program = c.program; rec.version = c.new_version;
    strncpy(rec.last_command_id, c.command_id, CS_CMD_ID_LEN - 1);
    if (!cs_save(h->store, &rec)) return reply(h, c.command_id, RES_REJECTED, "PERSIST_FAILED", true, false, out, cap);
    if (h->lock) h->lock(h->lock_ctx);
    bool swapped = ctl_set_program(h->ctl, &c.program, c.new_version, now);
    if (h->unlock) h->unlock(h->lock_ctx);
    if (!swapped) return reply(h, c.command_id, RES_REJECTED, "PERSIST_FAILED", true, false, out, cap);
    ch_set_last_applied(h, c.command_id);
    return reply(h, c.command_id, RES_APPLIED, NULL, true, true, out, cap);
}
