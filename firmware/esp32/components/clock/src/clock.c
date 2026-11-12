#include "clock.h"
#include "esp_timer.h"
uint64_t clock_now_ms(void) { return (uint64_t)(esp_timer_get_time() / 1000); }
