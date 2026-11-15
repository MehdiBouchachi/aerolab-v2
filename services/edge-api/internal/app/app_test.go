package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

// The whole service starts without a broker (local UI must come up), and retention prunes only what is
// older than the window.
func TestAppStartsWithoutBrokerAndRetentionPrunesOnlyOldRows(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{SiteID: "site-app-01", SiteName: "T", HTTPAddr: "127.0.0.1:0", DBPath: filepath.Join(dir, "a.db"), MQTTURL: "tcp://127.0.0.1:1",
		StaleAfter: 15 * time.Second, CommandRetry: 5 * time.Second, CommandMaxTries: 3, CommandTTL: time.Minute, MemSoftLimitMiB: 192,
		RetentionDays: 30, RecordQueueSize: 64}
	a, err := New(context.Background(), cfg, testutil.Logger())
	if err != nil {
		t.Fatal(err)
	}
	defer a.DB.Close()
	ins := func(id string, age time.Duration) {
		at := storage.FormatTime(time.Now().Add(-age))
		if _, err := a.DB.W.Exec(`INSERT INTO telemetry_snapshots(event_id,site_id,device_id,unit_id,boot_id,sequence_number,uptime_ms,clock_quality,received_at_utc,effective_at_utc,time_quality,readings_json,source)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, "site-app-01", "d", "u", "boot-aaaaaaaa", len(id), 1, "monotonic_only", at, at, "RECEIVED_AT_ONLY", "{}", "SIMULATOR"); err != nil {
			t.Fatal(err)
		}
	}
	ins("old-1", 31*24*time.Hour)
	ins("old-22", 45*24*time.Hour)
	ins("new-333", 29*24*time.Hour)
	ins("now-4444", 0)
	a.RetentionOnce(context.Background())
	var ids []string
	rows, _ := a.DB.R.Query(`SELECT event_id FROM telemetry_snapshots ORDER BY event_id`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		ids = append(ids, s)
	}
	rows.Close()
	if len(ids) != 2 || ids[0] != "new-333" || ids[1] != "now-4444" {
		t.Fatalf("retention kept %v", ids)
	}
}
