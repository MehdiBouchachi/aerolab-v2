#pragma once
/* Minimal bounded JSON scanner for the flat-ish contract messages. No allocation. */
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef enum { J_NONE, J_STRING, J_NUMBER, J_BOOL, J_NULL, J_OBJECT, J_ARRAY } jtype_t;
typedef struct {
    jtype_t t;
    const char *p; /* start of value (for string: first char after the quote) */
    size_t n;      /* length (for string: raw length without quotes; no escapes allowed) */
    bool b;
    double num;
} jval_t;

/* Validates that [s,s+len) is exactly ONE JSON value with nesting <= max_depth and no trailing data. */
bool jscan_valid(const char *s, size_t len, int max_depth);
/* Finds a TOP-LEVEL key of an object. false if absent. */
bool jscan_get(const char *obj, size_t len, const char *key, jval_t *out);
bool jscan_str(const jval_t *v, char *dst, size_t cap);
