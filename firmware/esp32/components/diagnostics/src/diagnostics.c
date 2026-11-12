#include "diagnostics.h"
#include "esp_system.h"
#include "esp_wifi.h"
int64_t diag_free_heap(void) { return (int64_t)esp_get_free_heap_size(); }
int diag_rssi_dbm(void) {
    wifi_ap_record_t ap;
    return esp_wifi_sta_get_ap_info(&ap) == ESP_OK ? ap.rssi : -127;
}
