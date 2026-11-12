package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

func TestPasswordHashVerifyAndTamper(t *testing.T) {
	h, err := hashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") || strings.Contains(h, "correct-horse") {
		t.Fatalf("bad hash %q", h)
	}
	if !verifyPassword(h, "correct-horse-battery") || verifyPassword(h, "wrong") {
		t.Fatal("verify")
	}
	// a tampered row with absurd parameters must not be a DoS vector
	if verifyPassword("$argon2id$v=19$m=4000000,t=99,p=64$AAAA$AAAA", "x") {
		t.Fatal("accepted absurd parameters")
	}
	if verifyPassword("garbage", "x") || verifyPassword("", "x") {
		t.Fatal("accepted garbage hash")
	}
}

func TestSessionTokensAreHashedAtRestAndExpire(t *testing.T) {
	db := testutil.Store(t)
	s := New(db, "site-test-01")
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "alice", "correct-horse-battery", roles.Operator, "t"); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.Login(ctx, "Alice", "correct-horse-battery", "1.2.3.4") // username is case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	_ = db.R.QueryRow(`SELECT token_hash FROM local_sessions`).Scan(&stored)
	if stored == tok || len(stored) != 64 {
		t.Fatal("raw session token stored in the database")
	}
	if u, err := s.Authenticate(ctx, tok); err != nil || u.Role != roles.Operator {
		t.Fatalf("%v", err)
	}
	// idle expiry
	db.W.Exec(`UPDATE local_sessions SET expires_at=?`, storage.FormatTime(time.Now().Add(-time.Second)))
	if _, err := s.Authenticate(ctx, tok); err == nil {
		t.Fatal("expired session accepted")
	}
	// absolute expiry even if idle window was extended
	db.W.Exec(`UPDATE local_sessions SET expires_at=?, absolute_expires_at=?`, storage.FormatTime(time.Now().Add(time.Hour)), storage.FormatTime(time.Now().Add(-time.Second)))
	if _, err := s.Authenticate(ctx, tok); err == nil {
		t.Fatal("session outlived its absolute lifetime")
	}
	// logout revokes
	tok2, _, _ := s.Login(ctx, "alice", "correct-horse-battery", "1.2.3.4")
	s.Logout(ctx, tok2, "u")
	if _, err := s.Authenticate(ctx, tok2); err == nil {
		t.Fatal("logged-out token still valid")
	}
}

func TestDuplicateAndInvalidUsers(t *testing.T) {
	db := testutil.Store(t)
	s := New(db, "site-test-01")
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "bob", "correct-horse-battery", roles.Viewer, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "BOB", "correct-horse-battery", roles.Viewer, "t"); err != ErrExists {
		t.Fatalf("%v", err)
	}
	if _, err := s.CreateUser(ctx, "carl", "short", roles.Viewer, "t"); err != ErrWeakPassword {
		t.Fatalf("%v", err)
	}
	if _, err := s.CreateUser(ctx, "dora", "correct-horse-battery", roles.Role("ROOT"), "t"); err == nil {
		t.Fatal("invented role accepted")
	}
}

func TestPasswordNeverAppearsInAuditLog(t *testing.T) {
	db := testutil.Store(t)
	s := New(db, "site-test-01")
	ctx := context.Background()
	_, _ = s.CreateUser(ctx, "erin", "correct-horse-battery", roles.Admin, "t")
	_, _, _ = s.Login(ctx, "erin", "wrong-password-xx", "9.9.9.9")
	rows, _ := db.R.Query(`SELECT COALESCE(previous_json,'')||COALESCE(next_json,'')||action||COALESCE(target_id,'') FROM audit_events`)
	defer rows.Close()
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		if strings.Contains(s, "correct-horse") || strings.Contains(s, "wrong-password") || strings.Contains(s, "argon2") {
			t.Fatalf("secret leaked into audit: %s", s)
		}
	}
}

func TestUsernameSprayingIsThrottledPerIP(t *testing.T) {
	db := testutil.Store(t)
	s := New(db, "site-test-01")
	ctx := context.Background()
	_, _ = s.CreateUser(ctx, "frank", "correct-horse-battery", roles.Admin, "t")
	for i := 0; i < 30; i++ { // 30 DIFFERENT usernames from one address
		_, _, err := s.Login(ctx, "ghost"+string(rune('a'+i%26))+string(rune('a'+i/26)), "x", "6.6.6.6")
		if err != ErrInvalidCredentials {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := s.Login(ctx, "frank", "correct-horse-battery", "6.6.6.6"); err != ErrRateLimited {
		t.Fatalf("a sprayed address must be throttled even for a real account: %v", err)
	}
	// ...but another address is unaffected
	if _, _, err := s.Login(ctx, "frank", "correct-horse-battery", "7.7.7.7"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='auth.login_failed'`).Scan(&n)
	if n > 31 {
		t.Fatalf("audit table grew unbounded under spraying: %d rows", n)
	}
}
