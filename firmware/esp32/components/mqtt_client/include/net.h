#pragma once
#include "command_handler.h"
#include "message_buffer.h"
#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    identity_t *id;
    ch_t *ch;
    mb_t *mb;
    controller_t *ctl;
    const char *firmware;
    /* called from the net task to build the 5 s heartbeat from a consistent snapshot */
    void (*snapshot)(state_report_t *out);
} net_cfg_t;

/* Reads Wi-Fi + MQTT credentials from NVS namespace "aero" (provisioned per device; never in Git).
 * Starts the network task. The control loop does not depend on it in any way. */
void net_start(const net_cfg_t *cfg);
/* Thread-safe, non-blocking: queue a durable record (telemetry/event) for the journal. false = queue full. */
bool net_enqueue_record(const char *leaf /* "telemetry"|"events" */, int64_t seq, bool critical, const char *json, size_t len);
bool net_connected(void);
#ifdef __cplusplus
}
#endif
