package httpapi

// Relay execution: the cloud-served dashboard forwards one site API call (contracts/sync RelayRequest)
// and the Pi runs it through the SAME route and handler as a local browser, with the cloud member's role
// in place of a session. Only the routes listed here can be reached this way; everything else (sign-in,
// local accounts and passwords, phone pairing, the cloud link itself, controller credentials, live
// streams, and programme changes, which keep their own confirmed channel) answers 403.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
)

// RelayAllowed is the complete list of "METHOD /pattern" routes the cloud may forward.
var RelayAllowed = map[string]bool{
	"GET /api/v1/sites/{siteId}/crop-profiles":        true,
	"POST /api/v1/sites/{siteId}/crop-profiles":       true,
	"GET /api/v1/crop-profiles/{profileId}":           true,
	"PUT /api/v1/crop-profiles/{profileId}":           true,
	"DELETE /api/v1/crop-profiles/{profileId}":        true,
	"POST /api/v1/sites/{siteId}/units":               true, // add a unit (returns its one-time setup code)
	"PATCH /api/v1/units/{unitId}":                    true, // rename
	"POST /api/v1/units/{unitId}/retire":              true,
	"POST /api/v1/units/{unitId}/setup-code":          true,
	"PUT /api/v1/units/{unitId}/crop-profile":         true,
	"POST /api/v1/incidents/{incidentId}/acknowledge": true,
	"GET /api/v1/sites/{siteId}/devices":              true,
	"GET /api/v1/sites/{siteId}/storage":              true,
	"GET /api/v1/sites/{siteId}/audit":                true,
	"GET /api/v1/units/{unitId}/programs":             true,
	"GET /api/v1/diagnostics":                         true,
}

const keyRelayUser ctxKey = 100

// relayRecorder is a bounded in-memory ResponseWriter.
type relayRecorder struct {
	h    http.Header
	code int
	buf  bytes.Buffer
	over bool
}

func (r *relayRecorder) Header() http.Header { return r.h }
func (r *relayRecorder) WriteHeader(c int) {
	if r.code == 0 {
		r.code = c
	}
}
func (r *relayRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = 200
	}
	if r.buf.Len()+len(b) > sc.RelayMaxBody {
		r.over = true
		return len(b), nil
	}
	return r.buf.Write(b)
}

func relayErr(id string, status int, code, msg string) sc.RelayResponse {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": msg, "details": map[string]any{}}})
	return sc.RelayResponse{ID: id, Status: status, Body: b}
}

// ServeRelay runs one forwarded call. It never returns an error: every outcome is an HTTP-shaped answer.
func (s *Server) ServeRelay(ctx context.Context, in sc.RelayRequest) sc.RelayResponse {
	role := roles.Role(in.Role)
	if !roles.Valid(role) || in.RequestedBy == "" || in.ID == "" {
		return relayErr(in.ID, 400, "VALIDATION_FAILED", "relay request needs an id, a requester and a valid role")
	}
	if t, err := time.Parse(time.RFC3339, in.ExpiresAt); err == nil && time.Now().After(t.Add(30*time.Second)) {
		return relayErr(in.ID, 410, "EXPIRED", "this request arrived too late and was not run")
	}
	if !strings.HasPrefix(in.Path, "/api/v1/") {
		return relayErr(in.ID, 403, "REMOTE_NOT_ALLOWED", "only site API calls can be relayed")
	}
	var body *bytes.Reader
	if len(in.Body) > 0 && string(in.Body) != "null" {
		body = bytes.NewReader(in.Body)
	} else {
		body = bytes.NewReader(nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, in.Method, in.Path, body)
	if err != nil {
		return relayErr(in.ID, 400, "VALIDATION_FAILED", "invalid method or path")
	}
	req.Header.Set("Content-Type", "application/json")
	if _, pattern := s.mux.Handler(req); !RelayAllowed[pattern] {
		return relayErr(in.ID, 403, "REMOTE_NOT_ALLOWED", "this action is only available on site, from the Pi's own dashboard")
	}
	u := auth.User{ID: "cloud:" + in.RequestedBy, SiteID: s.Cfg.SiteID, Username: in.RequestedBy + " (via cloud)", Role: role}
	req = req.WithContext(context.WithValue(req.Context(), keyRelayUser, u))
	rec := &relayRecorder{h: http.Header{}}
	s.recoverer(s.requestID(s.mux)).ServeHTTP(rec, req)
	if rec.over {
		return relayErr(in.ID, 502, "RESPONSE_TOO_LARGE", "the answer is too large to send through the cloud")
	}
	out := sc.RelayResponse{ID: in.ID, Status: rec.code}
	if out.Status == 0 {
		out.Status = 200
	}
	if b := rec.buf.Bytes(); len(bytes.TrimSpace(b)) > 0 && json.Valid(b) {
		out.Body = append(json.RawMessage(nil), bytes.TrimSpace(b)...)
	}
	return out
}

// relayUser returns the cloud member a relayed call runs as. The value can only be set in-process by
// ServeRelay: nothing from the network can put it in a request context.
func relayUser(r *http.Request) (auth.User, bool) {
	u, ok := r.Context().Value(keyRelayUser).(auth.User)
	return u, ok
}
