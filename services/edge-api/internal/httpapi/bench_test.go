package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// benchEnv: 20 units, one day of 1-minute snapshots each (28 800 rows), a few incidents.
func benchEnv(b *testing.B) *env {
	t := &testing.T{}
	e := newEnv(t)
	ctx := context.Background()
	for i := 2; i <= 20; i++ {
		if _, _, err := e.reg.Provision(ctx, fmt.Sprintf("esp32-test-%03d", i), fmt.Sprintf("unit-test-%03d", i), fmt.Sprintf("Unit %d", i), "sim", "SIMULATOR", "t"); err != nil {
			b.Fatal(err)
		}
	}
	tx, _ := e.db.W.Begin()
	st, _ := tx.Prepare(`INSERT INTO telemetry_snapshots(event_id,site_id,device_id,unit_id,boot_id,sequence_number,uptime_ms,clock_quality,received_at_utc,effective_at_utc,time_quality,readings_json,source) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	now := time.Now()
	for u := 1; u <= 20; u++ {
		for m := 0; m < 1440; m++ {
			at := storage.FormatTime(now.Add(-time.Duration(m) * time.Minute))
			st.Exec(fmt.Sprintf("e%d-%d", u, m), "site-test-01", fmt.Sprintf("esp32-test-%03d", u), fmt.Sprintf("unit-test-%03d", u), "boot-aaaaaaaa", m+1, m*1000, "monotonic_only", at, at, "DEVICE_CLOCK",
				`{"ph":{"value":6.1,"quality":"valid"},"ec_ms_cm":{"value":1.7,"quality":"valid"},"water_temp_c":{"value":22.5,"quality":"valid"},"reservoir_level_ok":{"value":true,"quality":"valid"},"light_lux":{"value":null,"quality":"missing"}}`, "SIMULATOR")
		}
		tx.Exec(`INSERT INTO incidents(id,site_id,unit_id,severity,code,status,first_seen_at,last_seen_at) VALUES (?,?,?,?,?,?,?,?)`, fmt.Sprintf("i%d", u), "site-test-01", fmt.Sprintf("unit-test-%03d", u), "warning", "PH_INVALID", "RESOLVED_ACKNOWLEDGED", storage.Now(), storage.Now())
	}
	tx.Commit()
	return e
}

func BenchmarkListUnits20(b *testing.B) {
	e := benchEnv(b)
	tok := e.toks[roles.Viewer]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/sites/site-test-01/units?limit=100", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		e.h.ServeHTTP(w, r)
		if w.Code != 200 {
			b.Fatal(w.Code)
		}
	}
}

func BenchmarkSnapshot20(b *testing.B) {
	e := benchEnv(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.srv.Snapshot(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
