package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/ingest"
)

type intentView struct {
	ID                     string          `json:"id"`
	UnitID                 string          `json:"unit_id"`
	Status                 string          `json:"status"`
	Reason                 *string         `json:"reason_code"`
	ExpectedProgramVersion int             `json:"expected_program_version"`
	Program                json.RawMessage `json:"program"`
	RequestedBy            string          `json:"requested_by"`
	AppliedVersion         *int            `json:"applied_version"`
	CreatedAt              string          `json:"created_at"`
	ExpiresAt              string          `json:"expires_at"`
	UpdatedAt              string          `json:"updated_at"`
	AppliedConfirmed       bool            `json:"applied_confirmed"`
	Note                   string          `json:"note"`
}

const intentSelect = `SELECT id,unit_id,status,reason,expected_program_version,program,requested_by,applied_version,created_at,expires_at,updated_at FROM remote_command_intents`

func scanIntent(row interface{ Scan(...any) error }, v *intentView) error {
	var reason sql.NullString
	var applied sql.NullInt64
	var c, e, u time.Time
	var prog []byte
	if err := row.Scan(&v.ID, &v.UnitID, &v.Status, &reason, &v.ExpectedProgramVersion, &prog, &v.RequestedBy, &applied, &c, &e, &u); err != nil {
		return err
	}
	v.Program = prog
	if reason.Valid {
		v.Reason = &reason.String
	}
	if applied.Valid {
		a := int(applied.Int64)
		v.AppliedVersion = &a
	}
	f := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	v.CreatedAt, v.ExpiresAt, v.UpdatedAt = f(c), f(e), f(u)
	v.AppliedConfirmed = v.Status == sc.IntentApplied
	switch v.Status {
	case sc.IntentPendingSite:
		v.Note = "Stored in the cloud. The site has not picked it up yet; NOTHING has changed on the controller. It expires and will never run late."
	case sc.IntentPendingDevice:
		v.Note = "The site accepted it and is waiting for the controller. Not applied until the controller confirms."
	case sc.IntentUnknown:
		v.Note = "No confirmation received. It may or may not have been applied; check the unit's applied version."
	case sc.IntentExpired:
		v.Note = "Expired before it reached the controller. It will not run."
	case sc.IntentApplied:
		v.Note = "Confirmed by the controller."
	}
	return nil
}

// remoteRequest stores an INTENT. It never talks to a device and never says APPLIED.
func (s *Server) remoteRequest(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	var in struct {
		CommandID              string     `json:"command_id"`
		UnitID                 string     `json:"unit_id"`
		ExpectedProgramVersion int        `json:"expected_program_version"`
		Program                mc.Program `json:"program"`
	}
	if !decode(w, r, &in, 16<<10) {
		return
	}
	if in.CommandID == "" {
		in.CommandID = "cmd-" + auth.NewID()
	}
	if len(in.CommandID) < 8 || len(in.CommandID) > 64 || strings.Trim(in.CommandID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
		writeErr(w, r, 400, "VALIDATION_FAILED", "command_id must be 8-64 chars [A-Za-z0-9-]", nil)
		return
	}
	if err := in.Program.Validate(); err != nil {
		writeErr(w, r, 400, "VALIDATION_FAILED", err.Error(), nil)
		return
	}
	if in.ExpectedProgramVersion < 0 {
		writeErr(w, r, 400, "VALIDATION_FAILED", "expected_program_version must be >= 0", nil)
		return
	}
	var exists bool
	_ = s.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM units WHERE site_id=$1 AND id=$2)`, site, in.UnitID).Scan(&exists)
	if !exists {
		writeErr(w, r, 404, "NOT_FOUND", "unit not found at this site", nil)
		return
	}
	pj, _ := json.Marshal(in.Program)
	expires := time.Now().Add(s.Cfg.IntentTTL)

	// idempotent replay
	var prev intentView
	if err := scanIntent(s.DB.QueryRowContext(r.Context(), intentSelect+` WHERE id=$1`, in.CommandID), &prev); err == nil {
		if prev.UnitID == in.UnitID && prev.ExpectedProgramVersion == in.ExpectedProgramVersion && string(normJSON(prev.Program)) == string(normJSON(pj)) {
			writeJSON(w, 200, prev)
			return
		}
		writeErr(w, r, 409, "IDEMPOTENCY_CONFLICT", "command_id already used for a different request", nil)
		return
	}
	_, err := s.DB.ExecContext(r.Context(), `INSERT INTO remote_command_intents(id,site_id,unit_id,requested_by,expected_program_version,program,status,expires_at) VALUES ($1,$2,$3,$4,$5,$6,'PENDING_SITE',$7)`,
		in.CommandID, site, in.UnitID, u.ID, in.ExpectedProgramVersion, pj, expires)
	if err != nil {
		if strings.Contains(err.Error(), "ux_intent_one_inflight") {
			writeErr(w, r, 409, "COMMAND_PENDING", "another remote change for this unit is still in flight", nil)
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r.Context(), site, u.ID, "user", "remote.program_request", in.CommandID, map[string]any{"unit": in.UnitID, "expected": in.ExpectedProgramVersion})
	var out intentView
	if err := scanIntent(s.DB.QueryRowContext(r.Context(), intentSelect+` WHERE id=$1`, in.CommandID), &out); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/sites/"+site+"/remote-commands/"+in.CommandID)
	writeJSON(w, 202, out)
}

func normJSON(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	o, _ := json.Marshal(v)
	return o
}

func (s *Server) getIntent(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	var v intentView
	err := scanIntent(s.DB.QueryRowContext(r.Context(), intentSelect+` WHERE id=$1 AND site_id=$2`, r.PathValue("commandId"), site), &v)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, r, 404, "NOT_FOUND", "command not found", nil)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

// ExpireIntents marks never-picked-up requests past their expiry as EXPIRED so
// they can never execute late. Called by a background sweeper and before each fetch.
func ExpireIntents(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `UPDATE remote_command_intents SET status='EXPIRED', reason='EXPIRED_BEFORE_PICKUP', updated_at=now() WHERE status='PENDING_SITE' AND expires_at<=now()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------- edge API

func (s *Server) edgeSite(w http.ResponseWriter, r *http.Request) (string, bool) {
	site, err := s.Auth.EdgeSite(r.Context(), bearer(r))
	if err != nil {
		writeErr(w, r, 401, "UNAUTHORIZED", "invalid edge credential", nil)
		return "", false
	}
	return site, true
}

func (s *Server) edgeBatch(w http.ResponseWriter, r *http.Request) {
	site, ok := s.edgeSite(w, r)
	if !ok {
		return
	}
	var b sc.Batch
	if !decode(w, r, &b, sc.MaxBatchBytes) {
		return
	}
	if err := b.Validate(); err != nil {
		writeErr(w, r, 400, "VALIDATION_FAILED", err.Error(), nil)
		return
	}
	if b.SiteID != site { // the body is never trusted to name the tenant
		s.audit(r.Context(), site, "", "edge", "authz.denied", "batch for "+b.SiteID, nil)
		writeErr(w, r, 403, "FORBIDDEN", "this credential is not valid for that site", nil)
		return
	}
	resp, err := ingest.Apply(r.Context(), s.DB, site, b)
	if err != nil {
		s.Log.Error("ingest", "site", site, "err", err)
		writeErr(w, r, 500, "INTERNAL_ERROR", "ingest failed; resend the same batch", nil)
		return
	}
	writeJSON(w, 200, resp)
}

func (s *Server) edgeCommands(w http.ResponseWriter, r *http.Request) {
	site, ok := s.edgeSite(w, r)
	if !ok {
		return
	}
	_, _ = ExpireIntents(r.Context(), s.DB)
	_, _ = s.DB.ExecContext(r.Context(), `UPDATE sites SET last_edge_seen_at=now() WHERE id=$1`, site)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id,unit_id,expected_program_version,program,requested_by,created_at,expires_at FROM remote_command_intents WHERE site_id=$1 AND status='PENDING_SITE' AND expires_at>now() ORDER BY created_at LIMIT 20`, site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	list := sc.IntentList{Intents: []sc.Intent{}, ServerTime: time.Now().UTC().Format(time.RFC3339Nano)}
	for rows.Next() {
		var i sc.Intent
		var prog []byte
		var c, e time.Time
		var by string
		if err := rows.Scan(&i.ID, &i.UnitID, &i.ExpectedProgramVersion, &prog, &by, &c, &e); err != nil {
			s.fail(w, r, err)
			return
		}
		_ = json.Unmarshal(prog, &i.Program)
		var email string
		_ = s.DB.QueryRowContext(r.Context(), `SELECT email FROM cloud_users WHERE id=$1`, by).Scan(&email)
		i.RequestedBy, i.CreatedAt, i.ExpiresAt = email, c.UTC().Format(time.RFC3339), e.UTC().Format(time.RFC3339)
		list.Intents = append(list.Intents, i)
	}
	writeJSON(w, 200, list)
}

func (s *Server) edgeResult(w http.ResponseWriter, r *http.Request) {
	site, ok := s.edgeSite(w, r)
	if !ok {
		return
	}
	var in sc.IntentResult
	if !decode(w, r, &in, 8<<10) {
		return
	}
	if err := in.Validate(); err != nil {
		writeErr(w, r, 400, "VALIDATION_FAILED", err.Error(), nil)
		return
	}
	var applied any
	if in.AppliedVersion != nil {
		applied = *in.AppliedVersion
	}
	// final states are immutable; UNKNOWN and PENDING_* may still move forward.
	res, err := s.DB.ExecContext(r.Context(), `UPDATE remote_command_intents SET status=$3, reason=NULLIF($4,''), applied_version=COALESCE($5,applied_version), updated_at=now()
		WHERE id=$1 AND site_id=$2 AND status IN ('PENDING_SITE','PENDING_DEVICE','UNKNOWN')`, in.CommandID, site, in.Status, in.Reason, applied)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists bool
		_ = s.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM remote_command_intents WHERE id=$1 AND site_id=$2)`, in.CommandID, site).Scan(&exists)
		if !exists {
			writeErr(w, r, 404, "NOT_FOUND", "unknown command for this site", nil)
			return
		}
		// already in a final state: acknowledge so the Pi stops retrying it
	}
	s.audit(r.Context(), site, "", "edge", "remote.result", in.CommandID, map[string]any{"status": in.Status})
	w.WriteHeader(204)
}
