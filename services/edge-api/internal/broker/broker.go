// Package broker lets the edge issue and revoke controller credentials on the local Mosquitto broker
// without anyone editing files by hand, so a grower can add a unit from the dashboard.
//
// The broker keeps using its ordinary password_file + pattern ACL (topics are scoped by %u, the
// controller's own id), so no ACL change is needed per device. The edge rewrites only the lines of
// devices it manages (devices.broker_password_hash IS NOT NULL); every other line, such as the edge's
// own account or installer-provisioned devices, is preserved byte for byte. The file is replaced
// atomically with the previous owner and mode, then the broker is asked to reload:
//   - AEROLAB_BROKER_PID_FILE set: SIGHUP to that pid (development, tests, containers);
//   - otherwise the systemd path unit in infrastructure/raspberry-pi watches the file and runs
//     `systemctl reload mosquitto`, so the edge needs no privileges over the broker.
//
// Mosquitto does not drop a client that is already connected when its password is removed; the edge
// additionally ignores everything from a retired device (registry.Disabled), so a retired controller
// can no longer change anything the site stores.
package broker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ErrNotConfigured means the edge was not told where the broker's password file is, so it cannot
// create controller credentials (the installer must do it, as before).
var ErrNotConfigured = errors.New("broker credential management is not configured (AEROLAB_BROKER_PASSWD_FILE)")

type Manager struct {
	PasswdFile string
	PIDFile    string
	mu         sync.Mutex
}

func (m *Manager) Enabled() bool { return m != nil && m.PasswdFile != "" }

// NewPassword returns a random 24-byte secret, URL-safe so it survives QR codes and copy/paste.
func NewPassword() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hash returns a Mosquitto 2.x "$7$" hash (PBKDF2-HMAC-SHA512, 12-byte salt), the format
// mosquitto_passwd writes. Verified against a real broker by the integration tests.
func Hash(password string) (string, error) {
	salt := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const iter = 101
	dk, err := pbkdf2.Key(sha512.New, password, salt, iter, 64)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$7$%d$%s$%s", iter, base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(dk)), nil
}

type entry struct{ user, hash string }

// Sync rewrites the password file from the database: active managed devices get their stored hash,
// retired ones are removed, everything else is kept. It then asks the broker to reload.
func (m *Manager) Sync(ctx context.Context, db *sql.DB) error {
	if !m.Enabled() {
		return ErrNotConfigured
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := db.QueryContext(ctx, `SELECT id, broker_password_hash, disabled_at IS NOT NULL FROM devices WHERE broker_password_hash IS NOT NULL`)
	if err != nil {
		return err
	}
	managed := map[string]bool{}
	var active []entry
	for rows.Next() {
		var e entry
		var disabled bool
		if err := rows.Scan(&e.user, &e.hash, &disabled); err != nil {
			rows.Close()
			return err
		}
		managed[e.user] = true
		if !disabled {
			active = append(active, e)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Slice(active, func(i, j int) bool { return active[i].user < active[j].user })

	old, err := os.ReadFile(m.PasswdFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(old))
	for sc.Scan() {
		line := sc.Text()
		user, _, ok := strings.Cut(line, ":")
		if ok && managed[user] {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	for _, e := range active {
		fmt.Fprintf(&out, "%s:%s\n", e.user, e.hash)
	}
	if bytes.Equal(out.Bytes(), old) {
		return nil
	}
	if err := atomicReplace(m.PasswdFile, out.Bytes()); err != nil {
		return err
	}
	return m.reload()
}

// atomicReplace writes next to path and renames over it, keeping the old file's owner and mode so the
// broker (which usually runs as another user) can still read it.
func atomicReplace(path string, data []byte) error {
	mode := os.FileMode(0o640)
	uid, gid := -1, -1
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(sys.Uid), int(sys.Gid)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aerolab-passwd-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if uid >= 0 && (uid != os.Geteuid() || gid != os.Getegid()) {
		if err := os.Chown(tmp.Name(), uid, gid); err != nil {
			return fmt.Errorf("keep password file ownership: %w", err)
		}
	}
	return os.Rename(tmp.Name(), path)
}

func (m *Manager) reload() error {
	if m.PIDFile == "" {
		return nil // systemd path unit reloads the broker when the file changes
	}
	b, err := os.ReadFile(m.PIDFile)
	if err != nil {
		return fmt.Errorf("broker pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return fmt.Errorf("broker pid file: bad pid %q", strings.TrimSpace(string(b)))
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}
