package telemetry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

func mkJob(seq int64) recordJob {
	u := "unit-test-001"
	t := &mc.Telemetry{Header: mc.Header{SchemaVersion: 1, SiteID: "site-test-01", DeviceID: "esp32-test-001", UnitID: &u, BootID: "boot-aaaaaaaa", DataSource: mc.SourceSimulator},
		Type: "telemetry", Seq: seq, UptimeMS: seq * 1000, ClockQuality: mc.ClockMonotonicOnly,
		Readings: map[string]mc.Reading{"ph": {Value: 6.1, Quality: mc.QualityValid}}}
	now := time.Now()
	return recordJob{kind: kindTelemetry, siteID: "site-test-01", deviceID: "esp32-test-001", unitID: u, bootID: "boot-aaaaaaaa", seq: seq,
		source: "SIMULATOR", received: now, effective: now, timeQ: "RECEIVED_AT_ONLY", tel: t}
}

func newW(t *testing.T) (*Writer, *testutil.FakePub, *diagnostics.Metrics, *storage.Store) {
	db := testutil.Store(t)
	pub := testutil.NewFakePub()
	m := diagnostics.New()
	return NewWriter(db, pub, sse.NewHub(), m, testutil.Logger(), "site-test-01", 64, false), pub, m, db
}

func rowCount(t *testing.T, db *storage.Store) int {
	var n int
	if err := db.R.QueryRow(`SELECT COUNT(*) FROM telemetry_snapshots`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A04: duplicates (same device, boot, seq) store ONE row but are still acked.
func TestDuplicateRecordsStoredOnceButAcknowledged(t *testing.T) {
	w, pub, m, db := newW(t)
	ctx := context.Background()
	w.commitBatch(ctx, []recordJob{mkJob(1), mkJob(2), mkJob(2), mkJob(1)})
	if n := rowCount(t, db); n != 2 {
		t.Fatalf("rows=%d want 2", n)
	}
	if m.DuplicatesIgnored.Load() != 2 {
		t.Fatalf("dups=%d", m.DuplicatesIgnored.Load())
	}
	rc := pub.Take("/receipts")
	if len(rc) != 1 {
		t.Fatalf("receipts=%d", len(rc))
	}
	var r mc.Receipt
	_ = json.Unmarshal(rc[0].Payload, &r)
	if err := r.Validate(); err != nil {
		t.Fatalf("receipt violates contract: %v", err)
	}
	// resend of the same records later (lost receipt) is acked again, no new rows
	pub.Reset()
	w.commitBatch(ctx, []recordJob{mkJob(1), mkJob(2)})
	if rowCount(t, db) != 2 || len(pub.Take("/receipts")) != 1 {
		t.Fatal("replay of persisted records must be idempotent and re-acknowledged")
	}
}

// A10: when the database cannot commit, NO receipt may be sent. Uses the real
// SQLite SQLITE_FULL path (max_page_count), not a test hook.
func TestNoReceiptWhenDatabaseCannotCommit(t *testing.T) {
	w, pub, m, db := newW(t)
	ctx := context.Background()
	w.commitBatch(ctx, []recordJob{mkJob(1)})
	pub.Reset()

	var pages int
	if err := db.W.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`PRAGMA max_page_count=` + itoa(pages)); err != nil { // disk "full"
		t.Fatal(err)
	}
	big := make([]recordJob, 0, 400)
	for i := int64(10); i < 410; i++ {
		big = append(big, mkJob(i))
	}
	w.commitBatch(ctx, big)

	if got := pub.Take("/receipts"); len(got) != 0 {
		t.Fatalf("receipt sent for data that is not durable: %d receipts", len(got))
	}
	if m.CommitErrors.Load() == 0 || m.StorageOK.Load() {
		t.Fatal("commit failure not surfaced in metrics")
	}
	if n := rowCount(t, db); n != 1 {
		t.Fatalf("partial batch leaked into the database: rows=%d", n)
	}

	// space returns: the device resends, we persist and acknowledge exactly once
	if _, err := db.W.Exec(`PRAGMA max_page_count=1073741823`); err != nil {
		t.Fatal(err)
	}
	w.commitBatch(ctx, big[:5])
	if len(pub.Take("/receipts")) != 1 || !m.StorageOK.Load() {
		t.Fatal("did not recover after space returned")
	}
}

func TestLowDiskSpaceFloorRefusesCommitAndReceipt(t *testing.T) {
	w, pub, m, db := newW(t)
	db2, err := storage.Open(db.Path, 1<<62) // floor higher than any disk
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	w.db = db2
	w.commitBatch(context.Background(), []recordJob{mkJob(1)})
	if len(pub.Take("/receipts")) != 0 || m.StorageOK.Load() {
		t.Fatal("receipt issued below the free-space floor")
	}
}

func TestRejectedRecordGetsRejectedReceiptOnly(t *testing.T) {
	w, pub, _, db := newW(t)
	w.commitBatch(context.Background(), []recordJob{{kind: kindReject, siteID: "site-test-01", deviceID: "esp32-test-001", bootID: "boot-aaaaaaaa", seq: 7}})
	rc := pub.Take("/receipts")
	if len(rc) != 1 || rowCount(t, db) != 0 {
		t.Fatalf("receipts=%d rows=%d", len(rc), rowCount(t, db))
	}
	var r mc.Receipt
	_ = json.Unmarshal(rc[0].Payload, &r)
	if len(r.AckedSequences) != 0 || len(r.RejectedSequences) != 1 || r.RejectedSequences[0] != 7 {
		t.Fatalf("%+v", r)
	}
}

func TestReceiptsChunkedAt100(t *testing.T) {
	w, pub, _, _ := newW(t)
	var batch []recordJob
	for i := int64(1); i <= 150; i++ {
		batch = append(batch, mkJob(i))
	}
	w.commitBatch(context.Background(), batch)
	rc := pub.Take("/receipts")
	if len(rc) != 2 {
		t.Fatalf("want 2 receipts, got %d", len(rc))
	}
	for _, m := range rc {
		var r mc.Receipt
		_ = json.Unmarshal(m.Payload, &r)
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueueIsBoundedAndDropsWithoutReceipt(t *testing.T) {
	w, _, m, _ := newW(t) // queue size 64, writer not running
	for i := int64(1); i <= 100; i++ {
		w.enqueue(mkJob(i))
	}
	if m.QueueDrops.Load() != 36 {
		t.Fatalf("drops=%d want 36", m.QueueDrops.Load())
	}
	if r, _ := w.QueueDepth(); r != 64 {
		t.Fatalf("depth=%d", r)
	}
}

func TestEffectiveTimeNeverFabricatesAndLabelsEstimates(t *testing.T) {
	recv := time.Now()
	rt := devices.Runtime{Seen: true, BootID: "boot-1", StateAt: recv, State: mc.State{UptimeMS: 100_000}}
	// same boot: estimated from uptime delta (record is 40 s older than the heartbeat)
	got, q := effective(nil, 60_000, "boot-1", rt, recv)
	if q != "ESTIMATED_FROM_UPTIME" || recv.Sub(got) < 39*time.Second || recv.Sub(got) > 41*time.Second {
		t.Fatalf("est=%v q=%s", recv.Sub(got), q)
	}
	// different boot (replay from before a reset): only the receive time is honest
	if got, q = effective(nil, 60_000, "boot-0", rt, recv); q != "RECEIVED_AT_ONLY" || !got.Equal(recv.UTC()) {
		t.Fatalf("q=%s", q)
	}
	// device with a trusted clock
	s := "2026-11-12T13:00:00Z"
	if got, q = effective(&s, 1, "boot-1", rt, recv); q != "DEVICE_CLOCK" || got.Format(time.RFC3339) != s {
		t.Fatalf("q=%s got=%v", q, got)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
