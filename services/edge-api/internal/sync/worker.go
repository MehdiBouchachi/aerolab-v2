package sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	gosync "sync"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// CloudStatus is what the UI may say about the cloud link. It is observed by
// actually talking to the cloud API - never inferred from Wi-Fi or DNS.
type CloudStatus struct {
	Enabled             bool    `json:"enabled"`
	Reachable           *bool   `json:"reachable"` // nil until the first attempt
	LastSyncAt          *string `json:"last_sync_at"`
	LastError           string  `json:"last_error,omitempty"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	Backlog             int     `json:"backlog"`
	RemotePrograms      bool    `json:"remote_programs_accepted"`
	RejectedItems       int64   `json:"rejected_items_dropped"`
}

type Options struct {
	SiteID      string
	URL, Token  string
	Interval    time.Duration
	EdgeVersion string
	// RemotePrograms: local opt-in for cloud-originated programme REQUESTS.
	RemotePrograms bool
}

// Worker is the outbound sync loop. It is isolated from MQTT handling: it has
// its own goroutine, its own HTTP client with hard timeouts, bounded retries
// with jittered exponential backoff, and it can never block the ingest path.
type Worker struct {
	opt    Options
	db     *storage.Store
	cmds   *commands.Manager
	state  func(ctx context.Context) (sc.State, error)
	client *http.Client
	log    *slog.Logger

	mu     gosync.Mutex
	status CloudStatus
	kick   chan struct{}
}

func NewWorker(opt Options, db *storage.Store, cmds *commands.Manager, state func(context.Context) (sc.State, error), log *slog.Logger) *Worker {
	return &Worker{opt: opt, db: db, cmds: cmds, state: state, log: log, kick: make(chan struct{}, 1),
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		status: CloudStatus{Enabled: true, RemotePrograms: opt.RemotePrograms}}
}

func (w *Worker) SetStateFunc(f func(context.Context) (sc.State, error)) { w.state = f }

func (w *Worker) Status() CloudStatus { w.mu.Lock(); defer w.mu.Unlock(); return w.status }

func (w *Worker) set(f func(s *CloudStatus)) { w.mu.Lock(); f(&w.status); w.mu.Unlock() }

// backoff: 1x,2x,4x... of the interval, capped at 5 minutes, +/-50% jitter so
// a fleet of sites does not stampede the cloud after an outage.
func backoff(base time.Duration, failures int) time.Duration {
	d := base
	for i := 0; i < failures && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return time.Duration(float64(d) * (0.5 + rand.Float64()))
}

func (w *Worker) Run(ctx context.Context) {
	failures := 0
	for {
		more, err := w.cycle(ctx)
		next := w.opt.Interval
		switch {
		case err != nil:
			failures++
			w.set(func(s *CloudStatus) {
				f := false
				s.Reachable, s.LastError, s.ConsecutiveFailures = &f, err.Error(), failures
			})
			next = backoff(w.opt.Interval, failures)
			w.log.Warn("cloud sync failed; local operation is unaffected", "err", err, "retry_in", next.Round(time.Millisecond))
		default:
			if failures > 0 {
				w.log.Info("cloud sync recovered", "after_failures", failures)
			}
			failures = 0
			w.set(func(s *CloudStatus) { t := true; s.Reachable, s.LastError, s.ConsecutiveFailures = &t, "", 0 })
			if more {
				next = 200 * time.Millisecond // drain a backlog in bounded steps
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		case <-time.After(next):
		}
	}
}

type outRow struct {
	id, typ, payload, created string
	pri                       int
}

// cycle: (1) current truth + highest-priority outbox rows, (2) fetch remote
// requests, (3) report remote request progress. Returns whether more outbox
// rows are waiting.
func (w *Worker) cycle(ctx context.Context) (bool, error) {
	rows, err := w.pending(ctx)
	if err != nil {
		return false, err
	}
	st, err := w.state(ctx)
	if err != nil {
		return false, fmt.Errorf("build state: %w", err)
	}
	backlog := w.backlog(ctx)
	st.Connectivity.SyncBacklog = backlog
	b := sc.Batch{SchemaVersion: sc.SchemaVersion, BatchID: "b-" + auth.NewID(), SiteID: w.opt.SiteID, SentAt: time.Now().UTC().Format(time.RFC3339), EdgeVersion: w.opt.EdgeVersion, State: &st, Events: []sc.Event{}}
	for _, r := range rows {
		// created_at is when THIS edge recorded the item, not when it is uploaded
		b.Events = append(b.Events, sc.Event{EventID: r.id, EventType: r.typ, Priority: r.pri, CreatedAt: r.created, Payload: json.RawMessage(r.payload)})
	}
	var ack sc.AckResponse
	if err := w.call(ctx, "POST", "/api/v1/edge/sync/batch", b, &ack); err != nil {
		w.bumpAttempts(ctx, rows)
		return false, err
	}
	if err := w.markAcked(ctx, ack); err != nil {
		return false, err
	}
	now := storage.Now()
	w.set(func(s *CloudStatus) { s.LastSyncAt = &now; s.Backlog = backlog - len(ack.Acked) - len(ack.Rejected) })
	_, _ = w.db.W.ExecContext(ctx, `UPDATE sites SET cloud_linked=1,last_cloud_sync_at=? WHERE id=?`, now, w.opt.SiteID)

	if err := w.fetchIntents(ctx); err != nil {
		return false, err
	}
	if err := w.reportResults(ctx); err != nil {
		return false, err
	}
	return len(rows) == sc.MaxBatchEvents, nil
}

func (w *Worker) pending(ctx context.Context) ([]outRow, error) {
	// strict priority: P0 current/critical first, historic telemetry (P3) last
	q, err := w.db.R.QueryContext(ctx, `SELECT event_id,event_type,priority,payload_json,created_at FROM outbox WHERE cloud_ack_at IS NULL ORDER BY priority ASC, created_at ASC LIMIT ?`, sc.MaxBatchEvents)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []outRow
	size := 0
	for q.Next() {
		var r outRow
		if err := q.Scan(&r.id, &r.typ, &r.pri, &r.payload, &r.created); err != nil {
			return nil, err
		}
		if size += len(r.payload); size > sc.MaxBatchBytes/2 && len(out) > 0 {
			break // keep the request body bounded
		}
		out = append(out, r)
	}
	return out, q.Err()
}

func (w *Worker) backlog(ctx context.Context) (n int) {
	_ = w.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE cloud_ack_at IS NULL`).Scan(&n)
	return
}

func (w *Worker) bumpAttempts(ctx context.Context, rows []outRow) {
	for _, r := range rows {
		_, _ = w.db.W.ExecContext(ctx, `UPDATE outbox SET attempts=attempts+1,last_attempt_at=? WHERE event_id=?`, storage.Now(), r.id)
	}
}

// markAcked records the cloud's acknowledgement in SQLite. Rejected (poisoned)
// items are retired too, otherwise they would be resent forever.
func (w *Worker) markAcked(ctx context.Context, ack sc.AckResponse) error {
	ids := append([]string(nil), ack.Acked...)
	for id, reason := range ack.Rejected {
		w.log.Warn("cloud rejected an outbox item; retiring it", "event", id, "reason", reason)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	w.set(func(s *CloudStatus) { s.RejectedItems += int64(len(ack.Rejected)) })
	return w.db.Tx(ctx, func(tx *sql.Tx) error {
		now := storage.Now()
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE outbox SET cloud_ack_at=? WHERE event_id=? AND cloud_ack_at IS NULL`, now, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// call performs one authenticated JSON request. The token is never logged.
func (w *Worker) call(ctx context.Context, method, path string, in, out any) error {
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(w.opt.URL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.opt.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("cloud unreachable: %s", scrub(err.Error(), w.opt.Token))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == 204:
		return nil
	case resp.StatusCode == 401:
		return errors.New("cloud rejected the edge credential (401); rotate/reconfigure AEROLAB_CLOUD_TOKEN")
	case resp.StatusCode >= 300:
		return fmt.Errorf("cloud returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("bad cloud response: %w", err)
		}
	}
	return nil
}

func scrub(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// ---------------------------------------------------------------- intents

// fetchIntents pulls remote programme requests and revalidates each one
// LOCALLY. The cloud's claim that a requester is an admin is not trusted: the
// request is accepted only if this site opted in, it has not expired, and the
// normal local checks (bounds, expected version, live controller) pass.
func (w *Worker) fetchIntents(ctx context.Context) error {
	var list sc.IntentList
	if err := w.call(ctx, "GET", "/api/v1/edge/sync/commands", nil, &list); err != nil {
		return err
	}
	for _, in := range list.Intents {
		w.handleIntent(ctx, in)
	}
	return nil
}

func (w *Worker) report(ctx context.Context, id, status, reason string, applied *int) error {
	return w.call(ctx, "POST", "/api/v1/edge/sync/command-results", sc.IntentResult{CommandID: id, Status: status, Reason: reason, AppliedVersion: applied, ObservedAt: time.Now().UTC().Format(time.RFC3339)}, nil)
}

func (w *Worker) handleIntent(ctx context.Context, in sc.Intent) {
	// already known locally (e.g. fetched twice): progress is reported by reportResults
	var known int
	_ = w.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM commands WHERE id=?`, in.ID).Scan(&known)
	if known > 0 {
		return
	}
	reject := func(status, reason string) {
		w.log.Info("remote programme request not accepted", "id", in.ID, "status", status, "reason", reason)
		if err := w.report(ctx, in.ID, status, reason, nil); err != nil {
			w.log.Warn("could not report intent result; will retry next cycle", "id", in.ID, "err", err)
		}
	}
	if !w.opt.RemotePrograms {
		reject(sc.IntentRejected, "REMOTE_CHANGES_DISABLED_ON_SITE")
		return
	}
	if exp, err := time.Parse(time.RFC3339, in.ExpiresAt); err != nil || time.Now().After(exp) {
		reject(sc.IntentExpired, "EXPIRED_BEFORE_PICKUP")
		return
	}
	if in.RequestedBy == "" || len(in.RequestedBy) > 200 {
		reject(sc.IntentRejected, "MISSING_REQUESTER")
		return
	}
	if err := in.Program.Validate(); err != nil {
		reject(sc.IntentRejected, "INVALID_PROGRAM")
		return
	}
	actor := auth.User{ID: "cloud:" + in.RequestedBy, SiteID: w.opt.SiteID, Username: in.RequestedBy, Role: roles.Admin}
	_, _, err := w.cmds.CreateProgramRequest(ctx, actor, in.UnitID, commands.ProgramRequest{CommandID: in.ID, ExpectedProgramVersion: in.ExpectedProgramVersion, Program: mc.Program(in.Program)})
	if err != nil {
		var ce *commands.Error
		if errors.As(err, &ce) {
			switch ce.Code {
			case "PROGRAM_VERSION_CONFLICT":
				reject(sc.IntentConflict, "PROGRAM_VERSION_CONFLICT")
			default:
				reject(sc.IntentRejected, ce.Code)
			}
			return
		}
		w.log.Error("remote request failed locally", "id", in.ID, "err", err)
		return // transient: leave it PENDING_SITE in the cloud; it expires if we never succeed
	}
	// accepted: the local command now exists; reportResults tells the cloud PENDING_DEVICE
}

var cloudStatus = map[commands.Status]string{
	commands.Pending: sc.IntentPendingDevice, commands.Sent: sc.IntentPendingDevice, commands.Applied: sc.IntentApplied,
	commands.Rejected: sc.IntentRejected, commands.Conflict: sc.IntentConflict, commands.Expired: sc.IntentExpired, commands.Unknown: sc.IntentUnknown,
}

// reportResults tells the cloud about every cloud-originated command whose
// local status changed. APPLIED is reported only for a command the ESP32 confirmed.
func (w *Worker) reportResults(ctx context.Context) error {
	rows, err := w.db.R.QueryContext(ctx, `SELECT id,status,COALESCE(reason_code,''),new_program_version FROM commands WHERE requested_by LIKE 'cloud:%' AND (cloud_reported_status IS NULL OR cloud_reported_status<>status) LIMIT 20`)
	if err != nil {
		return err
	}
	type item struct {
		id, status, reason string
		ver                sql.NullInt64
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.status, &it.reason, &it.ver) == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	for _, it := range items {
		cs, ok := cloudStatus[commands.Status(it.status)]
		if !ok {
			continue
		}
		var applied *int
		if cs == sc.IntentApplied && it.ver.Valid {
			v := int(it.ver.Int64)
			applied = &v
		}
		if err := w.report(ctx, it.id, cs, it.reason, applied); err != nil {
			return err
		}
		if _, err := w.db.W.ExecContext(ctx, `UPDATE commands SET cloud_reported_status=? WHERE id=?`, it.status, it.id); err != nil {
			return err
		}
	}
	return nil
}

// Kick asks for an immediate cycle (e.g. after a critical incident).
func (w *Worker) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}
