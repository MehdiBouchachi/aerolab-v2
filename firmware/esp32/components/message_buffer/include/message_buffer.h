#pragma once
/*
 * Bounded write-ahead journal for telemetry/events. Metadata lives in RAM; the
 * payload bytes live in a backend (LittleFS on the device, RAM in host tests).
 * A record is freed ONLY by mb_reclaim(), which is called on a Pi durable
 * receipt - never on MQTT PUBACK. When full, the oldest NON-critical record is
 * evicted and counted: a real gap is reported, never hidden.
 */
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define MB_BOOT_LEN 72

typedef struct {
    int (*put)(void *ctx, int slot, const char *data, size_t len);
    int (*get)(void *ctx, int slot, char *buf, size_t cap); /* returns length or <0 */
    void (*del)(void *ctx, int slot);
} mb_backend_t;

typedef struct {
    bool used, critical;
    int64_t seq;
    char boot[MB_BOOT_LEN];
    uint64_t order, last_sent_ms;
    uint16_t len;
} mb_meta_t;

typedef struct {
    mb_meta_t *meta;
    int cap;
    mb_backend_t be;
    void *ctx;
    uint64_t next_order;
    int64_t overflow;
} mb_t;

void mb_init(mb_t *m, mb_meta_t *storage, int cap, mb_backend_t be, void *ctx);
/* returns slot or <0 on backend failure. May evict (counts overflow). */
int mb_append(mb_t *m, const char *boot, int64_t seq, bool critical, const char *data, size_t len);
int mb_reclaim(mb_t *m, const char *boot, const int64_t *seqs, int n); /* returns how many freed */
/* Picks up to `limit` records needing (re)send: never-sent or older than resend_ms; critical first,
 * then oldest. Respects max_inflight (sent but unreceipted within resend_ms). Marks them sent. */
int mb_next_batch(mb_t *m, uint64_t now_ms, uint32_t resend_ms, int limit, int max_inflight, int *slots_out);
int mb_read(mb_t *m, int slot, char *buf, size_t cap);
int mb_occupancy(const mb_t *m);

#ifdef __cplusplus
}
#endif
