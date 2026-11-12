package storage

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func TestMigrateIsIdempotentAndOrdered(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "m.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fs := fstest.MapFS{
		"0002_b.sql": {Data: []byte(`CREATE TABLE b (x INTEGER REFERENCES a(x)) STRICT;`)},
		"0001_a.sql": {Data: []byte(`CREATE TABLE a (x INTEGER PRIMARY KEY) STRICT;`)},
	}
	got, err := db.Migrate(context.Background(), fs)
	if err != nil || len(got) != 2 || got[0] != "0001_a" {
		t.Fatalf("%v %v", got, err)
	}
	if again, err := db.Migrate(context.Background(), fs); err != nil || len(again) != 0 {
		t.Fatalf("second run applied %v (%v)", again, err)
	}
	// failing migration rolls back entirely and is not recorded
	bad := fstest.MapFS{"0003_bad.sql": {Data: []byte(`CREATE TABLE c (x INTEGER); INSERT INTO nope VALUES (1);`)}}
	if _, err := db.Migrate(context.Background(), bad); err == nil {
		t.Fatal("bad migration succeeded")
	}
	var n int
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='c'`).Scan(&n)
	if n != 0 {
		t.Fatal("partial migration left a table behind")
	}
}

func TestPragmasAreTheDurableOnes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "p.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var jm string
	var sync, fk int
	_ = db.W.QueryRow(`PRAGMA journal_mode`).Scan(&jm)
	_ = db.W.QueryRow(`PRAGMA synchronous`).Scan(&sync)
	_ = db.W.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if jm != "wal" || sync != 2 /* FULL */ || fk != 1 {
		t.Fatalf("journal=%s synchronous=%d foreign_keys=%d", jm, sync, fk)
	}
	// the read pool cannot write
	if _, err := db.R.Exec(`CREATE TABLE x (a)`); err == nil {
		t.Fatal("read pool is writable")
	}
}

func TestTimeFormatIsFixedWidthSoTextOrderEqualsTimeOrder(t *testing.T) {
	base := time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)
	ts := []time.Time{base, base.Add(time.Millisecond), base.Add(999 * time.Millisecond), base.Add(time.Second), base.Add(time.Hour), base.Add(24 * time.Hour)}
	for i := 1; i < len(ts); i++ {
		a, b := FormatTime(ts[i-1]), FormatTime(ts[i])
		if len(a) != len(b) || !(a < b) {
			t.Fatalf("%q !< %q", a, b)
		}
	}
	// offsets are normalised to UTC
	if FormatTime(base.In(time.FixedZone("x", 3600))) != FormatTime(base) {
		t.Fatal("not normalised to UTC")
	}
}
