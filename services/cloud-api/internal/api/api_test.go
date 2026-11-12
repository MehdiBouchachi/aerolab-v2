package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/db"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/testpg"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/migrations"
	"gopkg.in/yaml.v3"
)

func TestMain(m *testing.M) {
	c := m.Run()
	testpg.Stop()
	os.Exit(c)
}

type env struct {
	t    *testing.T
	db   *sql.DB
	srv  *Server
	h    http.Handler
	auth *auth.Service
	tok  map[string]string // email -> session token
	edge map[string]string // site -> edge token
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := testpg.New(t)
	ctx := context.Background()
	d, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d, migrations.FS); err != nil {
		t.Fatal(err)
	}
	a := auth.New(d)
	cfg := &config.Config{StaleAfter: 90 * time.Second, IntentTTL: 2 * time.Minute}
	s := New(cfg, d, a, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := &env{t: t, db: d, srv: s, h: s.Handler(), auth: a, tok: map[string]string{}, edge: map[string]string{}}
	return e
}

func (e *env) user(email string) string {
	e.t.Helper()
	u, err := e.auth.CreateUser(context.Background(), email, "correct-horse-battery")
	if err != nil {
		e.t.Fatal(err)
	}
	tok, _, err := e.auth.Login(context.Background(), email, "correct-horse-battery", "127.0.0.1")
	if err != nil {
		e.t.Fatal(err)
	}
	e.tok[email] = tok
	return u.ID
}

func (e *env) site(id string) {
	e.t.Helper()
	e.exec(`INSERT INTO organizations(id,name) VALUES ($1,$1) ON CONFLICT DO NOTHING`, "org-"+id)
	e.exec(`INSERT INTO sites(id,org_id,name) VALUES ($1,$2,$1)`, id, "org-"+id)
	tok, err := e.auth.IssueEdgeToken(context.Background(), id, false)
	if err != nil {
		e.t.Fatal(err)
	}
	e.edge[id] = tok
}

func (e *env) member(site, userID, role string) {
	e.exec(`INSERT INTO site_members(site_id,user_id,role) VALUES ($1,$2,$3)`, site, userID, role)
}

func (e *env) exec(q string, a ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, a...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *env) do(method, path, tok string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
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

func (e *env) count(q string, a ...any) (n int) {
	e.t.Helper()
	if err := e.db.QueryRow(q, a...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return
}

// ---- fixtures

func record(t *testing.T, kind string, seq int64, mut func(map[string]any)) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../../../../contracts/examples/valid/" + kind + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["site_id"], m["seq"] = "site-a-0001", seq
	if mut != nil {
		mut(m)
	}
	out, _ := json.Marshal(m)
	return out
}

func envelope(rec json.RawMessage, eff time.Time, tq string) json.RawMessage {
	b, _ := json.Marshal(sc.RecordEnvelope{Record: rec, EffectiveAtUTC: eff.UTC().Format(time.RFC3339Nano), TimeQuality: tq, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	return b
}

func telEvent(t *testing.T, seq int64, eff time.Time, mut func(map[string]any)) sc.Event {
	return sc.Event{EventID: fmt.Sprintf("esp32-sim-004/boot-9f3a1c22-5b/%d", seq), EventType: sc.EventTelemetry, Priority: sc.P3,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: envelope(record(t, "telemetry.monotonic", seq, mut), eff, "ESTIMATED_FROM_UPTIME")}
}

func batch(site string, st *sc.State, evs ...sc.Event) sc.Batch {
	return sc.Batch{SchemaVersion: 1, BatchID: "batch-" + auth.NewID(), SiteID: site, SentAt: time.Now().UTC().Format(time.RFC3339), EdgeVersion: "test", State: st, Events: evs}
}

func unitJSON(id, name, conn, ctl string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"id": id, "name": name, "device_id": "esp32-sim-004", "is_simulated": true, "connection": conn, "control_state": ctl,
		"pump_commanded": "ON", "water_delivery": "NOT_VERIFIED", "applied_program_version": 3, "essential_interlocks_ok": true, "fault_codes": []string{},
		"active_incidents": map[string]int{},
		"latest_readings":  map[string]any{"effective_at_utc": time.Now().UTC().Format(time.RFC3339), "outdated": false, "readings": map[string]any{"ph": map[string]any{"value": 6.1, "quality": "valid"}}}})
	return b
}

func state(at time.Time, units ...json.RawMessage) *sc.State {
	return &sc.State{ObservedAt: at.UTC().Format(time.RFC3339Nano), SiteName: "Pilot A", Connectivity: sc.Connectivity{MQTTBrokerConnected: true}, Units: units}
}

// ------------------------------------------------------------------ tests

func TestMigrateIsIdempotent(t *testing.T) {
	e := newEnv(t)
	again, err := db.Migrate(context.Background(), e.db, migrations.FS)
	if err != nil || len(again) != 0 {
		t.Fatalf("%v %v", again, err)
	}
}

// Tenant isolation (spec 18.2): membership is checked on EVERY site resource.
func TestTenantIsolationAndRoles(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.user("alice@x.test"), e.user("bob@x.test")
	e.user("carol@x.test") // member of nothing
	e.site("site-a-0001")
	e.site("site-b-0001")
	e.member("site-a-0001", alice, "ADMIN")
	e.member("site-a-0001", bob, "VIEWER") // Viewer at A ...
	e.member("site-b-0001", bob, "ADMIN")  // ... Admin at B
	for _, s := range []string{"site-a-0001", "site-b-0001"} {
		e.exec(`INSERT INTO units(site_id,id,name,view) VALUES ($1,'unit-sim-004','U4',$2)`, s, unitJSON("unit-sim-004", "U4", "ONLINE", "RUNNING"))
		e.exec(`INSERT INTO latest_site_status(site_id,state,observed_at) VALUES ($1,'{}',now())`, s)
		e.exec(`UPDATE sites SET last_sync_at=now() WHERE id=$1`, s)
	}
	prog := map[string]any{"unit_id": "unit-sim-004", "expected_program_version": 3, "program": map[string]any{"mode": "TIMED_CYCLE", "on_duration_seconds": 5, "off_duration_seconds": 60, "max_continuous_on_seconds": 10, "require_reservoir_ok": true}}
	type c struct {
		name, method, path, who string
		body                    any
		want                    int
	}
	cases := []c{
		{"anonymous", "GET", "/api/v1/sites/site-a-0001/summary", "", nil, 401},
		{"garbage token", "GET", "/api/v1/sites/site-a-0001/summary", "nope", nil, 401},
		{"edge token is not a user token", "GET", "/api/v1/sites/site-a-0001/summary", e.edge["site-a-0001"], nil, 401},
		{"non-member sees 404, not 403 (no tenant enumeration)", "GET", "/api/v1/sites/site-a-0001/summary", e.tok["carol@x.test"], nil, 404},
		{"non-member cannot read units", "GET", "/api/v1/sites/site-a-0001/units", e.tok["carol@x.test"], nil, 404},
		{"admin of A cannot read B", "GET", "/api/v1/sites/site-b-0001/units", e.tok["alice@x.test"], nil, 404},
		{"unit of another site", "GET", "/api/v1/sites/site-b-0001/units/unit-sim-004", e.tok["alice@x.test"], nil, 404},
		{"viewer at A reads A", "GET", "/api/v1/sites/site-a-0001/units", e.tok["bob@x.test"], nil, 200},
		{"viewer at A cannot request a change", "POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["bob@x.test"], prog, 403},
		{"same user is admin at B", "POST", "/api/v1/sites/site-b-0001/remote-program-requests", e.tok["bob@x.test"], prog, 202},
		{"admin at A may request", "POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["alice@x.test"], prog, 202},
		{"viewer cannot list members", "GET", "/api/v1/sites/site-a-0001/members", e.tok["bob@x.test"], nil, 403},
		{"viewer cannot rotate the edge credential", "POST", "/api/v1/sites/site-a-0001/edge-credentials/rotate", e.tok["bob@x.test"], nil, 403},
		{"user token on edge route", "GET", "/api/v1/edge/sync/commands", e.tok["alice@x.test"], nil, 401},
	}
	for _, c := range cases {
		if w := e.do(c.method, c.path, c.who, c.body); w.Code != c.want {
			t.Errorf("%s: %s %s -> %d, want %d (%s)", c.name, c.method, c.path, w.Code, c.want, w.Body.String())
		}
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM remote_command_intents WHERE site_id='site-a-0001' AND requested_by=$1`, bob).Scan(&n)
	if n != 0 {
		t.Fatal("a forbidden request created an intent")
	}
	// sites list only shows the caller's sites, with per-site roles
	var out struct{ Sites []siteRow }
	_ = json.Unmarshal(e.do("GET", "/api/v1/sites", e.tok["bob@x.test"], nil).Body.Bytes(), &out)
	roles := map[string]string{}
	for _, s := range out.Sites {
		roles[s.ID] = s.Role
	}
	if len(roles) != 2 || roles["site-a-0001"] != "VIEWER" || roles["site-b-0001"] != "ADMIN" {
		t.Fatalf("roles %v", roles)
	}
	_ = json.Unmarshal(e.do("GET", "/api/v1/sites", e.tok["carol@x.test"], nil).Body.Bytes(), &out)
	if len(out.Sites) != 0 {
		t.Fatal("carol sees sites she is not a member of")
	}
}

func TestEdgeCredentialIsBoundToItsSiteAndStoredHashed(t *testing.T) {
	e := newEnv(t)
	e.site("site-a-0001")
	e.site("site-b-0001")
	// edge A tries to write into site B: body names B -> refused, nothing stored
	w := e.do("POST", "/api/v1/edge/sync/batch", e.edge["site-a-0001"], batch("site-b-0001", state(time.Now(), unitJSON("unit-sim-004", "X", "ONLINE", "RUNNING"))))
	if w.Code != 403 {
		t.Fatalf("cross-site batch: %d", w.Code)
	}
	if e.count(`SELECT COUNT(*) FROM units`) != 0 {
		t.Fatal("cross-site write leaked")
	}
	if w := e.do("POST", "/api/v1/edge/sync/batch", "", batch("site-a-0001", nil)); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := e.do("POST", "/api/v1/edge/sync/batch", "aeroedge_forged", batch("site-a-0001", nil)); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if e.count(`SELECT COUNT(*) FROM site_edge_credentials WHERE token_hash LIKE '%aeroedge%' OR token_hash=$1`, e.edge["site-a-0001"]) != 0 {
		t.Fatal("raw edge token stored in the database")
	}
	// rotate: the old credential dies, the new works; revocation is immediate
	admin := e.user("root@x.test")
	e.member("site-a-0001", admin, "ADMIN")
	var rot struct {
		Token string `json:"edge_token"`
	}
	w = e.do("POST", "/api/v1/sites/site-a-0001/edge-credentials/rotate", e.tok["root@x.test"], nil)
	_ = json.Unmarshal(w.Body.Bytes(), &rot)
	if w.Code != 200 || rot.Token == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	if c := e.do("GET", "/api/v1/edge/sync/commands", e.edge["site-a-0001"], nil).Code; c != 401 {
		t.Fatalf("old credential still valid: %d", c)
	}
	if c := e.do("GET", "/api/v1/edge/sync/commands", rot.Token, nil).Code; c != 200 {
		t.Fatalf("new credential rejected: %d", c)
	}
}

// A04 (cloud half) + A19: idempotent ingest, stable timestamps, poisoned items isolated.
func TestIngestIsIdempotentAndIsolatesPoisonedItems(t *testing.T) {
	e := newEnv(t)
	e.site("site-a-0001")
	eff := time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)
	good1, good2 := telEvent(t, 761, eff, nil), telEvent(t, 762, eff.Add(time.Minute), nil)
	bad := telEvent(t, 763, eff, func(m map[string]any) { m["readings"] = map[string]any{} }) // violates the contract
	ev := sc.Event{EventID: "esp32-sim-004/boot-9f3a1c22-5b/764", EventType: sc.EventDeviceEvent, Priority: 0, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload: envelope(record(t, "event.low_water", 764, nil), eff, "RECEIVED_AT_ONLY")}
	unknown := sc.Event{EventID: "weird-1", EventType: "mystery.type", Payload: json.RawMessage(`{}`)}
	b := batch("site-a-0001", state(time.Now(), unitJSON("unit-sim-004", "U4", "ONLINE", "RUNNING")), good1, good2, bad, ev, unknown)

	send := func() sc.AckResponse {
		w := e.do("POST", "/api/v1/edge/sync/batch", e.edge["site-a-0001"], b)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var r sc.AckResponse
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	r1 := send()
	if len(r1.Acked) != 3 || r1.Duplicates != 0 || len(r1.Rejected) != 2 {
		t.Fatalf("first: %+v", r1)
	}
	if r1.Rejected[bad.EventID] != "INVALID_TELEMETRY" || r1.Rejected["weird-1"] != "UNKNOWN_EVENT_TYPE" {
		t.Fatalf("reasons: %v", r1.Rejected)
	}
	r2 := send() // lost response: the Pi sends the SAME batch again
	if len(r2.Acked) != 3 || r2.Duplicates != 3 {
		t.Fatalf("replay must be acked as duplicates: %+v", r2)
	}
	if n := e.count(`SELECT COUNT(*) FROM telemetry_snapshots`); n != 2 {
		t.Fatalf("telemetry rows %d, want 2 (one logical event, one row)", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM device_events`); n != 1 {
		t.Fatalf("events %d", n)
	}
	// original time evidence preserved, cloud ingest time kept separately
	var sampled sql.NullTime
	var tq string
	var effAt, ingested time.Time
	_ = e.db.QueryRow(`SELECT sampled_at_utc,time_quality,effective_at,cloud_ingested_at FROM telemetry_snapshots WHERE event_id=$1`, good1.EventID).Scan(&sampled, &tq, &effAt, &ingested)
	if sampled.Valid || tq != "ESTIMATED_FROM_UPTIME" || !effAt.Equal(eff) || time.Since(ingested) > time.Minute {
		t.Fatalf("time evidence: sampled=%v tq=%s eff=%v ingested=%v", sampled, tq, effAt, ingested)
	}
	if e.count(`SELECT COUNT(*) FROM sites WHERE id='site-a-0001' AND last_sync_at IS NOT NULL`) != 1 {
		t.Fatal("last_sync_at not recorded")
	}
}

func TestOlderSnapshotNeverOverwritesNewerAndIncidentsNeverReopen(t *testing.T) {
	e := newEnv(t)
	e.site("site-a-0001")
	now := time.Now()
	post := func(b sc.Batch) { e.do("POST", "/api/v1/edge/sync/batch", e.edge["site-a-0001"], b) }
	post(batch("site-a-0001", state(now, unitJSON("unit-sim-004", "NEW", "ONLINE", "RUNNING"))))
	post(batch("site-a-0001", state(now.Add(-time.Hour), unitJSON("unit-sim-004", "OLD", "ONLINE", "RUNNING")))) // late retry
	var name string
	_ = e.db.QueryRow(`SELECT name FROM units WHERE id='unit-sim-004'`).Scan(&name)
	if name != "NEW" {
		t.Fatalf("an hour-old snapshot replaced the current one: %s", name)
	}

	inc := func(status string, changed time.Time) sc.Event {
		p, _ := json.Marshal(sc.IncidentUpdate{ID: "inc-1", UnitID: "unit-sim-004", Severity: "critical", Code: "LOW_WATER", Status: status, Title: "t", FirstSeenAt: now.Format(time.RFC3339), LastSeenAt: now.Format(time.RFC3339), ChangedAt: changed.UTC().Format(time.RFC3339Nano)})
		return sc.Event{EventID: "inc/inc-1/" + status + changed.Format("150405.000"), EventType: sc.EventIncident, Priority: 1, CreatedAt: now.Format(time.RFC3339Nano), Payload: p}
	}
	t1, t2 := now.Add(-10*time.Minute), now.Add(-5*time.Minute)
	post(batch("site-a-0001", nil, inc("RESOLVED_UNACKNOWLEDGED", t2))) // newer arrives first
	post(batch("site-a-0001", nil, inc("ACTIVE_UNACKNOWLEDGED", t1)))   // older transition uploaded late
	var st string
	_ = e.db.QueryRow(`SELECT status FROM incidents WHERE id='inc-1'`).Scan(&st)
	if st != "RESOLVED_UNACKNOWLEDGED" {
		t.Fatalf("late upload re-opened a resolved incident: %s", st)
	}
}

// The cloud cannot see the LAN: when the Pi stops syncing it must say so,
// not keep showing "Running".
func TestStaleSiteIsShownAsLastKnownNotAsLive(t *testing.T) {
	e := newEnv(t)
	u := e.user("v@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", u, "VIEWER")
	e.do("POST", "/api/v1/edge/sync/batch", e.edge["site-a-0001"], batch("site-a-0001", state(time.Now(), unitJSON("unit-sim-004", "U4", "ONLINE", "RUNNING"))))
	get := func() map[string]any {
		var o struct{ Units []map[string]any }
		_ = json.Unmarshal(e.do("GET", "/api/v1/sites/site-a-0001/units", e.tok["v@x.test"], nil).Body.Bytes(), &o)
		return o.Units[0]
	}
	if v := get(); v["connection"] != "ONLINE" || v["control_state"] != "RUNNING" || v["site_stale"] != false {
		t.Fatalf("fresh: %v", v)
	}
	e.exec(`UPDATE sites SET last_sync_at=now()-interval '10 minutes' WHERE id='site-a-0001'`)
	v := get()
	if v["connection"] != "STALE" || v["control_state"] != "UNKNOWN_STALE" || v["pump_commanded"] != nil || v["site_stale"] != true {
		t.Fatalf("stale site presented as live: %v", v)
	}
	if note, _ := v["control_state_note"].(string); !strings.Contains(note, "does not prove the pump stopped") {
		t.Fatalf("note: %q", note)
	}
	if lr := v["latest_readings"].(map[string]any); lr["outdated"] != true || lr["readings"].(map[string]any)["ph"].(map[string]any)["quality"] != "outdated" {
		t.Fatalf("readings not marked outdated: %v", lr)
	}
	var sum struct {
		Connectivity map[string]any
		Units        map[string]int
	}
	_ = json.Unmarshal(e.do("GET", "/api/v1/sites/site-a-0001/summary", e.tok["v@x.test"], nil).Body.Bytes(), &sum)
	if sum.Connectivity["site_wan_reachable"] != false || sum.Connectivity["is_currently_stale"] != true || sum.Units["stale"] != 1 || sum.Units["healthy"] != 0 {
		t.Fatalf("summary: %v", sum)
	}
	var ss map[string]any
	_ = json.Unmarshal(e.do("GET", "/api/v1/sites/site-a-0001/sync-status", e.tok["v@x.test"], nil).Body.Bytes(), &ss)
	if ss["stale"] != true || ss["last_sync_at"] == nil {
		t.Fatalf("%v", ss)
	}
}

// Remote requests are INTENTS: PENDING_SITE -> PENDING_DEVICE -> APPLIED only on edge report.
func TestRemoteIntentLifecycle(t *testing.T) {
	e := newEnv(t)
	adm := e.user("adm@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", adm, "ADMIN")
	e.exec(`INSERT INTO units(site_id,id,name,view) VALUES ('site-a-0001','unit-sim-004','U4',$1)`, unitJSON("unit-sim-004", "U4", "ONLINE", "RUNNING"))
	prog := map[string]any{"mode": "TIMED_CYCLE", "on_duration_seconds": 5, "off_duration_seconds": 60, "max_continuous_on_seconds": 10, "require_reservoir_ok": true}
	req := map[string]any{"command_id": "cmd-remote-0001", "unit_id": "unit-sim-004", "expected_program_version": 3, "program": prog}
	var iv intentView
	w := e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], req)
	_ = json.Unmarshal(w.Body.Bytes(), &iv)
	if w.Code != 202 || iv.Status != "PENDING_SITE" || iv.AppliedConfirmed {
		t.Fatalf("%d %+v", w.Code, iv)
	}
	// idempotent replay, different body refused, second in-flight refused
	if w := e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], req); w.Code != 200 {
		t.Fatalf("replay %d", w.Code)
	}
	req2 := map[string]any{"command_id": "cmd-remote-0001", "unit_id": "unit-sim-004", "expected_program_version": 9, "program": prog}
	if w := e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], req2); w.Code != 409 {
		t.Fatalf("id reuse %d", w.Code)
	}
	req3 := map[string]any{"command_id": "cmd-remote-0002", "unit_id": "unit-sim-004", "expected_program_version": 3, "program": prog}
	if w := e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], req3); w.Code != 409 {
		t.Fatalf("second in-flight %d", w.Code)
	}
	// the Pi fetches it
	var list sc.IntentList
	_ = json.Unmarshal(e.do("GET", "/api/v1/edge/sync/commands", e.edge["site-a-0001"], nil).Body.Bytes(), &list)
	if len(list.Intents) != 1 || list.Intents[0].ID != "cmd-remote-0001" || list.Intents[0].RequestedBy != "adm@x.test" || list.Intents[0].ExpectedProgramVersion != 3 {
		t.Fatalf("%+v", list)
	}
	status := func() string {
		var v intentView
		_ = json.Unmarshal(e.do("GET", "/api/v1/sites/site-a-0001/remote-commands/cmd-remote-0001", e.tok["adm@x.test"], nil).Body.Bytes(), &v)
		return v.Status
	}
	post := func(st string, ver *int) int {
		return e.do("POST", "/api/v1/edge/sync/command-results", e.edge["site-a-0001"], sc.IntentResult{CommandID: "cmd-remote-0001", Status: st, AppliedVersion: ver, ObservedAt: time.Now().Format(time.RFC3339)}).Code
	}
	if post(sc.IntentPendingDevice, nil) != 204 || status() != "PENDING_DEVICE" {
		t.Fatal("PENDING_DEVICE not recorded")
	}
	four := 4
	if post(sc.IntentApplied, &four) != 204 || status() != "APPLIED" {
		t.Fatal("APPLIED not recorded")
	}
	// final is final: a late/contradictory report changes nothing, but is acknowledged
	if post(sc.IntentRejected, nil) != 204 || status() != "APPLIED" {
		t.Fatal("final state changed")
	}
	// once picked up it is not offered again
	_ = json.Unmarshal(e.do("GET", "/api/v1/edge/sync/commands", e.edge["site-a-0001"], nil).Body.Bytes(), &list)
	if len(list.Intents) != 0 {
		t.Fatal("finished intent offered again")
	}
	// another site's Pi cannot report on this intent
	e.site("site-b-0001")
	if c := e.do("POST", "/api/v1/edge/sync/command-results", e.edge["site-b-0001"], sc.IntentResult{CommandID: "cmd-remote-0001", Status: "APPLIED", ObservedAt: "x"}).Code; c != 404 {
		t.Fatalf("cross-site result %d", c)
	}
}

// A06/A21 (cloud half): an old request never executes late.
func TestExpiredIntentIsNeverOfferedAndIsMarkedExpired(t *testing.T) {
	e := newEnv(t)
	adm := e.user("adm@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", adm, "ADMIN")
	e.exec(`INSERT INTO units(site_id,id,name,view) VALUES ('site-a-0001','unit-sim-004','U4',$1)`, unitJSON("unit-sim-004", "U4", "ONLINE", "RUNNING"))
	prog := map[string]any{"mode": "TIMED_CYCLE", "on_duration_seconds": 5, "off_duration_seconds": 60, "max_continuous_on_seconds": 10, "require_reservoir_ok": true}
	e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], map[string]any{"command_id": "cmd-remote-0003", "unit_id": "unit-sim-004", "expected_program_version": 3, "program": prog})
	e.exec(`UPDATE remote_command_intents SET expires_at=now()-interval '1 second' WHERE id='cmd-remote-0003'`)
	var list sc.IntentList
	_ = json.Unmarshal(e.do("GET", "/api/v1/edge/sync/commands", e.edge["site-a-0001"], nil).Body.Bytes(), &list)
	if len(list.Intents) != 0 {
		t.Fatal("an expired request was offered to the Pi")
	}
	var st, reason string
	_ = e.db.QueryRow(`SELECT status,reason FROM remote_command_intents WHERE id='cmd-remote-0003'`).Scan(&st, &reason)
	if st != "EXPIRED" || reason != "EXPIRED_BEFORE_PICKUP" {
		t.Fatalf("%s %s", st, reason)
	}
	// and the unit can be changed again afterwards
	if w := e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], map[string]any{"command_id": "cmd-remote-0004", "unit_id": "unit-sim-004", "expected_program_version": 3, "program": prog}); w.Code != 202 {
		t.Fatal(w.Code)
	}
}

func TestRemoteRequestValidation(t *testing.T) {
	e := newEnv(t)
	adm := e.user("adm@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", adm, "ADMIN")
	e.exec(`INSERT INTO units(site_id,id,name,view) VALUES ('site-a-0001','unit-sim-004','U4',$1)`, unitJSON("unit-sim-004", "U4", "ONLINE", "RUNNING"))
	good := map[string]any{"mode": "TIMED_CYCLE", "on_duration_seconds": 5, "off_duration_seconds": 60, "max_continuous_on_seconds": 10, "require_reservoir_ok": true}
	with := func(f func(p map[string]any)) map[string]any {
		p := map[string]any{}
		for k, v := range good {
			p[k] = v
		}
		f(p)
		return map[string]any{"unit_id": "unit-sim-004", "expected_program_version": 3, "program": p}
	}
	for name, body := range map[string]map[string]any{
		"interlock disabled": with(func(p map[string]any) { p["require_reservoir_ok"] = false }),
		"zero on time":       with(func(p map[string]any) { p["on_duration_seconds"] = 0 }),
		"max below on":       with(func(p map[string]any) { p["max_continuous_on_seconds"] = 1 }),
		"unknown mode":       with(func(p map[string]any) { p["mode"] = "ALWAYS_ON" }),
		"unknown unit":       {"unit_id": "unit-nope-99", "expected_program_version": 3, "program": good},
	} {
		want := 400
		if name == "unknown unit" {
			want = 404
		}
		if w := e.do("POST", "/api/v1/sites/site-a-0001/remote-program-requests", e.tok["adm@x.test"], body); w.Code != want {
			t.Errorf("%s: %d want %d", name, w.Code, want)
		}
	}
	// there is no cloud route for a manual pump test at all
	if w := e.do("POST", "/api/v1/sites/site-a-0001/units/unit-sim-004/maintenance-tests", e.tok["adm@x.test"], map[string]any{}); w.Code != 404 && w.Code != 405 {
		t.Fatalf("a cloud pump-test route exists: %d", w.Code)
	}
	if e.count(`SELECT COUNT(*) FROM remote_command_intents`) != 0 {
		t.Fatal("invalid requests stored")
	}
}

func TestHistoryFromSyncedTelemetryShowsGapsAndBounds(t *testing.T) {
	e := newEnv(t)
	u := e.user("v@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", u, "VIEWER")
	base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	evs := []sc.Event{
		telEvent(t, 1, base, nil), telEvent(t, 2, base.Add(time.Minute), nil), telEvent(t, 3, base.Add(2*time.Minute), func(m map[string]any) {
			m["readings"] = map[string]any{"ph": map[string]any{"value": nil, "quality": "invalid"}, "light_lux": map[string]any{"value": nil, "quality": "missing"}}
		}),
		telEvent(t, 4, base.Add(12*time.Minute), nil),
	}
	if w := e.do("POST", "/api/v1/edge/sync/batch", e.edge["site-a-0001"], batch("site-a-0001", nil, evs...)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	from, to := base.Add(-time.Minute).Format(time.RFC3339), base.Add(20*time.Minute).Format(time.RFC3339)
	w := e.do("GET", "/api/v1/sites/site-a-0001/units/unit-sim-004/readings/history?resolution=raw&from="+from+"&to="+to, e.tok["v@x.test"], nil)
	var h struct {
		Points []struct {
			Readings map[string]map[string]any
		}
		Gaps []map[string]string
		Sim  bool `json:"contains_simulated_data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &h)
	if w.Code != 200 || len(h.Points) != 4 || len(h.Gaps) != 1 || !h.Sim {
		t.Fatalf("%d points=%d gaps=%d: %s", w.Code, len(h.Points), len(h.Gaps), w.Body.String())
	}
	if p := h.Points[2].Readings["ph"]; p["value"] != nil || p["quality"] != "invalid" {
		t.Fatalf("invalid pH became a number: %v", p)
	}
	for _, q := range []string{"resolution=weird", "from=" + time.Now().Add(-40*24*time.Hour).Format(time.RFC3339), "resolution=1m&from=" + time.Now().Add(-30*24*time.Hour).Format(time.RFC3339)} {
		if c := e.do("GET", "/api/v1/sites/site-a-0001/units/unit-sim-004/readings/history?"+q, e.tok["v@x.test"], nil).Code; c != 400 {
			t.Errorf("%s -> %d", q, c)
		}
	}
}

func TestLastAdminProtectionAndMemberManagement(t *testing.T) {
	e := newEnv(t)
	adm := e.user("adm@x.test")
	vw := e.user("vw@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", adm, "ADMIN")
	if w := e.do("PUT", "/api/v1/sites/site-a-0001/members/"+adm, e.tok["adm@x.test"], map[string]any{"role": "VIEWER"}); w.Code != 409 {
		t.Fatalf("demoted the last admin: %d", w.Code)
	}
	if w := e.do("DELETE", "/api/v1/sites/site-a-0001/members/"+adm, e.tok["adm@x.test"], nil); w.Code != 409 {
		t.Fatalf("removed the last admin: %d", w.Code)
	}
	if w := e.do("PUT", "/api/v1/sites/site-a-0001/members/"+vw, e.tok["adm@x.test"], map[string]any{"role": "OPERATOR"}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := e.do("PUT", "/api/v1/sites/site-a-0001/members/"+vw, e.tok["adm@x.test"], map[string]any{"role": "ROOT"}); w.Code != 400 {
		t.Fatalf("invented role accepted: %d", w.Code)
	}
	// a revoked membership takes effect immediately (cloud enforces revocation at once)
	if w := e.do("DELETE", "/api/v1/sites/site-a-0001/members/"+vw, e.tok["adm@x.test"], nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if c := e.do("GET", "/api/v1/sites/site-a-0001/units", e.tok["vw@x.test"], nil).Code; c != 404 {
		t.Fatalf("removed member still reads the site: %d", c)
	}
}

func TestLoginThrottleAndSessionRevocation(t *testing.T) {
	e := newEnv(t)
	e.user("a@x.test")
	login := func(em, pw string) int {
		return e.do("POST", "/api/v1/auth/login", "", map[string]any{"email": em, "password": pw}).Code
	}
	if login("a@x.test", "wrong-password-1") != 401 || login("ghost@x.test", "wrong-password-1") != 401 {
		t.Fatal("must be 401 for known and unknown accounts alike")
	}
	for i := 0; i < 5; i++ {
		login("a@x.test", "wrong-password-x")
	}
	if c := login("a@x.test", "correct-horse-battery"); c != 429 {
		t.Fatalf("not throttled: %d", c)
	}
	// logout revokes the session
	if w := e.do("POST", "/api/v1/auth/logout", e.tok["a@x.test"], nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if c := e.do("GET", "/api/v1/session", e.tok["a@x.test"], nil).Code; c != 401 {
		t.Fatalf("revoked session still valid: %d", c)
	}
	// passwords are never stored or audited in clear
	if e.count(`SELECT COUNT(*) FROM cloud_users WHERE password_hash LIKE '%correct-horse%'`) != 0 {
		t.Fatal("plaintext password stored")
	}
}

func TestSessionShapeForSharedWebApp(t *testing.T) {
	e := newEnv(t)
	u := e.user("o@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", u, "OPERATOR")
	var s struct {
		Source string
		Site   map[string]any
		User   map[string]any
		Caps   []string `json:"capabilities"`
	}
	_ = json.Unmarshal(e.do("GET", "/api/v1/session", e.tok["o@x.test"], nil).Body.Bytes(), &s)
	if s.Source != "cloud" || s.Site["id"] != "site-a-0001" || s.User["role"] != "OPERATOR" {
		t.Fatalf("%+v", s)
	}
	for _, c := range s.Caps {
		if c == "edit_program" {
			t.Fatal("operator granted edit_program")
		}
	}
}

func TestRoutesMatchOpenAPIExactly(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/openapi/cloud.v1.yaml")
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
	s := New(&config.Config{}, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code := map[string]bool{}
	for _, r := range s.Routes() {
		code[r] = true
	}
	for r := range code {
		if !spec[r] {
			t.Errorf("implemented but undocumented: %s", r)
		}
	}
	for r := range spec {
		if !code[r] {
			t.Errorf("documented but not implemented: %s", r)
		}
	}
}
