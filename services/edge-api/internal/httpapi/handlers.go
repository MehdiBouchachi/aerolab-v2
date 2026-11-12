package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/incidents"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	edgesync "github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sync"
)

// readingStaleAfter: a snapshot older than 2.5 snapshot periods is outdated.
const readingStaleAfter = 150 * time.Second

func (s *Server) register() {
	// ---- unauthenticated, minimal information
	s.handle("GET", "/healthz", s.healthz)
	s.handle("GET", "/readyz", s.readyz)
	s.handle("POST", "/api/v1/local/auth/login", s.login)

	// ---- session & identity
	s.protected("POST", "/api/v1/local/auth/logout", "", s.logout)
	s.protected("GET", "/api/v1/session", "", s.session)
	s.protected("GET", "/api/v1/local/users", roles.ManageUsers, s.listUsers)
	s.protected("POST", "/api/v1/local/users", roles.ManageUsers, s.createUser)
	s.protected("PATCH", "/api/v1/local/users/{userId}", roles.ManageUsers, s.updateUser)

	// ---- read API
	s.protected("GET", "/api/v1/sites", roles.ViewData, s.listSites)
	s.protected("GET", "/api/v1/sites/{siteId}/summary", roles.ViewData, s.summary)
	s.protected("GET", "/api/v1/sites/{siteId}/units", roles.ViewData, s.listUnits)
	s.protected("GET", "/api/v1/units/{unitId}", roles.ViewData, s.getUnit)
	s.protected("GET", "/api/v1/units/{unitId}/readings/latest", roles.ViewData, s.latestReadings)
	s.protected("GET", "/api/v1/units/{unitId}/readings/history", roles.ViewData, s.history)
	s.protected("GET", "/api/v1/units/{unitId}/programs/applied", roles.ViewData, s.appliedProgram)
	s.protected("GET", "/api/v1/units/{unitId}/programs", roles.ViewData, s.listPrograms)
	s.protected("GET", "/api/v1/sites/{siteId}/incidents", roles.ViewData, s.listIncidents)
	s.protected("GET", "/api/v1/incidents/{incidentId}", roles.ViewData, s.getIncident)
	s.protected("GET", "/api/v1/commands/{commandId}", roles.ViewData, s.getCommand)
	s.protected("GET", "/api/v1/sites/{siteId}/events", roles.ViewData, s.events)

	// ---- writes
	s.protected("POST", "/api/v1/units/{unitId}/program-requests", roles.EditProgram, s.programRequest)
	s.protected("POST", "/api/v1/incidents/{incidentId}/acknowledge", roles.AckIncident, s.ackIncident)
	s.protected("GET", "/api/v1/sites/{siteId}/devices", roles.ManageDevices, s.listDevices)
	s.protected("POST", "/api/v1/sites/{siteId}/devices", roles.ManageDevices, s.provisionDevice)

	// ---- operations (Admin)
	s.protected("GET", "/api/v1/sites/{siteId}/storage", roles.ViewData, s.storageInfo)
	s.protected("GET", "/api/v1/sites/{siteId}/audit", roles.ViewAudit, s.auditLog)
	s.protected("GET", "/api/v1/diagnostics", roles.ViewDiagnostics, s.diag)

	s.mux.Handle("/", s.staticHandler())
}

// ------------------------------------------------------------------ health

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

// readyz exposes only booleans: it tells a supervisor whether the edge can do
// its job, not how the site is configured.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	dbOK := s.DB.R.PingContext(r.Context()) == nil
	st := map[string]any{"database": dbOK, "mqtt_broker": s.MQTT.Connected(), "storage_ok": s.Metrics.StorageOK.Load()}
	code := 200
	if !dbOK {
		code = 503 // the broker may be down; the local UI still works
	}
	writeJSON(w, code, st)
}

// -------------------------------------------------------------------- auth

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	tok, u, err := s.Auth.Login(r.Context(), in.Username, in.Password, clientIP(r))
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "300")
		writeError(w, r, 429, "RATE_LIMITED", "too many failed attempts; try again later", nil)
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, r, 401, "UNAUTHORIZED", "invalid username or password", nil)
		return
	case err != nil:
		s.mapErr(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(auth.IdleTTL.Seconds())})
	writeJSON(w, 200, map[string]any{"token": tok, "user": u, "capabilities": roles.Capabilities(u.Role)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, u auth.User) {
	tok := ""
	if h := r.Header.Get("Authorization"); len(h) > 7 {
		tok = h[7:]
	} else if c, err := r.Cookie(cookieName); err == nil {
		tok = c.Value
	}
	s.Auth.Logout(r.Context(), tok, u.ID)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, u auth.User) {
	writeJSON(w, 200, map[string]any{
		"user": u, "source": "local", "capabilities": roles.Capabilities(u.Role),
		"site":                    map[string]any{"id": s.Cfg.SiteID, "name": s.Cfg.SiteName, "role": u.Role},
		"simulated_data_accepted": s.Cfg.AllowSimulatedData,
		"server_time":             storage.Now(),
		"cloud":                   s.cloudInfo(),
	})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, u auth.User) {
	us, err := s.Auth.ListUsers(r.Context())
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"users": us})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, u auth.User) {
	var in struct {
		Username string     `json:"username"`
		Password string     `json:"password"`
		Role     roles.Role `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	nu, err := s.Auth.CreateUser(r.Context(), in.Username, in.Password, in.Role, u.ID)
	switch {
	case errors.Is(err, auth.ErrExists):
		writeError(w, r, 409, "VALIDATION_FAILED", "username already exists", nil)
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), map[string]any{"field": "password"})
	case err != nil && (nu == auth.User{}):
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), nil)
	default:
		writeJSON(w, 201, nu)
	}
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, u auth.User) {
	var in struct {
		Role     *roles.Role `json:"role"`
		Disabled *bool       `json:"disabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	nu, err := s.Auth.UpdateUser(r.Context(), r.PathValue("userId"), in.Role, in.Disabled, u.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, r, 404, "NOT_FOUND", "user not found", nil)
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, r, 409, "VALIDATION_FAILED", err.Error(), nil)
	case err != nil:
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), nil)
	default:
		writeJSON(w, 200, nu)
	}
}

// ------------------------------------------------------------- sites/units

func (s *Server) listSites(w http.ResponseWriter, r *http.Request, u auth.User) {
	writeJSON(w, 200, map[string]any{"sites": []map[string]any{{"id": s.Cfg.SiteID, "name": s.Cfg.SiteName, "role": u.Role, "source": "local"}}})
}

type unitView struct {
	ID                  string           `json:"id"`
	SiteID              string           `json:"site_id"`
	Name                string           `json:"name"`
	DeviceID            string           `json:"device_id"`
	DataSource          string           `json:"data_source"`
	IsSimulated         bool             `json:"is_simulated"`
	Connection          string           `json:"connection"`
	LastStateAt         *string          `json:"last_state_at"`
	ControlState        *string          `json:"control_state"`
	ControlStateNote    string           `json:"control_state_note,omitempty"`
	PumpCommanded       *string          `json:"pump_commanded"`
	WaterDelivery       *string          `json:"water_delivery"`
	AppliedProgram      *int             `json:"applied_program_version"`
	EssentialInterlocks *bool            `json:"essential_interlocks_ok"`
	FaultCodes          []string         `json:"fault_codes"`
	FirmwareVersion     *string          `json:"firmware_version"`
	Buffer              *mc.BufferStatus `json:"controller_buffer"`
	ActiveIncidents     map[string]int   `json:"active_incidents"`
	LatestReadings      *readingView     `json:"latest_readings"`
}

type readingView struct {
	EffectiveAt string                `json:"effective_at_utc"`
	ReceivedAt  string                `json:"received_at_utc"`
	TimeQuality string                `json:"time_quality"`
	AgeSeconds  int64                 `json:"age_seconds"`
	Outdated    bool                  `json:"outdated"`
	Source      string                `json:"data_source"`
	Readings    map[string]mc.Reading `json:"readings"`
}

func (s *Server) latestReading(ctx context.Context, unitID string) (*readingView, error) {
	var eff, recv, tq, src, rj string
	err := s.DB.R.QueryRowContext(ctx, `SELECT effective_at_utc,received_at_utc,time_quality,source,readings_json FROM telemetry_snapshots WHERE unit_id=? ORDER BY effective_at_utc DESC LIMIT 1`, unitID).
		Scan(&eff, &recv, &tq, &src, &rj)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := &readingView{EffectiveAt: eff, ReceivedAt: recv, TimeQuality: tq, Source: src}
	if t, e := time.Parse(storage.TimeFormat, eff); e == nil {
		v.AgeSeconds = int64(time.Since(t).Seconds())
		v.Outdated = time.Since(t) > readingStaleAfter
	}
	if err := json.Unmarshal([]byte(rj), &v.Readings); err != nil {
		return nil, err
	}
	if v.Outdated { // never present an old value as current
		for k, rd := range v.Readings {
			rd.Quality = mc.QualityOutdated
			v.Readings[k] = rd
		}
	}
	return v, nil
}

func (s *Server) buildUnit(ctx context.Context, id, name, deviceID string, withReadings bool) (unitView, error) {
	now := time.Now()
	v := unitView{ID: id, SiteID: s.Cfg.SiteID, Name: name, DeviceID: deviceID, Connection: string(devices.ConnNeverSeen), FaultCodes: []string{}, ActiveIncidents: map[string]int{}}
	if rt, ok := s.Reg.ByUnit(id); ok {
		v.DataSource, v.IsSimulated = rt.DataSource, rt.DataSource == string(mc.SourceSimulator)
		conn := rt.Connection(now, s.Cfg.StaleAfter)
		v.Connection = string(conn)
		if rt.Seen {
			ls := storage.FormatTime(rt.StateAt)
			v.LastStateAt = &ls
			st := rt.State
			applied := st.ProgramVersionApplied
			v.AppliedProgram = applied
			fw := st.FirmwareVersion
			v.FirmwareVersion = &fw
			v.Buffer = &st.Buffer
			if conn == devices.ConnOnline {
				cs, pc, wd, il := string(st.ControlState), st.PumpCommanded, st.WaterDelivery, st.EssentialInterlocksOK
				v.ControlState, v.PumpCommanded, v.WaterDelivery, v.EssentialInterlocks = &cs, &pc, &wd, &il
				v.FaultCodes = append([]string{}, st.FaultCodes...)
			} else {
				// stale: show the last known state only as outdated, never as current
				cs := "UNKNOWN_STALE"
				v.ControlState = &cs
				v.ControlStateNote = "Controller is not reporting. Last known state was " + string(st.ControlState) + " at " + ls + ". This does not prove the pump stopped."
			}
		}
	}
	rows, err := s.DB.R.QueryContext(ctx, `SELECT severity, COUNT(*) FROM incidents WHERE unit_id=? AND status LIKE 'ACTIVE%' GROUP BY severity`, id)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var sev string
		var n int
		if rows.Scan(&sev, &n) == nil {
			v.ActiveIncidents[sev] = n
		}
	}
	rows.Close()
	if withReadings {
		if v.LatestReadings, err = s.latestReading(ctx, id); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (s *Server) listUnits(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	q := r.URL.Query()
	limit, offset := atoiDefault(q.Get("limit"), 50), atoiDefault(q.Get("offset"), 0)
	if limit < 1 || limit > 100 || offset < 0 {
		writeError(w, r, 400, "VALIDATION_FAILED", "limit must be 1..100, offset >= 0", nil)
		return
	}
	name := q.Get("q")
	rows, err := s.DB.R.QueryContext(r.Context(), `SELECT id,name,COALESCE(device_id,'') FROM growing_units WHERE site_id=? AND name LIKE ? ORDER BY name,id LIMIT ? OFFSET ?`,
		s.Cfg.SiteID, "%"+name+"%", limit, offset)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	type row struct{ id, name, dev string }
	var rs []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.name, &x.dev) == nil {
			rs = append(rs, x)
		}
	}
	rows.Close()
	out := make([]unitView, 0, len(rs))
	for _, x := range rs {
		v, err := s.buildUnit(r.Context(), x.id, x.name, x.dev, true)
		if err != nil {
			s.mapErr(w, r, err)
			return
		}
		out = append(out, v)
	}
	var total int
	_ = s.DB.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM growing_units WHERE site_id=? AND name LIKE ?`, s.Cfg.SiteID, "%"+name+"%").Scan(&total)
	writeJSON(w, 200, map[string]any{"units": out, "total": total, "limit": limit, "offset": offset, "observed_at": storage.Now(), "source": "local"})
}

func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func (s *Server) unitRow(w http.ResponseWriter, r *http.Request) (id, name, dev string, ok bool) {
	id = r.PathValue("unitId")
	err := s.DB.R.QueryRowContext(r.Context(), `SELECT name,COALESCE(device_id,'') FROM growing_units WHERE id=? AND site_id=?`, id, s.Cfg.SiteID).Scan(&name, &dev)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, 404, "NOT_FOUND", "unit not found", nil)
		return "", "", "", false
	}
	if err != nil {
		s.mapErr(w, r, err)
		return "", "", "", false
	}
	return id, name, dev, true
}

func (s *Server) getUnit(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, name, dev, ok := s.unitRow(w, r)
	if !ok {
		return
	}
	v, err := s.buildUnit(r.Context(), id, name, dev, true)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	var pending *commands.Command
	var cid string
	if s.DB.R.QueryRowContext(r.Context(), `SELECT id FROM commands WHERE unit_id=? AND status IN ('PENDING','SENT') LIMIT 1`, id).Scan(&cid) == nil {
		if c, err := s.Cmds.Get(r.Context(), cid); err == nil {
			pending = &c
		}
	}
	writeJSON(w, 200, map[string]any{"unit": v, "pending_command": pending, "observed_at": storage.Now(), "source": "local"})
}

func (s *Server) latestReadings(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _, _, ok := s.unitRow(w, r)
	if !ok {
		return
	}
	rv, err := s.latestReading(r.Context(), id)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"unit_id": id, "snapshot": rv, "observed_at": storage.Now()})
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	now := time.Now()
	total, healthy, attention, stale, never := 0, 0, 0, 0, 0
	sim := false
	for _, rt := range s.Reg.All() {
		if rt.UnitID == "" {
			continue
		}
		total++
		sim = sim || rt.DataSource == string(mc.SourceSimulator)
		switch rt.Connection(now, s.Cfg.StaleAfter) {
		case devices.ConnNeverSeen:
			never++
			attention++
		case devices.ConnStale, devices.ConnOffline:
			stale++
			attention++
		default:
			if rt.State.ControlState == mc.StateRunning && rt.State.EssentialInterlocksOK {
				healthy++
			} else {
				attention++
			}
		}
	}
	crit, warn, act, err := incidents.CountActive(r.Context(), s.DB.R, s.Cfg.SiteID)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	mq := s.MQTT.Connected()
	writeJSON(w, 200, map[string]any{
		"site":                    map[string]any{"id": s.Cfg.SiteID, "name": s.Cfg.SiteName},
		"units":                   map[string]int{"total": total, "healthy": healthy, "attention": attention, "stale": stale, "never_seen": never},
		"active_incidents":        map[string]int{"critical": crit, "warning": warn, "total": act},
		"contains_simulated_data": sim,
		// spec 10.5: separate facts, each with an honest "unknown" where the
		// edge cannot observe it. The edge does not probe the WAN in Phase 1.
		"connectivity": map[string]any{
			"local_api_reachable": true, "site_lan_healthy": mq, "mqtt_broker_connected": mq,
			"site_wan_reachable": s.cloudReach(), "cloud_api_reachable": s.cloudReach(), "last_cloud_sync_at": s.cloudInfo()["last_sync_at"],
			"edge_seen_at": storage.FormatTime(now), "connection_path": "LOCAL", "is_currently_stale": false,
		},
		"observed_at": storage.FormatTime(now), "source": "local",
	})
}

// ---------------------------------------------------------------- programs

func (s *Server) appliedProgram(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _, _, ok := s.unitRow(w, r)
	if !ok {
		return
	}
	var ver int
	var payload, appliedAt, by string
	err := s.DB.R.QueryRowContext(r.Context(), `SELECT version,payload_json,COALESCE(applied_at,''),created_by FROM programs WHERE unit_id=? AND status='APPLIED' ORDER BY version DESC LIMIT 1`, id).
		Scan(&ver, &payload, &appliedAt, &by)
	reported := (*int)(nil)
	if rt, ok := s.Reg.ByUnit(id); ok && rt.Seen {
		reported = rt.State.ProgramVersionApplied
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"unit_id": id, "applied": nil, "controller_reported_version": reported,
			"note": "No programme confirmed through this Pi yet. The controller may hold one provisioned on-site."})
		return
	}
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"unit_id": id, "applied": map[string]any{"version": ver, "program": json.RawMessage(payload), "applied_at": appliedAt, "requested_by": by},
		"controller_reported_version": reported})
}

func (s *Server) listPrograms(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _, _, ok := s.unitRow(w, r)
	if !ok {
		return
	}
	rows, err := s.DB.R.QueryContext(r.Context(), `SELECT version,payload_json,created_by,COALESCE(command_id,''),requested_at,COALESCE(applied_at,''),status FROM programs WHERE unit_id=? ORDER BY version DESC LIMIT 100`, id)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var v int
		var p, by, cid, req, app, st string
		if rows.Scan(&v, &p, &by, &cid, &req, &app, &st) == nil {
			out = append(out, map[string]any{"version": v, "program": json.RawMessage(p), "requested_by": by, "command_id": cid, "requested_at": req, "applied_at": app, "status": st})
		}
	}
	writeJSON(w, 200, map[string]any{"unit_id": id, "programs": out})
}

func (s *Server) programRequest(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _, _, ok := s.unitRow(w, r)
	if !ok {
		return
	}
	var req commands.ProgramRequest
	if !decode(w, r, &req) {
		return
	}
	if k := r.Header.Get("Idempotency-Key"); k != "" && req.CommandID == "" {
		req.CommandID = k
	}
	cmd, created, err := s.Cmds.CreateProgramRequest(r.Context(), u, id, req)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	code := 202
	if !created {
		code = 200 // idempotent replay
	}
	w.Header().Set("Location", "/api/v1/commands/"+cmd.ID)
	writeJSON(w, code, cmd)
}

func (s *Server) getCommand(w http.ResponseWriter, r *http.Request, u auth.User) {
	c, err := s.Cmds.Get(r.Context(), r.PathValue("commandId"))
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	writeJSON(w, 200, c)
}

// --------------------------------------------------------------- incidents

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	q := r.URL.Query()
	f := incidents.Filter{Severity: q.Get("severity"), UnitID: q.Get("unit_id"), Limit: atoiDefault(q.Get("limit"), 50), Offset: atoiDefault(q.Get("offset"), 0)}
	switch q.Get("state") {
	case "active":
		t := true
		f.Active = &t
	case "resolved":
		t := false
		f.Active = &t
	case "", "all":
	default:
		writeError(w, r, 400, "VALIDATION_FAILED", "state must be active, resolved or all", nil)
		return
	}
	if f.Limit < 1 || f.Limit > 200 || f.Offset < 0 || (f.Severity != "" && f.Severity != "info" && f.Severity != "warning" && f.Severity != "critical") {
		writeError(w, r, 400, "VALIDATION_FAILED", "invalid limit/offset/severity", nil)
		return
	}
	list, err := incidents.List(r.Context(), s.DB.R, s.Cfg.SiteID, f)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	if list == nil {
		list = []incidents.Incident{}
	}
	writeJSON(w, 200, map[string]any{"incidents": list, "observed_at": storage.Now()})
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request, u auth.User) {
	i, err := incidents.Get(r.Context(), s.DB.R, s.Cfg.SiteID, r.PathValue("incidentId"))
	if errors.Is(err, incidents.ErrNotFound) {
		writeError(w, r, 404, "NOT_FOUND", "incident not found", nil)
		return
	}
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	writeJSON(w, 200, i)
}

func (s *Server) ackIncident(w http.ResponseWriter, r *http.Request, u auth.User) {
	var inc incidents.Incident
	var changed bool
	err := s.DB.Tx(r.Context(), func(tx *sql.Tx) error {
		var e error
		inc, changed, e = incidents.Acknowledge(r.Context(), tx, s.Cfg.SiteID, r.PathValue("incidentId"), u.ID)
		if e == nil && changed && s.Cfg.CloudSyncEnabled {
			e = edgesync.EnqueueIncident(r.Context(), tx, s.Cfg.SiteID, inc)
		}
		return e
	})
	if errors.Is(err, incidents.ErrNotFound) {
		writeError(w, r, 404, "NOT_FOUND", "incident not found", nil)
		return
	}
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	if changed {
		s.Hub.Publish("incident.updated", inc)
	}
	// Acknowledged != resolved: the response says so explicitly.
	writeJSON(w, 200, map[string]any{"incident": inc, "note": "Acknowledged means a person has seen this. It does not mean the condition is fixed."})
}

// ----------------------------------------------------------------- devices

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	now := time.Now()
	out := []map[string]any{}
	for _, rt := range s.Reg.All() {
		out = append(out, map[string]any{"device_id": rt.ID, "unit_id": rt.UnitID, "data_source": rt.DataSource, "hardware_profile": rt.Hardware,
			"connection": rt.Connection(now, s.Cfg.StaleAfter), "disabled": rt.Disabled})
	}
	writeJSON(w, 200, map[string]any{"devices": out})
}

// provisionDevice registers a device + unit. MQTT credentials are NOT created
// here (the broker password file is managed out of band by the installer /
// aerolab-seed); the API never handles or returns device secrets.
func (s *Server) provisionDevice(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	var in struct {
		DeviceID        string `json:"device_id"`
		UnitID          string `json:"unit_id"`
		UnitName        string `json:"unit_name"`
		HardwareProfile string `json:"hardware_profile"`
		DataSource      string `json:"data_source"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.DataSource == "" {
		in.DataSource = string(mc.SourceDevice)
	}
	if in.DataSource == string(mc.SourceSimulator) && !s.Cfg.AllowSimulatedData {
		writeError(w, r, 400, "VALIDATION_FAILED", "simulated devices are disabled on this site", map[string]any{"field": "data_source"})
		return
	}
	d, un, err := s.Reg.Provision(r.Context(), in.DeviceID, in.UnitID, in.UnitName, in.HardwareProfile, in.DataSource, u.ID)
	switch {
	case errors.Is(err, devices.ErrExists):
		writeError(w, r, 409, "VALIDATION_FAILED", "device or unit already exists", nil)
	case err != nil:
		writeError(w, r, 400, "VALIDATION_FAILED", err.Error(), nil)
	default:
		writeJSON(w, 201, map[string]any{"device": map[string]any{"id": d.ID, "unit_id": d.UnitID, "data_source": d.DataSource}, "unit": map[string]any{"id": un.ID, "name": un.Name}})
	}
}

// -------------------------------------------------------------- operations

func (s *Server) storageInfo(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	d, err := s.DB.Disk()
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	var tel, ev, ob int64
	_ = s.DB.R.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM telemetry_snapshots),(SELECT COUNT(*) FROM device_events),(SELECT COUNT(*) FROM outbox WHERE cloud_ack_at IS NULL)`).Scan(&tel, &ev, &ob)
	status := "OK"
	if !s.Metrics.StorageOK.Load() {
		status = "WRITE_FAILURES"
	} else if d.FreeBytes < s.Cfg.MinFreeBytes*2 {
		status = "LOW_SPACE"
	}
	writeJSON(w, 200, map[string]any{"status": status, "database_bytes": d.DBBytes, "wal_bytes": d.WALBytes, "disk_total_bytes": d.TotalBytes, "disk_free_bytes": d.FreeBytes,
		"min_free_bytes": s.Cfg.MinFreeBytes, "retention_days": s.Cfg.RetentionDays, "telemetry_rows": tel, "event_rows": ev,
		"cloud_sync": s.cloudDetail(ob), "durable_receipts_enabled": s.Metrics.StorageOK.Load(), "observed_at": storage.Now()})
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	limit, offset := atoiDefault(r.URL.Query().Get("limit"), 50), atoiDefault(r.URL.Query().Get("offset"), 0)
	if limit < 1 || limit > 200 || offset < 0 {
		writeError(w, r, 400, "VALIDATION_FAILED", "limit must be 1..200", nil)
		return
	}
	rows, err := s.DB.R.QueryContext(r.Context(), `SELECT id,COALESCE(actor_id,''),actor_source,action,COALESCE(target_type,''),COALESCE(target_id,''),COALESCE(previous_json,''),COALESCE(next_json,''),occurred_at
		FROM audit_events WHERE site_id=? ORDER BY occurred_at DESC, id LIMIT ? OFFSET ?`, s.Cfg.SiteID, limit, offset)
	if err != nil {
		s.mapErr(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, actor, src, action, tt, tid, prev, next, at string
		if rows.Scan(&id, &actor, &src, &action, &tt, &tid, &prev, &next, &at) == nil {
			out = append(out, map[string]any{"id": id, "actor_id": actor, "actor_source": src, "action": action, "target_type": tt, "target_id": tid, "previous": prev, "next": next, "occurred_at": at})
		}
	}
	writeJSON(w, 200, map[string]any{"events": out, "limit": limit, "offset": offset})
}

func (s *Server) diag(w http.ResponseWriter, r *http.Request, u auth.User) {
	rq, oq := s.Writer.QueueDepth()
	writeJSON(w, 200, map[string]any{
		"uptime_seconds": int(time.Since(s.Metrics.Started).Seconds()),
		"resources":      diagnostics.ReadResources(),
		"mqtt":           map[string]any{"connected": s.MQTT.Connected(), "messages_in": s.Metrics.MQTTIn.Load(), "unknown_device": s.Metrics.UnknownDevice.Load()},
		"ingest": map[string]any{"records_persisted": s.Metrics.RecordsPersisted.Load(), "duplicates_ignored": s.Metrics.DuplicatesIgnored.Load(), "receipts_sent": s.Metrics.ReceiptsSent.Load(),
			"rejected": s.Metrics.Rejected.Load(), "reject_reasons": s.Metrics.Reasons(), "queue_drops": s.Metrics.QueueDrops.Load(), "ops_dropped": s.Metrics.OpsDropped.Load(),
			"commit_errors": s.Metrics.CommitErrors.Load(), "last_commit_micros": s.Metrics.LastCommitMicros.Load(), "storage_ok": s.Metrics.StorageOK.Load()},
		"queues":  map[string]int{"records": rq, "ops": oq},
		"sse":     map[string]any{"subscribers": s.Hub.Count(), "slow_clients_dropped": s.Hub.Dropped()},
		"devices": s.Reg.Count(),
	})
}

var _ = fmt.Sprintf

// cloudInfo / cloudReach report ONLY what the sync worker actually observed by
// talking to the cloud API. With sync disabled (the permanently-offline site)
// reachability is unknown (null), never "false" and never invented.
func (s *Server) cloudInfo() map[string]any {
	if s.Cloud == nil {
		return map[string]any{"enabled": false, "last_sync_at": nil}
	}
	st := s.Cloud.Status()
	return map[string]any{"enabled": true, "last_sync_at": st.LastSyncAt, "reachable": st.Reachable, "backlog": st.Backlog, "consecutive_failures": st.ConsecutiveFailures, "remote_programs_accepted": st.RemotePrograms}
}

func (s *Server) cloudReach() any {
	if s.Cloud == nil {
		return nil
	}
	if r := s.Cloud.Status().Reachable; r != nil {
		return *r
	}
	return nil
}

func (s *Server) cloudDetail(pending int64) map[string]any {
	m := map[string]any{"enabled": s.Cfg.CloudSyncEnabled, "outbox_pending": pending}
	if s.Cloud != nil {
		st := s.Cloud.Status()
		m["reachable"], m["last_sync_at"], m["last_error"], m["consecutive_failures"], m["rejected_items_dropped"] = st.Reachable, st.LastSyncAt, st.LastError, st.ConsecutiveFailures, st.RejectedItems
	}
	return m
}
