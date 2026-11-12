// Package api is the cloud REST surface. Authorisation is per SITE
// membership on every call; a caller without membership gets 404 so the
// existence of other tenants' sites is not revealed.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/config"
)

type Server struct {
	Cfg    *config.Config
	DB     *sql.DB
	Auth   *auth.Service
	Static fs.FS
	Log    *slog.Logger
	mux    *http.ServeMux
	routes []string
}

func New(cfg *config.Config, db *sql.DB, a *auth.Service, static fs.FS, log *slog.Logger) *Server {
	s := &Server{Cfg: cfg, DB: db, Auth: a, Static: static, Log: log, mux: http.NewServeMux()}
	s.register()
	return s
}

func (s *Server) Routes() []string { return append([]string(nil), s.routes...) }

type ctxKey int

const keyReq ctxKey = 0

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := "req-" + auth.NewID()
		w.Header().Set("X-Request-Id", id)
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic", "path", r.URL.Path, "panic", rec)
				writeErr(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
			}
		}()
		s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyReq, id)))
	})
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(c)
	case err := <-errc:
		return err
	}
}

func reqID(r *http.Request) string { v, _ := r.Context().Value(keyReq).(string); return v }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, code int, c, msg string, d map[string]any) {
	if d == nil {
		d = map[string]any{}
	}
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": c, "message": msg, "details": d, "request_id": reqID(r)}})
}

func decode(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeErr(w, r, 415, "VALIDATION_FAILED", "Content-Type must be application/json", nil)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, r, 400, "VALIDATION_FAILED", "invalid JSON body: "+err.Error(), nil)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeErr(w, r, 400, "VALIDATION_FAILED", "trailing data", nil)
		return false
	}
	return true
}

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) handle(method, pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, method+" "+pattern)
	s.mux.HandleFunc(method+" "+pattern, h)
}

type userH func(w http.ResponseWriter, r *http.Request, u auth.User)

// userRoute authenticates a cloud user session.
func (s *Server) userRoute(method, pattern string, h userH) {
	s.handle(method, pattern, func(w http.ResponseWriter, r *http.Request) {
		u, err := s.Auth.Authenticate(r.Context(), bearer(r))
		if err != nil {
			writeErr(w, r, 401, "UNAUTHORIZED", "sign in required", nil)
			return
		}
		h(w, r, u)
	})
}

// siteRoute additionally requires membership of {siteId} with at least `min`.
// Non-members and unknown sites are both 404; a member with too low a role is 403.
func (s *Server) siteRoute(method, pattern string, min auth.Role, h func(w http.ResponseWriter, r *http.Request, u auth.User, siteID string, role auth.Role)) {
	s.userRoute(method, pattern, func(w http.ResponseWriter, r *http.Request, u auth.User) {
		site := r.PathValue("siteId")
		role, ok := s.Auth.SiteRole(r.Context(), u.ID, site)
		if !ok {
			writeErr(w, r, 404, "NOT_FOUND", "site not found", nil)
			return
		}
		if !role.AtLeast(min) {
			s.audit(r.Context(), site, u.ID, "user", "authz.denied", method+" "+pattern, nil)
			writeErr(w, r, 403, "FORBIDDEN", "your role at this site does not permit this action", map[string]any{"required": min})
			return
		}
		h(w, r, u, site, role)
	})
}

func (s *Server) audit(ctx context.Context, site, actor, kind, action, target string, detail any) {
	var d []byte
	if detail != nil {
		d, _ = json.Marshal(detail)
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_events(site_id,actor_id,actor_kind,action,target,detail) VALUES (NULLIF($1,''),NULLIF($2,''),$3,$4,$5,$6)`, site, actor, kind, action, target, d)
}

func (s *Server) staticHandler() http.Handler {
	var root fs.FS = s.Static
	if s.Cfg.StaticDir != "" {
		root = os.DirFS(s.Cfg.StaticDir)
	}
	if root == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, r, 404, "NOT_FOUND", "no such endpoint", nil)
		})
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			writeErr(w, r, 404, "NOT_FOUND", "no such endpoint", nil)
			return
		}
		if p := strings.TrimPrefix(path.Clean(r.URL.Path), "/"); p != "" {
			if f, err := root.Open(p); err == nil {
				_ = f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}
