// Package db opens PostgreSQL (pgx stdlib) and applies embedded migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(10)
	d.SetMaxIdleConns(4)
	d.SetConnMaxLifetime(30 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := d.PingContext(pctx); err != nil {
		return nil, fmt.Errorf("postgres unreachable: %w", err)
	}
	return d, nil
}

// Migrate applies *.sql in order, each in a transaction, under an advisory
// lock so concurrent starts cannot race. Idempotent.
func Migrate(ctx context.Context, d *sql.DB, fsys fs.FS) ([]string, error) {
	conn, err := d.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(727001)`); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(727001)`) //nolint:errcheck
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, n := range names {
		var exists bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, n).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		body, err := fs.ReadFile(fsys, n)
		if err != nil {
			return applied, err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", n, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, n); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied = append(applied, n)
	}
	return applied, nil
}
