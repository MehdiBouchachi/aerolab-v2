#include "message_buffer.h"
#include <string.h>

void mb_init(mb_t *m, mb_meta_t *st, int cap, mb_backend_t be, void *ctx) {
    memset(st, 0, sizeof(mb_meta_t) * (size_t)cap);
    m->meta = st; m->cap = cap; m->be = be; m->ctx = ctx; m->next_order = 1; m->overflow = 0;
}

int mb_occupancy(const mb_t *m) { int n = 0; for (int i = 0; i < m->cap; i++) n += m->meta[i].used; return n; }

static int find_free(mb_t *m) { for (int i = 0; i < m->cap; i++) if (!m->meta[i].used) return i; return -1; }

static int victim(mb_t *m) {
    int best = -1;
    for (int i = 0; i < m->cap; i++) /* oldest non-critical first */
        if (m->meta[i].used && !m->meta[i].critical && (best < 0 || m->meta[i].order < m->meta[best].order)) best = i;
    if (best >= 0) return best;
    for (int i = 0; i < m->cap; i++) /* everything critical: drop the oldest, still counted */
        if (m->meta[i].used && (best < 0 || m->meta[i].order < m->meta[best].order)) best = i;
    return best;
}

int mb_append(mb_t *m, const char *boot, int64_t seq, bool critical, const char *data, size_t len) {
    if (len > 0xFFFF) return -1;
    int s = find_free(m);
    if (s < 0) {
        s = victim(m);
        if (s < 0) return -1;
        m->be.del(m->ctx, s);
        m->meta[s].used = false;
        m->overflow++;
    }
    if (m->be.put(m->ctx, s, data, len) < 0) return -2; /* storage failure: caller reports, control is unaffected */
    mb_meta_t *e = &m->meta[s];
    e->used = true; e->critical = critical; e->seq = seq; e->order = m->next_order++; e->last_sent_ms = 0; e->len = (uint16_t)len;
    strncpy(e->boot, boot, MB_BOOT_LEN - 1); e->boot[MB_BOOT_LEN - 1] = 0;
    return s;
}

int mb_reclaim(mb_t *m, const char *boot, const int64_t *seqs, int n) {
    int freed = 0;
    for (int i = 0; i < m->cap; i++) {
        mb_meta_t *e = &m->meta[i];
        if (!e->used || strcmp(e->boot, boot)) continue; /* a receipt only covers its own boot */
        for (int k = 0; k < n; k++)
            if (seqs[k] == e->seq) { m->be.del(m->ctx, i); e->used = false; freed++; break; }
    }
    return freed;
}

int mb_next_batch(mb_t *m, uint64_t now, uint32_t resend_ms, int limit, int max_inflight, int *out) {
    int inflight = 0;
    for (int i = 0; i < m->cap; i++)
        if (m->meta[i].used && m->meta[i].last_sent_ms && now - m->meta[i].last_sent_ms < resend_ms) inflight++;
    int room = max_inflight - inflight;
    if (room <= 0) return 0;
    if (room < limit) limit = room;
    int n = 0;
    for (int pass = 0; pass < 2 && n < limit; pass++) { /* pass 0: critical, pass 1: the rest */
        for (;;) {
            int best = -1;
            for (int i = 0; i < m->cap; i++) {
                mb_meta_t *e = &m->meta[i];
                if (!e->used || (pass == 0) != e->critical) continue;
                if (e->last_sent_ms && now - e->last_sent_ms < resend_ms) continue;
                bool taken = false;
                for (int k = 0; k < n; k++) taken |= out[k] == i;
                if (taken) continue;
                if (best < 0 || e->order < m->meta[best].order) best = i;
            }
            if (best < 0 || n >= limit) break;
            out[n++] = best;
        }
    }
    for (int k = 0; k < n; k++) m->meta[out[k]].last_sent_ms = now ? now : 1;
    return n;
}

int mb_read(mb_t *m, int slot, char *buf, size_t cap) { return m->meta[slot].used ? m->be.get(m->ctx, slot, buf, cap) : -1; }
