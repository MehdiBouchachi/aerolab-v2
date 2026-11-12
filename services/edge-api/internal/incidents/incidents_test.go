package incidents

import (
	"context"
	"database/sql"
	"testing"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

func setup(t *testing.T) *storage.Store {
	db := testutil.Store(t)
	_, err := db.W.Exec(`INSERT INTO growing_units(id,site_id,name,created_at) VALUES ('unit-test-001','site-test-01','U1',?),('unit-test-002','site-test-01','U2',?)`, storage.Now(), storage.Now())
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func reconcile(t *testing.T, db *storage.Store, unit string, faults ...string) []Change {
	var ch []Change
	if err := db.Tx(context.Background(), func(tx *sql.Tx) error {
		var e error
		ch, e = Reconcile(context.Background(), tx, "site-test-01", unit, faults, storage.Now())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return ch
}

func count(t *testing.T, db *storage.Store, where string) (n int) {
	if err := db.R.QueryRow(`SELECT COUNT(*) FROM incidents WHERE ` + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRepeatedConditionUpdatesOneIncident(t *testing.T) {
	db := setup(t)
	for i := 0; i < 50; i++ {
		reconcile(t, db, "unit-test-001", mc.FaultLowWater)
	}
	if n := count(t, db, `unit_id='unit-test-001'`); n != 1 {
		t.Fatalf("%d incidents for one continuing fault", n)
	}
	var occ int
	_ = db.R.QueryRow(`SELECT occurrences FROM incidents`).Scan(&occ)
	if occ != 50 {
		t.Fatalf("occurrences=%d", occ)
	}
}

func TestLifecycleAckDoesNotResolveAndResolveKeepsHistory(t *testing.T) {
	db := setup(t)
	ch := reconcile(t, db, "unit-test-001", mc.FaultLowWater)
	if len(ch) != 1 || ch[0].Incident.Status != "ACTIVE_UNACKNOWLEDGED" || ch[0].Incident.Severity != mc.SevCritical {
		t.Fatalf("%+v", ch)
	}
	id := ch[0].Incident.ID

	var inc Incident
	var changed bool
	ack := func() {
		_ = db.Tx(context.Background(), func(tx *sql.Tx) error {
			var e error
			inc, changed, e = Acknowledge(context.Background(), tx, "site-test-01", id, "user-1")
			return e
		})
	}
	ack()
	if !changed || inc.Status != "ACTIVE_ACKNOWLEDGED" || inc.ResolvedAt != nil {
		t.Fatalf("ack must not resolve: %+v", inc)
	}
	ack()
	if changed {
		t.Fatal("second ack should be a no-op")
	}
	// fault clears -> resolved but acknowledged, history retained
	ch = reconcile(t, db, "unit-test-001")
	if len(ch) != 1 || ch[0].Incident.Status != "RESOLVED_ACKNOWLEDGED" || ch[0].Incident.ResolvedAt == nil {
		t.Fatalf("%+v", ch)
	}
	// it recurs: a NEW incident, the old one is not resurrected as "just happened"
	reconcile(t, db, "unit-test-001", mc.FaultLowWater)
	if n := count(t, db, `unit_id='unit-test-001'`); n != 2 {
		t.Fatalf("want 2 rows (history + new), got %d", n)
	}
	if n := count(t, db, `status LIKE 'ACTIVE%'`); n != 1 {
		t.Fatal(n)
	}
}

func TestResolvedUnacknowledgedCanStillBeAcknowledged(t *testing.T) {
	db := setup(t)
	id := reconcile(t, db, "unit-test-001", mc.FaultPHInvalid)[0].Incident.ID
	reconcile(t, db, "unit-test-001")
	var inc Incident
	_ = db.Tx(context.Background(), func(tx *sql.Tx) error {
		var e error
		inc, _, e = Acknowledge(context.Background(), tx, "site-test-01", id, "u")
		return e
	})
	if inc.Status != "RESOLVED_ACKNOWLEDGED" {
		t.Fatal(inc.Status)
	}
}

func TestUnitsAreIsolatedAndStaleIsEdgeOwned(t *testing.T) {
	db := setup(t)
	reconcile(t, db, "unit-test-001", mc.FaultLowWater)
	reconcile(t, db, "unit-test-002") // a healthy neighbour must not touch unit 1
	if n := count(t, db, `unit_id='unit-test-001' AND status LIKE 'ACTIVE%'`); n != 1 {
		t.Fatal("neighbour's heartbeat changed unit 1's incident")
	}
	// DEVICE_STALE is not cleared by a fault-set reconcile (device cannot speak while stale)
	_ = db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, e := SetCondition(context.Background(), tx, "site-test-01", "unit-test-002", CodeDeviceStale, true, storage.Now())
		return e
	})
	reconcile(t, db, "unit-test-002")
	if n := count(t, db, `code='DEVICE_STALE' AND status LIKE 'ACTIVE%'`); n != 1 {
		t.Fatal("stale incident cleared by unrelated reconcile")
	}
	// NO_VALID_PROGRAM is a setup state, not an incident
	reconcile(t, db, "unit-test-002", mc.FaultNoProgram)
	if n := count(t, db, `code='NO_VALID_PROGRAM'`); n != 0 {
		t.Fatal("setup state raised as incident")
	}
}

func TestPlainLanguageForEveryKnownCode(t *testing.T) {
	for _, c := range []string{mc.FaultLowWater, mc.FaultLevelSensor, mc.FaultPHInvalid, mc.FaultECInvalid, CodeDeviceStale, "SOMETHING_NEW"} {
		d := Describe(c)
		if d.Title == "" || d.Cause == "" || d.Action == "" {
			t.Errorf("%s lacks title/cause/action", c)
		}
	}
	if Describe(CodeDeviceStale).Cause == "" || !contains(Describe(CodeDeviceStale).Cause, "does not prove") {
		t.Error("stale text must warn it does not prove the pump stopped")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
