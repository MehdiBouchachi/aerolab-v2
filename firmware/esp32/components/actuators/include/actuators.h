#pragma once
#include <stdbool.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
/* Configure the pump output. If the board is not verified/configured the pin is NEVER driven. */
void act_init(void);
/* Request the pump state. This layer is the SECOND line of defence: independent of the controller it
 * forces the pump OFF if it has been ON longer than the board's hard cap. Safe to call every tick. */
void act_set_pump(bool on, uint64_t now_ms);
bool act_pump_is_on(void);
#ifdef __cplusplus
}
#endif
