// Package pairing: site identity + physical-presence pairing for the mobile app (spec 7.4).
//
//	identity  - the Pi signs a caller-chosen challenge with its Ed25519 site key. The phone pins the public
//	            key at pairing time, so a different (spoofed) Pi cannot pass later probes.
//	pairing   - an on-site Admin creates a short-lived single-use code (5 min). The QR holds the opaque site id,
//	            the public key, URL hints and that code. There is NO long-term secret in the QR.
package pairing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

const (
	CodeTTL        = 5 * time.Minute
	MaxOutstanding = 5
	alphabet       = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I
	MsgPrefix      = "aerolab-identity-v1\n"
)

var (
	ErrInvalidCode  = errors.New("invalid or expired pairing code")
	ErrTooMany      = errors.New("too many outstanding pairing codes")
	ErrBadChallenge = errors.New("challenge must be 16..64 random bytes, base64url")
)

type Service struct {
	db     *storage.Store
	siteID string
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	mu     sync.Mutex
	fails  map[string][]time.Time
}

// Ensure loads the site key, generating it on first start.
func Ensure(ctx context.Context, db *storage.Store, siteID string) (*Service, error) {
	s := &Service{db: db, siteID: siteID, fails: map[string][]time.Time{}}
	var pub, priv []byte
	err := db.R.QueryRowContext(ctx, `SELECT public_key,private_key FROM site_identity WHERE id=1`).Scan(&pub, &priv)
	if errors.Is(err, sql.ErrNoRows) {
		pk, sk, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return nil, gerr
		}
		if err := db.Tx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO site_identity(id,public_key,private_key,created_at) VALUES (1,?,?,?)`, []byte(pk), []byte(sk), storage.Now())
			return e
		}); err != nil {
			return nil, err
		}
		err = db.R.QueryRowContext(ctx, `SELECT public_key,private_key FROM site_identity WHERE id=1`).Scan(&pub, &priv)
	}
	if err != nil {
		return nil, err
	}
	if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("site identity corrupt")
	}
	s.pub, s.priv = pub, priv
	return s, nil
}

func (s *Service) PublicKey() string { return base64.RawURLEncoding.EncodeToString(s.pub) }

// Sign proves possession of the site key for a caller-chosen challenge.
func (s *Service) Sign(challengeB64 string) (sig string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(challengeB64)
	if err != nil || len(raw) < 16 || len(raw) > 64 {
		return "", ErrBadChallenge
	}
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.priv, []byte(MsgPrefix+s.siteID+"\n"+challengeB64))), nil
}

// VerifyIdentity is what a client does (exported so tests prove the scheme end to end).
func VerifyIdentity(pubB64, siteID, challengeB64, sigB64 string) bool {
	pub, e1 := base64.RawURLEncoding.DecodeString(pubB64)
	sig, e2 := base64.RawURLEncoding.DecodeString(sigB64)
	if e1 != nil || e2 != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, []byte(MsgPrefix+siteID+"\n"+challengeB64), sig)
}

func hashCode(c string) string {
	h := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(c))))
	return hex.EncodeToString(h[:])
}

func newCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i := range b {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

// Start creates a single-use code for an on-site Admin to show as a QR.
func (s *Service) Start(ctx context.Context, actor string) (code string, expires time.Time, err error) {
	code, err = newCode()
	if err != nil {
		return
	}
	expires = time.Now().Add(CodeTTL)
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pairing_codes WHERE used_at IS NULL AND expires_at>?`, storage.Now()).Scan(&n); e != nil {
			return e
		}
		if n >= MaxOutstanding {
			return ErrTooMany
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO pairing_codes(code_hash,created_by,created_at,expires_at) VALUES (?,?,?,?)`, hashCode(code), actor, storage.Now(), storage.FormatTime(expires)); e != nil {
			return e
		}
		return auth.Audit(ctx, tx, s.siteID, actor, "local", "pairing.start", "site", s.siteID, "", "")
	})
	return
}

func (s *Service) rateLimited(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	keep := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if now.Sub(t) < 5*time.Minute {
			keep = append(keep, t)
		}
	}
	s.fails[ip] = keep
	if len(s.fails) > 5000 { // bounded memory
		s.fails = map[string][]time.Time{ip: keep}
	}
	return len(keep) >= 10
}

func (s *Service) noteFail(ip string) {
	s.mu.Lock()
	s.fails[ip] = append(s.fails[ip], time.Now())
	s.mu.Unlock()
}

var ErrRateLimited = errors.New("rate limited")

// Confirm consumes a code (exactly once) and returns what the app pins.
func (s *Service) Confirm(ctx context.Context, code, deviceName, ip string) error {
	if s.rateLimited(ip) {
		return ErrRateLimited
	}
	if len(deviceName) > 40 {
		deviceName = deviceName[:40]
	}
	var ok bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `UPDATE pairing_codes SET used_at=?, device_name=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`, storage.Now(), deviceName, hashCode(code), storage.Now())
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		ok = n == 1
		if ok {
			return auth.Audit(ctx, tx, s.siteID, "", "pairing", "pairing.confirm", "device", deviceName, "", "")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !ok {
		s.noteFail(ip)
		return ErrInvalidCode
	}
	return nil
}
