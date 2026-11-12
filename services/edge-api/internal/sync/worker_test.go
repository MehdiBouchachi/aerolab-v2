package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync"
	"testing"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

type fakeCloud struct {
	mu       gosync.Mutex
	srv      *httptest.Server
	down     bool
	batches  []sc.Batch
	intents  []sc.Intent
	results  []sc.IntentResult
	rejectID string
	auth     string
}

func newCloud(t *testing.T) *fakeCloud {
	f := &fakeCloud{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = r.Header.Get("Authorization")
		if f.down {
			http.Error(w, "down", 503)
			return
		}
		switch r.URL.Path {
		case "/api/v1/edge/sync/batch":
			var b sc.Batch
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.batches = append(f.batches, b)
			ack := sc.AckResponse{Acked: []string{}, Rejected: map[string]string{}}
			for _, e := range b.Events {
				if e.EventID == f.rejectID {
					ack.Rejected[e.EventID] = "INVALID_TELEMETRY"
				} else {
					ack.Acked = append(ack.Acked, e.EventID)
				}
			}
			_ = json.NewEncoder(w).Encode(ack)
		case "/api/v1/edge/sync/commands":
			l := sc.IntentList{Intents: f.intents}
			f.intents = nil
			_ = json.NewEncoder(w).Encode(l)
		case "/api/v1/edge/sync/command-results":
			var res sc.IntentResult
			_ = json.NewDecoder(r.Body).Decode(&res)
			f.results = append(f.results, res)
			w.WriteHeader(204)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCloud) take() ([]sc.Batch, []sc.IntentResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batches, f.results
}

type rig struct {
	w   *Worker
	db  *storage.Store
	f   *fakeCloud
	reg *devices.Registry
	cm  *commands.Manager
}

func newRig(t *testing.T, remote bool) *rig {
	db := testutil.Store(t)
	f := newCloud(t)
	reg := devices.NewRegistry(db, "site-test-01")
	if _, _, err := reg.Provision(context.Background(), "esp32-test-001", "unit-test-001", "U1", "sim", "SIMULATOR", "t"); err != nil {
		t.Fatal(err)
	}
	pub := testutil.NewFakePub()
	cm := commands.New(db, reg, pub, sse.NewHub(), commands.Options{SiteID: "site-test-01", StaleAfter: 10 * time.Second, Retry: time.Second, MaxTries: 3, TTL: time.Minute}, testutil.Logger())
	state := func(context.Context) (sc.State, error) { return sc.State{ObservedAt: storage.Now()}, nil }
	w := NewWorker(Options{SiteID: "site-test-01", URL: f.srv.URL, Token: "aeroedge_SECRETTOKEN", Interval: 20 * time.Millisecond, RemotePrograms: remote}, db, cm, state, testutil.Logger())
	return &rig{w, db, f, reg, cm}
}

func (r *rig) outbox(t *testing.T, id, typ string, pri int) {
	_, err := r.db.W.Exec(`INSERT INTO outbox(event_id,event_type,priority,site_id,payload_json,created_at) VALUES (?,?,?,?,?,?)`, id, typ, pri, "site-test-01", `{}`, storage.Now())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
}

func (r *rig) heartbeat(version int) {
	u := "unit-test-001"
	s := mc.State{Header: mc.Header{SchemaVersion: 1, SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: &u, BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator},
		ControlState: mc.StateRunning, PumpCommanded: "OFF", WaterDelivery: "NOT_VERIFIED", EssentialInterlocksOK: true, FaultCodes: []string{}, FirmwareVersion: "sim", SessionNonce: "nonce-12345678"}
	if version > 0 {
		s.ProgramVersionApplied = &version
	}
	r.reg.ApplyState("esp32-test-001", s, time.Now())
}

func TestStrictPriorityOrderAndAckMarking(t *testing.T) {
	r := newRig(t, false)
	// oldest first insertion order is the WORST order: P3 telemetry, then P1, then P0
	r.outbox(t, "tel-1", sc.EventTelemetry, sc.P3)
	r.outbox(t, "tel-2", sc.EventTelemetry, sc.P3)
	r.outbox(t, "inc-1", sc.EventIncident, sc.P1)
	r.outbox(t, "crit-1", sc.EventDeviceEvent, sc.P0)
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := r.f.take()
	var got []string
	for _, e := range b[0].Events {
		got = append(got, e.EventID)
	}
	if strings.Join(got, ",") != "crit-1,inc-1,tel-1,tel-2" {
		t.Fatalf("order %v: critical/incident data must go before historical telemetry", got)
	}
	if b[0].State == nil || b[0].SiteID != "site-test-01" || b[0].SchemaVersion != 1 {
		t.Fatal("current-truth state snapshot missing from the batch")
	}
	var pending int
	_ = r.db.R.QueryRow(`SELECT COUNT(*) FROM outbox WHERE cloud_ack_at IS NULL`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("%d rows still pending after a full ack", pending)
	}
}

// A18/A19 (edge half): the cloud is down -> nothing is lost, nothing is acked,
// bounded failures are surfaced, and recovery resends the same ids.
func TestCloudDownKeepsOutboxAndRecoversWithSameIds(t *testing.T) {
	r := newRig(t, false)
	r.outbox(t, "tel-1", sc.EventTelemetry, sc.P3)
	r.f.mu.Lock()
	r.f.down = true
	r.f.mu.Unlock()
	if _, err := r.w.cycle(context.Background()); err == nil {
		t.Fatal("a 503 must surface as an error")
	}
	var pending, attempts int
	_ = r.db.R.QueryRow(`SELECT COUNT(*),COALESCE(SUM(attempts),0) FROM outbox WHERE cloud_ack_at IS NULL`).Scan(&pending, &attempts)
	if pending != 1 || attempts != 1 {
		t.Fatalf("pending=%d attempts=%d", pending, attempts)
	}
	r.f.mu.Lock()
	r.f.down = false
	r.f.mu.Unlock()
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := r.f.take()
	if len(b) != 1 || b[0].Events[0].EventID != "tel-1" {
		t.Fatalf("resend must reuse the same event id: %+v", b)
	}
}

func TestBackoffIsBoundedExponentialWithJitter(t *testing.T) {
	base := time.Second
	for f := 0; f < 40; f++ {
		d := backoff(base, f)
		if d > 7500*time.Millisecond*60 { // 5 min cap * 1.5 jitter
			t.Fatalf("failures=%d: %v exceeds the cap", f, d)
		}
		if d < base/2 {
			t.Fatalf("failures=%d: %v below minimum", f, d)
		}
	}
	if backoff(base, 0) > 1500*time.Millisecond || backoff(base, 3) < 4*time.Second {
		t.Fatal("not exponential")
	}
	seen := map[time.Duration]bool{}
	for i := 0; i < 20; i++ {
		seen[backoff(base, 5)] = true
	}
	if len(seen) < 5 {
		t.Fatal("no jitter: a fleet of sites would reconnect in lock-step")
	}
}

func TestRejectedPoisonItemIsRetiredNotResentForever(t *testing.T) {
	r := newRig(t, false)
	r.outbox(t, "tel-bad", sc.EventTelemetry, sc.P3)
	r.outbox(t, "tel-ok", sc.EventTelemetry, sc.P3)
	r.f.rejectID = "tel-bad"
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pending int
	_ = r.db.R.QueryRow(`SELECT COUNT(*) FROM outbox WHERE cloud_ack_at IS NULL`).Scan(&pending)
	if pending != 0 || r.w.Status().RejectedItems != 1 {
		t.Fatalf("pending=%d rejected=%d", pending, r.w.Status().RejectedItems)
	}
}

func TestBatchesAreBoundedTo100Events(t *testing.T) {
	r := newRig(t, false)
	for i := 0; i < 250; i++ {
		r.outbox(t, "tel-"+string(rune('a'+i/26/26%26))+string(rune('a'+i/26%26))+string(rune('a'+i%26)), sc.EventTelemetry, sc.P3)
	}
	more, err := r.w.cycle(context.Background())
	if err != nil || !more {
		t.Fatalf("more=%v err=%v", more, err)
	}
	b, _ := r.f.take()
	if len(b[0].Events) != 100 {
		t.Fatalf("batch of %d", len(b[0].Events))
	}
}

func TestTokenIsSentAsBearerButNeverLeaksIntoErrors(t *testing.T) {
	r := newRig(t, false)
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.f.auth != "Bearer aeroedge_SECRETTOKEN" {
		t.Fatalf("auth header %q", r.f.auth)
	}
	r.f.srv.Close() // unreachable
	_, err := r.w.cycle(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatalf("token leaked into error text or no error: %v", err)
	}
}

// ---- remote intents: the Pi is the second line of authorisation

func (r *rig) intent(id string, expected int, mut func(*sc.Intent)) {
	in := sc.Intent{ID: id, UnitID: "unit-test-001", ExpectedProgramVersion: expected, RequestedBy: "remote@x.test",
		Program:   mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 5, OffDurationSeconds: 60, MaxContinuousOnSecs: 10, RequireReservoirOK: true},
		CreatedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}
	if mut != nil {
		mut(&in)
	}
	r.f.mu.Lock()
	r.f.intents = append(r.f.intents, in)
	r.f.mu.Unlock()
}

func (r *rig) lastResult(t *testing.T, id string) sc.IntentResult {
	_, res := r.f.take()
	for i := len(res) - 1; i >= 0; i-- {
		if res[i].CommandID == id {
			return res[i]
		}
	}
	t.Fatalf("no result reported for %s (%v)", id, res)
	return sc.IntentResult{}
}

func TestRemoteChangesAreOffByDefaultOnASite(t *testing.T) {
	r := newRig(t, false)
	r.heartbeat(0)
	r.intent("cmd-remote-0001", 0, nil)
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res := r.lastResult(t, "cmd-remote-0001"); res.Status != sc.IntentRejected || res.Reason != "REMOTE_CHANGES_DISABLED_ON_SITE" {
		t.Fatalf("%+v", res)
	}
	var n int
	_ = r.db.R.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&n)
	if n != 0 {
		t.Fatal("a command was created although the site never opted in")
	}
}

func TestRemoteIntentRevalidatedLocally(t *testing.T) {
	r := newRig(t, true)
	r.heartbeat(5) // the controller already runs v5
	// A06: the cloud still thinks v4
	r.intent("cmd-remote-0002", 4, nil)
	// expired in transit
	r.intent("cmd-remote-0003", 5, func(i *sc.Intent) { i.ExpiresAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339) })
	// the cloud's own requester claim is not enough: bounds are re-checked here
	r.intent("cmd-remote-0004", 5, func(i *sc.Intent) { i.Program.RequireReservoirOK = false })
	r.intent("cmd-remote-0005", 5, func(i *sc.Intent) { i.RequestedBy = "" })
	for i := 0; i < 4; i++ {
		if _, err := r.w.cycle(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string][2]string{
		"cmd-remote-0002": {sc.IntentConflict, "PROGRAM_VERSION_CONFLICT"},
		"cmd-remote-0003": {sc.IntentExpired, "EXPIRED_BEFORE_PICKUP"},
		"cmd-remote-0004": {sc.IntentRejected, "INVALID_PROGRAM"},
		"cmd-remote-0005": {sc.IntentRejected, "MISSING_REQUESTER"},
	}
	for id, w := range want {
		if res := r.lastResult(t, id); res.Status != w[0] || res.Reason != w[1] {
			t.Errorf("%s: %+v want %v", id, res, w)
		}
	}
	var n int
	_ = r.db.R.QueryRow(`SELECT COUNT(*) FROM commands`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d commands created from requests that must have been refused", n)
	}
}

// Demo step 10: Pending Site -> Pending Device -> Applied ONLY after the ESP32 confirms.
func TestRemoteIntentAppliedOnlyAfterControllerConfirms(t *testing.T) {
	r := newRig(t, true)
	r.heartbeat(5)
	r.intent("cmd-remote-0006", 5, nil)
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.w.cycle(context.Background()); err != nil { // report cycle
		t.Fatal(err)
	}
	res := r.lastResult(t, "cmd-remote-0006")
	if res.Status != sc.IntentPendingDevice || res.AppliedVersion != nil {
		t.Fatalf("before the controller answers the cloud may only hear PENDING_DEVICE: %+v", res)
	}
	var by string
	_ = r.db.R.QueryRow(`SELECT requested_by FROM commands WHERE id='cmd-remote-0006'`).Scan(&by)
	if by != "cloud:remote@x.test" {
		t.Fatalf("requester attribution lost: %s", by)
	}
	// the controller confirms (as the MQTT result path would)
	six := 6
	rr := mc.CommandResult{SchemaVersion: 1, CommandID: "cmd-remote-0006", SiteID: "site-test-01", DeviceID: "esp32-test-001", BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator, Status: mc.ResultApplied, CurrentProgramVersion: &six}
	var after func()
	if err := r.db.Tx(context.Background(), func(tx *sqlTx) error {
		var e error
		after, e = r.cm.ApplyResult(context.Background(), tx, rr)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if after != nil {
		after()
	}
	if _, err := r.w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	res = r.lastResult(t, "cmd-remote-0006")
	if res.Status != sc.IntentApplied || res.AppliedVersion == nil || *res.AppliedVersion != 6 {
		t.Fatalf("%+v", res)
	}
	// reported exactly once per status change
	_, all := r.f.take()
	n := 0
	for _, x := range all {
		if x.CommandID == "cmd-remote-0006" && x.Status == sc.IntentApplied {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("APPLIED reported %d times", n)
	}
}
