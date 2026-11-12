#pragma once
/*
 * Device-side command handling. Order of checks (each can end the command):
 *  decode+validate -> addressed to me -> idempotency (last 32) -> current MQTT session nonce
 *  -> QUERY_STATE | APPLY_PROGRAM (expected version, hardware cap, atomic persist) -> reply.
 * Results are produced as JSON ready to publish; the caller decides if it publishes.
 */
#include "controller.h"
#include "config_store.h"
#include "protocol.h"

#ifdef __cplusplus
extern "C" {
#endif

#define CH_PROCESSED_MAX 32
typedef struct { char id[PROTO_MAX_TOKEN]; result_status_t status; char reason[40]; } ch_done_t;

typedef struct {
    controller_t *ctl;
    cs_t *store;
    const identity_t *id;
    char nonce[PROTO_MAX_TOKEN];        /* current MQTT session nonce */
    char last_command_id[CS_CMD_ID_LEN]; /* last APPLIED command (survives reboot via the store) */
    ch_done_t done[CH_PROCESSED_MAX];
    int done_n, done_next;
    /* optional: held ONLY around the live-programme swap (never around flash), so the control tick can
     * try-lock without ever waiting on a flash write */
    void (*lock)(void *);
    void (*unlock)(void *);
    void *lock_ctx;
} ch_t;

void ch_init(ch_t *h, controller_t *c, cs_t *s, const identity_t *id);
void ch_set_session(ch_t *h, const char *nonce);
void ch_set_last_applied(ch_t *h, const char *command_id);

typedef enum { CH_IGNORE, CH_REPLY, CH_REPLY_AND_STATE } ch_action_t;
/* returns what to do; on CH_REPLY*, `reply` holds the command-result JSON. */
ch_action_t ch_handle(ch_t *h, const char *json, size_t len, uint64_t now_ms, char *reply, size_t cap);

#ifdef __cplusplus
}
#endif
