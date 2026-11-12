#pragma once
/*
 * Wire codec for the AeroLab MQTT contract v1 (contracts/mqtt). Pure C, no
 * allocation, bounded buffers. The same fixtures that the Go validators and the
 * JSON Schemas are tested against are replayed through this code on the host
 * (test/host), so the firmware cannot drift from the contract unnoticed.
 */
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include "controller.h"

#ifdef __cplusplus
extern "C" {
#endif

#define PROTO_MAX_PAYLOAD 8192
#define PROTO_MAX_ID 48
#define PROTO_MAX_TOKEN 72

typedef struct {
    char site_id[PROTO_MAX_ID], device_id[PROTO_MAX_ID], unit_id[PROTO_MAX_ID], boot_id[PROTO_MAX_TOKEN];
    bool has_unit;
    bool simulated; /* data_source: false = real hardware, true = SIMULATOR */
} identity_t;

typedef enum { Q_VALID, Q_INVALID, Q_MISSING, Q_OUTDATED } quality_t;
typedef struct {
    const char *name;
    double value;
    bool is_bool;
    quality_t q;
    const char *unit; /* may be NULL */
} reading_t;

/* ---- encoders: return bytes written (excluding NUL) or -1 if the buffer is too small ---- */
int proto_encode_telemetry(char *buf, size_t cap, const identity_t *id, int64_t seq, int64_t uptime_ms, int prog_version /* 0 = none */,
                           const reading_t *r, int n);
int proto_encode_event(char *buf, size_t cap, const identity_t *id, int64_t seq, int64_t uptime_ms, const char *event_type, severity_t sev,
                       const char *code);
typedef struct {
    int64_t uptime_ms;
    ctl_state_t state;
    bool pump_on;
    int prog_version; /* 0 = none */
    const char *last_command_id; /* NULL = none */
    bool interlocks_ok;
    uint32_t faults;
    int64_t free_heap;
    int rssi_dbm;
    const char *firmware;
    const char *session_nonce;
    int buf_occupancy, buf_capacity, buf_unreceipted;
    int64_t buf_overflow;
} state_report_t;
int proto_encode_state(char *buf, size_t cap, const identity_t *id, const state_report_t *s);
int proto_encode_presence(char *buf, size_t cap, const identity_t *id, bool online, const char *firmware, const char *session_nonce);

typedef enum { RES_APPLIED, RES_REJECTED, RES_ALREADY_PROCESSED, RES_UNKNOWN } result_status_t;
const char *result_status_name(result_status_t s);
int proto_encode_result(char *buf, size_t cap, const identity_t *id, const char *command_id, result_status_t st, bool has_orig,
                        result_status_t orig, int cur_version /* 0 = null */, const char *reason /* NULL = null */);

/* ---- decoders ---- */
typedef enum { CMD_APPLY_PROGRAM, CMD_QUERY_STATE } cmd_type_t;
typedef struct {
    char command_id[PROTO_MAX_TOKEN], site_id[PROTO_MAX_ID], device_id[PROTO_MAX_ID], unit_id[PROTO_MAX_ID], session_nonce[PROTO_MAX_TOKEN];
    cmd_type_t type;
    int expected_version, new_version, ttl_s;
    program_t program;
    bool has_program;
} command_t;

/* 0 = valid. Otherwise a stable negative code; *id_out gets the command id when it could be read
 * so a REJECTED result can still be sent. */
typedef enum {
    PERR_OK = 0, PERR_EMPTY = -1, PERR_TOO_LARGE = -2, PERR_MALFORMED = -3, PERR_TOO_DEEP = -4, PERR_SCHEMA_VERSION = -5, PERR_ID = -6,
    PERR_NONCE = -7, PERR_TTL = -8, PERR_TIME = -9, PERR_EXPIRY = -10, PERR_VERSION = -11, PERR_TYPE = -12, PERR_PROGRAM = -13,
    PERR_UNEXPECTED_PROGRAM = -14
} proto_err_t;
const char *proto_err_name(proto_err_t e);
proto_err_t proto_decode_command(const char *json, size_t len, command_t *out);

typedef struct {
    char site_id[PROTO_MAX_ID], device_id[PROTO_MAX_ID], boot_id[PROTO_MAX_TOKEN];
    int64_t acked[100], rejected[100];
    int n_acked, n_rejected;
} receipt_t;
proto_err_t proto_decode_receipt(const char *json, size_t len, receipt_t *out);

/* helpers shared with tests */
bool proto_valid_id(const char *s);    /* ^[a-z0-9][a-z0-9-]{2,39}$ */
bool proto_valid_token(const char *s); /* ^[A-Za-z0-9-]{8,64}$ */

#ifdef __cplusplus
}
#endif
