package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

func simApp(t *testing.T, allowSim bool) (*App, error) {
	t.Helper()
	dir := t.TempDir()
	pf := filepath.Join(dir, "devices.json")
	raw, _ := json.Marshal(map[string]any{"devices": []map[string]string{{"device_id": "esp32-sim-001", "unit_id": "unit-sim-001"}}})
	if err := os.WriteFile(pf, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SiteID: "site-app-01", SiteName: "T", HTTPAddr: "127.0.0.1:0", DBPath: filepath.Join(dir, "a.db"), MQTTURL: "tcp://127.0.0.1:1",
		StaleAfter: 15 * time.Second, CommandRetry: 5 * time.Second, CommandMaxTries: 3, CommandTTL: time.Minute, MemSoftLimitMiB: 192,
		RetentionDays: 30, RecordQueueSize: 64, ProvisionFile: pf, AllowSimulatedData: allowSim}
	a, err := New(context.Background(), cfg, testutil.Logger())
	if a != nil && a.DB != nil {
		t.Cleanup(func() { a.DB.Close() })
	}
	return a, err
}

// The simulated site gets exactly one TEST tomato profile; it passes the same validation as a profile
// typed in the dashboard and says plainly that it is test data.
func TestSimulatedSiteGetsOneLabelledTestTomatoProfile(t *testing.T) {
	p := testTomatoProfile()
	if err := p.Validate(); err != nil {
		t.Fatalf("test profile fails the Pi's own validation: %v", err)
	}
	if err := p.Program.Validate(); err != nil {
		t.Fatalf("test programme fails the protocol limits: %v", err)
	}
	if !strings.Contains(p.Name, "TEST") || !strings.Contains(p.ValueSource, "TEST DATA") || !strings.Contains(p.Notes, "Not growing advice") {
		t.Fatalf("test profile is not labelled as test data: %+v", p)
	}

	a, err := simApp(t, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // a restart must not create a second copy
		if err := a.seedProvisioned(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := a.DB.R.QueryRow(`SELECT COUNT(*) FROM crop_profiles WHERE name=? AND archived_at IS NULL`, TestTomatoProfileName).Scan(&n); err != nil || n != 1 {
		t.Fatalf("want 1 test profile, got %d (%v)", n, err)
	}
}

// A site that does not allow simulated data refuses to start with a simulator file, so it can never
// get the test profile.
func TestRealSiteNeverGetsTheTestProfile(t *testing.T) {
	if _, err := simApp(t, false); err == nil || !strings.Contains(err.Error(), "SIMULATOR") {
		t.Fatalf("simulator provisioning accepted on a site that does not allow simulated data: %v", err)
	}
}
