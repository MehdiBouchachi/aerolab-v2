package api

// Relay: one generic channel so the dashboard served by the cloud offers the same site features as the
// dashboard served by the Pi. The cloud never interprets the call: it checks site membership, stores the
// request and waits for the Pi's answer. The Pi (which only ever connects outwards) long-polls
// /api/v1/edge/relay/next, checks the call against its own allow-list, runs it with the member's role,
// and posts the response. When the site is offline the user gets a clear 503 straight away.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
)

const (
	relayPrefix = "/api/v1/sites/{siteId}/edge/"
	// relayTTL: how long a request may wait for the Pi to pick it up. After that it can never run.
	relayTTL = 8 * time.Second
	// relayWait: how long the browser waits in total (pickup + execution). Below the 30 s write deadline.
	relayWait = 20 * time.Second
	// relayOnline: a site whose Pi has not called in for this long is treated as offline.
	relayOnline = 30 * time.Second
	// relayPoll: how long the Pi's /relay/next call is held open when nothing is waiting.
	relayPoll = 10 * time.Second
	relayTick = 150 * time.Millisecond
)

func (s *Server) registerRelay() {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		s.siteRoute(m, relayPrefix+"{path...}", auth.Viewer, s.relay) // the Pi enforces the per-action role
	}
	s.handle("GET", "/api/v1/edge/relay/next", s.edgeRelayNext)
	s.handle("POST", "/api/v1/edge/relay/results", s.edgeRelayResult)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (s *Server) relay(w http.ResponseWriter, r *http.Request, u auth.User, site string, role auth.Role) {
	path := "/api/v1/" + r.PathValue("path")
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		writeErr(w, r, 400, "VALIDATION_FAILED", "invalid path", nil)
		return
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	var body sql.NullString
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, sc.RelayMaxBody))
		if err != nil {
			writeErr(w, r, 413, "VALIDATION_FAILED", "request body too large", nil)
			return
		}
		if len(b) > 0 {
			if !json.Valid(b) {
				writeErr(w, r, 400, "VALIDATION_FAILED", "body must be JSON", nil)
				return
			}
			body = sql.NullString{String: string(b), Valid: true}
		}
	}
	var seen sql.NullTime
	_ = s.DB.QueryRowContext(r.Context(), `SELECT last_edge_seen_at FROM sites WHERE id=$1`, site).Scan(&seen)
	if !seen.Valid || time.Since(seen.Time) > relayOnline {
		writeErr(w, r, 503, "SITE_OFFLINE", "The site's Raspberry Pi is not connected to the cloud right now, so this is only available on site. Nothing was changed.", nil)
		return
	}
	id := "rly-" + auth.NewID()
	pickup := time.Now().Add(relayTTL)
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO relay_requests(id,site_id,requested_by,role,method,path,body,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, site, u.ID, string(role), r.Method, path, body, pickup); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.audit(r.Context(), site, u.ID, "user", "relay.request", r.Method+" "+path, map[string]any{"relay_id": id})
	}

	deadline := time.Now().Add(relayWait)
	for {
		var st string
		var code sql.NullInt64
		var resp sql.NullString
		if err := s.DB.QueryRowContext(r.Context(), `SELECT status,response_status,response_body FROM relay_requests WHERE id=$1`, id).Scan(&st, &code, &resp); err != nil {
			s.fail(w, r, err)
			return
		}
		switch {
		case st == "DONE":
			// delivered: keep only the metadata (responses can hold one-time setup codes)
			_, _ = s.DB.ExecContext(context.WithoutCancel(r.Context()), `UPDATE relay_requests SET body=NULL, response_body=NULL WHERE id=$1`, id)
			if resp.Valid && resp.String != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(int(code.Int64))
				_, _ = io.WriteString(w, resp.String)
			} else {
				w.WriteHeader(int(code.Int64))
			}
			return
		case st == "PENDING" && time.Now().After(pickup):
			// never picked up: make sure it can never run late, then say so
			res, err := s.DB.ExecContext(r.Context(), `UPDATE relay_requests SET status='EXPIRED' WHERE id=$1 AND status='PENDING'`, id)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if n, _ := res.RowsAffected(); n == 1 {
				writeErr(w, r, 504, "SITE_UNREACHABLE", "The site did not pick this up in time. Nothing was changed; try again.", nil)
				return
			}
			// claimed in the meantime: keep waiting for the answer
		case time.Now().After(deadline):
			writeErr(w, r, 504, "OUTCOME_UNKNOWN", "The site took the request but did not answer in time. It may have been applied; reload to check.", nil)
			return
		}
		if !sleepCtx(r.Context(), relayTick) {
			return
		}
	}
}

// edgeRelayNext hands the Pi the oldest waiting request, holding the call open for up to relayPoll.
func (s *Server) edgeRelayNext(w http.ResponseWriter, r *http.Request) {
	site, ok := s.edgeSite(w, r)
	if !ok {
		return
	}
	_, _ = s.DB.ExecContext(r.Context(), `UPDATE sites SET last_edge_seen_at=now() WHERE id=$1`, site)
	deadline := time.Now().Add(relayPoll)
	for {
		var q sc.RelayRequest
		var body sql.NullString
		var by string
		var exp time.Time
		err := s.DB.QueryRowContext(r.Context(), `UPDATE relay_requests SET status='CLAIMED'
			WHERE id = (SELECT id FROM relay_requests WHERE site_id=$1 AND status='PENDING' AND expires_at>now() ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING id,method,path,body,requested_by,role,expires_at`, site).Scan(&q.ID, &q.Method, &q.Path, &body, &by, &q.Role, &exp)
		if err == nil {
			if body.Valid {
				q.Body = json.RawMessage(body.String)
			}
			_ = s.DB.QueryRowContext(r.Context(), `SELECT email FROM cloud_users WHERE id=$1`, by).Scan(&q.RequestedBy)
			q.ExpiresAt = exp.UTC().Format(time.RFC3339)
			writeJSON(w, 200, q)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			s.fail(w, r, err)
			return
		}
		if time.Now().After(deadline) {
			_, _ = s.DB.ExecContext(r.Context(), `UPDATE sites SET last_edge_seen_at=now() WHERE id=$1`, site)
			w.WriteHeader(204)
			return
		}
		if !sleepCtx(r.Context(), relayTick) {
			return
		}
	}
}

func (s *Server) edgeRelayResult(w http.ResponseWriter, r *http.Request) {
	site, ok := s.edgeSite(w, r)
	if !ok {
		return
	}
	var in sc.RelayResponse
	if !decode(w, r, &in, sc.RelayMaxBody+(4<<10)) {
		return
	}
	if in.ID == "" || in.Status < 100 || in.Status > 599 {
		writeErr(w, r, 400, "VALIDATION_FAILED", "id and a valid HTTP status are required", nil)
		return
	}
	var body sql.NullString
	if len(in.Body) > 0 && string(in.Body) != "null" {
		body = sql.NullString{String: string(in.Body), Valid: true}
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE relay_requests SET status='DONE', response_status=$3, response_body=$4, done_at=now() WHERE id=$1 AND site_id=$2 AND status='CLAIMED'`,
		in.ID, site, in.Status, body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, r, 404, "NOT_FOUND", "no claimed relay request with that id for this site", nil)
		return
	}
	w.WriteHeader(204)
}

// SweepRelay deletes relay rows older than an hour (they are only needed while a browser waits).
func SweepRelay(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM relay_requests WHERE created_at < now() - interval '1 hour'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
