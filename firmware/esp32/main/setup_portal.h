#pragma once
#include <stdbool.h>
#ifdef __cplusplus
extern "C" {
#endif
/* true when NVS holds an identity, broker credentials and farm Wi-Fi (i.e. setup was completed) */
bool setup_is_provisioned(void);
/* Unconfigured controller: open the "AeroLab-Setup-XXXX" network and serve the setup page at
 * http://192.168.4.1. Saving a valid setup restarts the controller. Never touches the outputs. */
void setup_portal_start(void);
/* Configured controller: join the farm Wi-Fi from NVS and keep reconnecting with backoff. */
void wifi_sta_start(void);
#ifdef __cplusplus
}
#endif
