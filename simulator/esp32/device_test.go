package esp32

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

func testDevice(t *testing.T, mut func(*Config)) *Device {
	t.Helper()
	cfg := Config{SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: "unit-test-001",
		BrokerURL:   "tcp://127.0.0.1:1", // nothing listens: the device is permanently "offline"
		ControlTick: 10 * time.Millisecond, SelfCheckTime: 30 * time.Millisecond, TelemetryEvery: time.Hour, HeartbeatEvery: time.Hour,
		ReconnectMin: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond, Seed: 1}
	if mut != nil {
		mut(&cfg)
	}
	d := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	t.Cleanup(func() { cancel(); d.Stop() })
	return d
}

var prog1s = mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 1, OffDurationSeconds: 1, MaxContinuousOnSecs: 1, RequireReservoirOK: true}

func waitState(t *testing.T, d *Device, want mc.ControlState) {
	t.Helper()
	if err := WaitFor(3*time.Second, func() bool { return d.Status().ControlState == want }); err != nil {
		t.Fatalf("want %s, have %s", want, d.Status().ControlState)
	}
}

func cmd(id string, expected, next int, p mc.Program, nonce string) []byte {
	now := time.Now().UTC()
	c := mc.Command{SchemaVersion: 1, CommandID: id, SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: "unit-test-001",
		CommandType: mc.CmdApplyProgram, SessionNonce: nonce, ExpectedProgramVersion: expected, NewProgramVersion: next,
		IssuedAtUTC: now.Format(time.RFC3339), ExpiresAtUTC: now.Add(time.Minute).Format(time.RFC3339), TTLSeconds: 60, Program: &p}
	b, _ := json.Marshal(c)
	return b
}

// A20: unconfigured device keeps outputs safe - no programme, no irrigation.
func TestUnconfiguredDeviceNeverIrrigates(t *testing.T) {
	d := testDevice(t, nil)
	waitState(t, d, mc.StateNeedsSetup)
	time.Sleep(300 * time.Millisecond)
	if s := d.Status(); s.PumpOn || s.PumpCycles != 0 {
		t.Fatalf("pump ran without a programme: %+v", s)
	}
}

// Core autonomy: the broker is unreachable for the whole test and the saved
// programme still cycles the pump.
func TestCycleContinuesWithoutAnyNetwork(t *testing.T) {
	d := testDevice(t, nil)
	if err := d.Provision(prog1s, 1); err != nil {
		t.Fatal(err)
	}
	waitState(t, d, mc.StateRunning)
	if err := WaitFor(6*time.Second, func() bool { return d.Status().PumpCycles >= 2 }); err != nil {
		t.Fatalf("pump did not cycle offline: %+v", d.Status())
	}
	if d.Status().Connected {
		t.Fatal("test premise broken: device is connected")
	}
}

// A08: low water -> protected stop immediately, pump off, recovery needs a
// cleared fault AND explicit local confirmation.
func TestLowWaterProtectedStopAndRecovery(t *testing.T) {
	d := testDevice(t, nil)
	_ = d.Provision(prog1s, 1)
	waitState(t, d, mc.StateRunning)
	d.SetFaults(Faults{LowWater: true})
	waitState(t, d, mc.StateProtectedStop)
	if d.Status().PumpOn {
		t.Fatal("pump on during protected stop")
	}
	cycles := d.Status().PumpCycles
	time.Sleep(1500 * time.Millisecond)
	if d.Status().PumpCycles != cycles {
		t.Fatal("pump started while in protected stop")
	}
	if d.ConfirmRecovery() {
		t.Fatal("recovery confirmed while fault still present")
	}
	d.SetFaults(Faults{})
	time.Sleep(200 * time.Millisecond)
	if d.Status().ControlState != mc.StateProtectedStop {
		t.Fatal("device left protected stop without confirmation")
	}
	if !d.ConfirmRecovery() {
		t.Fatal("recovery refused after fault cleared")
	}
	waitState(t, d, mc.StateRunning)
}

// A09: invalid pH/EC degrade but do not stop irrigation; invalid level sensor
// (essential) fails safe.
func TestNonCriticalSensorFaultDegradesButKeepsIrrigating(t *testing.T) {
	d := testDevice(t, nil)
	_ = d.Provision(prog1s, 1)
	waitState(t, d, mc.StateRunning)
	d.SetFaults(Faults{PHInvalid: true, ECInvalid: true})
	waitState(t, d, mc.StateDegraded)
	before := d.Status().PumpCycles
	if err := WaitFor(4*time.Second, func() bool { return d.Status().PumpCycles > before }); err != nil {
		t.Fatal("degraded device stopped irrigating")
	}
	d.SetFaults(Faults{LevelSensorInvalid: true})
	waitState(t, d, mc.StateProtectedStop)
}

func TestCommandApplyDedupAndConflict(t *testing.T) {
	d := testDevice(t, nil)
	waitState(t, d, mc.StateNeedsSetup)
	const nonce = "nonce-abcdef12"

	d.handleCommand(cmd("cmd-aaaaaaaa-0001", 0, 1, prog1s, nonce), nonce)
	if v := d.Status().ProgramVersion; v != 1 {
		t.Fatalf("version %d", v)
	}
	waitState(t, d, mc.StateRunning)

	// same command id again: ALREADY_PROCESSED path, no second application
	d.handleCommand(cmd("cmd-aaaaaaaa-0001", 0, 1, prog1s, nonce), nonce)
	if v := d.Status().ProgramVersion; v != 1 {
		t.Fatalf("duplicate re-applied: version %d", v)
	}
	// stale expected version -> rejected, version unchanged (A06)
	d.handleCommand(cmd("cmd-aaaaaaaa-0002", 0, 1, mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 2, OffDurationSeconds: 2, MaxContinuousOnSecs: 2, RequireReservoirOK: true}, nonce), nonce)
	if v := d.Status().ProgramVersion; v != 1 {
		t.Fatalf("stale command changed version to %d", v)
	}
	// valid next version applies
	d.handleCommand(cmd("cmd-aaaaaaaa-0003", 1, 2, prog1s, nonce), nonce)
	if v := d.Status().ProgramVersion; v != 2 {
		t.Fatalf("version %d", v)
	}
	// wrong session nonce (a command parked across a reconnect) never executes (A21)
	d.handleCommand(cmd("cmd-aaaaaaaa-0004", 2, 3, prog1s, "nonce-OLDOLD12"), nonce)
	if v := d.Status().ProgramVersion; v != 2 {
		t.Fatalf("stale-session command executed: %d", v)
	}
	// hardware cap enforced on the device regardless of the sender
	big := mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 500, OffDurationSeconds: 10, MaxContinuousOnSecs: 500, RequireReservoirOK: true}
	d.handleCommand(cmd("cmd-aaaaaaaa-0005", 2, 3, big, nonce), nonce)
	if v := d.Status().ProgramVersion; v != 2 {
		t.Fatalf("over-limit programme accepted: %d", v)
	}
	// protection cannot be switched off by a command
	nointerlock := prog1s
	nointerlock.RequireReservoirOK = false
	d.handleCommand(cmd("cmd-aaaaaaaa-0006", 2, 3, nointerlock, nonce), nonce)
	if v := d.Status().ProgramVersion; v != 2 {
		t.Fatalf("interlock-disabling programme accepted: %d", v)
	}
}

func TestProgrammeSurvivesRebootAndPowerCycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nvs.json")
	d := testDevice(t, func(c *Config) { c.StatePath = path })
	_ = d.Provision(prog1s, 4)
	waitState(t, d, mc.StateRunning)
	boot1 := d.Status().BootID
	d.Reboot()
	if d.Status().BootID == boot1 {
		t.Fatal("boot_id did not change on reboot (A24)")
	}
	if d.Status().PumpOn {
		t.Fatal("outputs not OFF at boot")
	}
	waitState(t, d, mc.StateRunning)
	if d.Status().Seq == 0 {
		// seq restarts per boot; BOOT event is seq 1 so Seq>=1
		t.Fatal("seq not restarted/emitting")
	}
	// brand new process image reading the same NVS file
	d2 := New(Config{SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: "unit-test-001", StatePath: path, BrokerURL: "tcp://127.0.0.1:1"})
	if d2.Status().ProgramVersion != 4 {
		t.Fatalf("programme lost across power cycle: %+v", d2.Status())
	}
}

func TestCorruptNVSFallsBackToNeedsSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nvs.json")
	if err := writeFile(path, `{"program":{"mode":"TIMED_CYCLE","on_duration_seconds":9999,"off_duration_seconds":1,"max_continuous_on_seconds":9999,"require_reservoir_ok":true},"version":3}`); err != nil {
		t.Fatal(err)
	}
	d := testDevice(t, func(c *Config) { c.StatePath = path })
	waitState(t, d, mc.StateNeedsSetup)
}

func TestJournalBoundedAndCriticalPreserved(t *testing.T) {
	j := newJournal(5)
	j.append(&record{bootID: "b", seq: 1, critical: true})
	for i := int64(2); i <= 20; i++ {
		j.append(&record{bootID: "b", seq: i})
	}
	if j.occupancy() != 5 {
		t.Fatalf("journal grew beyond capacity: %d", j.occupancy())
	}
	if j.overflow != 15 {
		t.Fatalf("overflow count %d, want 15 (gaps must be counted, not hidden)", j.overflow)
	}
	if j.recs[0].seq != 1 {
		t.Fatal("critical record evicted before non-critical")
	}
	// reclaim only matches the right boot
	if n := j.reclaim("other-boot", []int64{1}); n != 0 {
		t.Fatal("reclaimed record of a different boot")
	}
	if n := j.reclaim("b", []int64{1}); n != 1 {
		t.Fatal("did not reclaim acked record")
	}
}

func TestJournalReplayIsBoundedAndInflightLimited(t *testing.T) {
	j := newJournal(400)
	for i := int64(1); i <= 300; i++ {
		j.append(&record{bootID: "b", seq: i})
	}
	now := time.Now()
	b := j.sendable(now, 15*time.Second, 20, 40)
	if len(b) != 20 {
		t.Fatalf("batch %d, want 20", len(b))
	}
	for _, r := range b {
		r.lastSent = now
	}
	b2 := j.sendable(now, 15*time.Second, 20, 40)
	for _, r := range b2 {
		r.lastSent = now
	}
	if b3 := j.sendable(now, 15*time.Second, 20, 40); len(b3) != 0 {
		t.Fatalf("exceeded in-flight cap: %d more records offered", len(b3))
	}
	// after the receipt timeout they are offered again (lost receipt)
	if b4 := j.sendable(now.Add(16*time.Second), 15*time.Second, 20, 40); len(b4) != 20 {
		t.Fatalf("no resend after timeout: %d", len(b4))
	}
}

func TestMonotonicClockNeverFabricatesTimestamp(t *testing.T) {
	d := testDevice(t, nil)
	d.sampleTelemetry(time.Now())
	d.mu.Lock()
	defer d.mu.Unlock()
	var found bool
	for _, r := range d.jr.recs {
		if r.topic == mc.TopicTelemetry {
			found = true
			if err := mc.ValidatePayload("telemetry", r.payload); err != nil {
				t.Fatalf("simulator emitted contract-invalid telemetry: %v", err)
			}
			var tl mc.Telemetry
			_ = json.Unmarshal(r.payload, &tl)
			if tl.SampledAtUTC != nil || tl.ClockQuality != mc.ClockMonotonicOnly || tl.DataSource != mc.SourceSimulator {
				t.Fatalf("bad clock/source fields: %+v", tl.Header)
			}
			if tl.Readings["light_lux"].Value != nil || tl.Readings["light_lux"].Quality != mc.QualityMissing {
				t.Fatal("missing sensor reported with a value")
			}
		}
	}
	if !found {
		t.Fatal("no telemetry journaled")
	}
}

// Every payload the simulator builds must satisfy the shared contract.
func TestEmittedStateAndEventsSatisfyContract(t *testing.T) {
	d := testDevice(t, nil)
	_ = d.Provision(prog1s, 1)
	waitState(t, d, mc.StateRunning)
	d.SetFaults(Faults{LowWater: true, PHInvalid: true})
	waitState(t, d, mc.StateProtectedStop)
	b, _ := json.Marshal(d.buildState("nonce-12345678"))
	if err := mc.ValidatePayload("state", b); err != nil {
		t.Fatalf("state: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.jr.recs {
		kind := "event"
		if r.topic == mc.TopicTelemetry {
			kind = "telemetry"
		}
		if err := mc.ValidatePayload(kind, r.payload); err != nil {
			t.Fatalf("%s seq %d: %v", kind, r.seq, err)
		}
	}
}
