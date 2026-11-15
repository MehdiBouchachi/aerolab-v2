package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// startCloudWithoutSite runs the cloud with one organisation owner and NO site: the Pi must connect itself.
func (s *stack) startCloudWithoutSite() *cloudSite {
	s.t.Helper()
	dsn := newPGDSN(s.t)
	c := &cloudSite{s: s, dsn: dsn, port: freePort(s.t), email: "owner@coop.example", pw: "owner-password-long-1"}
	cmd := exec.Command(buildCloud(s.t), "create-user", "-email", c.email)
	cmd.Env = s.cloudEnv(dsn, "AEROLAB_NEW_PASSWORD="+c.pw)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("create-user: %v\n%s", err, out)
	}
	if _, err := c.pg().Exec(`INSERT INTO organizations(id,name,kind) VALUES ('org-it','IT Coop','BUSINESS')`); err != nil {
		s.t.Fatal(err)
	}
	if _, err := c.pg().Exec(`INSERT INTO organization_members(org_id,user_id,role) SELECT 'org-it', id, 'OWNER' FROM cloud_users WHERE email=$1`, c.email); err != nil {
		s.t.Fatal(err)
	}
	c.startProc("30s", "10s")
	c.wan = newWAN(s.t, fmt.Sprintf("127.0.0.1:%d", c.port))
	s.t.Cleanup(c.stop)
	return c
}

// F2: an organisation owner creates a one-time code in the cloud; the farmer types it into the Pi's
// local dashboard; the Pi connects itself (no env edits, no restart) and shows up in the fleet
// overview; the link survives a Pi restart; disconnecting stops the sync; the code cannot be reused.
func TestF2_ConnectAPiToTheCloudWithAClaimCode(t *testing.T) {
	s := newStack(t, 2)
	s.startBroker()
	c := s.startCloudWithoutSite()
	env := map[string]string{"AEROLAB_CLOUD_ALLOW_INSECURE": "true", "AEROLAB_CLOUD_SYNC_INTERVAL": "1s"}
	s.startEdge(env)
	s.startFleet(2, nil)
	factoryProgram(s, 2)
	s.waitUntil(20*time.Second, "units online on the Pi", func() bool { return s.allOnline(2) })

	c.login()
	var claim struct{ Code string }
	if code := c.call("POST", "/api/v1/orgs/org-it/site-claims", nil, &claim); code != 201 {
		t.Fatalf("claim code: %d", code)
	}
	var li struct{ Link struct{ Linked bool } }
	if s.call("GET", "/api/v1/cloud-link", s.admin(), nil, &li); li.Link.Linked {
		t.Fatal("linked before connecting")
	}
	var bad struct{ Error struct{ Code string } }
	if code := s.call("POST", "/api/v1/cloud-link", s.admin(), map[string]any{"cloud_url": c.wan.URL(), "code": "WRONG-WRON-GWRO"}, &bad); code != 400 || bad.Error.Code != "INVALID_CLAIM_CODE" {
		t.Fatalf("wrong code: %d %+v", code, bad)
	}
	if code := s.call("POST", "/api/v1/cloud-link", s.admin(), map[string]any{"cloud_url": c.wan.URL(), "code": claim.Code}, nil); code != 201 {
		t.Fatalf("connect: %d (edge log: %s)", code, tail(readFile(filepath.Join(s.dir, "edge.log")), 2000))
	}

	type fleetResp struct {
		Organizations []struct {
			Sites []struct {
				ID          string  `json:"id"`
				Role        string  `json:"role"`
				UnitsTotal  int     `json:"units_total"`
				UnitsOnline int     `json:"units_online"`
				LastSyncAt  *string `json:"last_sync_at"`
			} `json:"sites"`
		} `json:"organizations"`
	}
	site := func() (id string, total, online int, last string) {
		var f fleetResp
		c.call("GET", "/api/v1/fleet", nil, &f)
		if len(f.Organizations) == 0 || len(f.Organizations[0].Sites) == 0 {
			return "", 0, 0, ""
		}
		x := f.Organizations[0].Sites[0]
		l := ""
		if x.LastSyncAt != nil {
			l = *x.LastSyncAt
		}
		return x.ID, x.UnitsTotal, x.UnitsOnline, l
	}
	s.waitUntil(20*time.Second, "Pi and its units visible in the cloud fleet", func() bool {
		id, total, online, _ := site()
		return id == s.prov.SiteID && total == 2 && online == 2
	})

	// the link is stored: after a restart the Pi keeps syncing with no configuration change
	s.stopEdgeGracefully()
	time.Sleep(1500 * time.Millisecond)
	_, _, _, before := site()
	s.startEdge(env)
	s.waitUntil(20*time.Second, "sync resumes after the Pi restarts", func() bool {
		_, _, _, after := site()
		return after != before
	})

	// disconnect: the cloud stops hearing from this Pi
	if code := s.call("DELETE", "/api/v1/cloud-link", s.admin(), nil, nil); code != 204 {
		t.Fatalf("disconnect: %d", code)
	}
	time.Sleep(2 * time.Second)
	_, _, _, a := site()
	time.Sleep(3 * time.Second)
	if _, _, _, b := site(); a != b {
		t.Fatalf("the Pi kept syncing after it was disconnected (%s -> %s)", a, b)
	}
	if code := s.call("POST", "/api/v1/cloud-link", s.admin(), map[string]any{"cloud_url": c.wan.URL(), "code": claim.Code}, &bad); code != 400 {
		t.Fatalf("a used code connected again: %d", code)
	}
	if s.allOnline(2) != true {
		t.Fatal("local operation affected by cloud link changes")
	}
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }
