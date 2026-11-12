package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
)

func (s *Server) register() {
	s.handle("GET", "/healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	s.handle("GET", "/readyz", func(w http.ResponseWriter, r *http.Request) {
		ok := s.DB.PingContext(r.Context()) == nil
		c := 200
		if !ok {
			c = 503
		}
		writeJSON(w, c, map[string]bool{"database": ok})
	})
	s.handle("POST", "/api/v1/auth/login", s.login)
	s.userRoute("POST", "/api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request, u auth.User) {
		s.Auth.Logout(r.Context(), bearer(r))
		w.WriteHeader(204)
	})
	s.userRoute("GET", "/api/v1/session", s.session)
	s.userRoute("GET", "/api/v1/sites", s.listSites)

	s.siteRoute("GET", "/api/v1/sites/{siteId}/summary", auth.Viewer, s.summary)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/units", auth.Viewer, s.listUnits)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/units/{unitId}", auth.Viewer, s.getUnit)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/units/{unitId}/readings/latest", auth.Viewer, s.latest)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/units/{unitId}/readings/history", auth.Viewer, s.history)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/units/{unitId}/programs/applied", auth.Viewer, s.applied)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/incidents", auth.Viewer, s.incidents)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/sync-status", auth.Viewer, s.syncStatus)

	s.siteRoute("POST", "/api/v1/sites/{siteId}/remote-program-requests", auth.Admin, s.remoteRequest)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/remote-commands/{commandId}", auth.Viewer, s.getIntent)
	s.siteRoute("GET", "/api/v1/sites/{siteId}/members", auth.Admin, s.members)
	s.siteRoute("PUT", "/api/v1/sites/{siteId}/members/{userId}", auth.Admin, s.putMember)
	s.siteRoute("DELETE", "/api/v1/sites/{siteId}/members/{userId}", auth.Admin, s.delMember)
	s.siteRoute("POST", "/api/v1/sites/{siteId}/edge-credentials/rotate", auth.Admin, s.rotate)

	// edge-authenticated (per-site bearer token)
	s.handle("POST", "/api/v1/edge/sync/batch", s.edgeBatch)
	s.handle("GET", "/api/v1/edge/sync/commands", s.edgeCommands)
	s.handle("POST", "/api/v1/edge/sync/command-results", s.edgeResult)

	s.mux.Handle("/", s.staticHandler())
}

func ipOf(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"` // accepted so the shared web app can reuse its form
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in, 8<<10) {
		return
	}
	email := in.Email
	if email == "" {
		email = in.Username
	}
	tok, u, err := s.Auth.Login(r.Context(), email, in.Password, ipOf(r))
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "300")
		writeErr(w, r, 429, "RATE_LIMITED", "too many failed attempts; try again later", nil)
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeErr(w, r, 401, "UNAUTHORIZED", "invalid email or password", nil)
	case err != nil:
		s.Log.Error("login", "err", err)
		writeErr(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
	default:
		s.audit(r.Context(), "", u.ID, "user", "auth.login", u.ID, nil)
		writeJSON(w, 200, map[string]any{"token": tok, "user": map[string]string{"id": u.ID, "username": u.Email}})
	}
}

type siteRow struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Role       string  `json:"role"`
	LastSyncAt *string `json:"last_sync_at"`
	Stale      bool    `json:"stale"`
	Source     string  `json:"source"`
}

func (s *Server) visibleSites(ctx context.Context, userID string) ([]siteRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id,s.name,m.role,s.last_sync_at FROM sites s JOIN site_members m ON m.site_id=s.id WHERE m.user_id=$1 ORDER BY s.name,s.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []siteRow{}
	for rows.Next() {
		var x siteRow
		var ls sql.NullTime
		if err := rows.Scan(&x.ID, &x.Name, &x.Role, &ls); err != nil {
			return nil, err
		}
		x.Source, x.Stale = "cloud", true
		if ls.Valid {
			t := ls.Time.UTC().Format(time.RFC3339)
			x.LastSyncAt = &t
			x.Stale = time.Since(ls.Time) > s.Cfg.StaleAfter
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

var capsByRole = map[auth.Role][]string{
	// only what the cloud API really implements (no remote acknowledge / crop entry yet)
	auth.Viewer:   {"view_data"},
	auth.Operator: {"view_data"},
	auth.Admin:    {"view_data", "edit_program", "manage_members"},
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, u auth.User) {
	sites, err := s.visibleSites(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var sel *siteRow
	for i := range sites {
		if sites[i].ID == r.URL.Query().Get("site") || (sel == nil && r.URL.Query().Get("site") == "") {
			sel = &sites[i]
			if r.URL.Query().Get("site") != "" {
				break
			}
		}
	}
	out := map[string]any{"user": map[string]any{"id": u.ID, "username": u.Email, "role": nil}, "source": "cloud", "sites": sites,
		"server_time": time.Now().UTC().Format(time.RFC3339), "simulated_data_accepted": true,
		"cloud": map[string]any{"enabled": true}, "capabilities": []string{}}
	if sel != nil {
		out["site"] = map[string]any{"id": sel.ID, "name": sel.Name, "role": sel.Role}
		out["user"].(map[string]any)["role"] = sel.Role
		out["capabilities"] = capsByRole[auth.Role(sel.Role)]
	}
	writeJSON(w, 200, out)
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request, u auth.User) {
	sites, err := s.visibleSites(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sites": sites})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "path", r.URL.Path, "err", err, "request_id", reqID(r))
	writeErr(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
}

// ---- snapshot presentation -------------------------------------------------

type snapshot struct {
	state      sc.State
	observedAt time.Time
	receivedAt time.Time
	have       bool
	stale      bool
}

func (s *Server) loadSnapshot(ctx context.Context, site string) (snapshot, error) {
	var raw []byte
	var sn snapshot
	err := s.DB.QueryRowContext(ctx, `SELECT state,observed_at,received_at FROM latest_site_status WHERE site_id=$1`, site).Scan(&raw, &sn.observedAt, &sn.receivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		sn.stale = true
		return sn, nil
	}
	if err != nil {
		return sn, err
	}
	sn.have = true
	_ = json.Unmarshal(raw, &sn.state)
	var last sql.NullTime
	_ = s.DB.QueryRowContext(ctx, `SELECT last_sync_at FROM sites WHERE id=$1`, site).Scan(&last)
	sn.stale = !last.Valid || time.Since(last.Time) > s.Cfg.StaleAfter
	return sn, nil
}

// present turns the stored edge unit view into what a cloud viewer may be
// told. If the site is not syncing, live-looking fields are replaced by an
// explicit "last known / not proof" state - the cloud cannot measure the LAN.
func present(view []byte, sn snapshot) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(view, &m)
	if m == nil {
		m = map[string]any{}
	}
	m["site_stale"] = sn.stale
	m["snapshot_observed_at"] = sn.observedAt.UTC().Format(time.RFC3339)
	if sn.stale {
		last, _ := m["control_state"].(string)
		m["connection"] = "STALE"
		m["control_state"] = "UNKNOWN_STALE"
		m["pump_commanded"], m["water_delivery"], m["essential_interlocks_ok"] = nil, nil, nil
		m["fault_codes"] = []string{}
		m["control_state_note"] = "The site has not synced since " + sn.receivedAt.UTC().Format("2006-01-02 15:04:05") + " UTC. Last known controller state was " + last + ". This does not prove the pump stopped."
		if lr, ok := m["latest_readings"].(map[string]any); ok {
			lr["outdated"] = true
			if rd, ok := lr["readings"].(map[string]any); ok {
				for k, v := range rd {
					if e, ok := v.(map[string]any); ok {
						e["quality"] = "outdated"
						rd[k] = e
					}
				}
			}
		}
	}
	return m
}

func (s *Server) unitViews(ctx context.Context, site string, sn snapshot, limit, offset int, q string) ([]map[string]any, int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT view FROM units WHERE site_id=$1 AND name ILIKE $2 ORDER BY name,id LIMIT $3 OFFSET $4`, site, "%"+q+"%", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var v []byte
		if err := rows.Scan(&v); err != nil {
			return nil, 0, err
		}
		out = append(out, present(v, sn))
	}
	var total int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM units WHERE site_id=$1 AND name ILIKE $2`, site, "%"+q+"%").Scan(&total)
	return out, total, rows.Err()
}

func atoi(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func (s *Server) listUnits(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	limit, offset := atoi(r.URL.Query().Get("limit"), 50), atoi(r.URL.Query().Get("offset"), 0)
	if limit < 1 || limit > 100 || offset < 0 {
		writeErr(w, r, 400, "VALIDATION_FAILED", "limit must be 1..100", nil)
		return
	}
	sn, err := s.loadSnapshot(r.Context(), site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	units, total, err := s.unitViews(r.Context(), site, sn, limit, offset, r.URL.Query().Get("q"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"units": units, "total": total, "limit": limit, "offset": offset, "observed_at": sn.observedAt.UTC().Format(time.RFC3339), "site_stale": sn.stale, "source": "cloud"})
}

func (s *Server) unitView(ctx context.Context, site, unit string, sn snapshot) (map[string]any, bool, error) {
	var v []byte
	err := s.DB.QueryRowContext(ctx, `SELECT view FROM units WHERE site_id=$1 AND id=$2`, site, unit).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return present(v, sn), true, nil
}

func (s *Server) getUnit(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	sn, err := s.loadSnapshot(r.Context(), site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, ok, err := s.unitView(r.Context(), site, r.PathValue("unitId"), sn)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, r, 404, "NOT_FOUND", "unit not found", nil)
		return
	}
	// a pending remote request for this unit, if any (cloud-side state)
	var pending any
	var in intentView
	if err := scanIntent(s.DB.QueryRowContext(r.Context(), intentSelect+` WHERE site_id=$1 AND unit_id=$2 AND status IN ('PENDING_SITE','PENDING_DEVICE') LIMIT 1`, site, r.PathValue("unitId")), &in); err == nil {
		pending = in
	}
	writeJSON(w, 200, map[string]any{"unit": v, "pending_command": pending, "observed_at": sn.observedAt.UTC().Format(time.RFC3339), "site_stale": sn.stale, "source": "cloud"})
}

func (s *Server) latest(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	sn, _ := s.loadSnapshot(r.Context(), site)
	v, ok, err := s.unitView(r.Context(), site, r.PathValue("unitId"), sn)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, r, 404, "NOT_FOUND", "unit not found", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"unit_id": r.PathValue("unitId"), "snapshot": v["latest_readings"], "observed_at": sn.observedAt.UTC().Format(time.RFC3339), "site_stale": sn.stale})
}

func (s *Server) applied(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	sn, _ := s.loadSnapshot(r.Context(), site)
	v, ok, err := s.unitView(r.Context(), site, r.PathValue("unitId"), sn)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, r, 404, "NOT_FOUND", "unit not found", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"unit_id": r.PathValue("unitId"), "applied": nil, "controller_reported_version": v["applied_program_version"],
		"note": "Programme contents are held on the site; the cloud shows the version the controller last reported."})
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	sn, err := s.loadSnapshot(r.Context(), site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	units, _, err := s.unitViews(r.Context(), site, sn, 100, 0, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cnt := map[string]int{"total": len(units), "healthy": 0, "attention": 0, "stale": 0, "never_seen": 0}
	sim := false
	for _, v := range units {
		sim = sim || v["is_simulated"] == true
		switch v["connection"] {
		case "NEVER_SEEN":
			cnt["never_seen"]++
			cnt["attention"]++
		case "ONLINE":
			if v["control_state"] == "RUNNING" && v["essential_interlocks_ok"] == true {
				cnt["healthy"]++
			} else {
				cnt["attention"]++
			}
		default:
			cnt["stale"]++
			cnt["attention"]++
		}
	}
	var crit, warn int
	rows, _ := s.DB.QueryContext(r.Context(), `SELECT severity,COUNT(*) FROM incidents WHERE site_id=$1 AND status LIKE 'ACTIVE%' GROUP BY 1`, site)
	if rows != nil {
		for rows.Next() {
			var sev string
			var n int
			if rows.Scan(&sev, &n) == nil {
				if sev == "critical" {
					crit = n
				} else if sev == "warning" {
					warn = n
				}
			}
		}
		rows.Close()
	}
	var name string
	var last sql.NullTime
	_ = s.DB.QueryRowContext(r.Context(), `SELECT name,last_sync_at FROM sites WHERE id=$1`, site).Scan(&name, &last)
	var lastS any
	if last.Valid {
		lastS = last.Time.UTC().Format(time.RFC3339)
	}
	writeJSON(w, 200, map[string]any{
		"site": map[string]any{"id": site, "name": name}, "units": cnt,
		"active_incidents":        map[string]int{"critical": crit, "warning": warn, "total": crit + warn},
		"contains_simulated_data": sim,
		// The cloud cannot measure the site LAN or broker: it reports what the
		// edge LAST said, and says when that was. site_wan_reachable is inferred
		// from "did the Pi sync recently", nothing more.
		"connectivity": map[string]any{"local_api_reachable": nil, "site_lan_healthy": nil, "mqtt_broker_connected": sn.state.Connectivity.MQTTBrokerConnected && !sn.stale,
			"site_wan_reachable": !sn.stale, "cloud_api_reachable": true, "last_cloud_sync_at": lastS, "edge_seen_at": lastS,
			"connection_path": "CLOUD", "is_currently_stale": sn.stale, "sync_backlog": sn.state.Connectivity.SyncBacklog},
		"observed_at": sn.observedAt.UTC().Format(time.RFC3339), "received_at": sn.receivedAt.UTC().Format(time.RFC3339), "site_stale": sn.stale, "source": "cloud"})
}

func (s *Server) syncStatus(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	var seen, last sql.NullTime
	var ver sql.NullString
	_ = s.DB.QueryRowContext(r.Context(), `SELECT last_edge_seen_at,last_sync_at,edge_version FROM sites WHERE id=$1`, site).Scan(&seen, &last, &ver)
	var pend int
	_ = s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM remote_command_intents WHERE site_id=$1 AND status IN ('PENDING_SITE','PENDING_DEVICE')`, site).Scan(&pend)
	f := func(t sql.NullTime) any {
		if !t.Valid {
			return nil
		}
		return t.Time.UTC().Format(time.RFC3339)
	}
	stale := !last.Valid || time.Since(last.Time) > s.Cfg.StaleAfter
	writeJSON(w, 200, map[string]any{"last_sync_at": f(last), "last_edge_seen_at": f(seen), "stale": stale, "stale_after_seconds": int(s.Cfg.StaleAfter.Seconds()), "edge_version": ver.String, "pending_remote_commands": pend})
}

func (s *Server) incidents(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	q := r.URL.Query()
	limit, offset := atoi(q.Get("limit"), 50), atoi(q.Get("offset"), 0)
	if limit < 1 || limit > 200 || offset < 0 {
		writeErr(w, r, 400, "VALIDATION_FAILED", "limit must be 1..200", nil)
		return
	}
	where := ` WHERE site_id=$1`
	args := []any{site}
	switch q.Get("state") {
	case "active":
		where += ` AND status LIKE 'ACTIVE%'`
	case "resolved":
		where += ` AND status LIKE 'RESOLVED%'`
	case "", "all":
	default:
		writeErr(w, r, 400, "VALIDATION_FAILED", "state must be active, resolved or all", nil)
		return
	}
	if v := q.Get("unit_id"); v != "" {
		args = append(args, v)
		where += ` AND unit_id=$` + strconv.Itoa(len(args))
	}
	if v := q.Get("severity"); v != "" {
		args = append(args, v)
		where += ` AND severity=$` + strconv.Itoa(len(args))
	}
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT data FROM incidents`+where+` ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, changed_at DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var d []byte
		if rows.Scan(&d) == nil {
			out = append(out, d)
		}
	}
	writeJSON(w, 200, map[string]any{"incidents": out, "observed_at": time.Now().UTC().Format(time.RFC3339), "source": "cloud"})
}

var _ = mc.ValidID
var _ = strings.TrimSpace
