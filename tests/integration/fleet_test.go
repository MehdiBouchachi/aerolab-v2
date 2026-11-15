package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/simulator/esp32"
)

// F1: a grower adds a unit from the dashboard with nobody editing broker files: the edge issues the
// controller credential, the REAL broker accepts it after a reload, the controller's data is stored,
// and the unit's crop profile turns out-of-range readings into an advisory incident. Retiring the
// unit then locks that credential out of the broker.
//
// The range numbers are deliberately absurd test data (the simulator's pH is ~6); they are not crop values.
func TestF1_AddUnitFromDashboardThenCropRangeAlert(t *testing.T) {
	s := newStack(t, 1)
	s.startBroker()
	mdir := filepath.Join(s.dir, "dev", "mosquitto")
	s.startEdge(map[string]string{"AEROLAB_BROKER_PASSWD_FILE": filepath.Join(mdir, "passwd"), "AEROLAB_BROKER_PID_FILE": filepath.Join(mdir, "mosquitto.pid")})
	site := s.prov.SiteID

	var prof struct{ ID string }
	if c := s.call("POST", "/api/v1/sites/"+site+"/crop-profiles", s.admin(), map[string]any{
		"name": "IT profile", "crop": "test-crop", "value_source": "integration test (not agronomic)",
		"thresholds": map[string]any{"ph": map[string]any{"max": 1.0}},
	}, &prof); c != 201 {
		t.Fatalf("create profile: %d", c)
	}
	var added struct {
		SetupCode string `json:"setup_code"`
		Warning   string `json:"warning"`
	}
	if c := s.call("POST", "/api/v1/sites/"+site+"/units", s.admin(), map[string]any{"name": "Added from UI", "crop_profile_id": prof.ID, "data_source": "SIMULATOR"}, &added); c != 201 || added.Warning != "" {
		t.Fatalf("add unit: %d %q", c, added.Warning)
	}
	raw, err := base64.RawURLEncoding.DecodeString(added.SetupCode)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		SiteID   string `json:"site_id"`
		DeviceID string `json:"device_id"`
		UnitID   string `json:"unit_id"`
		MQTTUser string `json:"mqtt_user"`
		MQTTPass string `json:"mqtt_pass"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || b.SiteID != site {
		t.Fatalf("bundle %s %v", raw, err)
	}

	// the controller is configured ONLY from the setup code
	f := esp32.NewFleet()
	d := esp32.New(esp32.Config{SiteID: b.SiteID, DeviceID: b.DeviceID, UnitID: b.UnitID, BrokerURL: s.prov.BrokerURL, Username: b.MQTTUser, Password: b.MQTTPass,
		HeartbeatEvery: 300 * time.Millisecond, TelemetryEvery: 300 * time.Millisecond, ControlTick: 20 * time.Millisecond, SelfCheckTime: 100 * time.Millisecond,
		ReceiptTimeout: 2 * time.Second, ReconnectMin: 200 * time.Millisecond, ReconnectMax: time.Second, Seed: 7})
	f.Add(d)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); f.Stop() })
	f.Start(ctx)

	s.waitUntil(15*time.Second, "new unit online through the real broker", func() bool { return s.unit(b.UnitID).Connection == "ONLINE" })
	s.waitUntil(10*time.Second, "its telemetry stored", func() bool {
		return s.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE device_id=?`, b.DeviceID) >= 3
	})
	s.waitUntil(10*time.Second, "advisory range incident", func() bool {
		for _, i := range s.incidents("active") {
			if i["unit_id"] == b.UnitID && i["code"] == "RANGE_HIGH:ph" {
				return true
			}
		}
		return false
	})

	// retire: the broker no longer accepts this credential (a fresh login fails)
	if c := s.call("POST", "/api/v1/units/"+b.UnitID+"/retire", s.admin(), map[string]any{"confirm_name": "Added from UI"}, nil); c != 200 {
		t.Fatalf("retire: %d", c)
	}
	d.SetNetwork(false) // force a fresh login, which must now be refused
	time.Sleep(300 * time.Millisecond)
	d.SetNetwork(true)
	time.Sleep(3 * time.Second)
	if d.Status().Connected {
		t.Fatal("retired controller reconnected to the broker")
	}
	for _, u := range s.units() {
		if u.ID == b.UnitID {
			t.Fatal("retired unit still listed")
		}
	}
	if !strings.HasPrefix(b.DeviceID, "esp32-") {
		t.Fatalf("generated device id %q", b.DeviceID)
	}
}
