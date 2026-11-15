package api

// Self-service fleets: sign-up with an organisation, "connect a Pi" with a one-time claim code, and
// one overview of every Pi and unit a person may see. See docs/architecture/fleet-management.md.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
)

func (s *Server) registerFleet() {
	s.handle("POST", "/api/v1/auth/signup", s.signup)
	s.handle("POST", "/api/v1/edge/claim", s.edgeClaim)
	s.userRoute("GET", "/api/v1/orgs", s.listOrgs)
	s.userRoute("POST", "/api/v1/orgs/{orgId}/site-claims", s.createClaim)
	s.userRoute("PUT", "/api/v1/orgs/{orgId}/members", s.putOrgMember)
	s.userRoute("GET", "/api/v1/fleet", s.fleet)
}

var orgKinds = map[string]bool{"PERSONAL": true, "BUSINESS": true, "GOVERNMENT": true}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if s.Cfg.Signup != "open" {
		writeErr(w, r, 403, "SIGNUP_DISABLED", "accounts on this service are created by its operator", nil)
		return
	}
	var in struct {
		Email            string `json:"email"`
		Password         string `json:"password"`
		OrganizationName string `json:"organization_name"`
		OrganizationKind string `json:"organization_kind"`
	}
	if !decode(w, r, &in, 8<<10) {
		return
	}
	if in.OrganizationKind == "" {
		in.OrganizationKind = "PERSONAL"
	}
	if !orgKinds[in.OrganizationKind] {
		writeErr(w, r, 400, "VALIDATION_FAILED", "organization_kind must be PERSONAL, BUSINESS or GOVERNMENT", map[string]any{"field": "organization_kind"})
		return
	}
	in.OrganizationName = strings.TrimSpace(in.OrganizationName)
	if in.OrganizationName == "" {
		if in.OrganizationKind != "PERSONAL" {
			writeErr(w, r, 400, "VALIDATION_FAILED", "organization_name is required", map[string]any{"field": "organization_name"})
			return
		}
		in.OrganizationName = "My farm"
	}
	if len(in.OrganizationName) > 120 {
		writeErr(w, r, 400, "VALIDATION_FAILED", "organization_name too long", map[string]any{"field": "organization_name"})
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), in.Email, in.Password)
	switch {
	case errors.Is(err, auth.ErrWeakPassword):
		writeErr(w, r, 400, "VALIDATION_FAILED", "password must be at least 12 characters", map[string]any{"field": "password"})
		return
	case err != nil && strings.Contains(err.Error(), "exists"):
		writeErr(w, r, 409, "ACCOUNT_EXISTS", "an account with this email already exists; sign in instead", nil)
		return
	case err != nil:
		writeErr(w, r, 400, "VALIDATION_FAILED", err.Error(), map[string]any{"field": "email"})
		return
	}
	org := "org-" + strings.ToLower(auth.NewID())
	err = s.tx(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO organizations(id,name,kind) VALUES ($1,$2,$3)`, org, in.OrganizationName, in.OrganizationKind); err != nil {
			return err
		}
		_, err := tx.ExecContext(r.Context(), `INSERT INTO organization_members(org_id,user_id,role) VALUES ($1,$2,'OWNER')`, org, u.ID)
		return err
	})
	if err != nil {
		_, _ = s.DB.ExecContext(r.Context(), `DELETE FROM cloud_users WHERE id=$1`, u.ID) // no orphan account
		s.Log.Error("signup", "err", err)
		writeErr(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
		return
	}
	tok, _, err := s.Auth.Login(r.Context(), u.Email, in.Password, ipOf(r))
	if err != nil {
		writeErr(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
		return
	}
	s.audit(r.Context(), "", u.ID, "user", "org.create", org, map[string]string{"kind": in.OrganizationKind})
	writeJSON(w, 201, map[string]any{"token": tok, "user": map[string]string{"id": u.ID, "username": u.Email},
		"organization": map[string]string{"id": org, "name": in.OrganizationName, "kind": in.OrganizationKind}})
}

func (s *Server) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) orgRole(ctx context.Context, userID, orgID string) string {
	var role string
	_ = s.DB.QueryRowContext(ctx, `SELECT role FROM organization_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&role)
	return role
}

func (s *Server) listOrgs(w http.ResponseWriter, r *http.Request, u auth.User) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT o.id,o.name,o.kind,m.role,(SELECT COUNT(*) FROM sites s WHERE s.org_id=o.id)
		FROM organizations o JOIN organization_members m ON m.org_id=o.id WHERE m.user_id=$1 ORDER BY o.name`, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	type org struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Role  string `json:"role"`
		Sites int    `json:"site_count"`
	}
	out := []org{}
	for rows.Next() {
		var o org
		if err := rows.Scan(&o.ID, &o.Name, &o.Kind, &o.Role, &o.Sites); err != nil {
			s.fail(w, r, err)
			return
		}
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"organizations": out})
}

// putOrgMember lets an owner add an existing account to the organisation. Organisation OWNERs become
// ADMIN of every Pi connected afterwards; MEMBERs see nothing until a site admin grants them a role,
// so a cooperative or ministry can give each person only the farms they work on.
func (s *Server) putOrgMember(w http.ResponseWriter, r *http.Request, u auth.User) {
	org := r.PathValue("orgId")
	if s.orgRole(r.Context(), u.ID, org) != "OWNER" {
		writeErr(w, r, 403, "FORBIDDEN", "only organisation owners can manage members", nil)
		return
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &in, 4<<10) {
		return
	}
	if in.Role != "OWNER" && in.Role != "MEMBER" {
		writeErr(w, r, 400, "VALIDATION_FAILED", "role must be OWNER or MEMBER", map[string]any{"field": "role"})
		return
	}
	var uid string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT id FROM cloud_users WHERE email=lower($1) AND disabled_at IS NULL`, strings.TrimSpace(in.Email)).Scan(&uid); err != nil {
		writeErr(w, r, 404, "NOT_FOUND", "no account with this email; ask them to sign up first", map[string]any{"field": "email"})
		return
	}
	if uid == u.ID && in.Role != "OWNER" {
		writeErr(w, r, 400, "VALIDATION_FAILED", "you cannot demote yourself", nil)
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO organization_members(org_id,user_id,role) VALUES ($1,$2,$3) ON CONFLICT (org_id,user_id) DO UPDATE SET role=excluded.role`, org, uid, in.Role); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r.Context(), "", u.ID, "user", "org.member_put", org, map[string]string{"user_id": uid, "role": in.Role})
	writeJSON(w, 200, map[string]string{"user_id": uid, "role": in.Role})
}

// ------------------------------------------------------------------ connect a Pi

// claimAlphabet avoids look-alike characters (0/O, 1/I/L) because codes are read off a screen and typed.
const claimAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newClaimCode() string {
	b := make([]byte, 12) // 12 x log2(31) ~ 59 bits; single use, 24 h, rate-limited
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var sb strings.Builder
	for i, x := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(claimAlphabet[int(x)%len(claimAlphabet)])
	}
	return sb.String()
}

func normalizeClaim(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(c)))
}

func (s *Server) createClaim(w http.ResponseWriter, r *http.Request, u auth.User) {
	org := r.PathValue("orgId")
	if s.orgRole(r.Context(), u.ID, org) != "OWNER" {
		writeErr(w, r, 403, "FORBIDDEN", "only organisation owners can connect a Pi", nil)
		return
	}
	ttl := s.Cfg.ClaimTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	code := newClaimCode()
	exp := time.Now().Add(ttl).UTC()
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO site_claim_codes(code_hash,org_id,created_by,expires_at) VALUES ($1,$2,$3,$4)`,
		auth.HashCode(normalizeClaim(code)), org, u.ID, exp); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r.Context(), "", u.ID, "user", "site.claim_code", org, nil)
	writeJSON(w, 201, map[string]any{"code": code, "expires_at": exp.Format(time.RFC3339),
		"instructions": "On the Pi's local dashboard open Admin > Connect to cloud, enter this service's address and this code. It works once."})
}

// edgeClaim: the Pi presents a claim code and receives its site's edge token. Unauthenticated (the code
// is the credential), rate-limited per address, single-use, and atomic: the code is consumed, the site
// created, owners granted and the token issued in ONE transaction.
func (s *Server) edgeClaim(w http.ResponseWriter, r *http.Request) {
	ip := ipOf(r)
	if !s.Auth.ClaimAllowed(ip) {
		w.Header().Set("Retry-After", "900")
		writeErr(w, r, 429, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	var in struct {
		Code        string `json:"code"`
		SiteID      string `json:"site_id"`
		SiteName    string `json:"site_name"`
		EdgeVersion string `json:"edge_version"`
	}
	if !decode(w, r, &in, 4<<10) {
		return
	}
	in.SiteName = strings.TrimSpace(in.SiteName)
	if !mc.ValidID(in.SiteID) || in.SiteName == "" || len(in.SiteName) > 120 {
		writeErr(w, r, 400, "VALIDATION_FAILED", "site_id and site_name are required", nil)
		return
	}
	var tok, orgID, orgName string
	errBadCode := errors.New("bad code")
	errSiteTaken := errors.New("site taken")
	err := s.tx(r.Context(), func(tx *sql.Tx) error {
		var createdBy string
		err := tx.QueryRowContext(r.Context(), `UPDATE site_claim_codes SET used_at=now(), site_id=NULL
			WHERE code_hash=$1 AND used_at IS NULL AND expires_at > now() RETURNING org_id, created_by`, auth.HashCode(normalizeClaim(in.Code))).Scan(&orgID, &createdBy)
		if err != nil {
			return errBadCode
		}
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO sites(id,org_id,name,edge_version) VALUES ($1,$2,$3,NULLIF($4,''))`, in.SiteID, orgID, in.SiteName, in.EdgeVersion); err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				return errSiteTaken
			}
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE site_claim_codes SET site_id=$1 WHERE code_hash=$2`, in.SiteID, auth.HashCode(normalizeClaim(in.Code))); err != nil {
			return err
		}
		// the person who made the code and every organisation owner administer the new Pi
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO site_members(site_id,user_id,role)
			SELECT $1, user_id, 'ADMIN' FROM organization_members WHERE org_id=$2 AND role='OWNER'
			UNION SELECT $1, $3, 'ADMIN' ON CONFLICT DO NOTHING`, in.SiteID, orgID, createdBy); err != nil {
			return err
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT name FROM organizations WHERE id=$1`, orgID).Scan(&orgName); err != nil {
			return err
		}
		tok, err = auth.IssueEdgeTokenTx(r.Context(), tx, in.SiteID, false)
		return err
	})
	switch {
	case errors.Is(err, errBadCode):
		s.Auth.ClaimFailed(ip)
		writeErr(w, r, 400, "INVALID_CLAIM_CODE", "this code is wrong, expired or already used; create a new one in the cloud dashboard", nil)
	case errors.Is(err, errSiteTaken):
		writeErr(w, r, 409, "SITE_ID_TAKEN", "a Pi with this site id is already connected to this service", nil)
	case err != nil:
		s.fail(w, r, err)
	default:
		s.audit(r.Context(), in.SiteID, "", "edge", "site.claimed", in.SiteID, map[string]string{"org_id": orgID})
		writeJSON(w, 201, map[string]any{"token": tok, "site_id": in.SiteID, "organization": map[string]string{"id": orgID, "name": orgName}})
	}
}

// ------------------------------------------------------------------ fleet overview

type cropCount struct {
	Crop  string `json:"crop"`
	Units int    `json:"units"`
}

type fleetSite struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Role            string         `json:"role"`
	Stale           bool           `json:"stale"`
	LastSyncAt      *string        `json:"last_sync_at"`
	EdgeVersion     *string        `json:"edge_version"`
	UnitsTotal      int            `json:"units_total"`
	UnitsOnline     int            `json:"units_online"`
	UnitsAttention  int            `json:"units_attention"`
	UnitsNoCrop     int            `json:"units_without_crop"`
	Crops           []cropCount    `json:"crops"`
	ActiveIncidents map[string]int `json:"active_incidents"`
}

type fleetOrg struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Kind  string      `json:"kind"`
	Sites []fleetSite `json:"sites"`
}

// fleet summarises every Pi the person may see, grouped by organisation. Unit figures come from each
// Pi's last snapshot: when a Pi is stale they are its LAST KNOWN state, and the response says so.
func (s *Server) fleet(w http.ResponseWriter, r *http.Request, u auth.User) {
	ctx := r.Context()
	rows, err := s.DB.QueryContext(ctx, `SELECT o.id,o.name,o.kind,s.id,s.name,m.role,s.last_sync_at,s.edge_version
		FROM site_members m JOIN sites s ON s.id=m.site_id JOIN organizations o ON o.id=s.org_id
		WHERE m.user_id=$1 ORDER BY o.name,o.id,s.name,s.id`, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	orgs := []*fleetOrg{}
	byOrg := map[string]*fleetOrg{}
	idx := map[string][2]int{}
	var siteIDs []string
	for rows.Next() {
		var oid, oname, kind string
		var fs fleetSite
		var ls sql.NullTime
		var ev sql.NullString
		if err := rows.Scan(&oid, &oname, &kind, &fs.ID, &fs.Name, &fs.Role, &ls, &ev); err != nil {
			rows.Close()
			s.fail(w, r, err)
			return
		}
		fs.Stale, fs.Crops, fs.ActiveIncidents = true, []cropCount{}, map[string]int{}
		if ls.Valid {
			t := ls.Time.UTC().Format(time.RFC3339)
			fs.LastSyncAt, fs.Stale = &t, time.Since(ls.Time) > s.Cfg.StaleAfter
		}
		if ev.Valid {
			fs.EdgeVersion = &ev.String
		}
		o := byOrg[oid]
		if o == nil {
			o = &fleetOrg{ID: oid, Name: oname, Kind: kind}
			byOrg[oid] = o
			orgs = append(orgs, o)
		}
		o.Sites = append(o.Sites, fs)
		idx[fs.ID] = [2]int{len(orgs) - 1, len(o.Sites) - 1}
		siteIDs = append(siteIDs, fs.ID)
	}
	rows.Close()
	if len(siteIDs) > 0 {
		// one pass over the stored unit views of every visible site
		ur, err := s.DB.QueryContext(ctx, `SELECT site_id, view FROM units WHERE site_id = ANY($1)`, siteIDs)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		crops := map[string]map[string]int{}
		for ur.Next() {
			var sid string
			var raw []byte
			if err := ur.Scan(&sid, &raw); err != nil {
				ur.Close()
				s.fail(w, r, err)
				return
			}
			var v struct {
				Connection      string         `json:"connection"`
				ActiveIncidents map[string]int `json:"active_incidents"`
				Crop            *struct {
					Crop string `json:"crop"`
				} `json:"crop"`
			}
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			p := idx[sid]
			fs := &orgs[p[0]].Sites[p[1]]
			fs.UnitsTotal++
			if v.Connection == "ONLINE" {
				fs.UnitsOnline++
			}
			n := 0
			for sev, c := range v.ActiveIncidents {
				fs.ActiveIncidents[sev] += c
				n += c
			}
			if n > 0 || v.Connection != "ONLINE" {
				fs.UnitsAttention++
			}
			if v.Crop == nil || v.Crop.Crop == "" {
				fs.UnitsNoCrop++
			} else {
				if crops[sid] == nil {
					crops[sid] = map[string]int{}
				}
				crops[sid][v.Crop.Crop]++
			}
		}
		ur.Close()
		for sid, m := range crops {
			p := idx[sid]
			fs := &orgs[p[0]].Sites[p[1]]
			for c, n := range m {
				fs.Crops = append(fs.Crops, cropCount{c, n})
			}
			sort.Slice(fs.Crops, func(i, j int) bool { return fs.Crops[i].Crop < fs.Crops[j].Crop })
		}
	}
	writeJSON(w, 200, map[string]any{"organizations": orgs, "observed_at": time.Now().UTC().Format(time.RFC3339),
		"note": "Unit figures are each Pi's last snapshot; for a stale Pi they are last known, not current."})
}
