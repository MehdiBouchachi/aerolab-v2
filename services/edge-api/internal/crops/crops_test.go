package crops

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

// The numbers below are arbitrary test data for the range mechanics, NOT crop recommendations.
func f(v float64) *float64 { return &v }

func valid(v float64) mc.Reading { return mc.Reading{Value: v, Quality: mc.QualityValid} }

func TestValidateRejectsBadShapesButNeverJudgesAgronomy(t *testing.T) {
	base := func() Profile {
		return Profile{Name: "Bay 1 test", Crop: "test-crop", ValueSource: "unit test", Thresholds: map[string]Range{"ph": {Min: f(1), Max: f(2)}}}
	}
	for name, mut := range map[string]func(*Profile){
		"no name":     func(p *Profile) { p.Name = " " },
		"no crop":     func(p *Profile) { p.Crop = "" },
		"no source":   func(p *Profile) { p.ValueSource = "" },
		"min>=max":    func(p *Profile) { p.Thresholds["ph"] = Range{Min: f(2), Max: f(2)} },
		"empty range": func(p *Profile) { p.Thresholds["ph"] = Range{} },
		"bad name":    func(p *Profile) { p.Thresholds["PH value"] = Range{Max: f(1)} },
		"interlock off": func(p *Profile) {
			p.Program = &mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 5, OffDurationSeconds: 5, MaxContinuousOnSecs: 5}
		},
		"max below on": func(p *Profile) {
			p.Program = &mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 5, OffDurationSeconds: 5, MaxContinuousOnSecs: 4, RequireReservoirOK: true}
		},
	} {
		p := base()
		mut(&p)
		var fe *FieldError
		if err := p.Validate(); !errors.As(err, &fe) && err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p := base()
	p.Thresholds["water_temp_c"] = Range{Max: f(-3)} // any finite number is the grower's call
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*sql.DB, func(func(*sql.Tx) error)) {
	t.Helper()
	db := testutil.Store(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO sites(id,name,created_at) VALUES ('site-a','A','x')`,
		`INSERT INTO growing_units(id,site_id,name,created_at) VALUES ('unit-1','site-a','One','x'),('unit-2','site-a','Two','x')`,
	} {
		if _, err := db.W.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return db.R, func(fn func(*sql.Tx) error) {
		t.Helper()
		if err := db.Tx(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProfileLifecycleAndOptimisticVersion(t *testing.T) {
	ctx := context.Background()
	rdb, tx := setup(t)
	var p Profile
	tx(func(t2 *sql.Tx) (err error) {
		p, err = Create(ctx, t2, "site-a", Profile{Name: "A", Crop: "crop-a", ValueSource: "test", Thresholds: map[string]Range{"ec": {Max: f(3)}}}, "u1")
		return
	})
	if p.Version != 1 || p.ID == "" {
		t.Fatalf("%+v", p)
	}
	tx(func(t2 *sql.Tx) error { return Assign(ctx, t2, "site-a", "unit-1", p.ID, "u1") })
	tx(func(t2 *sql.Tx) error {
		_, err := Update(ctx, t2, "site-a", p.ID, 1, Profile{Name: "A", Crop: "crop-a", ValueSource: "test v2", Thresholds: map[string]Range{"ec": {Max: f(4)}}}, "u1")
		return err
	})
	// a second editor still holding version 1 must not overwrite version 2
	var err error
	tx(func(t2 *sql.Tx) error {
		_, err = Update(ctx, t2, "site-a", p.ID, 1, Profile{Name: "A", Crop: "x", ValueSource: "late"}, "u2")
		return nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	var archErr error
	tx(func(t2 *sql.Tx) error { archErr = Archive(ctx, t2, "site-a", p.ID, "u1"); return nil })
	if !errors.Is(archErr, ErrInUse) {
		t.Fatalf("archive while assigned: %v", archErr)
	}
	tx(func(t2 *sql.Tx) error { return Assign(ctx, t2, "site-a", "unit-1", "", "u1") })
	tx(func(t2 *sql.Tx) error { return Archive(ctx, t2, "site-a", p.ID, "u1") })
	if ps, _ := List(ctx, rdb, "site-a"); len(ps) != 0 {
		t.Fatalf("archived profile still listed: %+v", ps)
	}
	// the name is free again once archived
	tx(func(t2 *sql.Tx) error {
		_, err := Create(ctx, t2, "site-a", Profile{Name: "A", Crop: "crop-a", ValueSource: "test"}, "u1")
		return err
	})
}

func TestEvaluatorDebouncesAndIgnoresMissingReadings(t *testing.T) {
	ctx := context.Background()
	rdb, tx := setup(t)
	var p Profile
	tx(func(t2 *sql.Tx) (err error) {
		p, err = Create(ctx, t2, "site-a", Profile{Name: "A", Crop: "c", ValueSource: "test", Thresholds: map[string]Range{"ph": {Min: f(1), Max: f(2)}}}, "u")
		return
	})
	tx(func(t2 *sql.Tx) error { return Assign(ctx, t2, "site-a", "unit-1", p.ID, "u") })
	e := NewEvaluator()
	if ts, err := e.Load(ctx, rdb, "site-a"); err != nil || len(ts) != 0 {
		t.Fatal(ts, err)
	}
	hi := map[string]mc.Reading{"ph": valid(9)}
	ok := map[string]mc.Reading{"ph": valid(1.5)}
	missing := map[string]mc.Reading{"ph": {Quality: mc.QualityMissing}}
	for i := 0; i < Debounce-1; i++ {
		if len(e.Evaluate("unit-1", hi)) != 0 {
			t.Fatal("raised before debounce")
		}
	}
	if len(e.Evaluate("unit-1", missing)) != 0 { // a missing reading neither confirms nor clears
		t.Fatal("missing reading produced a transition")
	}
	ts := e.Evaluate("unit-1", hi)
	if len(ts) != 1 || ts[0] != (Transition{"unit-1", "RANGE_HIGH:ph", true}) {
		t.Fatalf("raise: %+v", ts)
	}
	if len(e.Evaluate("unit-2", hi)) != 0 {
		t.Fatal("unit without a profile must not alert")
	}
	e.Evaluate("unit-1", ok)
	e.Evaluate("unit-1", hi) // a relapse resets the clearing streak
	e.Evaluate("unit-1", ok)
	e.Evaluate("unit-1", ok)
	if ts := e.Evaluate("unit-1", ok); len(ts) != 1 || ts[0].Active {
		t.Fatalf("clear: %+v", ts)
	}
}

func TestLoadClosesAlertsWhoseBoundWasRemoved(t *testing.T) {
	ctx := context.Background()
	rdb, tx := setup(t)
	tx(func(t2 *sql.Tx) error {
		_, err := t2.ExecContext(ctx, `INSERT INTO incidents(id,site_id,unit_id,severity,code,status,first_seen_at,last_seen_at) VALUES ('i1','site-a','unit-1','warning','RANGE_HIGH:ph','ACTIVE_UNACKNOWLEDGED','x','x')`)
		return err
	})
	ts, err := NewEvaluator().Load(ctx, rdb, "site-a")
	if err != nil || len(ts) != 1 || ts[0].Active || ts[0].Code != "RANGE_HIGH:ph" {
		t.Fatalf("%+v %v", ts, err)
	}
}
