package httpapi

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/broker"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/crops"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/incidents"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
)

func passwdHas(t *testing.T, file, user, password string) bool {
	t.Helper()
	raw, _ := os.ReadFile(file)
	for _, line := range strings.Split(string(raw), "\n") {
		u, h, ok := strings.Cut(line, ":")
		if !ok || u != user {
			continue
		}
		p := strings.Split(h, "$")
		salt, _ := base64.StdEncoding.DecodeString(p[3])
		want, _ := base64.StdEncoding.DecodeString(p[4])
		got, _ := pbkdf2.Key(sha512.New, password, salt, 101, 64)
		return string(got) == string(want)
	}
	return false
}

func fleetEnv(t *testing.T) (*env, string) {
	e := newEnv(t)
	pf := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(pf, []byte("aerolab-edge:$7$101$keep$me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.srv.Broker = &broker.Manager{PasswdFile: pf}
	e.srv.Ranges = crops.NewEvaluator()
	return e, pf
}

type bundleResp struct {
	Setup     setupBundle `json:"setup"`
	SetupCode string      `json:"setup_code"`
	Unit      struct{ ID, Name string }
	Warning   string `json:"warning"`
}

func TestAddUnitIssuesWorkingBrokerCredentialOnce(t *testing.T) {
	e, pf := fleetEnv(t)
	if w := e.do("POST", "/api/v1/sites/site-test-01/units", e.toks[roles.Operator], map[string]any{"name": "Bay 3"}); w.Code != 403 {
		t.Fatalf("operator added a unit: %d", w.Code)
	}
	w := e.do("POST", "/api/v1/sites/site-test-01/units", e.toks[roles.Admin], map[string]any{"name": "Bay 3"})
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var b bundleResp
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	s := b.Setup
	if s.SiteID != "site-test-01" || !mc.ValidID(s.DeviceID) || !mc.ValidID(s.UnitID) || s.MQTTUser != s.DeviceID || b.Warning != "" {
		t.Fatalf("bundle %+v", b)
	}
	if s.MQTTURI != "mqtt://example.com:1883" { // derived from the host the dashboard was opened on
		t.Fatalf("uri %q", s.MQTTURI)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(b.SetupCode)
	var decoded setupBundle
	if json.Unmarshal(raw, &decoded) != nil || decoded != s {
		t.Fatalf("setup_code does not round-trip: %s", raw)
	}
	if !passwdHas(t, pf, s.DeviceID, s.MQTTPass) {
		t.Fatal("broker file has no working credential for the new controller")
	}
	if got, _ := os.ReadFile(pf); !strings.HasPrefix(string(got), "aerolab-edge:$7$101$keep$me\n") {
		t.Fatalf("edge's own line lost:\n%s", got)
	}
	// the password is not kept anywhere on the Pi
	var n int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM devices WHERE broker_password_hash LIKE ?`, "%"+s.MQTTPass+"%").Scan(&n)
	if n != 0 {
		t.Fatal("plaintext password stored")
	}
	if _, ok := e.reg.ByUnit(s.UnitID); !ok {
		t.Fatal("registry does not know the new unit; its telemetry would be dropped")
	}

	// lost the code: a new one replaces the old credential
	w = e.do("POST", "/api/v1/units/"+s.UnitID+"/setup-code", e.toks[roles.Admin], nil)
	var b2 bundleResp
	_ = json.Unmarshal(w.Body.Bytes(), &b2)
	if w.Code != 200 || passwdHas(t, pf, s.DeviceID, s.MQTTPass) || !passwdHas(t, pf, s.DeviceID, b2.Setup.MQTTPass) {
		t.Fatalf("rotation: %d %s", w.Code, w.Body)
	}

	// retire needs the typed name; then the credential is gone and the unit leaves the list
	if w := e.do("POST", "/api/v1/units/"+s.UnitID+"/retire", e.toks[roles.Admin], map[string]any{"confirm_name": "bay 3"}); w.Code != 400 {
		t.Fatalf("retired without exact confirmation: %d", w.Code)
	}
	if w := e.do("POST", "/api/v1/units/"+s.UnitID+"/retire", e.toks[roles.Admin], map[string]any{"confirm_name": "Bay 3"}); w.Code != 200 {
		t.Fatalf("retire %d %s", w.Code, w.Body)
	}
	if passwdHas(t, pf, s.DeviceID, b2.Setup.MQTTPass) {
		t.Fatal("retired controller can still log in")
	}
	if rt, _ := e.reg.ByUnit(s.UnitID); !rt.Disabled {
		t.Fatal("retired device still accepted by ingest")
	}
	w = e.do("GET", "/api/v1/sites/site-test-01/units", e.toks[roles.Viewer], nil)
	if strings.Contains(w.Body.String(), s.UnitID) {
		t.Fatal("retired unit still listed")
	}
}

func TestAddUnitWithoutBrokerManagementSaysWhy(t *testing.T) {
	e := newEnv(t)
	w := e.do("POST", "/api/v1/sites/site-test-01/units", e.toks[roles.Admin], map[string]any{"name": "X"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "BROKER_NOT_MANAGED") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRenameUnit(t *testing.T) {
	e := newEnv(t)
	if w := e.do("PATCH", "/api/v1/units/unit-test-001", e.toks[roles.Admin], map[string]any{"name": ""}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := e.do("PATCH", "/api/v1/units/unit-test-001", e.toks[roles.Admin], map[string]any{"name": "Lettuce rack"}); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	if w := e.do("GET", "/api/v1/units/unit-test-001", e.toks[roles.Viewer], nil); !strings.Contains(w.Body.String(), "Lettuce rack") {
		t.Fatal(w.Body)
	}
}

// Range numbers here are arbitrary protocol test data, not crop advice.
func TestCropProfilesPerUnitAndRangeAlerts(t *testing.T) {
	e, _ := fleetEnv(t)
	ctx := context.Background()
	body := map[string]any{"name": "Rack A test", "crop": "test-crop", "value_source": "unit test",
		"thresholds": map[string]any{"ph": map[string]any{"min": 1.0, "max": 2.0}}}
	if w := e.do("POST", "/api/v1/sites/site-test-01/crop-profiles", e.toks[roles.Operator], body); w.Code != 403 {
		t.Fatalf("operator edited profiles: %d", w.Code)
	}
	w := e.do("POST", "/api/v1/sites/site-test-01/crop-profiles", e.toks[roles.Admin], body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var p crops.Profile
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w := e.do("POST", "/api/v1/sites/site-test-01/crop-profiles", e.toks[roles.Admin], map[string]any{"name": "x", "crop": "y"}); w.Code != 400 || !strings.Contains(w.Body.String(), "value_source") {
		t.Fatalf("profile without a value source accepted: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/v1/units/unit-test-001/crop-profile", e.toks[roles.Admin], map[string]any{"crop_profile_id": p.ID}); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	w = e.do("GET", "/api/v1/sites/site-test-01/units", e.toks[roles.Viewer], nil)
	if !strings.Contains(w.Body.String(), `"profile_name":"Rack A test"`) {
		t.Fatalf("unit list does not say what grows there: %s", w.Body)
	}
	// stale editor
	upd := map[string]any{"expected_version": 0, "name": "Rack A test", "crop": "test-crop", "value_source": "late"}
	if w := e.do("PUT", "/api/v1/crop-profiles/"+p.ID, e.toks[roles.Admin], upd); w.Code != 409 {
		t.Fatalf("stale update: %d", w.Code)
	}

	// three high samples -> one advisory incident; a heartbeat with no faults must not close it
	run := func(ts []crops.Transition) {
		t.Helper()
		if err := e.db.Tx(ctx, func(tx *sql.Tx) error { _, err := e.srv.Writer.RangeOp(ts).Fn(ctx, tx); return err }); err != nil {
			t.Fatal(err)
		}
	}
	hi := map[string]mc.Reading{"ph": {Value: 5.0, Quality: mc.QualityValid}}
	var ts []crops.Transition
	for i := 0; i < crops.Debounce; i++ {
		ts = append(ts, e.srv.Ranges.Evaluate("unit-test-001", hi)...)
	}
	run(ts)
	if err := e.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := incidents.Reconcile(ctx, tx, "site-test-01", "unit-test-001", nil, "2026-01-01T00:00:00Z")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w = e.do("GET", "/api/v1/sites/site-test-01/incidents?state=active", e.toks[roles.Viewer], nil)
	if !strings.Contains(w.Body.String(), "RANGE_HIGH:ph") || !strings.Contains(w.Body.String(), "advisory") {
		t.Fatalf("range incident missing or closed by heartbeat: %s", w.Body)
	}
	// removing the max closes it
	upd = map[string]any{"expected_version": 1, "name": "Rack A test", "crop": "test-crop", "value_source": "unit test",
		"thresholds": map[string]any{"ph": map[string]any{"min": 1.0}}}
	if w := e.do("PUT", "/api/v1/crop-profiles/"+p.ID, e.toks[roles.Admin], upd); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	ts, err := e.srv.Ranges.Load(ctx, e.db.R, "site-test-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 1 || ts[0].Active {
		t.Fatalf("orphaned alert not resolved: %+v", ts)
	}
	run(ts)
	w = e.do("GET", "/api/v1/sites/site-test-01/incidents?state=active", e.toks[roles.Viewer], nil)
	if strings.Contains(w.Body.String(), "RANGE_HIGH:ph") {
		t.Fatal("alert stayed open after its bound was removed")
	}
	if w := e.do("DELETE", "/api/v1/crop-profiles/"+p.ID, e.toks[roles.Admin], nil); w.Code != 409 {
		t.Fatalf("archived an assigned profile: %d", w.Code)
	}
}

// The firmware decodes contracts/examples/setup/valid.code in its host tests; this proves the Pi
// encodes exactly that string, so the Pi and the controller agree on the setup-code format.
func TestSetupCodeFixtureMatchesTheEdgeEncoder(t *testing.T) {
	e := newEnv(t)
	e.srv.Cfg.SiteID = "site-fixture-01"
	e.srv.Cfg.BrokerPublicURL = "mqtt://aerolab.local:1883"
	r := httptest.NewRequest("POST", "/", nil)
	got := e.srv.bundle(r, "esp32-fixture-01", "unit-fixture-01", "fixture-password-not-a-secret-0001")["setup_code"]
	want, err := os.ReadFile("../../../../contracts/examples/setup/valid.code")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.TrimSpace(string(want)) {
		t.Fatalf("edge setup code drifted from the firmware fixture:\n got %v\nwant %s", got, want)
	}
}
