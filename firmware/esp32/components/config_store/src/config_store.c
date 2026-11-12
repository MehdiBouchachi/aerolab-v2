#include "config_store.h"
#include <string.h>

#define MAGIC 0xAE70C0DEu
typedef struct {
    uint32_t magic, generation;
    uint8_t has_program, require_ok, _pad[2];
    uint32_t on_s, off_s, max_s;
    int32_t version;
    char last_command_id[CS_CMD_ID_LEN];
    uint32_t crc;
} slot_t;

uint32_t cs_crc32(const uint8_t *d, size_t n) {
    uint32_t c = 0xFFFFFFFFu;
    for (size_t i = 0; i < n; i++) { c ^= d[i]; for (int k = 0; k < 8; k++) c = (c >> 1) ^ (0xEDB88320u & (0u - (c & 1u))); }
    return ~c;
}

void cs_init(cs_t *c, cs_backend_t be, void *ctx, uint32_t hard) { c->be = be; c->ctx = ctx; c->generation = 0; c->active = -1; c->hard_max_on_s = hard; }

static bool slot_valid(const cs_t *c, const slot_t *s, cs_record_t *rec) {
    if (s->magic != MAGIC) return false;
    if (cs_crc32((const uint8_t *)s, offsetof(slot_t, crc)) != s->crc) return false;
    memset(rec, 0, sizeof *rec);
    rec->has_program = s->has_program;
    memcpy(rec->last_command_id, s->last_command_id, CS_CMD_ID_LEN);
    rec->last_command_id[CS_CMD_ID_LEN - 1] = 0;
    if (s->has_program) {
        rec->program = (program_t){s->on_s, s->off_s, s->max_s, s->require_ok != 0};
        rec->version = s->version;
        /* revalidate on EVERY boot: bounds, protection flag and the board's hard cap */
        if (!program_valid(&rec->program) || rec->version < 1 || c->hard_max_on_s == 0 || rec->program.on_s > c->hard_max_on_s || rec->program.max_continuous_on_s > c->hard_max_on_s) return false;
    }
    return true;
}

bool cs_load(cs_t *c, cs_record_t *out) {
    int best = -1; uint32_t gen = 0; cs_record_t best_rec;
    for (int i = 0; i < 2; i++) {
        slot_t s; cs_record_t r;
        if (c->be.read(c->ctx, i, (uint8_t *)&s, sizeof s) != (int)sizeof s) continue;
        if (!slot_valid(c, &s, &r)) continue;
        if (best < 0 || s.generation > gen) { best = i; gen = s.generation; best_rec = r; }
    }
    if (best < 0) { c->active = -1; return false; }
    c->active = best; c->generation = gen; *out = best_rec;
    return true;
}

bool cs_save(cs_t *c, const cs_record_t *rec) {
    slot_t s; memset(&s, 0, sizeof s);
    if (rec->has_program && !program_valid(&rec->program)) return false;
    s.magic = MAGIC; s.generation = c->generation + 1; s.has_program = rec->has_program;
    if (rec->has_program) { s.require_ok = rec->program.require_reservoir_ok; s.on_s = rec->program.on_s; s.off_s = rec->program.off_s; s.max_s = rec->program.max_continuous_on_s; s.version = rec->version; }
    strncpy(s.last_command_id, rec->last_command_id, CS_CMD_ID_LEN - 1);
    s.crc = cs_crc32((const uint8_t *)&s, offsetof(slot_t, crc));
    int target = c->active == 0 ? 1 : 0; /* never overwrite the slot we are running from */
    if (c->be.write(c->ctx, target, (const uint8_t *)&s, sizeof s) != (int)sizeof s) return false;
    c->active = target; c->generation = s.generation;
    return true;
}
