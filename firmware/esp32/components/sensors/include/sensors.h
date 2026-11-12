#pragma once
#include "protocol.h"
#include "safety.h"
#ifdef __cplusplus
extern "C" {
#endif
void sensors_init(void);
/* Fill the interlock inputs (every control tick) */
void sensors_read_safety(safety_inputs_t *in);
/* Fill `r` (max n) with the periodic snapshot; returns count. Sensors that are not fitted are MISSING
 * (value null), never 0. */
int sensors_snapshot(reading_t *r, int n);
#ifdef __cplusplus
}
#endif
