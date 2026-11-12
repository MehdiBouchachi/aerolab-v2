// Package storage owns the SQLite database: one controlled writer
// connection (short transactions, FULL durability) plus a small read pool.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// TimeFormat is fixed-width UTC so that text comparison == time comparison.
const TimeFormat = "2006-01-02T15:04:05.000Z"

func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }
func Now() string                   { return FormatTime(time.Now()) }

// ErrStorageLow is returned when free disk space is below the safety floor.
// The ingest path then refuses to commit, so no durable receipt is issued.
var ErrStorageLow = errors.New("storage: free space below safety floor")

type Store struct {
	W    *sql.DB // single writer connection
	R    *sql.DB // read pool
	Path string

	minFree uint64
}

func dsn(path string, readOnly bool) string {
	q := "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=cache_size(-4000)&_pragma=temp_store(MEMORY)"
	if readOnly {
		q += "&_pragma=query_only(1)"
	} else {
		// FULL: a receipt must mean the data survived a power cut.
		q += "&_pragma=synchronous(FULL)&_pragma=wal_autocheckpoint(1000)"
	}
	return "file:" + path + q
}

// Open opens (creating if needed) the database at path.
func Open(path string, minFreeBytes uint64) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	if err := w.Ping(); err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, err
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(2)
	return &Store{W: w, R: r, Path: path, minFree: minFreeBytes}, nil
}

func (s *Store) Close() error {
	_ = s.R.Close()
	// fold the WAL into the main file for clean backups
	_, _ = s.W.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.W.Close()
}

// Migrate applies embedded *.sql files in lexical order, each in its own
// transaction, recording them in schema_migrations. Idempotent.
func (s *Store) Migrate(ctx context.Context, fsys fs.FS) ([]string, error) {
	if _, err := s.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL) STRICT`); err != nil {
		return nil, err
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, n := range names {
		var x string
		err := s.W.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version=?`, n).Scan(&x)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return applied, err
		}
		body, err := fs.ReadFile(fsys, n)
		if err != nil {
			return applied, err
		}
		tx, err := s.W.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?,?)`, n, Now()); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied = append(applied, strings.TrimSuffix(n, ".sql"))
	}
	return applied, nil
}

// IntegrityCheck runs PRAGMA quick_check (cheap enough for startup).
func (s *Store) IntegrityCheck(ctx context.Context) error {
	var res string
	if err := s.W.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("database integrity check failed: %s", res)
	}
	return nil
}

// Tx runs fn in a write transaction on the single writer connection. It is
// deliberately NOT gated by the free-space floor: logins, acknowledgements
// and commands must keep working so an operator can see and fix a storage
// problem. Only bulk device ingest respects the floor (IngestTx).
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// IngestTx is Tx for bulk telemetry: it refuses to start when free space is
// below the safety floor, so no durable receipt is promised for data that
// would eat the reserve kept for operator actions.
func (s *Store) IngestTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if err := s.CheckFreeSpace(); err != nil {
		return err
	}
	return s.Tx(ctx, fn)
}

// DiskStats reports capacity of the filesystem holding the database.
type DiskStats struct {
	TotalBytes, FreeBytes uint64
	DBBytes, WALBytes     int64
}

func (s *Store) Disk() (DiskStats, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(s.Path), &st); err != nil {
		return DiskStats{}, err
	}
	d := DiskStats{TotalBytes: st.Blocks * uint64(st.Bsize), FreeBytes: st.Bavail * uint64(st.Bsize)}
	if fi, err := os.Stat(s.Path); err == nil {
		d.DBBytes = fi.Size()
	}
	if fi, err := os.Stat(s.Path + "-wal"); err == nil {
		d.WALBytes = fi.Size()
	}
	return d, nil
}

func (s *Store) CheckFreeSpace() error {
	if s.minFree == 0 {
		return nil
	}
	d, err := s.Disk()
	if err != nil {
		return nil // cannot tell; a real write failure is still caught on commit
	}
	if d.FreeBytes < s.minFree {
		return ErrStorageLow
	}
	return nil
}

// Checkpoint truncates the WAL (called from the retention worker).
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.W.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
