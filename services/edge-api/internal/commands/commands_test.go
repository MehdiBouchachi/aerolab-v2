package commands

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

type fx struct {
	m   *Manager
	pub *testutil.FakePub
	reg *devices.Registry
	db  *storage.Store
	adm auth.User
}

var prog = mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 5, OffDurationSeconds: 60, MaxContinuousOnSecs: 10, RequireReservoirOK: true}

func setup(t *testing.T, mut func(*Options)) *fx {
	t.Helper()
	db := testutil.Store(t)
	pub := testutil.NewFakePub()
	reg := devices.NewRegistry(db, "site-test-01")
	ctx := context.Background()
	if _, _, err := reg.Provision(ctx, "esp32-test-001", "unit-test-001", "U1", "sim", "SIMULATOR", "t"); err != nil {
		t.Fatal(err)
	}
	opt := Options{SiteID: "site-test-01", StaleAfter: 10 * time.Second, Retry: 50 * time.Millisecond, MaxTries: 3, TTL: time.Minute}
	if mut != nil {
		mut(&opt)
	}
	a := auth.New(db, "site-test-01")
	u, err := a.CreateUser(ctx, "admin", "correct-horse-battery", roles.Admin, "t")
	if err != nil {
		t.Fatal(err)
	}
	f := &fx{NewManagerFor(db, reg, pub, opt), pub, reg, db, u}
	f.heartbeat(0, nil)
	return f
}

func NewManagerFor(db *storage.Store, reg *devices.Registry, pub Publisher, opt Options) *Manager {
	return New(db, reg, pub, sse.NewHub(), opt, testutil.Logger())
}

// heartbeat simulates the controller reporting its applied version.
func (f *fx) heartbeat(version int, lastCmd *string) {
	u := "unit-test-001"
	s := mc.State{Header: mc.Header{SchemaVersion: 1, SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: &u, BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator},
		ControlState: mc.StateRunning, PumpCommanded: "OFF", WaterDelivery: "NOT_VERIFIED", EssentialInterlocksOK: true, FaultCodes: []string{},
		FirmwareVersion: "sim", SessionNonce: "nonce-12345678", LastCommandID: lastCmd}
	if version > 0 {
		s.ProgramVersionApplied = &version
	}
	f.reg.ApplyState("esp32-test-001", s, time.Now())
}

func (f *fx) create(t *testing.T, id string, expected int) (Command, bool, error) {
	return f.m.CreateProgramRequest(context.Background(), f.adm, "unit-test-001", ProgramRequest{CommandID: id, ExpectedProgramVersion: expected, Program: prog})
}

func (f *fx) result(t *testing.T, id string, st mc.ResultStatus, ver int, reason string, orig mc.ResultStatus) {
	t.Helper()
	r := mc.CommandResult{SchemaVersion: 1, CommandID: id, SiteID: "site-test-01", DeviceID: "esp32-test-001", BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator, Status: st, OriginalStatus: orig}
	if ver >= 0 {
		r.CurrentProgramVersion = &ver
	}
	if reason != "" {
		r.ReasonCode = &reason
	}
	var after func()
	if err := f.db.Tx(context.Background(), func(tx *sqlTx) error {
		var e error
		after, e = f.m.ApplyResult(context.Background(), tx, r)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if after != nil {
		after()
	}
}

func status(t *testing.T, f *fx, id string) Command {
	c, err := f.m.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPendingIsNeverAppliedUntilDeviceConfirms(t *testing.T) {
	f := setup(t, nil)
	c, created, err := f.create(t, "cmd-test-0001", 0)
	if err != nil || !created {
		t.Fatal(err)
	}
	if c.Status != Sent || c.AppliedConfirmed {
		t.Fatalf("status=%s confirmed=%v: must be SENT/unconfirmed", c.Status, c.AppliedConfirmed)
	}
	// the wire command is bound to the controller's session, never retained, version-checked
	msgs := f.pub.Take("/commands")
	if len(msgs) != 1 || msgs[0].Retained || msgs[0].QoS != 1 {
		t.Fatalf("%+v", msgs)
	}
	var wire mc.Command
	_ = json.Unmarshal(msgs[0].Payload, &wire)
	if err := wire.Validate(); err != nil || wire.SessionNonce != "nonce-12345678" || wire.NewProgramVersion != 1 || wire.ExpectedProgramVersion != 0 {
		t.Fatalf("wire command: %v %+v", err, wire)
	}
	f.result(t, c.ID, mc.ResultApplied, 1, "", "")
	c = status(t, f, c.ID)
	if c.Status != Applied || !c.AppliedConfirmed {
		t.Fatal(c.Status)
	}
	var v int
	_ = f.db.R.QueryRow(`SELECT applied_program_version FROM growing_units WHERE id='unit-test-001'`).Scan(&v)
	if v != 1 {
		t.Fatalf("unit applied version %d", v)
	}
}

func TestAppliedClaimRequiresMatchingVersion(t *testing.T) {
	f := setup(t, nil)
	c, _, _ := f.create(t, "cmd-test-0002", 0)
	f.result(t, c.ID, mc.ResultApplied, 7, "", "") // device says APPLIED but reports the wrong version
	if got := status(t, f, c.ID); got.Status != Unknown || got.AppliedConfirmed {
		t.Fatalf("inconsistent evidence must not become APPLIED: %s", got.Status)
	}
}

// A06: an old/stale intent never overwrites a newer applied version.
func TestStaleExpectedVersionIsRejectedBeforeAnythingIsSent(t *testing.T) {
	f := setup(t, nil)
	f.heartbeat(5, nil) // controller already runs v5
	_, _, err := f.create(t, "cmd-test-0003", 4)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != "PROGRAM_VERSION_CONFLICT" || ce.Details["actual_version"] != 5 || ce.Details["expected_version"] != 4 {
		t.Fatalf("%v", err)
	}
	if len(f.pub.Take("/commands")) != 0 {
		t.Fatal("a conflicting request reached the broker")
	}
}

func TestDeviceRejectionAndConflictMapping(t *testing.T) {
	f := setup(t, nil)
	c, _, _ := f.create(t, "cmd-test-0004", 0)
	f.result(t, c.ID, mc.ResultRejected, 3, "VERSION_CONFLICT", "")
	if got := status(t, f, c.ID); got.Status != Conflict || got.AppliedConfirmed {
		t.Fatal(got.Status)
	}
	var st string
	_ = f.db.R.QueryRow(`SELECT status FROM programs WHERE unit_id='unit-test-001' AND version=1`).Scan(&st)
	if st != "REJECTED" {
		t.Fatalf("programme row %s", st)
	}
	// terminal states are immutable: a late contradictory result changes nothing
	f.result(t, c.ID, mc.ResultApplied, 1, "", "")
	if got := status(t, f, c.ID); got.Status != Conflict {
		t.Fatalf("final state changed to %s", got.Status)
	}
}

// A05: lost ack -> retries (max 3) -> UNKNOWN -> never assumed.
func TestRetriesAreBoundedThenUnknown(t *testing.T) {
	f := setup(t, nil)
	c, _, _ := f.create(t, "cmd-test-0005", 0)
	for i := 0; i < 12; i++ {
		time.Sleep(60 * time.Millisecond)
		f.m.sweep(context.Background())
	}
	if n := len(f.pub.Take("/commands")); n != 3 {
		t.Fatalf("sent %d times, want exactly 3", n)
	}
	got := status(t, f, c.ID)
	if got.Status != Unknown || got.AppliedConfirmed || got.ReasonCode == nil || *got.ReasonCode != "NO_RESULT_AFTER_RETRIES" {
		t.Fatalf("%+v", got)
	}
	// the same wire command id was used each time, so a device dedups it
	ids := map[string]bool{}
	for _, m := range f.pub.Take("/commands") {
		var w mc.Command
		_ = json.Unmarshal(m.Payload, &w)
		ids[w.CommandID] = true
	}
	if len(ids) != 1 {
		t.Fatal("retries must reuse the same command_id")
	}
	// a late ALREADY_PROCESSED{APPLIED} proves the truth and upgrades UNKNOWN
	f.result(t, c.ID, mc.ResultAlreadyProcessed, 1, "", mc.ResultApplied)
	if got := status(t, f, c.ID); got.Status != Applied {
		t.Fatal(got.Status)
	}
}

func TestReconcileFromHeartbeatWhenResultLost(t *testing.T) {
	f := setup(t, nil)
	c, _, _ := f.create(t, "cmd-test-0006", 0)
	st := mc.State{Header: mc.Header{DeviceID: "esp32-test-001"}, LastCommandID: &c.ID}
	v := 1
	st.ProgramVersionApplied = &v
	var after func()
	_ = f.db.Tx(context.Background(), func(tx *sqlTx) error {
		var e error
		after, e = f.m.ReconcileFromState(context.Background(), tx, "esp32-test-001", st)
		return e
	})
	if after != nil {
		after()
	}
	got := status(t, f, c.ID)
	if got.Status != Applied || got.ReasonCode == nil || *got.ReasonCode != "RECONCILED_FROM_STATE" {
		t.Fatalf("%+v", got)
	}
}

func TestExpiryBeforeDeliveryNeverExecutes(t *testing.T) {
	f := setup(t, func(o *Options) { o.TTL = 5 * time.Second })
	f.pub.Up = false // broker down: nothing can be sent
	c, _, err := f.create(t, "cmd-test-0007", 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != Pending || c.Attempts != 0 {
		t.Fatalf("%+v", c)
	}
	f.db.W.Exec(`UPDATE commands SET expires_at_utc=? WHERE id=?`, storage.FormatTime(time.Now().Add(-time.Second)), c.ID)
	f.pub.Up = true
	f.m.sweep(context.Background())
	if got := status(t, f, c.ID); got.Status != Expired {
		t.Fatal(got.Status)
	}
	if len(f.pub.Take("/commands")) != 0 {
		t.Fatal("an expired command was sent")
	}
}

func TestIdempotencyKeyAndSingleInflight(t *testing.T) {
	f := setup(t, nil)
	a, created, err := f.create(t, "cmd-test-0008", 0)
	if err != nil || !created {
		t.Fatal(err)
	}
	b, created, err := f.create(t, "cmd-test-0008", 0) // double-click / retried POST
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("replay not idempotent: %v created=%v", err, created)
	}
	if n := len(f.pub.Take("/commands")); n != 1 {
		t.Fatalf("replay re-sent the command (%d)", n)
	}
	// same id, different body -> refused
	_, _, err = f.m.CreateProgramRequest(context.Background(), f.adm, "unit-test-001", ProgramRequest{CommandID: "cmd-test-0008", ExpectedProgramVersion: 0, Program: mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 9, OffDurationSeconds: 9, MaxContinuousOnSecs: 9, RequireReservoirOK: true}})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("%v", err)
	}
	// a different command while the first is in flight -> COMMAND_PENDING
	_, _, err = f.create(t, "cmd-test-0009", 0)
	if !errors.As(err, &ce) || ce.Code != "COMMAND_PENDING" {
		t.Fatalf("%v", err)
	}
}

func TestOfflineDeviceAndInvalidProgramRefused(t *testing.T) {
	f := setup(t, nil)
	var ce *Error
	bad := prog
	bad.RequireReservoirOK = false
	_, _, err := f.m.CreateProgramRequest(context.Background(), f.adm, "unit-test-001", ProgramRequest{ExpectedProgramVersion: 0, Program: bad})
	if !errors.As(err, &ce) || ce.Code != "VALIDATION_FAILED" {
		t.Fatalf("interlock-disabling programme accepted: %v", err)
	}
	// stale controller
	g := setup(t, func(o *Options) { o.StaleAfter = time.Millisecond })
	time.Sleep(5 * time.Millisecond)
	_, _, err = g.create(t, "cmd-test-0010", 0)
	if !errors.As(err, &ce) || ce.Code != "DEVICE_OFFLINE" {
		t.Fatalf("%v", err)
	}
}

func TestResultFromAnotherDeviceIsIgnored(t *testing.T) {
	f := setup(t, nil)
	c, _, _ := f.create(t, "cmd-test-0011", 0)
	r := mc.CommandResult{SchemaVersion: 1, CommandID: c.ID, SiteID: "site-test-01", DeviceID: "esp32-other-9", BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator, Status: mc.ResultApplied}
	v := 1
	r.CurrentProgramVersion = &v
	_ = f.db.Tx(context.Background(), func(tx *sqlTx) error { _, e := f.m.ApplyResult(context.Background(), tx, r); return e })
	if got := status(t, f, c.ID); got.Status != Sent {
		t.Fatalf("spoofed result changed state: %s", got.Status)
	}
}
