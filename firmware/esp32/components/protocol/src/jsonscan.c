#include "jsonscan.h"
#include <ctype.h>
#include <stdlib.h>
#include <string.h>

static const char *skip_ws(const char *p, const char *e) {
    while (p < e && (*p == ' ' || *p == '\t' || *p == '\n' || *p == '\r')) p++;
    return p;
}

/* Parses one value at p; returns pointer after it or NULL on error. */
static const char *parse_value(const char *p, const char *e, int depth, int max_depth, jval_t *out) {
    p = skip_ws(p, e);
    if (p >= e || depth > max_depth) return NULL;
    jval_t v = {J_NONE, p, 0, false, 0};
    if (*p == '"') {
        const char *q = p + 1;
        while (q < e && *q != '"') {
            if (*q == '\\') return NULL; /* escapes are not part of the contract: refuse rather than misread */
            if ((unsigned char)*q < 0x20) return NULL;
            q++;
        }
        if (q >= e) return NULL;
        v.t = J_STRING; v.p = p + 1; v.n = (size_t)(q - p - 1);
        if (out) *out = v;
        return q + 1;
    }
    if (*p == '{' || *p == '[') {
        char open = *p, close = open == '{' ? '}' : ']';
        const char *q = skip_ws(p + 1, e);
        bool first = true;
        for (;;) {
            if (q >= e) return NULL;
            if (*q == close) { q++; break; }
            if (!first) {
                if (*q != ',') return NULL;
                q = skip_ws(q + 1, e);
            }
            first = false;
            if (open == '{') {
                if (q >= e || *q != '"') return NULL;
                q = parse_value(q, e, depth + 1, max_depth, NULL);
                if (!q) return NULL;
                q = skip_ws(q, e);
                if (q >= e || *q != ':') return NULL;
                q++;
            }
            q = parse_value(q, e, depth + 1, max_depth, NULL);
            if (!q) return NULL;
            q = skip_ws(q, e);
        }
        v.t = open == '{' ? J_OBJECT : J_ARRAY; v.n = (size_t)(q - p);
        if (out) *out = v;
        return q;
    }
    if (e - p >= 4 && !strncmp(p, "true", 4)) { v.t = J_BOOL; v.b = true; v.n = 4; if (out) *out = v; return p + 4; }
    if (e - p >= 5 && !strncmp(p, "false", 5)) { v.t = J_BOOL; v.n = 5; if (out) *out = v; return p + 5; }
    if (e - p >= 4 && !strncmp(p, "null", 4)) { v.t = J_NULL; v.n = 4; if (out) *out = v; return p + 4; }
    if (*p == '-' || isdigit((unsigned char)*p)) {
        char tmp[40]; const char *q = p; size_t k = 0;
        while (q < e && (isdigit((unsigned char)*q) || *q == '-' || *q == '+' || *q == '.' || *q == 'e' || *q == 'E') && k < sizeof tmp - 1) tmp[k++] = *q++;
        tmp[k] = 0;
        char *end; double d = strtod(tmp, &end);
        if (end == tmp || *end) return NULL;
        v.t = J_NUMBER; v.num = d; v.n = k;
        if (out) *out = v;
        return q;
    }
    return NULL;
}

bool jscan_valid(const char *s, size_t len, int max_depth) {
    const char *e = s + len;
    const char *p = parse_value(s, e, 1, max_depth, NULL);
    if (!p) return false;
    return skip_ws(p, e) == e;
}

bool jscan_get(const char *obj, size_t len, const char *key, jval_t *out) {
    const char *e = obj + len;
    const char *p = skip_ws(obj, e);
    if (p >= e || *p != '{') return false;
    p = skip_ws(p + 1, e);
    size_t kl = strlen(key);
    while (p < e && *p != '}') {
        jval_t k;
        p = parse_value(p, e, 2, 64, &k);
        if (!p || k.t != J_STRING) return false;
        p = skip_ws(p, e);
        if (p >= e || *p != ':') return false;
        jval_t v;
        p = parse_value(p + 1, e, 2, 64, &v);
        if (!p) return false;
        if (k.n == kl && !strncmp(k.p, key, kl)) { *out = v; return true; }
        p = skip_ws(p, e);
        if (p < e && *p == ',') p = skip_ws(p + 1, e);
    }
    return false;
}

bool jscan_str(const jval_t *v, char *dst, size_t cap) {
    if (v->t != J_STRING || v->n + 1 > cap) return false;
    memcpy(dst, v->p, v->n);
    dst[v->n] = 0;
    return true;
}
