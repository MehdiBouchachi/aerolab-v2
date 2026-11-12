// Package testpg starts a throw-away PostgreSQL cluster for tests (unix
// socket only, fsync off). Tests skip when PostgreSQL binaries are absent.
package integration

import (
	"database/sql"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"math/rand"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

var (
	once    sync.Once
	cluster struct {
		dir, bin string
		port     int
		err      error
		asUser   string
	}
)

func findBin() string {
	m, _ := filepath.Glob("/usr/lib/postgresql/*/bin/initdb")
	if len(m) == 0 {
		return ""
	}
	return filepath.Dir(m[len(m)-1])
}

func run(asUser string, name string, args ...string) ([]byte, error) {
	if asUser != "" {
		args = append([]string{"-u", asUser, "--", name}, args...)
		name = "runuser"
	}
	return exec.Command(name, args...).CombinedOutput()
}

func start() {
	cluster.bin = findBin()
	if cluster.bin == "" {
		cluster.err = fmt.Errorf("postgresql binaries not found")
		return
	}
	if os.Geteuid() == 0 { // postgres refuses to run as root
		if _, err := user.Lookup("postgres"); err != nil {
			cluster.err = fmt.Errorf("running as root and no 'postgres' user")
			return
		}
		cluster.asUser = "postgres"
	}
	dir, err := os.MkdirTemp("", "aeropg-")
	if err != nil {
		cluster.err = err
		return
	}
	_ = os.Chmod(dir, 0o755)
	if cluster.asUser != "" {
		u, _ := user.Lookup("postgres")
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		_ = os.Chown(dir, uid, gid)
	}
	cluster.dir = dir
	cluster.port = 20000 + rand.Intn(20000)
	if out, err := run(cluster.asUser, filepath.Join(cluster.bin, "initdb"), "-D", filepath.Join(dir, "data"), "-A", "trust", "-U", "aero", "--no-sync"); err != nil {
		cluster.err = fmt.Errorf("initdb: %v\n%s", err, out)
		return
	}
	opts := fmt.Sprintf("-p %d -k %s -c listen_addresses= -c fsync=off -c synchronous_commit=off -c full_page_writes=off", cluster.port, dir)
	if out, err := run(cluster.asUser, filepath.Join(cluster.bin, "pg_ctl"), "-D", filepath.Join(dir, "data"), "-o", opts, "-l", filepath.Join(dir, "pg.log"), "-w", "-t", "30", "start"); err != nil {
		cluster.err = fmt.Errorf("pg_ctl start: %v\n%s", err, out)
	}
}

func adminDSN() string {
	return fmt.Sprintf("postgres://aero@/postgres?host=%s&port=%d&sslmode=disable", cluster.dir, cluster.port)
}

// pgNew returns a DSN for a fresh empty database.
func pgNew(t testing.TB) string {
	t.Helper()
	once.Do(start)
	if cluster.err != nil {
		t.Skipf("PostgreSQL not available: %v", cluster.err)
	}
	name := fmt.Sprintf("t%d_%d", time.Now().UnixNano()%1e9, rand.Intn(1e6))
	a, err := sql.Open("pgx", adminDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a, err := sql.Open("pgx", adminDSN())
		if err == nil {
			_, _ = a.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
			a.Close()
		}
	})
	return fmt.Sprintf("postgres://aero@/%s?host=%s&port=%d&sslmode=disable", name, cluster.dir, cluster.port)
}

// Stop shuts the cluster down (call from TestMain).
func Stop() {
	if cluster.dir == "" || cluster.err != nil {
		return
	}
	_, _ = run(cluster.asUser, filepath.Join(cluster.bin, "pg_ctl"), "-D", filepath.Join(cluster.dir, "data"), "-m", "immediate", "stop")
	_ = os.RemoveAll(cluster.dir)
}
