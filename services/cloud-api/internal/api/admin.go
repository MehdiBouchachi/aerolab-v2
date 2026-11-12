package api

import (
	"errors"
	"net/http"

	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
)

type memberRow struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

func (s *Server) members(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT m.user_id,c.email,m.role FROM site_members m JOIN cloud_users c ON c.id=m.user_id WHERE m.site_id=$1 ORDER BY c.email`, site)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rows.Close()
	out := []memberRow{}
	for rows.Next() {
		var m memberRow
		if rows.Scan(&m.UserID, &m.Email, &m.Role) == nil {
			out = append(out, m)
		}
	}
	writeJSON(w, 200, map[string]any{"members": out})
}

// putMember adds or changes a member's role at THIS site. Roles are per site.
func (s *Server) putMember(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	var in struct {
		Role auth.Role `json:"role"`
	}
	if !decode(w, r, &in, 1<<10) {
		return
	}
	if in.Role != auth.Admin && in.Role != auth.Operator && in.Role != auth.Viewer {
		writeErr(w, r, 400, "VALIDATION_FAILED", "role must be ADMIN, OPERATOR or VIEWER", nil)
		return
	}
	target := r.PathValue("userId")
	var exists bool
	_ = s.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM cloud_users WHERE id=$1 AND disabled_at IS NULL)`, target).Scan(&exists)
	if !exists {
		writeErr(w, r, 404, "NOT_FOUND", "user not found", nil)
		return
	}
	if target == u.ID && in.Role != auth.Admin {
		if n := s.adminCount(r, site); n <= 1 {
			writeErr(w, r, 409, "VALIDATION_FAILED", "cannot demote the last admin of this site", nil)
			return
		}
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO site_members(site_id,user_id,role) VALUES ($1,$2,$3) ON CONFLICT (site_id,user_id) DO UPDATE SET role=EXCLUDED.role`, site, target, string(in.Role)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r.Context(), site, u.ID, "user", "member.put", target, map[string]any{"role": in.Role})
	writeJSON(w, 200, memberRow{UserID: target, Role: string(in.Role)})
}

func (s *Server) adminCount(r *http.Request, site string) (n int) {
	_ = s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM site_members WHERE site_id=$1 AND role='ADMIN'`, site).Scan(&n)
	return
}

func (s *Server) delMember(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	target := r.PathValue("userId")
	var role string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT role FROM site_members WHERE site_id=$1 AND user_id=$2`, site, target).Scan(&role); err != nil {
		writeErr(w, r, 404, "NOT_FOUND", "member not found", nil)
		return
	}
	if role == "ADMIN" && s.adminCount(r, site) <= 1 {
		writeErr(w, r, 409, "VALIDATION_FAILED", "cannot remove the last admin of this site", nil)
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM site_members WHERE site_id=$1 AND user_id=$2`, site, target); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r.Context(), site, u.ID, "user", "member.remove", target, nil)
	w.WriteHeader(204)
}

// rotate issues a NEW edge credential and revokes the old ones. The token is
// shown exactly once; only its hash is stored.
func (s *Server) rotate(w http.ResponseWriter, r *http.Request, u auth.User, site string, _ auth.Role) {
	tok, err := s.Auth.IssueEdgeToken(r.Context(), site, true)
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			writeErr(w, r, 403, "FORBIDDEN", "forbidden", nil)
			return
		}
		s.fail(w, r, err)
		return
	}
	s.audit(r.Context(), site, u.ID, "user", "edge_credential.rotate", site, nil)
	writeJSON(w, 200, map[string]any{"edge_token": tok, "note": "Shown once. Configure it on the Pi as AEROLAB_CLOUD_TOKEN; the previous token no longer works."})
}
