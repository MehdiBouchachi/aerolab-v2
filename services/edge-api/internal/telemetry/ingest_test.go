package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

type rig struct {
	in  *Ingest
	w   *Writer
	reg *devices.Registry
	m   *diagnostics.Metrics
}

func newRig(t *testing.T, allowSim bool) *rig {
	t.Helper()
	db := testutil.Store(t)
	pub := testutil.NewFakePub()
	hub := sse.NewHub()
	m := diagnostics.New()
	reg := devices.NewRegistry(db, "site-test-01")
	if _, _, err := reg.Provision(context.Background(), "esp32-test-001", "unit-test-001", "Unit 1", "sim", "SIMULATOR", "t"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.Provision(context.Background(), "esp32-test-002", "unit-test-002", "Unit 2", "real", "DEVICE", "t"); err != nil {
		t.Fatal(err)
	}
	w := NewWriter(db, pub, hub, m, testutil.Logger(), "site-test-01", 64, false)
	cm := commands.New(db, reg, pub, hub, commands.Options{SiteID: "site-test-01", StaleAfter: 10 * time.Second, Retry: time.Second, MaxTries: 3, TTL: time.Minute}, testutil.Logger())
	return &rig{NewIngest("site-test-01", allowSim, 10*time.Second, reg, w, cm, hub, m, testutil.Logger()), w, reg, m}
}

func fixture(t *testing.T, name string, mut func(map[string]any)) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../../contracts/examples/valid/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["device_id"], m["unit_id"], m["site_id"] = "esp32-test-001", "unit-test-001", "site-test-01"
	if mut != nil {
		mut(m)
	}
	out, _ := json.Marshal(m)
	return out
}

func depth(r *rig) int { n, _ := r.w.QueueDepth(); return n }

func TestIngestAcceptsValidSimulatorTelemetry(t *testing.T) {
	r := newRig(t, true)
	r.in.HandleMessage("aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", nil))
	if depth(r) != 1 || r.m.Rejected.Load() != 0 {
		t.Fatalf("depth=%d rejected=%d", depth(r), r.m.Rejected.Load())
	}
}

// A25-style at the application layer: even if the broker ACL were
// misconfigured, a device cannot speak for another device.
func TestIngestRefusesSpoofingAndMisconfiguration(t *testing.T) {
	r := newRig(t, true)
	cases := []struct {
		name, topic string
		payload     []byte
		reason      string
	}{
		{"topic/payload device mismatch", "aerolab/v1/devices/esp32-test-002/telemetry", fixture(t, "telemetry.monotonic", nil), "TOPIC_DEVICE_MISMATCH"},
		{"wrong site", "aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", func(m map[string]any) { m["site_id"] = "site-other-99" }), "SITE_MISMATCH"},
		{"unit mismatch", "aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", func(m map[string]any) { m["unit_id"] = "unit-test-002" }), "UNIT_MISMATCH"},
		{"simulator device claims DEVICE", "aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", func(m map[string]any) { m["data_source"] = "DEVICE" }), "DATA_SOURCE_MISMATCH"},
		{"unknown schema", "aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", func(m map[string]any) { m["schema_version"] = 9 }), "UNSUPPORTED_SCHEMA_VERSION"},
		{"fabricated timestamp", "aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", func(m map[string]any) { m["sampled_at_utc"] = "2026-01-01T00:00:00Z" }), "FABRICATED_TIMESTAMP"},
		{"garbage", "aerolab/v1/devices/esp32-test-001/telemetry", []byte("{not json"), "MALFORMED_JSON"},
		{"oversize", "aerolab/v1/devices/esp32-test-001/telemetry", []byte(`{"x":"` + strings.Repeat("a", 9000) + `"}`), "PAYLOAD_TOO_LARGE"},
		{"bad topic", "aerolab/v1/devices/+/telemetry", fixture(t, "telemetry.monotonic", nil), "BAD_TOPIC"},
	}
	for _, c := range cases {
		before := r.m.Reasons()[c.reason]
		r.in.HandleMessage(c.topic, c.payload)
		if r.m.Reasons()[c.reason] != before+1 {
			t.Errorf("%s: reason %s not recorded (%v)", c.name, c.reason, r.m.Reasons())
		}
	}
	// the only job allowed through is a "reject" receipt for a record we could identify
	for depth(r) > 0 {
		j := <-r.w.recs
		if j.kind != kindReject {
			t.Fatalf("a refused message produced a %v job", j.kind)
		}
	}
	// unknown device: no runtime slot is ever created
	r.in.HandleMessage("aerolab/v1/devices/esp32-ghost-1/state", fixture(t, "state.running", func(m map[string]any) { m["device_id"] = "esp32-ghost-1" }))
	if r.m.UnknownDevice.Load() != 1 || r.reg.Count() != 2 {
		t.Fatalf("unknown=%d devices=%d (registry must not grow)", r.m.UnknownDevice.Load(), r.reg.Count())
	}
}

func TestSimulatedDataRefusedUnlessAllowed(t *testing.T) {
	r := newRig(t, false)
	r.in.HandleMessage("aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", nil))
	if depth(r) != 0 || r.m.Reasons()["SIMULATED_DATA_NOT_ALLOWED"] != 1 {
		t.Fatal("simulator data accepted on a production-configured edge")
	}
}

func TestPoisonedRecordIsRejectedNotRetriedForever(t *testing.T) {
	r := newRig(t, true)
	// identity + seq are fine, content violates the contract
	r.in.HandleMessage("aerolab/v1/devices/esp32-test-001/telemetry", fixture(t, "telemetry.monotonic", func(m map[string]any) { m["readings"] = map[string]any{} }))
	if depth(r) != 1 {
		t.Fatal("poisoned record produced no reject job")
	}
	if j := <-r.w.recs; j.kind != kindReject || j.seq != 761 {
		t.Fatalf("%+v", j)
	}
}

func TestStateUpdatesRegistryAndPresenceDoesNotAssertHealth(t *testing.T) {
	r := newRig(t, true)
	r.in.HandleMessage("aerolab/v1/devices/esp32-test-001/state", fixture(t, "state.protected_stop", nil))
	rt, _ := r.reg.Lookup("esp32-test-001")
	if !rt.Seen || rt.State.ControlState != mc.StateProtectedStop || len(rt.State.FaultCodes) != 1 {
		t.Fatalf("%+v", rt.State)
	}
	if rt.Connection(time.Now(), 10*time.Second) != devices.ConnOnline {
		t.Fatal("fresh heartbeat should be ONLINE")
	}
	// retained "ONLINE" is not liveness: after the stale window, link state is STALE
	r.in.HandleMessage("aerolab/v1/devices/esp32-test-001/presence", fixture(t, "presence.online", nil))
	if c := rt.Connection(time.Now().Add(time.Minute), 10*time.Second); c != devices.ConnStale {
		t.Fatalf("got %s", c)
	}
	// LWT OFFLINE is reported distinctly
	r.in.HandleMessage("aerolab/v1/devices/esp32-test-001/presence", fixture(t, "presence.online", func(m map[string]any) { m["status"] = "OFFLINE" }))
	rt, _ = r.reg.Lookup("esp32-test-001")
	if c := rt.Connection(time.Now().Add(time.Minute), 10*time.Second); c != devices.ConnOffline {
		t.Fatalf("got %s", c)
	}
}
