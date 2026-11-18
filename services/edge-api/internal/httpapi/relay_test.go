package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
)

func relayReq(method, path, role string, body any) sc.RelayRequest {
	var b json.RawMessage
	if body != nil {
		b, _ = json.Marshal(body)
	}
	return sc.RelayRequest{ID: "rly-test", Method: method, Path: path, Body: b, RequestedBy: "owner@example.test", Role: role,
		ExpiresAt: time.Now().Add(8 * time.Second).UTC().Format(time.RFC3339)}
}

// A relayed call runs through the same handler as a local browser and is visible locally afterwards.
func TestRelayRunsTheSameHandlerAsALocalCall(t *testing.T) {
	e := newEnv(t)
	r := e.srv.ServeRelay(context.Background(), relayReq("PATCH", "/api/v1/units/unit-test-001", "ADMIN", map[string]any{"name": "Basil rack"}))
	if r.Status != 204 || r.ID != "rly-test" {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if w := e.do("GET", "/api/v1/units/unit-test-001", e.toks[roles.Viewer], nil); !strings.Contains(w.Body.String(), "Basil rack") {
		t.Fatal(w.Body)
	}
	// validation errors come back unchanged
	if r := e.srv.ServeRelay(context.Background(), relayReq("PATCH", "/api/v1/units/unit-test-001", "ADMIN", map[string]any{"name": ""})); r.Status != 400 {
		t.Fatalf("empty name: %d", r.Status)
	}
	// the audit log names the cloud member
	if w := e.do("GET", "/api/v1/sites/"+e.srv.Cfg.SiteID+"/audit?limit=50", e.toks[roles.Admin], nil); !strings.Contains(w.Body.String(), "cloud:owner@example.test") {
		t.Fatalf("relayed change not attributed: %s", w.Body)
	}
}

// The Pi enforces the member's role itself; the cloud's word on the route is not enough.
func TestRelayEnforcesTheMembersRole(t *testing.T) {
	e := newEnv(t)
	if r := e.srv.ServeRelay(context.Background(), relayReq("PATCH", "/api/v1/units/unit-test-001", "VIEWER", map[string]any{"name": "x"})); r.Status != 403 {
		t.Fatalf("viewer renamed a unit: %d", r.Status)
	}
	if r := e.srv.ServeRelay(context.Background(), relayReq("GET", "/api/v1/sites/"+e.srv.Cfg.SiteID+"/storage", "VIEWER", nil)); r.Status != 200 {
		t.Fatalf("viewer storage: %d %s", r.Status, r.Body)
	}
	if r := e.srv.ServeRelay(context.Background(), relayReq("GET", "/api/v1/sites/"+e.srv.Cfg.SiteID+"/storage", "ROOT", nil)); r.Status != 400 {
		t.Fatalf("unknown role accepted: %d", r.Status)
	}
}

// Anything not on the allow-list is refused before any handler runs, whatever the role.
func TestRelayRefusesEverythingOffTheAllowList(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/local/users"},
		{"POST", "/api/v1/local/users"},
		{"POST", "/api/v1/local/auth/login"},
		{"POST", "/api/v1/site-pairing/start"},
		{"GET", "/api/v1/cloud-link"},
		{"DELETE", "/api/v1/cloud-link"},
		{"POST", "/api/v1/sites/" + e.srv.Cfg.SiteID + "/devices"},
		{"POST", "/api/v1/units/unit-test-001/program-requests"},
		{"GET", "/api/v1/sites/" + e.srv.Cfg.SiteID + "/events"},
		{"GET", "/api/v1/units/../local/users"},
		{"GET", "/healthz"},
	} {
		if r := e.srv.ServeRelay(context.Background(), relayReq(c.m, c.p, "ADMIN", map[string]any{})); r.Status != 403 {
			t.Errorf("%s %s: got %d, want 403", c.m, c.p, r.Status)
		}
	}
	// every allow-listed pattern really exists, so a renamed route cannot silently fall off the list
	have := map[string]bool{}
	for _, r := range e.srv.Routes() {
		have[r] = true
	}
	for p := range RelayAllowed {
		if !have[p] {
			t.Errorf("allow-listed route does not exist: %s", p)
		}
	}
}

func TestRelayRefusesALateRequest(t *testing.T) {
	e := newEnv(t)
	q := relayReq("PATCH", "/api/v1/units/unit-test-001", "ADMIN", map[string]any{"name": "late"})
	q.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if r := e.srv.ServeRelay(context.Background(), q); r.Status != 410 {
		t.Fatalf("late request ran: %d", r.Status)
	}
}

func TestMetaSaysEdge(t *testing.T) {
	e := newEnv(t)
	if w := e.do("GET", "/api/v1/meta", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"origin":"edge"`) {
		t.Fatal(w.Code, w.Body)
	}
}
