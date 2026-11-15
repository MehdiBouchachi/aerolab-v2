#include "setup_code.h"
#include <string.h>
#include "jsonscan.h"

#define CODE_MAX 1024 /* encoded length; the Pi's codes are ~270 characters */
#define JSON_MAX 768

bool setup_valid_id(const char *s) {
    size_t n = strlen(s);
    if (n < 3 || n > 40) return false;
    if (!((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= '0' && s[0] <= '9'))) return false;
    for (size_t i = 1; i < n; i++) {
        char c = s[i];
        if (!((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-')) return false;
    }
    return true;
}

static int b64v(char c) {
    if (c >= 'A' && c <= 'Z') return c - 'A';
    if (c >= 'a' && c <= 'z') return c - 'a' + 26;
    if (c >= '0' && c <= '9') return c - '0' + 52;
    if (c == '-' || c == '+') return 62; /* accept both alphabets: codes get copied through many apps */
    if (c == '_' || c == '/') return 63;
    return -1;
}

/* base64url without padding (a trailing '=' is tolerated). Returns decoded length or -1. */
static int b64url_decode(const char *in, size_t n, char *out, size_t cap) {
    while (n > 0 && in[n - 1] == '=') n--;
    if (n % 4 == 1) return -1;
    size_t o = 0;
    unsigned acc = 0;
    int bits = 0;
    for (size_t i = 0; i < n; i++) {
        int v = b64v(in[i]);
        if (v < 0) return -1;
        acc = (acc << 6) | (unsigned)v;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            if (o + 1 >= cap) return -1;
            out[o++] = (char)((acc >> bits) & 0xFF);
        }
    }
    out[o] = 0;
    return (int)o;
}

static bool get_str(const char *js, size_t n, const char *key, char *dst, size_t cap) {
    jval_t v;
    if (!jscan_get(js, n, key, &v) || v.t != J_STRING || v.n == 0 || v.n >= cap) return false;
    return jscan_str(&v, dst, cap);
}

static bool printable_no_space(const char *s) {
    for (; *s; s++)
        if (*s <= ' ' || *s > '~') return false;
    return true;
}

setup_err_t setup_decode_code(const char *code, size_t len, setup_t *out) {
    while (len > 0 && (*code == ' ' || *code == '\n' || *code == '\r' || *code == '\t')) { code++; len--; }
    while (len > 0 && (code[len - 1] == ' ' || code[len - 1] == '\n' || code[len - 1] == '\r' || code[len - 1] == '\t')) len--;
    if (len == 0 || len > CODE_MAX) return SETUP_BAD_ENCODING;
    char js[JSON_MAX];
    int n = b64url_decode(code, len, js, sizeof js);
    if (n <= 0) return SETUP_BAD_ENCODING;
    if (!jscan_valid(js, (size_t)n, 2)) return SETUP_BAD_JSON;
    jval_t v;
    if (!jscan_get(js, (size_t)n, "v", &v) || v.t != J_NUMBER) return SETUP_BAD_JSON;
    if (v.num != 1) return SETUP_BAD_VERSION;
    setup_t s;
    memset(&s, 0, sizeof s);
    if (!get_str(js, (size_t)n, "site_id", s.site_id, sizeof s.site_id) || !get_str(js, (size_t)n, "device_id", s.device_id, sizeof s.device_id) ||
        !get_str(js, (size_t)n, "unit_id", s.unit_id, sizeof s.unit_id) || !get_str(js, (size_t)n, "mqtt_uri", s.mqtt_uri, sizeof s.mqtt_uri) ||
        !get_str(js, (size_t)n, "mqtt_user", s.mqtt_user, sizeof s.mqtt_user) || !get_str(js, (size_t)n, "mqtt_pass", s.mqtt_pass, sizeof s.mqtt_pass))
        return SETUP_BAD_FIELD;
    if (!setup_valid_id(s.site_id) || !setup_valid_id(s.device_id) || !setup_valid_id(s.unit_id)) return SETUP_BAD_FIELD;
    /* the broker ACL is scoped by username, which must be this controller's own id */
    if (strcmp(s.mqtt_user, s.device_id) != 0) return SETUP_BAD_FIELD;
    if (strncmp(s.mqtt_uri, "mqtt://", 7) != 0 && strncmp(s.mqtt_uri, "mqtts://", 8) != 0) return SETUP_BAD_FIELD;
    const char *host = strstr(s.mqtt_uri, "://") + 3;
    if (*host == 0 || *host == ':' || *host == '/' || !printable_no_space(s.mqtt_uri)) return SETUP_BAD_FIELD;
    if (strlen(s.mqtt_pass) < 16 || !printable_no_space(s.mqtt_pass)) return SETUP_BAD_FIELD;
    memcpy(out->site_id, s.site_id, sizeof s.site_id);
    memcpy(out->device_id, s.device_id, sizeof s.device_id);
    memcpy(out->unit_id, s.unit_id, sizeof s.unit_id);
    memcpy(out->mqtt_uri, s.mqtt_uri, sizeof s.mqtt_uri);
    memcpy(out->mqtt_user, s.mqtt_user, sizeof s.mqtt_user);
    memcpy(out->mqtt_pass, s.mqtt_pass, sizeof s.mqtt_pass);
    return SETUP_OK;
}

static int hexv(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

/* URL-decodes [p,p+n) into dst ('+' = space). false on bad escape, NUL byte or overflow. */
static bool url_decode(const char *p, size_t n, char *dst, size_t cap) {
    size_t o = 0;
    for (size_t i = 0; i < n; i++) {
        char c = p[i];
        if (c == '+') c = ' ';
        else if (c == '%') {
            if (i + 2 >= n) return false; /* "%" needs two hex digits after it */
            int h = hexv(p[i + 1]), l = hexv(p[i + 2]);
            if (h < 0 || l < 0) return false;
            c = (char)(h * 16 + l);
            i += 2;
        }
        if (c == 0 || o + 1 >= cap) return false;
        dst[o++] = c;
    }
    dst[o] = 0;
    return true;
}

setup_err_t setup_parse_form(const char *body, size_t len, setup_t *out) {
    static char code[CODE_MAX + 1]; /* static: keeps the httpd task's stack small (one request at a time) */
    char ssid[SETUP_SSID_MAX] = {0}, pass[SETUP_WIFI_PASS_MAX] = {0};
    bool got_ssid = false, got_pass = false, got_code = false;
    code[0] = 0;
    size_t i = 0;
    while (i < len) {
        size_t j = i;
        while (j < len && body[j] != '&') j++;
        const char *eq = memchr(body + i, '=', j - i);
        if (!eq) return SETUP_BAD_FORM;
        size_t kn = (size_t)(eq - (body + i));
        const char *val = eq + 1;
        size_t vn = (size_t)(body + j - val);
        if (kn == 4 && memcmp(body + i, "ssid", 4) == 0) {
            if (got_ssid || !url_decode(val, vn, ssid, sizeof ssid)) return SETUP_BAD_WIFI;
            got_ssid = true;
        } else if (kn == 4 && memcmp(body + i, "pass", 4) == 0) {
            if (got_pass || !url_decode(val, vn, pass, sizeof pass)) return SETUP_BAD_WIFI;
            got_pass = true;
        } else if (kn == 4 && memcmp(body + i, "code", 4) == 0) {
            if (got_code || !url_decode(val, vn, code, sizeof code)) return SETUP_BAD_ENCODING;
            got_code = true;
        } /* unknown fields are ignored */
        i = j + 1;
    }
    if (!got_ssid || ssid[0] == 0) return SETUP_BAD_WIFI;
    size_t pl = strlen(pass);
    if (pl != 0 && (pl < 8 || pl > 64)) return SETUP_BAD_WIFI;
    if (!got_code) return SETUP_BAD_ENCODING;
    setup_t s;
    memset(&s, 0, sizeof s);
    setup_err_t e = setup_decode_code(code, strlen(code), &s);
    if (e != SETUP_OK) return e;
    memcpy(s.wifi_ssid, ssid, sizeof ssid);
    memcpy(s.wifi_pass, pass, sizeof pass);
    *out = s;
    return SETUP_OK;
}

const char *setup_err_text(setup_err_t e) {
    switch (e) {
    case SETUP_OK: return "Saved. The controller restarts and joins your Wi-Fi; it appears on the Pi dashboard within a minute.";
    case SETUP_BAD_ENCODING: return "The setup code is incomplete or damaged. Copy it again from the Pi dashboard (Add a unit, or New setup code on the unit page).";
    case SETUP_BAD_JSON: return "This is not an AeroLab setup code. Copy it again from the Pi dashboard.";
    case SETUP_BAD_VERSION: return "This setup code was made by a newer Pi. Update this controller's firmware, then try again.";
    case SETUP_BAD_FIELD: return "The setup code is not valid for this controller. Create a new one on the Pi dashboard.";
    case SETUP_BAD_WIFI: return "Enter the Wi-Fi name, and a password of 8 to 63 characters (or leave the password empty for an open network).";
    case SETUP_BAD_FORM: return "The form was not sent correctly. Reload the page and try again.";
    }
    return "Unknown error.";
}
