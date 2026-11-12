package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/simulator/esp32"
	"github.com/mehdibouchachi/aerolab-v2/simulator/seed"
)

func factoryProgram(s *stack, n int) {
	p := mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 1, OffDurationSeconds: 1, MaxContinuousOnSecs: 1, RequireReservoirOK: true}
	for _, d := range s.fleet.All() {
		if err := d.Provision(p, 1); err != nil {
			s.t.Fatal(err)
		}
	}
}

func cycles(s *stack) map[string]int {
	m := map[string]int{}
	for _, d := range s.fleet.All() {
		m[d.Status().DeviceID] = d.Status().PumpCycles
	}
	return m
}

func mqttClient(t *testing.T, s *stack, user, pass string) (paho.Client, error) {
	o := paho.NewClientOptions().AddBroker(s.prov.BrokerURL).SetClientID(user + "-probe").SetUsername(user).SetPassword(pass).SetAutoReconnect(false).SetConnectTimeout(3 * time.Second)
	c := paho.NewClient(o)
	tok := c.Connect()
	tok.WaitTimeout(4 * time.Second)
	return c, tok.Error()
}

// A25: anonymous MQTT is rejected; a device is confined to its own topics.
func TestA25_BrokerAuthenticationAndPerDeviceTopicIsolation(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()

	// anonymous
	o := paho.NewClientOptions().AddBroker(s.prov.BrokerURL).SetClientID("anon").SetConnectTimeout(3 * time.Second).SetAutoReconnect(false)
	c := paho.NewClient(o)
	if tok := c.Connect(); tok.WaitTimeout(4*time.Second) && tok.Error() == nil {
		t.Fatal("anonymous MQTT connection was accepted")
	}
	// wrong password
	if _, err := mqttClient(t, s, "esp32-sim-001", "not-the-password"); err == nil {
		t.Fatal("wrong device password accepted")
	}

	d1, d2 := s.prov.Devices[0], s.prov.Devices[1]
	dev1, err := mqttClient(t, s, d1.DeviceID, d1.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer dev1.Disconnect(100)
	edge, err := mqttClient(t, s, seed.EdgeMQTTUser, s.prov.EdgePassword)
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Disconnect(100)

	// Mosquitto 2.0 over MQTT 3.1.1 returns a *successful* SUBACK even for
	// ACL-denied subscriptions and then simply never delivers. So isolation is
	// asserted on what is actually DELIVERED, not on the SUBACK.
	got := make(chan string, 64)
	for _, tp := range []string{mc.DeviceTopic(d1.DeviceID, mc.TopicCommands), mc.DeviceTopic(d1.DeviceID, mc.TopicReceipts),
		mc.DeviceTopic(d2.DeviceID, mc.TopicCommands), mc.DeviceTopic(d2.DeviceID, mc.TopicReceipts),
		"aerolab/v1/devices/+/commands", "aerolab/v1/devices/+/receipts", "aerolab/v1/#", "#"} {
		dev1.Subscribe(tp, 1, func(_ paho.Client, m paho.Message) { got <- m.Topic() }).WaitTimeout(2 * time.Second)
	}
	for _, d := range []seed.DeviceCred{d1, d2} {
		edge.Publish(mc.DeviceTopic(d.DeviceID, mc.TopicCommands), 1, false, `{"x":1}`).WaitTimeout(2 * time.Second)
		edge.Publish(mc.DeviceTopic(d.DeviceID, mc.TopicReceipts), 1, false, `{"x":1}`).WaitTimeout(2 * time.Second)
	}
	time.Sleep(800 * time.Millisecond)
	close(got)
	seen := map[string]bool{}
	for tp := range got {
		seen[tp] = true
	}
	if !seen[mc.DeviceTopic(d1.DeviceID, mc.TopicCommands)] || !seen[mc.DeviceTopic(d1.DeviceID, mc.TopicReceipts)] {
		t.Fatalf("device 1 did not receive its OWN commands/receipts - ACL too strict or test not exercising delivery: %v", seen)
	}
	for tp := range seen {
		if strings.Contains(tp, d2.DeviceID) || strings.HasPrefix(tp, "$SYS") {
			t.Errorf("device 1 received traffic of another device / broker internals: %s", tp)
		}
	}

	// publishes into another device's topic space or onto commands never reach anyone
	recv := make(chan string, 16)
	for _, tp := range []string{"aerolab/v1/devices/+/telemetry", "aerolab/v1/devices/+/commands", "aerolab/v1/devices/+/receipts", "aerolab/v1/devices/+/state"} {
		edge.Subscribe(tp, 1, func(_ paho.Client, m paho.Message) { recv <- m.Topic() }).WaitTimeout(2 * time.Second)
	}
	dev1.Publish(mc.DeviceTopic(d2.DeviceID, mc.TopicTelemetry), 1, false, `{"spoof":true}`).WaitTimeout(2 * time.Second)
	dev1.Publish(mc.DeviceTopic(d1.DeviceID, mc.TopicCommands), 1, false, `{"forge":"command"}`).WaitTimeout(2 * time.Second)
	dev1.Publish(mc.DeviceTopic(d1.DeviceID, mc.TopicReceipts), 1, false, `{"forge":"receipt"}`).WaitTimeout(2 * time.Second)
	dev1.Publish(mc.DeviceTopic(d1.DeviceID, mc.TopicState), 1, false, `{"legit":"own state"}`).WaitTimeout(2 * time.Second) // control: must arrive
	select {
	case g := <-recv:
		if g != mc.DeviceTopic(d1.DeviceID, mc.TopicState) {
			t.Fatalf("forbidden publish was delivered on %s", g)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control publish on the device's own topic never arrived - test is not exercising the ACL")
	}
	select {
	case g := <-recv:
		t.Fatalf("forbidden publish was delivered on %s", g)
	case <-time.After(700 * time.Millisecond):
	}
}

// (kept for the next assertion block)
func init() {}

// Demo steps 1-3, 5: ten distinguishable SIMULATED units; a programme change
// is PENDING until the controller confirms and touches only its own unit.
func TestDemo_TenUnitsProgramChangeIsPendingThenAppliedAndIsolated(t *testing.T) {
	s := newStack(t, 10)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(10, nil)
	s.waitUntil(20*time.Second, "10 units ONLINE", func() bool { return s.allOnline(10) })

	s.waitUntil(15*time.Second, "all units finished self-check (NEEDS_SETUP)", func() bool {
		for _, u := range s.units() {
			if strOr(u.ControlState, "") != "NEEDS_SETUP" {
				return false
			}
		}
		return true
	})
	us := s.units()
	ids := map[string]bool{}
	for _, u := range us {
		ids[u.ID] = true
		if !u.IsSimulated {
			t.Errorf("%s not labelled simulated", u.ID)
		}
		if strOr(u.ControlState, "") != "NEEDS_SETUP" {
			t.Errorf("%s is %s, an unprovisioned device must wait for a programme", u.ID, strOr(u.ControlState, "<nil>"))
		}
	}
	if len(ids) != 10 {
		t.Fatal("units are not distinct")
	}
	s.waitUntil(15*time.Second, "latest readings visible for every unit", func() bool {
		for _, u := range s.units() {
			if u.Latest == nil || u.Latest.Source != "SIMULATOR" {
				return false
			}
		}
		return true
	})

	// first programme for unit 4 (expected version 0)
	var cmd struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Confirmed bool   `json:"applied_confirmed"`
		Note      string `json:"note"`
	}
	body := map[string]any{"expected_program_version": 0, "program": prog1s}
	if c := s.call("POST", "/api/v1/units/unit-sim-004/program-requests", s.admin(), body, &cmd); c != 202 {
		t.Fatalf("program request: %d", c)
	}
	if cmd.Confirmed || cmd.Status == "APPLIED" {
		t.Fatalf("API reported %s/confirmed=%v before the controller answered", cmd.Status, cmd.Confirmed)
	}
	var final struct {
		Status    string `json:"status"`
		Attempts  int    `json:"attempts"`
		Confirmed bool   `json:"applied_confirmed"`
	}
	s.waitUntil(10*time.Second, "command APPLIED", func() bool {
		s.call("GET", "/api/v1/commands/"+cmd.ID, s.admin(), nil, &final)
		return final.Status == "APPLIED"
	})
	if !final.Confirmed {
		t.Fatal("APPLIED without applied_confirmed")
	}
	// neighbours untouched; the actual device really runs version 1
	for _, d := range s.fleet.All() {
		st := d.Status()
		want := 0
		if st.DeviceID == "esp32-sim-004" {
			want = 1
		}
		if st.ProgramVersion != want {
			t.Errorf("%s runs version %d, want %d", st.DeviceID, st.ProgramVersion, want)
		}
	}
	s.waitUntil(10*time.Second, "unit 4 RUNNING and cycling", func() bool {
		u := s.unit("unit-sim-004")
		return strOr(u.ControlState, "") == "RUNNING" && u.Applied != nil && *u.Applied == 1
	})
	if u := s.unit("unit-sim-003"); u.Applied != nil || strOr(u.ControlState, "") != "NEEDS_SETUP" {
		t.Errorf("unit 3 changed: %+v", u)
	}

	// stale version -> conflict, nothing sent
	var errResp struct{ Error struct{ Code string } }
	if c := s.call("POST", "/api/v1/units/unit-sim-004/program-requests", s.admin(), body, &errResp); c != 409 || errResp.Error.Code != "PROGRAM_VERSION_CONFLICT" {
		t.Fatalf("stale request: %d %s", c, errResp.Error.Code)
	}
	// RBAC end to end: an Operator cannot change programmes; a Viewer cannot even acknowledge
	opTok := s.createAndLogin("operator1", "OPERATOR")
	viewerTok := s.createAndLogin("viewer1", "VIEWER")
	if c := s.call("POST", "/api/v1/units/unit-sim-005/program-requests", opTok, body, nil); c != 403 {
		t.Fatalf("operator changed a programme: %d", c)
	}
	if c := s.call("POST", "/api/v1/units/unit-sim-005/program-requests", viewerTok, body, nil); c != 403 {
		t.Fatalf("viewer changed a programme: %d", c)
	}
	if s.fleet.Get("esp32-sim-005").Status().ProgramVersion != 0 {
		t.Fatal("a forbidden request reached a controller")
	}
}

// A05: the ack is lost twice. Whether the truth arrives via a retry
// (ALREADY_PROCESSED) or via the heartbeat, the change is applied exactly once.
func TestA05_LostCommandResultNeverDoubleApplies(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(3, nil)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(3) })

	dev := s.fleet.Get("esp32-sim-002")
	dev.DropNextResults(2)
	var cmd struct{ ID string }
	if c := s.call("POST", "/api/v1/units/unit-sim-002/program-requests", s.admin(), map[string]any{"expected_program_version": 0, "program": prog1s}, &cmd); c != 202 {
		t.Fatal(c)
	}
	var st struct {
		Status   string
		Attempts int
		Reason   *string `json:"reason_code"`
	}
	s.waitUntil(15*time.Second, "APPLIED after retries", func() bool {
		s.call("GET", "/api/v1/commands/"+cmd.ID, s.admin(), nil, &st)
		return st.Status == "APPLIED"
	})
	// Truth can arrive two ways: a retry answered ALREADY_PROCESSED, or the
	// controller's own heartbeat naming the command (state reconciliation).
	// Either way: never more than 3 sends, and applied exactly once.
	if st.Attempts < 1 || st.Attempts > 3 {
		t.Fatalf("attempts=%d, want 1..3 (bounded retries)", st.Attempts)
	}
	t.Logf("resolved after %d send(s), reason=%s", st.Attempts, strOr(st.Reason, "-"))
	if v := dev.Status().ProgramVersion; v != 1 {
		t.Fatalf("device version %d", v)
	}
	if n := s.count(`SELECT COUNT(*) FROM device_events WHERE device_id='esp32-sim-002' AND event_type='PROGRAM_APPLIED'`); n != 1 {
		t.Fatalf("PROGRAM_APPLIED events: %d, want exactly 1 (idempotent apply)", n)
	}
}

// A08 + demo step 6: low water protects only the affected unit.
func TestA08_LowWaterProtectedStopIsolatedAndIncidentLifecycle(t *testing.T) {
	s := newStack(t, 5)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(5, nil)
	factoryProgram(s, 5)
	s.waitUntil(15*time.Second, "5 units RUNNING", func() bool {
		if !s.allOnline(5) {
			return false
		}
		for _, u := range s.units() {
			if strOr(u.ControlState, "") != "RUNNING" {
				return false
			}
		}
		return true
	})
	before := cycles(s)

	s.fleet.Get("esp32-sim-002").SetFaults(esp32.Faults{LowWater: true})
	s.waitUntil(10*time.Second, "unit 2 PROTECTED_STOP", func() bool { return strOr(s.unit("unit-sim-002").ControlState, "") == "PROTECTED_STOP" })
	if p := strOr(s.unit("unit-sim-002").Pump, "?"); p != "OFF" {
		t.Fatalf("pump %s in protected stop", p)
	}
	var crit map[string]any
	s.waitUntil(10*time.Second, "critical incident on unit 2", func() bool {
		for _, i := range s.incidents("active") {
			if i["unit_id"] == "unit-sim-002" && i["code"] == "LOW_WATER" && i["severity"] == "critical" {
				crit = i
				return true
			}
		}
		return false
	})
	if txt, _ := crit["suggested_action"].(string); !strings.Contains(strings.ToLower(txt), "refill") {
		t.Errorf("incident lacks a plain-language action: %v", crit["suggested_action"])
	}
	// others keep irrigating
	time.Sleep(2500 * time.Millisecond)
	after := cycles(s)
	for id := range after {
		if id == "esp32-sim-002" {
			if after[id] != before[id] && s.fleet.Get(id).Status().PumpOn {
				t.Error("unit 2 pump running while protected")
			}
			continue
		}
		if after[id] <= before[id] {
			t.Errorf("%s stopped irrigating (cycles %d -> %d) because of another unit's fault", id, before[id], after[id])
		}
	}
	// acknowledging does not resolve
	var ack struct {
		Incident struct{ Status string }
	}
	op := s.createAndLogin("operator1", "OPERATOR")
	if c := s.call("POST", "/api/v1/incidents/"+crit["id"].(string)+"/acknowledge", op, nil, &ack); c != 200 || ack.Incident.Status != "ACTIVE_ACKNOWLEDGED" {
		t.Fatalf("ack: %d %s", c, ack.Incident.Status)
	}
	if strOr(s.unit("unit-sim-002").ControlState, "") != "PROTECTED_STOP" {
		t.Fatal("acknowledging changed the physical state")
	}
	// physical fix + local confirmation -> resolved (acknowledged), history kept
	s.fleet.Get("esp32-sim-002").SetFaults(esp32.Faults{})
	time.Sleep(600 * time.Millisecond)
	if strOr(s.unit("unit-sim-002").ControlState, "") != "PROTECTED_STOP" {
		t.Fatal("unit left protected stop without local confirmation")
	}
	if !s.fleet.Get("esp32-sim-002").ConfirmRecovery() {
		t.Fatal("recovery refused")
	}
	s.waitUntil(10*time.Second, "incident resolved + unit RUNNING", func() bool {
		return strOr(s.unit("unit-sim-002").ControlState, "") == "RUNNING" && len(s.incidents("active")) == 0
	})
	resolved := s.incidents("resolved")
	if len(resolved) != 1 || resolved[0]["status"] != "RESOLVED_ACKNOWLEDGED" {
		t.Fatalf("history not retained: %v", resolved)
	}
}

func (s *stack) createAndLogin(user, role string) string {
	s.t.Helper()
	var u map[string]any
	if c := s.call("POST", "/api/v1/local/users", s.admin(), map[string]any{"username": user, "password": "correct-horse-battery", "role": role}, &u); c != 201 {
		s.t.Fatalf("create user: %d", c)
	}
	return s.login(user, "correct-horse-battery")
}

// A02/A03 + demo steps 7-8: broker outage. Controllers keep cycling, the
// edge UI stays up and honest, and on return the backlog replays in full.
func TestA02_A03_BrokerOutageControllersStayAutonomousAndBacklogReplaysWithoutLoss(t *testing.T) {
	s := newStack(t, 4)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(4, nil)
	factoryProgram(s, 4)
	s.waitUntil(15*time.Second, "online and running", func() bool { return s.allOnline(4) })
	s.waitUntil(10*time.Second, "records flowing", func() bool { return s.count(`SELECT COUNT(*) FROM telemetry_snapshots`) >= 8 })

	s.stopBroker()
	c0 := cycles(s)
	time.Sleep(6 * time.Second)
	c1 := cycles(s)
	for id := range c0 {
		if c1[id] < c0[id]+3 {
			t.Errorf("%s did not keep cycling during the broker outage (%d -> %d)", id, c0[id], c1[id])
		}
		if s.fleet.Get(id).Status().Connected {
			t.Errorf("%s claims to be connected to a dead broker", id)
		}
	}
	// the Pi itself is fine: login, units, honest staleness, DEVICE_STALE incidents
	var ready map[string]bool
	if c := s.call("GET", "/readyz", "", nil, &ready); c != 200 || !ready["database"] || ready["mqtt_broker"] {
		t.Fatalf("readyz %d %v", c, ready)
	}
	for _, u := range s.units() {
		if u.Connection != "STALE" || strOr(u.ControlState, "") != "UNKNOWN_STALE" || u.Pump != nil {
			t.Fatalf("unit shown as live with no broker: %+v", u)
		}
	}
	if n := len(s.incidents("active")); n != 4 {
		t.Fatalf("want 4 DEVICE_STALE incidents, got %d", n)
	}
	occ := 0
	for _, d := range s.fleet.All() {
		occ += d.Status().JournalOcc
	}
	if occ < 12 {
		t.Fatalf("devices did not buffer records during the outage (journal total %d)", occ)
	}

	s.startBroker()
	s.waitUntil(25*time.Second, "all units back ONLINE", func() bool { return s.allOnline(4) })
	t.Logf("backlog fully durable %s after the broker returned", s.waitNoLoss(30*time.Second, 8))
	s.waitUntil(10*time.Second, "stale incidents resolved", func() bool { return len(s.incidents("active")) == 0 })
}

// Pi loss: SIGKILL the edge (power-cut semantics), controllers carry on, and
// everything buffered is persisted exactly once after restart. Sessions are
// durable too.
func TestA02_EdgeKilledMidFlightThenRestartedRecoversEverything(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(3, nil)
	factoryProgram(s, 3)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(3) })
	tok := s.admin()
	s.waitUntil(10*time.Second, "records flowing", func() bool { return s.count(`SELECT COUNT(*) FROM telemetry_snapshots`) >= 6 })

	s.killEdge()
	c0 := cycles(s)
	time.Sleep(5 * time.Second)
	for id, n := range cycles(s) {
		if n < c0[id]+2 {
			t.Errorf("%s stopped irrigating while the Pi was down", id)
		}
	}
	s.startEdge(nil)
	var sess struct{ User struct{ Username string } }
	if c := s.call("GET", "/api/v1/session", tok, nil, &sess); c != 200 {
		t.Fatalf("session did not survive an edge restart: %d", c)
	}
	s.waitUntil(25*time.Second, "units ONLINE", func() bool { return s.allOnline(3) })
	t.Logf("backlog fully durable %s after the Pi restarted", s.waitNoLoss(30*time.Second, 8))
	// a restarted edge must not resurrect old commands
	if n := s.count(`SELECT COUNT(*) FROM commands`); n != 0 {
		t.Fatalf("%d commands appeared from nowhere", n)
	}
}

// Autonomy under a single-unit radio loss; independent units; recovery.
func TestSingleUnitWiFiLossKeepsItsCycleAndOthersStayOnline(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(3, nil)
	factoryProgram(s, 3)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(3) })

	d := s.fleet.Get("esp32-sim-003")
	before := d.Status().PumpCycles
	d.SetNetwork(false)
	s.waitUntil(10*time.Second, "unit 3 STALE", func() bool { return s.unit("unit-sim-003").Connection != "ONLINE" })
	if s.unit("unit-sim-001").Connection != "ONLINE" || s.unit("unit-sim-002").Connection != "ONLINE" {
		t.Fatal("a neighbour's Wi-Fi loss degraded other units")
	}
	var stale bool
	s.waitUntil(8*time.Second, "DEVICE_STALE incident", func() bool {
		for _, i := range s.incidents("active") {
			if i["unit_id"] == "unit-sim-003" && i["code"] == "DEVICE_STALE" {
				stale = true
			}
		}
		return stale
	})
	time.Sleep(3 * time.Second)
	if d.Status().PumpCycles < before+2 {
		t.Fatal("offline controller stopped its own cycle")
	}
	d.SetNetwork(true)
	s.waitUntil(20*time.Second, "unit 3 back + incident resolved", func() bool {
		return s.unit("unit-sim-003").Connection == "ONLINE" && len(s.incidents("active")) == 0
	})
	t.Logf("backlog fully durable %s after Wi-Fi returned", s.waitNoLoss(30*time.Second, 8))
}

// Production-style edge: simulator data is refused outright.
func TestSimulatedDataIsRefusedByEdgeNotConfiguredForSimulators(t *testing.T) {
	s := newStack(t, 2)
	s.startBroker()
	// the provisioning file is a simulator feature: refuse to start with it
	s.edgeEnv = nil
	s.startEdgeExpectFailure(map[string]string{"AEROLAB_ALLOW_SIMULATED_DATA": "false"})

	s.startEdge(map[string]string{"AEROLAB_ALLOW_SIMULATED_DATA": "true", "AEROLAB_PROVISION_FILE": ""})
	// a real-hardware-registered device (data_source DEVICE) cannot be fed by a simulator
	if c := s.call("POST", "/api/v1/sites/"+s.prov.SiteID+"/devices", s.admin(), map[string]any{"device_id": "esp32-sim-001", "unit_id": "unit-sim-001", "unit_name": "Real tomato unit", "data_source": "DEVICE"}, nil); c != 201 {
		t.Fatalf("provision: %d", c)
	}
	s.startFleet(1, nil)
	time.Sleep(3 * time.Second)
	if n := s.count(`SELECT COUNT(*) FROM telemetry_snapshots`); n != 0 {
		t.Fatalf("%d SIMULATOR records were stored against a unit registered as real hardware", n)
	}
	var d struct {
		Ingest struct {
			Reject map[string]int `json:"reject_reasons"`
		}
	}
	s.call("GET", "/api/v1/diagnostics", s.admin(), nil, &d)
	if d.Ingest.Reject["DATA_SOURCE_MISMATCH"] == 0 {
		t.Fatalf("mismatch not counted: %v", d.Ingest.Reject)
	}
}

func (s *stack) startEdgeExpectFailure(extra map[string]string) {
	s.t.Helper()
	cmd, logs := s.runEdgeOnce(extra)
	_ = cmd
	if !strings.Contains(logs, "SIMULATOR feature") {
		s.t.Fatalf("edge started (or failed for another reason) with a provisioning file and simulated data disabled:\n%s", logs)
	}
}

// A10 end to end: below the free-space floor the edge stops acknowledging;
// operators can still log in and see why; nothing is lost after recovery.
func TestA10_NoDurableReceiptsBelowFreeSpaceFloorThenFullRecovery(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(3, nil)
	factoryProgram(s, 3)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(3) })
	s.waitUntil(10*time.Second, "records flowing", func() bool { return s.count(`SELECT COUNT(*) FROM telemetry_snapshots`) >= 6 })

	s.stopEdgeGracefully()
	s.startEdge(map[string]string{"AEROLAB_MIN_FREE_MIB": "999999999"}) // floor above any disk => "disk full"
	time.Sleep(1500 * time.Millisecond)
	rowsA := s.count(`SELECT COUNT(*) FROM telemetry_snapshots`)
	time.Sleep(4 * time.Second)
	if rowsB := s.count(`SELECT COUNT(*) FROM telemetry_snapshots`); rowsB != rowsA {
		t.Fatalf("rows grew (%d -> %d) below the free-space floor", rowsA, rowsB)
	}
	growing := false
	for _, d := range s.fleet.All() {
		if d.Status().JournalOcc >= 8 {
			growing = true
		}
	}
	if !growing {
		t.Fatal("devices reclaimed flash although no durable receipt existed")
	}
	var st struct {
		Status  string
		Durable bool `json:"durable_receipts_enabled"`
	}
	if c := s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/storage", s.admin(), nil, &st); c != 200 || st.Status == "OK" || st.Durable {
		t.Fatalf("operators are not told about the storage problem: %d %+v", c, st)
	}
	// controllers did not care
	c0 := cycles(s)
	time.Sleep(3 * time.Second)
	for id, n := range cycles(s) {
		if n < c0[id]+1 {
			t.Errorf("%s stopped irrigating when the Pi's disk filled", id)
		}
	}

	s.stopEdgeGracefully()
	s.startEdge(nil)
	t.Logf("backlog fully durable %s after space returned", s.waitNoLoss(40*time.Second, 8))
}

// A20 + A09-ish: an unconfigured controller never irrigates; surfaced as setup-needed.
func TestA20_UnconfiguredControllerStaysSafe(t *testing.T) {
	s := newStack(t, 2)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(2, nil)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(2) })
	time.Sleep(2 * time.Second)
	for _, d := range s.fleet.All() {
		if st := d.Status(); st.PumpOn || st.PumpCycles != 0 {
			t.Fatalf("%s irrigated without a programme: %+v", st.DeviceID, st)
		}
	}
	var sum struct {
		Units struct{ Healthy, Attention int }
	}
	s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/summary", s.admin(), nil, &sum)
	if sum.Units.Healthy != 0 || sum.Units.Attention != 2 {
		t.Fatalf("setup-needed units counted as healthy: %+v", sum.Units)
	}
}

var _ = fmt.Sprintf
