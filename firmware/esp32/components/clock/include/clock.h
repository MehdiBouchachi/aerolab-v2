#pragma once
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
/* Monotonic milliseconds since boot. The ONLY time base the control loop uses. */
uint64_t clock_now_ms(void);
/* This build has no trusted absolute clock: telemetry reports clock_quality=monotonic_only and
 * sampled_at_utc=null. An RTC/NTP driver must be added deliberately before that changes. */
#ifdef __cplusplus
}
#endif
