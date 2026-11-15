package broker

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

// verify recomputes a "$7$" hash the way Mosquitto does.
func verify(t *testing.T, hash, password string) bool {
	t.Helper()
	p := strings.Split(hash, "$")
	if len(p) != 5 || p[1] != "7" || p[2] != "101" {
		t.Fatalf("not a mosquitto $7$ hash: %q", hash)
	}
	salt, _ := base64.StdEncoding.DecodeString(p[3])
	want, _ := base64.StdEncoding.DecodeString(p[4])
	got, err := pbkdf2.Key(sha512.New, password, salt, 101, 64)
	if err != nil {
		t.Fatal(err)
	}
	return string(got) == string(want)
}

func TestHashIsMosquittoFormat(t *testing.T) {
	pw := NewPassword()
	if len(pw) < 32 || strings.ContainsAny(pw, "+/=:") {
		t.Fatalf("password %q not URL/passwd-file safe", pw)
	}
	h, err := Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !verify(t, h, pw) || verify(t, h, pw+"x") {
		t.Fatal("hash does not verify exactly")
	}
}

func TestSyncKeepsForeignLinesAndReplacesManagedOnes(t *testing.T) {
	ctx := context.Background()
	db := testutil.Store(t)
	dir := t.TempDir()
	pf := filepath.Join(dir, "passwd")
	if err := os.WriteFile(pf, []byte("aerolab-edge:$7$101$edge\nesp32-installer:$7$101$inst\nesp32-new:$7$101$stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, err := db.W.ExecContext(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sites(id,name,created_at) VALUES ('site-a','A','2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO devices(id,site_id,data_source,provisioned_at,broker_password_hash) VALUES ('esp32-new','site-a','DEVICE','x','$7$101$fresh')`)
	exec(`INSERT INTO devices(id,site_id,data_source,provisioned_at,broker_password_hash,disabled_at) VALUES ('esp32-old','site-a','DEVICE','x','$7$101$gone','y')`)
	exec(`INSERT INTO devices(id,site_id,data_source,provisioned_at) VALUES ('esp32-installer','site-a','DEVICE','x')`) // unmanaged: line kept

	m := &Manager{PasswdFile: pf}
	if err := m.Sync(ctx, db.R); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(pf)
	want := "aerolab-edge:$7$101$edge\nesp32-installer:$7$101$inst\nesp32-new:$7$101$fresh\n"
	if string(got) != want {
		t.Fatalf("passwd =\n%s\nwant\n%s", got, want)
	}
	if st, _ := os.Stat(pf); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
	// retiring removes the line; nothing else moves
	exec(`UPDATE devices SET disabled_at='z' WHERE id='esp32-new'`)
	if err := m.Sync(ctx, db.R); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(pf)
	if string(got) != "aerolab-edge:$7$101$edge\nesp32-installer:$7$101$inst\n" {
		t.Fatalf("after retire:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".aerolab-passwd-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestSyncSignalsBrokerPid(t *testing.T) {
	ctx := context.Background()
	db := testutil.Store(t)
	dir := t.TempDir()
	m := &Manager{PasswdFile: filepath.Join(dir, "passwd"), PIDFile: filepath.Join(dir, "pid")}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO sites(id,name,created_at) VALUES ('site-a','A','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO devices(id,site_id,data_source,provisioned_at,broker_password_hash) VALUES ('esp32-new','site-a','DEVICE','x','h')`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.PIDFile, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Sync(ctx, db.R); err == nil || !strings.Contains(err.Error(), "bad pid") {
		t.Fatalf("pid 1 must never be signalled, got %v", err)
	}
	if (&Manager{}).Enabled() || (*Manager)(nil).Enabled() {
		t.Fatal("Enabled")
	}
	if err := (&Manager{}).Sync(ctx, db.R); err != ErrNotConfigured {
		t.Fatalf("unconfigured: %v", err)
	}
}
