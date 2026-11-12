#include "config_store.h"
#include "nvs.h"

/* NVS backend: two blobs "slot0"/"slot1" in namespace "aero". NVS is for low-frequency settings only. */
static int nvs_read(void *ctx, int slot, uint8_t *buf, size_t cap) {
    (void)ctx; nvs_handle_t h;
    if (nvs_open("aero", NVS_READONLY, &h) != ESP_OK) return -1;
    size_t len = cap; char key[8] = {'s','l','o','t','0' + (char)slot, 0};
    esp_err_t e = nvs_get_blob(h, key, buf, &len);
    nvs_close(h);
    return e == ESP_OK ? (int)len : -1;
}
static int nvs_write(void *ctx, int slot, const uint8_t *buf, size_t len) {
    (void)ctx; nvs_handle_t h;
    if (nvs_open("aero", NVS_READWRITE, &h) != ESP_OK) return -1;
    char key[8] = {'s','l','o','t','0' + (char)slot, 0};
    esp_err_t e = nvs_set_blob(h, key, buf, len);
    if (e == ESP_OK) e = nvs_commit(h);
    nvs_close(h);
    return e == ESP_OK ? (int)len : -1;
}
const cs_backend_t CS_NVS_BACKEND = {nvs_read, nvs_write};
