/*
 * First-time setup and Wi-Fi bring-up (ESP-IDF glue). The decoding and validation it relies on is the
 * portable, host-tested components/protocol/src/setup_code.c.
 *
 * STATUS: compiles with ESP-IDF v5.3; NOT yet run on an ESP32. The control task is independent of
 * everything here: an unconfigured controller has no programme, so it never irrigates while in setup.
 */
#include "setup_portal.h"
#include <stdio.h>
#include <string.h>
#include "esp_event.h"
#include "esp_http_server.h"
#include "esp_log.h"
#include "esp_mac.h"
#include "esp_netif.h"
#include "esp_system.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs.h"
#include "sdkconfig.h"
#include "setup_code.h"

static const char *TAG = "setup";

static bool has(nvs_handle_t h, const char *k) {
    size_t l = 0;
    return nvs_get_str(h, k, NULL, &l) == ESP_OK && l > 1;
}

bool setup_is_provisioned(void) {
    nvs_handle_t h;
    if (nvs_open("aero", NVS_READONLY, &h) != ESP_OK) return false;
    bool ok = has(h, "site_id") && has(h, "device_id") && has(h, "mqtt_uri") && has(h, "mqtt_user") && has(h, "mqtt_pass") && has(h, "wifi_ssid");
    nvs_close(h);
    return ok;
}

static esp_err_t save(const setup_t *s) {
    nvs_handle_t h;
    esp_err_t e = nvs_open("aero", NVS_READWRITE, &h);
    if (e != ESP_OK) return e;
    const char *kv[][2] = {{"site_id", s->site_id}, {"device_id", s->device_id}, {"unit_id", s->unit_id}, {"mqtt_uri", s->mqtt_uri},
                           {"mqtt_user", s->mqtt_user}, {"mqtt_pass", s->mqtt_pass}, {"wifi_ssid", s->wifi_ssid}, {"wifi_pass", s->wifi_pass}};
    for (size_t i = 0; i < sizeof kv / sizeof *kv && e == ESP_OK; i++) e = nvs_set_str(h, kv[i][0], kv[i][1]);
    if (e == ESP_OK) e = nvs_commit(h); /* one commit: all keys or (after a power cut) the old state */
    nvs_close(h);
    return e;
}

static const char PAGE_HEAD[] =
    "<!doctype html><html lang=en><head><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'>"
    "<title>AeroLab controller setup</title><style>body{font:16px system-ui,sans-serif;max-width:30rem;margin:1rem auto;padding:0 1rem}"
    "label{display:block;margin:.8rem 0 .2rem}input,textarea{width:100%;font:inherit;padding:.5rem;box-sizing:border-box}"
    "button{margin-top:1rem;padding:.7rem 1.2rem;font:inherit}.m{padding:.6rem;border:1px solid #888;border-radius:6px}</style></head><body>"
    "<h1>Set up this AeroLab controller</h1>";
static const char PAGE_FORM[] =
    "<p>On the Pi dashboard choose <b>Add a unit</b> (or <b>New setup code</b> on the unit page) and copy the setup code.</p>"
    "<form method=post action=/setup><label for=s>Farm Wi-Fi name</label><input id=s name=ssid maxlength=32 required autocomplete=off>"
    "<label for=p>Wi-Fi password (empty for an open network)</label><input id=p name=pass type=password maxlength=64 autocomplete=off>"
    "<label for=c>Setup code</label><textarea id=c name=code rows=5 required autocomplete=off></textarea>"
    "<button>Save and restart</button></form></body></html>";

static esp_err_t page(httpd_req_t *r, const char *status, const char *msg, bool form) {
    httpd_resp_set_status(r, status);
    httpd_resp_set_type(r, "text/html; charset=utf-8");
    httpd_resp_set_hdr(r, "Cache-Control", "no-store");
    httpd_resp_sendstr_chunk(r, PAGE_HEAD);
    if (msg) { /* messages are fixed strings from setup_err_text(): nothing user-supplied is echoed */
        httpd_resp_sendstr_chunk(r, "<p class=m role=alert>");
        httpd_resp_sendstr_chunk(r, msg);
        httpd_resp_sendstr_chunk(r, "</p>");
    }
    if (form) httpd_resp_sendstr_chunk(r, PAGE_FORM);
    else httpd_resp_sendstr_chunk(r, "</body></html>");
    return httpd_resp_sendstr_chunk(r, NULL);
}

static esp_err_t get_root(httpd_req_t *r) { return page(r, "200 OK", NULL, true); }

static void restart_task(void *arg) {
    (void)arg;
    vTaskDelay(pdMS_TO_TICKS(1500)); /* let the response reach the phone */
    esp_restart();
}

static esp_err_t post_setup(httpd_req_t *r) {
    static char body[1600]; /* one request at a time (max_open_sockets is small); keeps the stack small */
    static setup_t s;
    if (r->content_len == 0 || r->content_len >= sizeof body) return page(r, "413 Payload Too Large", setup_err_text(SETUP_BAD_FORM), true);
    size_t got = 0;
    while (got < r->content_len) {
        int n = httpd_req_recv(r, body + got, r->content_len - got);
        if (n == HTTPD_SOCK_ERR_TIMEOUT) continue;
        if (n <= 0) return ESP_FAIL;
        got += (size_t)n;
    }
    body[got] = 0;
    setup_err_t e = setup_parse_form(body, got, &s);
    memset(body, 0, sizeof body); /* the body held passwords */
    if (e != SETUP_OK) return page(r, "400 Bad Request", setup_err_text(e), true);
    esp_err_t se = save(&s);
    ESP_LOGI(TAG, "setup %s for device %s", se == ESP_OK ? "saved" : "NOT saved", s.device_id);
    memset(&s, 0, sizeof s);
    if (se != ESP_OK) return page(r, "500 Internal Server Error", "The controller could not save the setup. Try again; if it keeps failing the controller's flash may be faulty.", true);
    page(r, "200 OK", setup_err_text(SETUP_OK), false);
    xTaskCreate(restart_task, "restart", 2048, NULL, 5, NULL);
    return ESP_OK;
}

void setup_portal_start(void) {
    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    esp_netif_create_default_wifi_ap();
    wifi_init_config_t wc = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&wc));
    uint8_t mac[6] = {0};
    esp_read_mac(mac, ESP_MAC_WIFI_SOFTAP);
    wifi_config_t ap = {0};
    int n = snprintf((char *)ap.ap.ssid, sizeof ap.ap.ssid, "AeroLab-Setup-%02X%02X", mac[4], mac[5]);
    ap.ap.ssid_len = (uint8_t)n;
    ap.ap.channel = 1;
    ap.ap.max_connection = 2;
    const char *pw = CONFIG_AEROLAB_SETUP_AP_PASSWORD;
    if (strlen(pw) >= 8) {
        strncpy((char *)ap.ap.password, pw, sizeof ap.ap.password - 1);
        ap.ap.authmode = WIFI_AUTH_WPA2_PSK;
    } else {
        ap.ap.authmode = WIFI_AUTH_OPEN; /* see README: set CONFIG_AEROLAB_SETUP_AP_PASSWORD for production */
        ESP_LOGW(TAG, "setup network is OPEN (no CONFIG_AEROLAB_SETUP_AP_PASSWORD)");
    }
    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_AP));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_AP, &ap));
    ESP_ERROR_CHECK(esp_wifi_start());
    httpd_config_t hc = HTTPD_DEFAULT_CONFIG();
    hc.max_open_sockets = 3;
    hc.lru_purge_enable = true;
    httpd_handle_t srv = NULL;
    if (httpd_start(&srv, &hc) != ESP_OK) {
        ESP_LOGE(TAG, "setup page could not start");
        return;
    }
    static const httpd_uri_t root = {.uri = "/", .method = HTTP_GET, .handler = get_root};
    static const httpd_uri_t post = {.uri = "/setup", .method = HTTP_POST, .handler = post_setup};
    httpd_register_uri_handler(srv, &root);
    httpd_register_uri_handler(srv, &post);
    ESP_LOGW(TAG, "UNCONFIGURED: join Wi-Fi '%s' and open http://192.168.4.1", (char *)ap.ap.ssid);
}

// ---------------------------------------------------------------- station mode

static int s_retries;

static void retry_task(void *arg) {
    (void)arg;
    int d = s_retries < 6 ? (1 << s_retries) : 60; /* 1,2,4..32 s then every 60 s */
    vTaskDelay(pdMS_TO_TICKS(d * 1000));
    esp_wifi_connect();
    vTaskDelete(NULL);
}

static void on_wifi(void *arg, esp_event_base_t base, int32_t id, void *data) {
    (void)arg;
    (void)data;
    if (base == WIFI_EVENT && id == WIFI_EVENT_STA_START) {
        esp_wifi_connect();
    } else if (base == WIFI_EVENT && id == WIFI_EVENT_STA_DISCONNECTED) {
        /* never block the event loop: back off in a short-lived task */
        xTaskCreate(retry_task, "wifi_retry", 2048, NULL, 3, NULL);
        if (s_retries < 30) s_retries++;
    } else if (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP) {
        s_retries = 0;
        ESP_LOGI(TAG, "joined the farm Wi-Fi");
    }
}

void wifi_sta_start(void) {
    wifi_config_t sta = {0};
    nvs_handle_t h;
    size_t l;
    if (nvs_open("aero", NVS_READONLY, &h) != ESP_OK) return;
    l = sizeof sta.sta.ssid;
    esp_err_t e = nvs_get_str(h, "wifi_ssid", (char *)sta.sta.ssid, &l);
    l = sizeof sta.sta.password;
    if (nvs_get_str(h, "wifi_pass", (char *)sta.sta.password, &l) != ESP_OK) sta.sta.password[0] = 0;
    nvs_close(h);
    if (e != ESP_OK) return;
    sta.sta.threshold.authmode = sta.sta.password[0] ? WIFI_AUTH_WPA2_PSK : WIFI_AUTH_OPEN;
    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    esp_netif_create_default_wifi_sta();
    wifi_init_config_t wc = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&wc));
    ESP_ERROR_CHECK(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, on_wifi, NULL));
    ESP_ERROR_CHECK(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, on_wifi, NULL));
    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &sta));
    ESP_ERROR_CHECK(esp_wifi_start());
    memset(&sta, 0, sizeof sta);
}
