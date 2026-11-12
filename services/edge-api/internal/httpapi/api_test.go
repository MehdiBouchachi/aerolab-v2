package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/telemetry"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/static"
)

type mqttUp struct{}

func (mqttUp) Connected() bool { return true }

type env struct {
	srv  *Server
	h    http.Handler
	db   *storage.Store
	reg  *devices.Registry
	pub  *testutil.FakePub
	auth *auth.Service
	toks map[roles.Role]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testutil.Store(t)
	cfg := &config.Config{SiteID: "site-test-01", SiteName: "Test", StaleAfter: 10 * time.Second, AllowSimulatedData: true, MinFreeBytes: 0}
	hub := sse.NewHub()
	pub := testutil.NewFakePub()
	m := diagnostics.New()
	reg := devices.NewRegistry(db, cfg.SiteID)
	a := auth.New(db, cfg.SiteID)
	cm := commands.New(db, reg, pub, hub, commands.Options{SiteID: cfg.SiteID, StaleAfter: cfg.StaleAfter, Retry: time.Second, MaxTries: 3, TTL: time.Minute}, testutil.Logger())
	w := telemetry.NewWriter(db, pub, hub, m, testutil.Logger(), cfg.SiteID, 16, false)
	s := New(Deps{Cfg: cfg, DB: db, Auth: a, Reg: reg, Cmds: cm, Hub: hub, Metrics: m, Writer: w, MQTT: mqttUp{}, Static: static.FS, Log: testutil.Logger()})
	e := &env{srv: s, h: s.Handler(), db: db, reg: reg, pub: pub, auth: a, toks: map[roles.Role]string{}}
	ctx := context.Background()
	for _, r := range []roles.Role{roles.Admin, roles.Operator, roles.Viewer} {
		name := strings.ToLower(string(r)) + "1"
		if _, err := a.CreateUser(ctx, name, "correct-horse-battery", r, "t"); err != nil {
			t.Fatal(err)
		}
		tok, _, err := a.Login(ctx, name, "correct-horse-battery", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		e.toks[r] = tok
	}
	if _, _, err := reg.Provision(ctx, "esp32-test-001", "unit-test-001", "Unit One", "sim", "SIMULATOR", "t"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) do(method, path, tok string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *env) heartbeat(v int, state mc.ControlState, faults []string, at time.Time) {
	u := "unit-test-001"
	s := mc.State{Header: mc.Header{SchemaVersion: 1, SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: &u, BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator},
		ControlState: state, PumpCommanded: "ON", WaterDelivery: "NOT_VERIFIED", EssentialInterlocksOK: true, FaultCodes: faults, FirmwareVersion: "sim", SessionNonce: "nonce-12345678"}
	if v > 0 {
		s.ProgramVersionApplied = &v
	}
	e.reg.ApplyState("esp32-test-001", s, at)
}

// ---------------------------------------------------------------- contract

func TestRoutesMatchOpenAPIExactly(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/openapi/edge.v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	spec := map[string]bool{}
	for p, ops := range doc.Paths {
		for m := range ops {
			spec[strings.ToUpper(m)+" "+p] = true
		}
	}
	e := newEnv(t)
	code := map[string]bool{}
	for _, r := range e.srv.Routes() {
		code[r] = true
	}
	var missing, extra []string
	for r := range code {
		if !spec[r] {
			missing = append(missing, r)
		}
	}
	for r := range spec {
		if !code[r] {
			extra = append(extra, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("OpenAPI drift.\n  implemented but undocumented: %v\n  documented but not implemented: %v", missing, extra)
	}
}

// -------------------------------------------------------------------- RBAC

// A07: the matrix is verified against the real server with an INDEPENDENT oracle.
func TestRBACMatrixIsEnforcedByTheServer(t *testing.T) {
	e := newEnv(t)
	prog := map[string]any{"expected_program_version": 0, "program": map[string]any{"mode": "TIMED_CYCLE", "on_duration_seconds": 5, "off_duration_seconds": 60, "max_continuous_on_seconds": 10, "require_reservoir_ok": true}}
	A, O, V := roles.Admin, roles.Operator, roles.Viewer
	type tc struct {
		method, path string
		body         any
		allowed      []roles.Role
	}
	all := []roles.Role{A, O, V}
	cases := []tc{
		{"GET", "/api/v1/session", nil, all},
		{"GET", "/api/v1/sites", nil, all},
		{"GET", "/api/v1/sites/site-test-01/summary", nil, all},
		{"GET", "/api/v1/sites/site-test-01/units", nil, all},
		{"GET", "/api/v1/units/unit-test-001", nil, all},
		{"GET", "/api/v1/units/unit-test-001/readings/latest", nil, all},
		{"GET", "/api/v1/units/unit-test-001/readings/history", nil, all},
		{"GET", "/api/v1/units/unit-test-001/programs", nil, all},
		{"GET", "/api/v1/units/unit-test-001/programs/applied", nil, all},
		{"GET", "/api/v1/sites/site-test-01/incidents", nil, all},
		{"GET", "/api/v1/sites/site-test-01/storage", nil, all},
		{"POST", "/api/v1/incidents/nope/acknowledge", nil, []roles.Role{A, O}},
		{"POST", "/api/v1/units/unit-test-001/program-requests", prog, []roles.Role{A}},
		{"GET", "/api/v1/sites/site-test-01/devices", nil, []roles.Role{A}},
		{"POST", "/api/v1/sites/site-test-01/devices", map[string]any{"device_id": "esp32-x-0001", "unit_id": "unit-x-0001", "unit_name": "X", "data_source": "SIMULATOR"}, []roles.Role{A}},
		{"GET", "/api/v1/local/users", nil, []roles.Role{A}},
		{"POST", "/api/v1/local/users", map[string]any{"username": "newbie", "password": "correct-horse-battery", "role": "VIEWER"}, []roles.Role{A}},
		{"PATCH", "/api/v1/local/users/nope", map[string]any{"disabled": true}, []roles.Role{A}},
		{"GET", "/api/v1/sites/site-test-01/audit", nil, []roles.Role{A}},
		{"GET", "/api/v1/diagnostics", nil, []roles.Role{A}},
	}
	for _, c := range cases {
		// no credentials at all
		if w := e.do(c.method, c.path, "", c.body); w.Code != 401 {
			t.Errorf("%s %s without token: %d, want 401", c.method, c.path, w.Code)
		}
		if w := e.do(c.method, c.path, "garbage-token", c.body); w.Code != 401 {
			t.Errorf("%s %s with bad token: %d, want 401", c.method, c.path, w.Code)
		}
		for _, role := range all {
			ok := false
			for _, a := range c.allowed {
				ok = ok || a == role
			}
			w := e.do(c.method, c.path, e.toks[role], c.body)
			if ok && (w.Code == 401 || w.Code == 403) {
				t.Errorf("%s %s as %s: %d, should be permitted", c.method, c.path, role, w.Code)
			}
			if !ok && w.Code != 403 {
				t.Errorf("%s %s as %s: %d, want 403", c.method, c.path, role, w.Code)
			}
		}
	}
	// a Viewer's forged admin write must leave NO side effect
	var n int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&n)
	if n != 0 {
		t.Fatalf("forbidden requests created %d commands", n)
	}
	// ...and denials are audited
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='authz.denied'`).Scan(&n)
	if n == 0 {
		t.Fatal("authorization denials are not audited")
	}
}

func TestSecurityHeadersAndNoStore(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/session", e.toks[roles.Viewer], nil)
	for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store"} {
		if w.Header().Get(h) != want {
			t.Errorf("%s=%q", h, w.Header().Get(h))
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "http") {
		t.Errorf("CSP must forbid remote assets: %q", csp)
	}
}

func TestLoginRateLimitAndNoUserEnumeration(t *testing.T) {
	e := newEnv(t)
	login := func(u, p string) int {
		return e.do("POST", "/api/v1/local/auth/login", "", map[string]any{"username": u, "password": p}).Code
	}
	if login("admin1", "wrong-password-1") != 401 || login("no-such-user", "wrong-password-1") != 401 {
		t.Fatal("bad credentials must be 401 for known and unknown users alike")
	}
	for i := 0; i < 5; i++ {
		login("admin1", "wrong-password-x")
	}
	if c := login("admin1", "correct-horse-battery"); c != 429 {
		t.Fatalf("brute force not throttled: %d", c)
	}
}

func TestStrictJSONInputs(t *testing.T) {
	e := newEnv(t)
	tok := e.toks[roles.Admin]
	// mass assignment: unknown fields are refused, not silently ignored
	if w := e.do("POST", "/api/v1/local/users", tok, map[string]any{"username": "evil1", "password": "correct-horse-battery", "role": "VIEWER", "is_admin": true}); w.Code != 400 {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
	if w := e.do("POST", "/api/v1/local/users", tok, map[string]any{"username": "weak1", "password": "short", "role": "VIEWER"}); w.Code != 400 {
		t.Fatalf("weak password accepted: %d", w.Code)
	}
	// wrong content type
	r := httptest.NewRequest("POST", "/api/v1/local/users", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("%d", w.Code)
	}
	// oversize body
	r = httptest.NewRequest("POST", "/api/v1/local/users", strings.NewReader(`{"username":"`+strings.Repeat("a", 100000)+`"}`))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("oversize body accepted: %d", w.Code)
	}
}

func TestCSRFOriginCheckOnCookieSessions(t *testing.T) {
	e := newEnv(t)
	post := func(origin string) int {
		r := httptest.NewRequest("POST", "/api/v1/local/users", strings.NewReader(`{"username":"csrf1","password":"correct-horse-battery","role":"VIEWER"}`))
		r.Host = "aerolab.local"
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: e.toks[roles.Admin]})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w.Code
	}
	if c := post("https://evil.example"); c != 403 {
		t.Fatalf("cross-site cookie write allowed: %d", c)
	}
	if c := post("http://aerolab.local"); c != 201 {
		t.Fatalf("same-origin write refused: %d", c)
	}
}

func TestLastAdminCannotBeDemotedOrDisabled(t *testing.T) {
	e := newEnv(t)
	var id string
	_ = e.db.R.QueryRow(`SELECT id FROM local_users WHERE username='admin1'`).Scan(&id)
	if w := e.do("PATCH", "/api/v1/local/users/"+id, e.toks[roles.Admin], map[string]any{"disabled": true}); w.Code != 409 {
		t.Fatalf("locked ourselves out: %d", w.Code)
	}
	// disabling a user revokes their live session immediately
	var vid string
	_ = e.db.R.QueryRow(`SELECT id FROM local_users WHERE username='viewer1'`).Scan(&vid)
	if w := e.do("PATCH", "/api/v1/local/users/"+vid, e.toks[roles.Admin], map[string]any{"disabled": true}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := e.do("GET", "/api/v1/session", e.toks[roles.Viewer], nil); w.Code != 401 {
		t.Fatalf("disabled user's session still valid: %d", w.Code)
	}
}

// -------------------------------------------------------- truthful display

func TestStaleControllerIsNeverShownAsCurrentlyRunning(t *testing.T) {
	e := newEnv(t)
	tok := e.toks[roles.Viewer]
	e.heartbeat(3, mc.StateRunning, []string{}, time.Now())
	var out struct {
		Unit struct {
			Connection   string  `json:"connection"`
			ControlState *string `json:"control_state"`
			Pump         *string `json:"pump_commanded"`
		} `json:"unit"`
	}
	get := func() {
		w := e.do("GET", "/api/v1/units/unit-test-001", tok, nil)
		out.Unit.ControlState, out.Unit.Pump = nil, nil
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	get()
	if out.Unit.Connection != "ONLINE" || *out.Unit.ControlState != "RUNNING" {
		t.Fatalf("%+v", out.Unit)
	}
	e.heartbeat(3, mc.StateRunning, []string{}, time.Now().Add(-time.Minute)) // last beat a minute ago
	get()
	if out.Unit.Connection != "STALE" || *out.Unit.ControlState != "UNKNOWN_STALE" || out.Unit.Pump != nil {
		t.Fatalf("stale unit presented as live: conn=%s state=%v pump=%v", out.Unit.Connection, *out.Unit.ControlState, out.Unit.Pump)
	}
}

func insertTel(t *testing.T, e *env, seq int64, at time.Time, readings string, tq string) {
	t.Helper()
	_, err := e.db.W.Exec(`INSERT INTO telemetry_snapshots(event_id,site_id,device_id,unit_id,boot_id,sequence_number,uptime_ms,clock_quality,received_at_utc,effective_at_utc,time_quality,readings_json,source)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, "e"+time.Now().Format("150405.000000")+string(rune('a'+seq%26))+itoa(seq), "site-test-01", "esp32-test-001", "unit-test-001", "boot-aaaaaaaa", seq, seq*1000, "monotonic_only",
		storage.FormatTime(at), storage.FormatTime(at), tq, readings, "SIMULATOR")
	if err != nil {
		t.Fatal(err)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestHistoryHasExplicitGapsAndNeverInventsValues(t *testing.T) {
	e := newEnv(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	good := `{"ph":{"value":6.0,"quality":"valid"},"light_lux":{"value":null,"quality":"missing"},"reservoir_level_ok":{"value":true,"quality":"valid"}}`
	bad := `{"ph":{"value":null,"quality":"invalid"},"light_lux":{"value":null,"quality":"missing"},"reservoir_level_ok":{"value":false,"quality":"valid"}}`
	insertTel(t, e, 1, base, good, "DEVICE_CLOCK")
	insertTel(t, e, 2, base.Add(time.Minute), good, "ESTIMATED_FROM_UPTIME")
	insertTel(t, e, 3, base.Add(2*time.Minute), bad, "RECEIVED_AT_ONLY")
	// 10 minute outage, then data again
	insertTel(t, e, 4, base.Add(12*time.Minute), good, "DEVICE_CLOCK")

	from, to := base.Add(-time.Minute).Format(time.RFC3339), base.Add(20*time.Minute).Format(time.RFC3339)
	w := e.do("GET", "/api/v1/units/unit-test-001/readings/history?resolution=raw&from="+from+"&to="+to, e.toks[roles.Viewer], nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var h struct {
		Points []struct {
			T        string                    `json:"t"`
			TimeQ    string                    `json:"time_quality"`
			Readings map[string]map[string]any `json:"readings"`
		} `json:"points"`
		Gaps []map[string]string `json:"gaps"`
		Sim  bool                `json:"contains_simulated_data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &h)
	if len(h.Points) != 4 || len(h.Gaps) != 1 {
		t.Fatalf("points=%d gaps=%d: %s", len(h.Points), len(h.Gaps), w.Body.String())
	}
	if !h.Sim {
		t.Fatal("simulated history not labelled")
	}
	p3 := h.Points[2]
	if p3.Readings["ph"]["value"] != nil || p3.Readings["ph"]["quality"] != "invalid" {
		t.Fatalf("invalid pH shown as a number: %v", p3.Readings["ph"])
	}
	if p3.Readings["light_lux"]["value"] != nil {
		t.Fatalf("missing sensor shown as a value: %v", p3.Readings["light_lux"])
	}
	if p3.Readings["reservoir_level_ok"]["value"] != false || p3.TimeQ != "RECEIVED_AT_ONLY" {
		t.Fatalf("%v %s", p3.Readings["reservoir_level_ok"], p3.TimeQ)
	}

	// aggregation: 5m buckets average only VALID samples
	w = e.do("GET", "/api/v1/units/unit-test-001/readings/history?resolution=5m&from="+from+"&to="+to, e.toks[roles.Viewer], nil)
	_ = json.Unmarshal(w.Body.Bytes(), &h)
	if len(h.Points) == 0 || h.Points[0].Readings["ph"]["value"] != 6.0 {
		t.Fatalf("aggregate wrong: %s", w.Body.String())
	}
}

func TestHistoryRangeAndPointLimitsAreEnforced(t *testing.T) {
	e := newEnv(t)
	tok := e.toks[roles.Viewer]
	now := time.Now().UTC()
	q := func(q string) int {
		return e.do("GET", "/api/v1/units/unit-test-001/readings/history?"+q, tok, nil).Code
	}
	if c := q("from=" + now.Add(-40*24*time.Hour).Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339)); c != 400 {
		t.Fatalf("40-day range allowed: %d", c)
	}
	if c := q("from=" + now.Add(-30*24*time.Hour).Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339) + "&resolution=1m"); c != 400 {
		t.Fatalf("43k-point request allowed: %d", c)
	}
	if c := q("resolution=nanosecond"); c != 400 {
		t.Fatal(c)
	}
	if c := q("from=" + now.Format(time.RFC3339) + "&to=" + now.Add(-time.Hour).Format(time.RFC3339)); c != 400 {
		t.Fatalf("inverted range allowed: %d", c)
	}
}

func TestOutdatedReadingsAreFlaggedNotPresentedAsCurrent(t *testing.T) {
	e := newEnv(t)
	insertTel(t, e, 1, time.Now().Add(-10*time.Minute), `{"ph":{"value":6.1,"quality":"valid"}}`, "DEVICE_CLOCK")
	w := e.do("GET", "/api/v1/units/unit-test-001/readings/latest", e.toks[roles.Viewer], nil)
	var r struct {
		Snapshot struct {
			Outdated bool                      `json:"outdated"`
			Readings map[string]map[string]any `json:"readings"`
		} `json:"snapshot"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if !r.Snapshot.Outdated || r.Snapshot.Readings["ph"]["quality"] != "outdated" {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestUnknownRoutesAndWrongSite(t *testing.T) {
	e := newEnv(t)
	tok := e.toks[roles.Viewer]
	if c := e.do("GET", "/api/v1/sites/other-site/units", tok, nil).Code; c != 404 {
		t.Fatalf("other site reachable: %d", c)
	}
	if c := e.do("GET", "/api/v1/units/unit-nope", tok, nil).Code; c != 404 {
		t.Fatal(c)
	}
	if c := e.do("GET", "/api/v1/nothing", tok, nil).Code; c != 404 {
		t.Fatal(c)
	}
	// unknown non-API paths fall back to the SPA shell (works offline)
	if w := e.do("GET", "/units/unit-test-001", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "AeroLab") {
		t.Fatalf("SPA fallback: %d", w.Code)
	}
}

func TestSSEStreamRequiresAuthAndDeliversTypedEvents(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	// unauthenticated and wrong-site streams are refused
	resp, _ := http.Get(srv.URL + "/api/v1/sites/site-test-01/events")
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous stream: %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/sites/site-test-01/events", nil)
	req.Header.Set("Authorization", "Bearer "+e.toks[roles.Viewer])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	wait := func(prefix string) string {
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed before %q", prefix)
				}
				if strings.HasPrefix(l, prefix) {
					return l
				}
			case <-ctx.Done():
				t.Fatalf("timeout waiting for %q", prefix)
			}
		}
	}
	wait("event: hello") // client is told to re-fetch a snapshot: no replay
	e.srv.Hub.Publish("incident.updated", map[string]string{"id": "x"})
	if l := wait("event: incident.updated"); l == "" {
		t.Fatal("no typed event")
	}
	if d := wait("data: "); !strings.Contains(d, `"id":"x"`) {
		t.Fatalf("payload %q", d)
	}
}
