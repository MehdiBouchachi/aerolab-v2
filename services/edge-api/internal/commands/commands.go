// Package commands is the Pi-side command state machine.
//
//	PENDING -> SENT -> APPLIED            (only on an ESP32 confirmation)
//	                -> REJECTED / CONFLICT (device said no)
//	                -> UNKNOWN            (retries exhausted / no result: query, never assume)
//	PENDING -> EXPIRED                    (never reached the device; will never execute)
//
// Rules enforced here: expected_program_version optimistic concurrency, an
// idempotency key, one in-flight change per unit (DB unique index), at most
// 3 sends, session-nonce-bound commands, no retained commands.
package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

type Status string

const (
	Pending  Status = "PENDING"
	Sent     Status = "SENT"
	Applied  Status = "APPLIED"
	Rejected Status = "REJECTED"
	Conflict Status = "CONFLICT"
	Expired  Status = "EXPIRED"
	Unknown  Status = "UNKNOWN"
)

// Error carries an API error code (spec 12.1).
type Error struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, msg string, details map[string]any) *Error { return &Error{code, msg, details} }

// Publisher is the slice of the MQTT client this package needs.
type Publisher interface {
	Publish(topic string, qos byte, retained bool, payload []byte) error
	Connected() bool
}

type Options struct {
	SiteID     string
	StaleAfter time.Duration
	Retry      time.Duration
	MaxTries   int
	TTL        time.Duration
	SyncOutbox bool
}

type Manager struct {
	db  *storage.Store
	reg *devices.Registry
	pub Publisher
	hub *sse.Hub
	opt Options
	log *slog.Logger

	mu      sync.Mutex
	pending map[string]struct{} // command ids awaiting a result (for state reconciliation)
}

func New(db *storage.Store, reg *devices.Registry, pub Publisher, hub *sse.Hub, opt Options, log *slog.Logger) *Manager {
	return &Manager{db: db, reg: reg, pub: pub, hub: hub, opt: opt, log: log, pending: map[string]struct{}{}}
}

type Command struct {
	ID              string          `json:"id"`
	UnitID          string          `json:"unit_id"`
	DeviceID        string          `json:"device_id"`
	Type            string          `json:"command_type"`
	Status          Status          `json:"status"`
	ReasonCode      *string         `json:"reason_code"`
	ExpectedVersion int             `json:"expected_program_version"`
	NewVersion      *int            `json:"new_program_version"`
	Program         *mc.Program     `json:"program,omitempty"`
	RequestedBy     string          `json:"requested_by"`
	Attempts        int             `json:"attempts"`
	ExpiresAt       string          `json:"expires_at_utc"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	Result          json.RawMessage `json:"result,omitempty"`
	// AppliedConfirmed is true ONLY after the controller confirmed.
	AppliedConfirmed bool   `json:"applied_confirmed"`
	Note             string `json:"note"`
}

const cmdCols = `id,unit_id,device_id,command_type,status,reason_code,expected_program_version,new_program_version,request_json,requested_by,attempts,expires_at_utc,created_at,updated_at,result_json`

func scanCmd(r interface{ Scan(...any) error }) (Command, error) {
	var c Command
	var reason, result sql.NullString
	var nv sql.NullInt64
	var reqJSON, typ, st string
	if err := r.Scan(&c.ID, &c.UnitID, &c.DeviceID, &typ, &st, &reason, &c.ExpectedVersion, &nv, &reqJSON, &c.RequestedBy, &c.Attempts, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt, &result); err != nil {
		return c, err
	}
	c.Type, c.Status = typ, Status(st)
	if reason.Valid {
		c.ReasonCode = &reason.String
	}
	if nv.Valid {
		v := int(nv.Int64)
		c.NewVersion = &v
	}
	if result.Valid {
		c.Result = json.RawMessage(result.String)
	}
	var req struct {
		Program *mc.Program `json:"program"`
	}
	_ = json.Unmarshal([]byte(reqJSON), &req)
	c.Program = req.Program
	c.AppliedConfirmed = c.Status == Applied
	switch c.Status {
	case Pending, Sent:
		c.Note = "Waiting for the controller. No confirmation has been received yet; this change is NOT applied."
	case Unknown:
		c.Note = "No confirmation received. The change may or may not have been applied. The controller's actual version is being queried; do not assume either outcome."
	case Expired:
		c.Note = "The request expired before reaching the controller and will never execute."
	case Applied:
		c.Note = "Confirmed by the controller."
	}
	return c, nil
}

func (m *Manager) Get(ctx context.Context, id string) (Command, error) {
	c, err := scanCmd(m.db.R.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=? AND site_id=?`, id, m.opt.SiteID))
	if errors.Is(err, sql.ErrNoRows) {
		return c, errf("NOT_FOUND", "command not found", nil)
	}
	return c, err
}

type ProgramRequest struct {
	CommandID              string     `json:"command_id"`
	ExpectedProgramVersion int        `json:"expected_program_version"`
	Program                mc.Program `json:"program"`
}

// CreateProgramRequest validates and records a programme change. created is
// false when an identical idempotency key was replayed.
func (m *Manager) CreateProgramRequest(ctx context.Context, user auth.User, unitID string, req ProgramRequest) (Command, bool, error) {
	id := req.CommandID
	if id == "" {
		id = "cmd-" + auth.NewID()
	}
	if len(id) < 8 || len(id) > 64 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
		return Command{}, false, errf("VALIDATION_FAILED", "command_id must be 8-64 chars [A-Za-z0-9-]", map[string]any{"field": "command_id"})
	}
	if err := req.Program.Validate(); err != nil {
		var ve *mc.ValidationError
		d := map[string]any{}
		if errors.As(err, &ve) {
			d["field"], d["rule"] = ve.Field, ve.Code
		}
		return Command{}, false, errf("VALIDATION_FAILED", err.Error(), d)
	}
	if req.ExpectedProgramVersion < 0 {
		return Command{}, false, errf("VALIDATION_FAILED", "expected_program_version must be >= 0", map[string]any{"field": "expected_program_version"})
	}

	rt, ok := m.reg.ByUnit(unitID)
	if !ok || rt.Disabled {
		return Command{}, false, errf("NOT_FOUND", "unit not found", nil)
	}
	reqJSON, _ := json.Marshal(map[string]any{"expected_program_version": req.ExpectedProgramVersion, "program": req.Program})

	// idempotent replay of the same command id
	if prev, err := scanCmd(m.db.R.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=?`, id)); err == nil {
		var pj struct {
			E int        `json:"expected_program_version"`
			P mc.Program `json:"program"`
		}
		_ = json.Unmarshal(mustReq(m.db, ctx, id), &pj)
		if prev.UnitID == unitID && pj.E == req.ExpectedProgramVersion && pj.P == req.Program {
			return prev, false, nil
		}
		return Command{}, false, errf("IDEMPOTENCY_CONFLICT", "command_id already used for a different request", nil)
	}

	now := time.Now()
	if rt.Connection(now, m.opt.StaleAfter) != devices.ConnOnline {
		return Command{}, false, errf("DEVICE_OFFLINE", "the controller is not reporting; programme changes need a live controller", map[string]any{"connection": rt.Connection(now, m.opt.StaleAfter)})
	}
	actual := 0
	if rt.State.ProgramVersionApplied != nil {
		actual = *rt.State.ProgramVersionApplied
	}
	if actual != req.ExpectedProgramVersion {
		return Command{}, false, errf("PROGRAM_VERSION_CONFLICT", "the controller has applied a different programme version", map[string]any{"expected_version": req.ExpectedProgramVersion, "actual_version": actual})
	}
	next := actual + 1
	created := storage.FormatTime(now)
	expires := storage.FormatTime(now.Add(m.opt.TTL))
	var cmd Command
	err := m.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO commands(id,site_id,unit_id,device_id,requested_by,command_type,expected_program_version,new_program_version,expires_at_utc,request_json,status,created_at,updated_at)
			VALUES (?,?,?,?,?,'APPLY_PROGRAM',?,?,?,?,'PENDING',?,?)`, id, m.opt.SiteID, unitID, rt.ID, user.ID, req.ExpectedProgramVersion, next, expires, string(reqJSON), created, created); err != nil {
			if strings.Contains(err.Error(), "ux_commands_one_inflight") || strings.Contains(err.Error(), "UNIQUE constraint failed: commands.unit_id") {
				return errf("COMMAND_PENDING", "another programme change for this unit is still in flight", nil)
			}
			return err
		}
		pj, _ := json.Marshal(req.Program)
		if _, err := tx.ExecContext(ctx, `INSERT INTO programs(unit_id,version,payload_json,created_by,command_id,requested_at,status) VALUES (?,?,?,?,?,?,'REQUESTED')
			ON CONFLICT(unit_id,version) DO UPDATE SET payload_json=excluded.payload_json, created_by=excluded.created_by, command_id=excluded.command_id, requested_at=excluded.requested_at, status='REQUESTED', applied_at=NULL
			WHERE programs.status<>'APPLIED'`, unitID, next, string(pj), user.ID, id, created); err != nil {
			return err
		}
		if err := auth.Audit(ctx, tx, m.opt.SiteID, user.ID, "local", "program.request", "unit", unitID,
			fmt.Sprintf(`{"applied_version":%d}`, actual), fmt.Sprintf(`{"new_version":%d,"command_id":%q}`, next, id)); err != nil {
			return err
		}
		var e error
		cmd, e = scanCmd(tx.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=?`, id))
		return e
	})
	if err != nil {
		return Command{}, false, err
	}
	m.mu.Lock()
	m.pending[id] = struct{}{}
	m.mu.Unlock()
	m.hub.Publish("program.updated", cmd)
	m.trySend(ctx, id) // first attempt immediately; the loop handles retries
	cmd, _ = m.Get(ctx, id)
	return cmd, true, nil
}

func mustReq(db *storage.Store, ctx context.Context, id string) []byte {
	var s string
	_ = db.R.QueryRowContext(ctx, `SELECT request_json FROM commands WHERE id=?`, id).Scan(&s)
	return []byte(s)
}

// trySend sends one attempt if the device is online and attempts remain.
func (m *Manager) trySend(ctx context.Context, id string) {
	c, err := m.Get(ctx, id)
	if err != nil || (c.Status != Pending && c.Status != Sent) {
		return
	}
	rt, ok := m.reg.Lookup(c.DeviceID)
	now := time.Now()
	if !ok || rt.Connection(now, m.opt.StaleAfter) != devices.ConnOnline || !m.pub.Connected() {
		return // not counted as an attempt; expiry still applies
	}
	exp, _ := time.Parse(storage.TimeFormat, c.ExpiresAt)
	remain := int(time.Until(exp).Seconds())
	if remain < 1 {
		return
	}
	if remain > 300 {
		remain = 300
	}
	created, _ := time.Parse(storage.TimeFormat, c.CreatedAt)
	wire := mc.Command{SchemaVersion: 1, CommandID: c.ID, SiteID: m.opt.SiteID, DeviceID: c.DeviceID, UnitID: c.UnitID, CommandType: mc.CmdApplyProgram,
		SessionNonce: rt.State.SessionNonce, ExpectedProgramVersion: c.ExpectedVersion, IssuedAtUTC: created.UTC().Format(time.RFC3339),
		ExpiresAtUTC: exp.UTC().Format(time.RFC3339), TTLSeconds: remain, Program: c.Program}
	if c.NewVersion != nil {
		wire.NewProgramVersion = *c.NewVersion
	}
	if err := wire.Validate(); err != nil {
		m.log.Error("refusing to send invalid command", "id", id, "err", err)
		return
	}
	b, _ := json.Marshal(wire)
	if err := m.pub.Publish(mc.DeviceTopic(c.DeviceID, mc.TopicCommands), 1, false, b); err != nil { // never retained
		m.log.Warn("command publish failed", "id", id, "err", err)
		return
	}
	_, _ = m.db.W.ExecContext(ctx, `UPDATE commands SET status='SENT', attempts=attempts+1, last_sent_at=?, updated_at=? WHERE id=? AND status IN ('PENDING','SENT')`, storage.Now(), storage.Now(), id)
}

// Run drives retries, expiry and UNKNOWN transitions until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sweep(ctx)
		}
	}
}

func (m *Manager) sweep(ctx context.Context) {
	rows, err := m.db.R.QueryContext(ctx, `SELECT id,status,attempts,COALESCE(last_sent_at,''),expires_at_utc FROM commands WHERE site_id=? AND status IN ('PENDING','SENT')`, m.opt.SiteID)
	if err != nil {
		return
	}
	type row struct {
		id, status, lastSent, expires string
		attempts                      int
	}
	var rs []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.status, &r.attempts, &r.lastSent, &r.expires) == nil {
			rs = append(rs, r)
		}
	}
	rows.Close()
	now := time.Now()
	for _, r := range rs {
		exp, _ := time.Parse(storage.TimeFormat, r.expires)
		last, _ := time.Parse(storage.TimeFormat, r.lastSent)
		switch {
		case now.After(exp):
			if r.attempts == 0 {
				m.finish(ctx, r.id, Expired, "EXPIRED_BEFORE_DELIVERY", nil)
			} else {
				m.finish(ctx, r.id, Unknown, "EXPIRED_NO_RESULT", nil)
			}
		case r.attempts >= m.opt.MaxTries:
			if now.Sub(last) >= m.opt.Retry {
				m.finish(ctx, r.id, Unknown, "NO_RESULT_AFTER_RETRIES", nil)
			}
		case r.attempts == 0 || now.Sub(last) >= m.opt.Retry:
			m.trySend(ctx, r.id)
		}
	}
}

// finish moves a command to a terminal-ish state and mirrors it into the
// programme table. Idempotent: only non-final commands change.
func (m *Manager) finish(ctx context.Context, id string, st Status, reason string, result []byte) {
	var out Command
	changed := false
	err := m.db.Tx(ctx, func(tx *sql.Tx) error {
		var e error
		out, changed, e = finishTx(ctx, tx, m.opt, id, st, reason, result)
		return e
	})
	if err != nil {
		m.log.Error("finish command", "id", id, "err", err)
		return
	}
	m.afterChange(out, changed)
}

func (m *Manager) afterChange(c Command, changed bool) {
	if !changed {
		return
	}
	if c.Status != Pending && c.Status != Sent {
		m.mu.Lock()
		if c.Status != Unknown {
			delete(m.pending, c.ID)
		}
		m.mu.Unlock()
	}
	m.hub.Publish("program.updated", c)
}

func finishTx(ctx context.Context, tx *sql.Tx, opt Options, id string, st Status, reason string, result []byte) (Command, bool, error) {
	cur, err := scanCmd(tx.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=?`, id))
	if err != nil {
		return Command{}, false, err
	}
	// final states are immutable; UNKNOWN may still be upgraded by a late truth
	if cur.Status != Pending && cur.Status != Sent && cur.Status != Unknown {
		return cur, false, nil
	}
	if cur.Status == Unknown && st == Unknown {
		return cur, false, nil
	}
	now := storage.Now()
	var res any
	if result != nil {
		res = string(result)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE commands SET status=?, reason_code=?, result_json=COALESCE(?,result_json), updated_at=? WHERE id=?`, string(st), nullIf(reason), res, now, id); err != nil {
		return cur, false, err
	}
	if cur.NewVersion != nil {
		switch st {
		case Applied:
			if _, err := tx.ExecContext(ctx, `UPDATE programs SET status='APPLIED', applied_at=? WHERE unit_id=? AND version=?`, now, cur.UnitID, *cur.NewVersion); err != nil {
				return cur, false, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE programs SET status='SUPERSEDED' WHERE unit_id=? AND version<? AND status='APPLIED'`, cur.UnitID, *cur.NewVersion); err != nil {
				return cur, false, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE growing_units SET applied_program_version=? WHERE id=?`, *cur.NewVersion, cur.UnitID); err != nil {
				return cur, false, err
			}
		case Rejected, Conflict:
			_, err = tx.ExecContext(ctx, `UPDATE programs SET status='REJECTED' WHERE unit_id=? AND version=? AND status<>'APPLIED'`, cur.UnitID, *cur.NewVersion)
		case Expired:
			_, err = tx.ExecContext(ctx, `UPDATE programs SET status='EXPIRED' WHERE unit_id=? AND version=? AND status<>'APPLIED'`, cur.UnitID, *cur.NewVersion)
		case Unknown:
			_, err = tx.ExecContext(ctx, `UPDATE programs SET status='UNKNOWN' WHERE unit_id=? AND version=? AND status='REQUESTED'`, cur.UnitID, *cur.NewVersion)
		}
		if err != nil {
			return cur, false, err
		}
	}
	if err := auth.Audit(ctx, tx, opt.SiteID, "", "device", "command."+strings.ToLower(string(st)), "command", id, "", fmt.Sprintf(`{"reason":%q}`, reason)); err != nil {
		return cur, false, err
	}
	if opt.SyncOutbox {
		p, _ := json.Marshal(map[string]any{"command_id": id, "unit_id": cur.UnitID, "status": st, "reason": reason, "at": now})
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO outbox(event_id,event_type,priority,site_id,payload_json,created_at) VALUES (?,?,1,?,?,?)`,
			"cmdres/"+id+"/"+string(st), "command.result", opt.SiteID, string(p), now); err != nil {
			return cur, false, err
		}
	}
	out, err := scanCmd(tx.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=?`, id))
	return out, true, err
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ApplyResult consumes a device command-result inside the writer's
// transaction. The returned func (may be nil) runs after commit.
func (m *Manager) ApplyResult(ctx context.Context, tx *sql.Tx, res mc.CommandResult) (func(), error) {
	cur, err := scanCmd(tx.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=? AND site_id=?`, res.CommandID, m.opt.SiteID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // result for a command we do not know: ignore
	}
	if err != nil {
		return nil, err
	}
	if cur.DeviceID != res.DeviceID {
		return nil, nil // spoofed: a device may only answer its own commands
	}
	st, reason := Unknown, ""
	if res.ReasonCode != nil {
		reason = *res.ReasonCode
	}
	outcome := res.Status
	if outcome == mc.ResultAlreadyProcessed {
		outcome = res.OriginalStatus
		if outcome == "" { // unknown history: only the version tells the truth
			if cur.NewVersion != nil && res.CurrentProgramVersion != nil && *res.CurrentProgramVersion == *cur.NewVersion {
				outcome = mc.ResultApplied
			} else {
				outcome = mc.ResultUnknown
			}
		}
	}
	switch outcome {
	case mc.ResultApplied:
		if cur.NewVersion != nil && (res.CurrentProgramVersion == nil || *res.CurrentProgramVersion != *cur.NewVersion) {
			st, reason = Unknown, "RESULT_VERSION_MISMATCH" // never claim APPLIED on inconsistent evidence
		} else {
			st = Applied
		}
	case mc.ResultRejected:
		st = Rejected
		if reason == "VERSION_CONFLICT" {
			st = Conflict
		}
	default:
		st = Unknown
		if reason == "" {
			reason = "DEVICE_REPORTED_UNKNOWN"
		}
	}
	raw, _ := json.Marshal(res)
	out, changed, err := finishTx(ctx, tx, m.opt, res.CommandID, st, reason, raw)
	if err != nil {
		return nil, err
	}
	return func() { m.afterChange(out, changed) }, nil
}

// HasPending reports whether a command id still awaits a result.
func (m *Manager) HasPending(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pending[id]
	return ok
}

// ReconcileFromState upgrades a SENT/UNKNOWN command to APPLIED when the
// controller's own heartbeat names it as the last applied command at the
// target version. This is the "query actual state before retrying" rule.
func (m *Manager) ReconcileFromState(ctx context.Context, tx *sql.Tx, deviceID string, st mc.State) (func(), error) {
	if st.LastCommandID == nil || st.ProgramVersionApplied == nil || !m.HasPending(*st.LastCommandID) {
		return nil, nil
	}
	cur, err := scanCmd(tx.QueryRowContext(ctx, `SELECT `+cmdCols+` FROM commands WHERE id=? AND device_id=?`, *st.LastCommandID, deviceID))
	if err != nil {
		return nil, nil
	}
	if cur.NewVersion == nil || *cur.NewVersion != *st.ProgramVersionApplied {
		return nil, nil
	}
	out, changed, err := finishTx(ctx, tx, m.opt, cur.ID, Applied, "RECONCILED_FROM_STATE", nil)
	if err != nil {
		return nil, err
	}
	return func() { m.afterChange(out, changed) }, nil
}

// LoadPending restores the in-memory pending set after a restart.
func (m *Manager) LoadPending(ctx context.Context) error {
	rows, err := m.db.R.QueryContext(ctx, `SELECT id FROM commands WHERE site_id=? AND status IN ('PENDING','SENT','UNKNOWN')`, m.opt.SiteID)
	if err != nil {
		return err
	}
	defer rows.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		m.pending[id] = struct{}{}
	}
	return rows.Err()
}
