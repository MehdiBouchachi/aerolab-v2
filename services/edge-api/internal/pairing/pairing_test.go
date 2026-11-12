package pairing

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/testutil"
)

func newSvc(t *testing.T) (*Service, *storage.Store) {
	db := testutil.Store(t)
	s, err := Ensure(context.Background(), db, "site-test-01")
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func chal(n int) string { return base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", n))) }

func TestIdentityProofVerifiesOnlyForTheRightSiteKeyAndChallenge(t *testing.T) {
	s, db := newSvc(t)
	c := chal(16)
	sig, err := s.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyIdentity(s.PublicKey(), "site-test-01", c, sig) {
		t.Fatal("a genuine proof was rejected")
	}
	for name, ok := range map[string]bool{
		"wrong site":                        VerifyIdentity(s.PublicKey(), "site-other-99", c, sig),
		"replayed to a different challenge": VerifyIdentity(s.PublicKey(), "site-test-01", chal(17), sig),
		"garbage signature":                 VerifyIdentity(s.PublicKey(), "site-test-01", c, "AAAA"),
		"garbage key":                       VerifyIdentity("AAAA", "site-test-01", c, sig),
	} {
		if ok {
			t.Errorf("%s: accepted", name)
		}
	}
	// another Pi (different key) cannot impersonate this one under a PINNED key
	db2 := testutil.Store(t)
	other, _ := Ensure(context.Background(), db2, "site-test-01")
	osig, _ := other.Sign(c)
	if VerifyIdentity(s.PublicKey(), "site-test-01", c, osig) {
		t.Fatal("an impostor with the same site id passed against the pinned key")
	}
	// the key is stable across restarts
	again, _ := Ensure(context.Background(), db, "site-test-01")
	if again.PublicKey() != s.PublicKey() {
		t.Fatal("site key changed on restart: every paired phone would break")
	}
	for _, bad := range []string{"", chal(8), chal(65), "!!!notb64"} {
		if _, err := s.Sign(bad); err == nil {
			t.Errorf("challenge %q accepted", bad)
		}
	}
}

func TestPairingCodeIsSingleUseExpiringAndStoredHashed(t *testing.T) {
	s, db := newSvc(t)
	ctx := context.Background()
	code, _, err := s.Start(ctx, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	_ = db.R.QueryRow(`SELECT code_hash FROM pairing_codes`).Scan(&stored)
	if strings.Contains(stored, strings.ReplaceAll(code, "-", "")) || len(stored) != 64 {
		t.Fatalf("code stored in clear: %q", stored)
	}
	if err := s.Confirm(ctx, strings.ToLower(code), "Pixel", "1.1.1.1"); err != nil { // case/space tolerant for hand entry
		t.Fatal(err)
	}
	if err := s.Confirm(ctx, code, "Another phone", "1.1.1.1"); err != ErrInvalidCode {
		t.Fatalf("a code worked twice: %v", err)
	}
	c2, _, _ := s.Start(ctx, "admin-1")
	db.W.Exec(`UPDATE pairing_codes SET expires_at=?`, storage.FormatTime(time.Now().Add(-time.Second)))
	if err := s.Confirm(ctx, c2, "late", "1.1.1.2"); err != ErrInvalidCode {
		t.Fatalf("expired code accepted: %v", err)
	}
}

func TestPairingCodeGuessingIsRateLimitedAndOutstandingCodesBounded(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	good, _, _ := s.Start(ctx, "a")
	for i := 0; i < 10; i++ {
		if err := s.Confirm(ctx, "AAAA-BBBB", "x", "9.9.9.9"); err != ErrInvalidCode {
			t.Fatal(err)
		}
	}
	if err := s.Confirm(ctx, good, "x", "9.9.9.9"); err != ErrRateLimited {
		t.Fatalf("a guessing address must be locked out even for the right code: %v", err)
	}
	if err := s.Confirm(ctx, good, "x", "8.8.8.8"); err != nil {
		t.Fatalf("another address is unaffected: %v", err)
	}
	for i := 0; i < MaxOutstanding; i++ {
		if _, _, err := s.Start(ctx, "a"); err != nil && i < MaxOutstanding-1 {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Start(ctx, "a"); err != ErrTooMany {
		t.Fatalf("unbounded outstanding codes: %v", err)
	}
}

// Cross-language contract: the phone (TypeScript) verifies a vector produced by THIS code. The vector is
// checked in at contracts/examples/identity.vector.json; regenerate with UPDATE_VECTOR=1 if the scheme
// is changed on purpose (and then the mobile test must still pass).
func TestIdentityVectorMatchesCheckedInFileUsedByTheMobileTests(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	s := &Service{siteID: "site-vector-01", priv: priv, pub: priv.Public().(ed25519.PublicKey)}
	challenge := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	sig, err := s.Sign(challenge)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{"site_id": "site-vector-01", "challenge": challenge, "public_key": s.PublicKey(), "signature": sig}
	path := "../../../../contracts/examples/identity.vector.json"
	if os.Getenv("UPDATE_VECTOR") == "1" {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	_ = json.Unmarshal(raw, &want)
	for k, v := range got {
		if want[k] != v {
			t.Fatalf("%s differs from the checked-in vector (the wire scheme changed): got %q want %q", k, v, want[k])
		}
	}
	if !VerifyIdentity(want["public_key"], want["site_id"], want["challenge"], want["signature"]) {
		t.Fatal("vector does not verify")
	}
}
