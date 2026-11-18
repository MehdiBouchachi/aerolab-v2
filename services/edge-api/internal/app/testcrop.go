package app

// A TEST tomato crop profile for the simulated demo site, so crop profiles, range alerts and "send this
// profile's programme" can be tried end to end. It is created ONLY together with simulated controllers
// (seedProvisioned, which refuses to run unless AEROLAB_ALLOW_SIMULATED_DATA=true), never on a real site.
//
// THESE NUMBERS ARE NOT TOMATO GROWING ADVICE. They were picked around the simulator's own fake sensor
// waves (simulator/esp32/sensors.go: air ~24 C, humidity 58 +/- 4 %, water ~22 C, pH ~6.1, EC ~1.7) so
// that most readings sit inside the ranges and the humidity range is crossed now and then, which shows an
// alert being raised and cleared. The name, notes and value_source all say so.

import (
	"context"
	"database/sql"
	"errors"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/crops"
)

const TestTomatoProfileName = "Tomato (TEST DATA)"

func f(v float64) *float64 { return &v }

func testTomatoProfile() crops.Profile {
	return crops.Profile{
		Name:        TestTomatoProfileName,
		Crop:        "Tomato",
		Variety:     "Test profile",
		Notes:       "Software test data for the simulated site. Not growing advice: replace every number with values from your agronomist before using a profile on real plants.",
		ValueSource: "TEST DATA - chosen around the simulator's fake sensor values, not agronomic values",
		Thresholds: map[string]crops.Range{
			"air_temp_c":            {Min: f(18), Max: f(30)},
			"relative_humidity_pct": {Min: f(50), Max: f(60)}, // tight on purpose: the simulated humidity crosses 60 %
			"water_temp_c":          {Min: f(18), Max: f(26)},
			"ph":                    {Min: f(5.5), Max: f(6.8)},
			"ec_ms_cm":              {Min: f(1.0), Max: f(2.5)},
		},
		Program: &mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 30, OffDurationSeconds: 270, MaxContinuousOnSecs: 60, RequireReservoirOK: true},
	}
}

// seedTestCropProfile creates the test profile once. If someone archived or renamed it, it is created
// again on the next simulated start; an existing one is never overwritten.
func (a *App) seedTestCropProfile(ctx context.Context) error {
	return a.DB.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM crop_profiles WHERE site_id=? AND name=? AND archived_at IS NULL`, a.Cfg.SiteID, TestTomatoProfileName).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		_, err := crops.Create(ctx, tx, a.Cfg.SiteID, testTomatoProfile(), "bootstrap")
		if errors.Is(err, crops.ErrName) {
			return nil
		}
		return err
	})
}
