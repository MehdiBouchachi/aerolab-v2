#pragma once
/*
 * First-time setup of a controller without a technician (docs/architecture/fleet-management.md).
 * The Pi's dashboard issues a SETUP CODE (base64url of a small JSON bundle) when a grower adds a unit.
 * While unconfigured, the controller serves a setup page on its own Wi-Fi network; the grower enters
 * the farm Wi-Fi and pastes the code. This file is the PORTABLE, host-tested part: decoding and strict
 * validation. Nothing here touches the radio, flash or outputs.
 */
#include <stdbool.h>
#include <stddef.h>
#include "protocol.h"

#define SETUP_URI_MAX 128
#define SETUP_PASS_MAX 64
#define SETUP_SSID_MAX 33 /* 32 bytes + NUL (802.11) */
#define SETUP_WIFI_PASS_MAX 65 /* WPA2 passphrase 8..63, or 64 hex digits */

typedef struct {
    char site_id[PROTO_MAX_ID], device_id[PROTO_MAX_ID], unit_id[PROTO_MAX_ID];
    char mqtt_uri[SETUP_URI_MAX], mqtt_user[PROTO_MAX_ID], mqtt_pass[SETUP_PASS_MAX];
    char wifi_ssid[SETUP_SSID_MAX], wifi_pass[SETUP_WIFI_PASS_MAX];
} setup_t;

typedef enum {
    SETUP_OK = 0,
    SETUP_BAD_ENCODING, /* not base64url / too long */
    SETUP_BAD_JSON,
    SETUP_BAD_VERSION,  /* a newer Pi made this code; this firmware does not understand it */
    SETUP_BAD_FIELD,    /* an id, address or password is missing or malformed */
    SETUP_BAD_WIFI,     /* Wi-Fi name empty/too long, or password neither empty nor 8..64 */
    SETUP_BAD_FORM,
} setup_err_t;

/* Decodes a setup code (surrounding whitespace allowed). Fills the identity and MQTT fields only. */
setup_err_t setup_decode_code(const char *code, size_t len, setup_t *out);
/* Parses the setup page's application/x-www-form-urlencoded body: ssid, pass, code. */
setup_err_t setup_parse_form(const char *body, size_t len, setup_t *out);
/* Plain-language message for the setup page. */
const char *setup_err_text(setup_err_t e);
/* Same id rule as the contract: ^[a-z0-9][a-z0-9-]{2,39}$ */
bool setup_valid_id(const char *s);
