// Package auth implements offline local accounts: argon2id password hashes,
// opaque random session tokens (only their SHA-256 is stored), sliding idle
// expiry with an absolute cap, and a bounded login rate limiter. No cloud
// dependency of any kind.
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

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("too many attempts")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrWeakPassword       = errors.New("password must be at least 12 characters")
	ErrExists             = errors.New("username already exists")
)

const (
	IdleTTL     = 12 * time.Hour
	AbsoluteTTL = 7 * 24 * time.Hour
	MinPassword = 12
)

// argon2id parameters (OWASP "minimum" profile; ~19 MiB, fits the Pi budget).
const (
	argonTime    = 2
	argonMemKiB  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

type User struct {
	ID       string     `json:"id"`
	SiteID   string     `json:"site_id"`
	Username string     `json:"username"`
	Role     roles.Role `json:"role"`
	Disabled bool       `json:"disabled"`
	Created  string     `json:"created_at"`
}

type Service struct {
	db     *storage.Store
	siteID string
	rl     *limiter // per (username, ip)
	rlIP   *limiter // per ip: bounds username-spraying and audit-table growth
	// serialise hashing so concurrent logins cannot blow the memory budget
	hashSem chan struct{}
	dummy   string
}

func New(db *storage.Store, siteID string) *Service {
	s := &Service{db: db, siteID: siteID, rl: newLimiter(5, 5*time.Minute), rlIP: newLimiter(30, 5*time.Minute), hashSem: make(chan struct{}, 2)}
	s.dummy, _ = hashPassword("dummy-password-for-timing")
	return s
}

func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k := argon2.IDKey([]byte(pw), salt, argonTime, argonMemKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k)), nil
}

func verifyPassword(encoded, pw string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 6 || p[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var th uint8
	if _, err := fmt.Sscanf(p[3], "m=%d,t=%d,p=%d", &m, &t, &th); err != nil {
		return false
	}
	// refuse absurd parameters from a tampered database row
	if m > 256*1024 || t > 10 || th > 8 || m == 0 || t == 0 || th == 0 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p[4])
	want, err2 := base64.RawStdEncoding.DecodeString(p[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, th, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Service) hash(pw string) (string, error) {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	return hashPassword(pw)
}

func (s *Service) verify(enc, pw string) bool {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	return verifyPassword(enc, pw)
}

func tokenHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func normUser(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// CreateUser adds a local account. actor is recorded in the audit trail.
func (s *Service) CreateUser(ctx context.Context, username, password string, role roles.Role, actor string) (User, error) {
	username = normUser(username)
	if len(username) < 3 || len(username) > 40 || strings.ContainsAny(username, " \t\n/\\:") {
		return User{}, fmt.Errorf("username must be 3-40 chars without spaces")
	}
	if !roles.Valid(role) {
		return User{}, fmt.Errorf("invalid role")
	}
	if len(password) < MinPassword {
		return User{}, ErrWeakPassword
	}
	h, err := s.hash(password)
	if err != nil {
		return User{}, err
	}
	u := User{ID: NewID(), SiteID: s.siteID, Username: username, Role: role, Created: storage.Now()}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_users(id,site_id,username,password_hash,role,created_at) VALUES (?,?,?,?,?,?)`,
			u.ID, s.siteID, username, h, string(role), u.Created); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrExists
			}
			return err
		}
		return Audit(ctx, tx, s.siteID, actor, "local", "user.create", "user", u.ID, "", fmt.Sprintf(`{"username":%q,"role":%q}`, username, role))
	})
	return u, err
}

// Audit appends to audit_events within the caller's transaction.
func Audit(ctx context.Context, tx *sql.Tx, siteID, actor, source, action, targetType, targetID, prev, next string) error {
	var a, p, n any
	if actor != "" {
		a = actor
	}
	if prev != "" {
		p = prev
	}
	if next != "" {
		n = next
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,site_id,actor_id,actor_source,action,target_type,target_id,previous_json,next_json,occurred_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		NewID(), siteID, a, source, action, targetType, targetID, p, n, storage.Now())
	return err
}

// AuditStandalone records an event that has no other transaction.
func (s *Service) AuditStandalone(ctx context.Context, actor, action, targetType, targetID, next string) {
	_ = s.db.Tx(ctx, func(tx *sql.Tx) error {
		return Audit(ctx, tx, s.siteID, actor, "local", action, targetType, targetID, "", next)
	})
}

func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_users`).Scan(&n)
	return n > 0, err
}

// Login verifies credentials and returns a new opaque session token.
func (s *Service) Login(ctx context.Context, username, password, ip string) (string, User, error) {
	username = normUser(username)
	key := username + "|" + ip
	if !s.rl.allow(key) || !s.rlIP.allow(ip) {
		return "", User{}, ErrRateLimited
	}
	var u User
	var hash string
	var disabled sql.NullString
	var role string
	err := s.db.R.QueryRowContext(ctx, `SELECT id,site_id,username,password_hash,role,disabled_at,created_at FROM local_users WHERE site_id=? AND username=?`,
		s.siteID, username).Scan(&u.ID, &u.SiteID, &u.Username, &hash, &role, &disabled, &u.Created)
	if errors.Is(err, sql.ErrNoRows) {
		s.verify(s.dummy, password) // equalise timing: no username oracle
		s.rl.fail(key)
		s.rlIP.fail(ip)
		s.AuditStandalone(ctx, "", "auth.login_failed", "user", username, "")
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	u.Role, u.Disabled = roles.Role(role), disabled.Valid
	if !s.verify(hash, password) || u.Disabled {
		s.rl.fail(key)
		s.rlIP.fail(ip)
		s.AuditStandalone(ctx, u.ID, "auth.login_failed", "user", u.ID, "")
		return "", User{}, ErrInvalidCredentials
	}
	s.rl.reset(key)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", User{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_sessions(token_hash,user_id,created_at,expires_at,absolute_expires_at) VALUES (?,?,?,?,?)`,
			tokenHash(tok), u.ID, storage.FormatTime(now), storage.FormatTime(now.Add(IdleTTL)), storage.FormatTime(now.Add(AbsoluteTTL))); err != nil {
			return err
		}
		return Audit(ctx, tx, s.siteID, u.ID, "local", "auth.login", "user", u.ID, "", "")
	})
	if err != nil {
		return "", User{}, err
	}
	return tok, u, nil
}

// Authenticate resolves a bearer/cookie token to a user (sliding expiry).
func (s *Service) Authenticate(ctx context.Context, tok string) (User, error) {
	if tok == "" || len(tok) > 128 {
		return User{}, ErrUnauthenticated
	}
	var u User
	var role, exp, abs string
	var revoked, disabled sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT u.id,u.site_id,u.username,u.role,u.disabled_at,u.created_at,s.expires_at,s.absolute_expires_at,s.revoked_at
		FROM local_sessions s JOIN local_users u ON u.id=s.user_id WHERE s.token_hash=?`, tokenHash(tok)).
		Scan(&u.ID, &u.SiteID, &u.Username, &role, &disabled, &u.Created, &exp, &abs, &revoked)
	if err != nil {
		return User{}, ErrUnauthenticated
	}
	now := time.Now()
	if revoked.Valid || disabled.Valid || storage.FormatTime(now) >= exp || storage.FormatTime(now) >= abs || u.SiteID != s.siteID {
		return User{}, ErrUnauthenticated
	}
	u.Role = roles.Role(role)
	// slide the idle window at most every 5 minutes (limits write load)
	if e, err := time.Parse(storage.TimeFormat, exp); err == nil && e.Sub(now) < IdleTTL-5*time.Minute {
		ne := now.Add(IdleTTL)
		if a, err := time.Parse(storage.TimeFormat, abs); err == nil && ne.After(a) {
			ne = a
		}
		_ = s.db.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE local_sessions SET expires_at=? WHERE token_hash=?`, storage.FormatTime(ne), tokenHash(tok))
			return err
		})
	}
	return u, nil
}

func (s *Service) Logout(ctx context.Context, tok string, userID string) {
	_ = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE local_sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL`, storage.Now(), tokenHash(tok)); err != nil {
			return err
		}
		return Audit(ctx, tx, s.siteID, userID, "local", "auth.logout", "user", userID, "", "")
	})
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id,site_id,username,role,disabled_at,created_at FROM local_users WHERE site_id=? ORDER BY username`, s.siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var role string
		var dis sql.NullString
		if err := rows.Scan(&u.ID, &u.SiteID, &u.Username, &role, &dis, &u.Created); err != nil {
			return nil, err
		}
		u.Role, u.Disabled = roles.Role(role), dis.Valid
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser changes role and/or disabled state. The last enabled admin
// cannot be demoted or disabled (no lock-out). Disabling revokes sessions.
func (s *Service) UpdateUser(ctx context.Context, id string, role *roles.Role, disabled *bool, actor string) (User, error) {
	var out User
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var r, dis sql.NullString
		var u User
		err := tx.QueryRowContext(ctx, `SELECT id,site_id,username,role,disabled_at,created_at FROM local_users WHERE id=? AND site_id=?`, id, s.siteID).
			Scan(&u.ID, &u.SiteID, &u.Username, &r, &dis, &u.Created)
		if err != nil {
			return err
		}
		u.Role, u.Disabled = roles.Role(r.String), dis.Valid
		prev := fmt.Sprintf(`{"role":%q,"disabled":%v}`, u.Role, u.Disabled)
		newRole, newDis := u.Role, u.Disabled
		if role != nil {
			if !roles.Valid(*role) {
				return fmt.Errorf("invalid role")
			}
			newRole = *role
		}
		if disabled != nil {
			newDis = *disabled
		}
		if u.Role == roles.Admin && !u.Disabled && (newRole != roles.Admin || newDis) {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM local_users WHERE site_id=? AND role='ADMIN' AND disabled_at IS NULL`, s.siteID).Scan(&n); err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		var dv any
		if newDis {
			dv = storage.Now()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE local_users SET role=?, disabled_at=? WHERE id=?`, string(newRole), dv, id); err != nil {
			return err
		}
		if newDis || newRole != u.Role {
			if _, err := tx.ExecContext(ctx, `UPDATE local_sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, storage.Now(), id); err != nil {
				return err
			}
		}
		u.Role, u.Disabled = newRole, newDis
		out = u
		return Audit(ctx, tx, s.siteID, actor, "local", "user.update", "user", id, prev, fmt.Sprintf(`{"role":%q,"disabled":%v}`, newRole, newDis))
	})
	return out, err
}

var ErrLastAdmin = errors.New("cannot demote or disable the last enabled admin")

// ---------------------------------------------------------- rate limiting

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	m      map[string]*attempt
}

type attempt struct {
	fails int
	first time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, m: map[string]*attempt{}}
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
	return a.fails < l.max
}

func (l *limiter) fail(k string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 10000 { // bounded memory under attack
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
		a = &attempt{first: time.Now()}
		l.m[k] = a
	}
	a.fails++
}

func (l *limiter) reset(k string) { l.mu.Lock(); delete(l.m, k); l.mu.Unlock() }
