// Package auth: cloud identities, sessions, per-site memberships and per-site
// edge credentials. Cloud and local (Pi) identity are SEPARATE trust domains;
// a cloud token is never accepted by a Pi and vice versa.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("rate limited")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrForbidden          = errors.New("forbidden")
	ErrWeakPassword       = errors.New("password must be at least 12 characters")
)

const SessionTTL = 12 * time.Hour

type Role string

const (
	Admin    Role = "ADMIN"
	Operator Role = "OPERATOR"
	Viewer   Role = "VIEWER"
)

func (r Role) AtLeast(min Role) bool { return rank[r] >= rank[min] && rank[r] > 0 }

var rank = map[Role]int{Viewer: 1, Operator: 2, Admin: 3}

type User struct{ ID, Email string }

type Service struct {
	db      *sql.DB
	rl      *limiter
	rlIP    *limiter
	hashSem chan struct{}
	dummy   string
}

func New(db *sql.DB) *Service {
	s := &Service{db: db, rl: newLimiter(5, 5*time.Minute), rlIP: newLimiter(30, 5*time.Minute), hashSem: make(chan struct{}, 4)}
	s.dummy, _ = hash("dummy-password-for-timing")
	return s
}

func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = (b[6]&0x0f)|0x40, (b[8]&0x3f)|0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func hash(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k := argon2.IDKey([]byte(pw), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", 19*1024, 2, 1, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k)), nil
}

func verify(enc, pw string) bool {
	p := strings.Split(enc, "$")
	if len(p) != 6 || p[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var th uint8
	if _, err := fmt.Sscanf(p[3], "m=%d,t=%d,p=%d", &m, &t, &th); err != nil || m == 0 || m > 256*1024 || t == 0 || t > 10 || th == 0 || th > 8 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[4])
	want, e2 := base64.RawStdEncoding.DecodeString(p[5])
	if e1 != nil || e2 != nil || len(want) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(argon2.IDKey([]byte(pw), salt, t, m, th, uint32(len(want))), want) == 1
}

func (s *Service) h(pw string) (string, error) {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	return hash(pw)
}
func (s *Service) v(e, p string) bool {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	return verify(e, p)
}

func tokHash(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) CreateUser(ctx context.Context, email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 200 {
		return User{}, fmt.Errorf("invalid email")
	}
	if len(password) < 12 {
		return User{}, ErrWeakPassword
	}
	ph, err := s.h(password)
	if err != nil {
		return User{}, err
	}
	u := User{ID: NewID(), Email: email}
	_, err = s.db.ExecContext(ctx, `INSERT INTO cloud_users(id,email,password_hash) VALUES ($1,$2,$3)`, u.ID, email, ph)
	if err != nil && strings.Contains(err.Error(), "duplicate") {
		return User{}, fmt.Errorf("user already exists")
	}
	return u, err
}

func (s *Service) Login(ctx context.Context, email, password, ip string) (string, User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	key := email + "|" + ip
	if !s.rl.allow(key) || !s.rlIP.allow(ip) {
		return "", User{}, ErrRateLimited
	}
	var u User
	var ph string
	var dis sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id,email,password_hash,disabled_at FROM cloud_users WHERE email=$1`, email).Scan(&u.ID, &u.Email, &ph, &dis)
	if errors.Is(err, sql.ErrNoRows) {
		s.v(s.dummy, password)
		s.rl.fail(key)
		s.rlIP.fail(ip)
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	if !s.v(ph, password) || dis.Valid {
		s.rl.fail(key)
		s.rlIP.fail(ip)
		return "", User{}, ErrInvalidCredentials
	}
	s.rl.reset(key)
	tok, err := NewToken()
	if err != nil {
		return "", User{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES ($1,$2,$3)`, tokHash(tok), u.ID, time.Now().Add(SessionTTL))
	return tok, u, err
}

func (s *Service) Authenticate(ctx context.Context, tok string) (User, error) {
	if tok == "" || len(tok) > 128 {
		return User{}, ErrUnauthenticated
	}
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.email FROM sessions s JOIN cloud_users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.disabled_at IS NULL`, tokHash(tok)).Scan(&u.ID, &u.Email)
	if err != nil {
		return User{}, ErrUnauthenticated
	}
	return u, nil
}

func (s *Service) Logout(ctx context.Context, tok string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE token_hash=$1`, tokHash(tok))
}

// SiteRole returns the caller's role at a site. A user with no membership gets
// ("", false): the API answers 404 so site existence is not revealed.
func (s *Service) SiteRole(ctx context.Context, userID, siteID string) (Role, bool) {
	var r string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM site_members WHERE site_id=$1 AND user_id=$2`, siteID, userID).Scan(&r); err != nil {
		return "", false
	}
	return Role(r), true
}

// ----- edge credentials (per site; hashed; rotatable; revocable)

func (s *Service) IssueEdgeToken(ctx context.Context, siteID string, revokeOld bool) (string, error) {
	tok, err := NewToken()
	if err != nil {
		return "", err
	}
	tok = "aeroedge_" + tok
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	if revokeOld {
		if _, err := tx.ExecContext(ctx, `UPDATE site_edge_credentials SET revoked_at=now() WHERE site_id=$1 AND revoked_at IS NULL`, siteID); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_edge_credentials(id,site_id,token_hash) VALUES ($1,$2,$3)`, NewID(), siteID, tokHash(tok)); err != nil {
		return "", err
	}
	return tok, tx.Commit()
}

// EdgeSite resolves a bearer token to the ONE site it may act for.
func (s *Service) EdgeSite(ctx context.Context, tok string) (string, error) {
	if !strings.HasPrefix(tok, "aeroedge_") || len(tok) > 128 {
		return "", ErrUnauthenticated
	}
	var site string
	err := s.db.QueryRowContext(ctx, `SELECT site_id FROM site_edge_credentials WHERE token_hash=$1 AND revoked_at IS NULL`, tokHash(tok)).Scan(&site)
	if err != nil {
		return "", ErrUnauthenticated
	}
	return site, nil
}

// ---- rate limiting (bounded memory)

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	m      map[string]*att
}
type att struct {
	n     int
	first time.Time
}

func newLimiter(max int, w time.Duration) *limiter {
	return &limiter{max: max, window: w, m: map[string]*att{}}
}
func (l *limiter) allow(k string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[k]
	if a == nil {
		return true
	}
	if time.Since(a.first) > l.window {
		delete(l.m, k)
		return true
	}
	return a.n < l.max
}
func (l *limiter) fail(k string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 10000 {
		for kk, a := range l.m {
			if time.Since(a.first) > l.window {
				delete(l.m, kk)
			}
		}
		if len(l.m) > 10000 {
			return
		}
	}
	a := l.m[k]
	if a == nil || time.Since(a.first) > l.window {
		a = &att{first: time.Now()}
		l.m[k] = a
	}
	a.n++
}
func (l *limiter) reset(k string) { l.mu.Lock(); delete(l.m, k); l.mu.Unlock() }
