#pragma once
/*
 * Atomic programme persistence. Two slots (A/B). A save writes the INACTIVE
 * slot with generation+1 and a CRC32; a load picks the valid slot with the highest
 * generation. If power fails mid-write, that slot fails its CRC and the previous
 * revision is used - the programme is never half-updated. A stored programme that
 * is invalid or exceeds the board cap is discarded (device stays in NEEDS_SETUP).
 */
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include "controller.h"

#ifdef __cplusplus
extern "C" {
#endif

#define CS_CMD_ID_LEN 72

typedef struct {
    int (*read)(void *ctx, int slot, uint8_t *buf, size_t cap); /* returns bytes or <0 */
    int (*write)(void *ctx, int slot, const uint8_t *buf, size_t len); /* may fail / be interrupted */
} cs_backend_t;

typedef struct {
    bool has_program;
    program_t program;
    int version;
    char last_command_id[CS_CMD_ID_LEN];
} cs_record_t;

typedef struct { cs_backend_t be; void *ctx; uint32_t generation; int active; uint32_t hard_max_on_s; } cs_t;

uint32_t cs_crc32(const uint8_t *d, size_t n);
void cs_init(cs_t *c, cs_backend_t be, void *ctx, uint32_t hard_max_on_s);
bool cs_load(cs_t *c, cs_record_t *out); /* false => nothing valid (NEEDS_SETUP) */
bool cs_save(cs_t *c, const cs_record_t *rec); /* false => nothing changed */

#ifdef __cplusplus
}
#endif
