// Package sync holds the cloud-sync outbox maintenance. In Phase 1 the
// outbox is WRITTEN transactionally (only when cloud sync is enabled) but
// there is no sender yet: the cloud API and the HTTPS sync worker are
// Phase 4. The bounded-retention rules below already apply so a site that is
// "linked but offline for months" cannot fill the SD card.
package sync

import (
	"context"
	"database/sql"
)

// MaxRows bounds the outbox. When exceeded, the lowest-priority (P3
// telemetry), oldest rows go first; P0/P1 incident and command evidence is
// dropped last. Returns how many rows were dropped so callers can surface it.
const MaxRows = 300000

func Trim(ctx context.Context, w *sql.DB) (dropped int64, err error) {
	if _, err = w.ExecContext(ctx, `DELETE FROM outbox WHERE cloud_ack_at IS NOT NULL AND cloud_ack_at < strftime('%Y-%m-%dT%H:%M:%fZ','now','-7 days')`); err != nil {
		return
	}
	var n int64
	if err = w.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox`).Scan(&n); err != nil || n <= MaxRows {
		return
	}
	res, err := w.ExecContext(ctx, `DELETE FROM outbox WHERE event_id IN (SELECT event_id FROM outbox ORDER BY priority DESC, created_at ASC LIMIT ?)`, n-MaxRows)
	if err != nil {
		return
	}
	dropped, _ = res.RowsAffected()
	return
}
