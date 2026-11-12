// Package httpapi is the edge REST + SSE surface. Every protected route goes
// through authenticate -> roles.Can; the server is the only authority (UI
// state is never trusted).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/commands"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/devices"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/diagnostics"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/telemetry"
)

const cookieName = "aerolab_session"

type MQTTStatus interface{ Connected() bool }

type Deps struct {
	Cfg     *config.Config
	DB      *storage.Store
	Auth    *auth.Service
	Reg     *devices.Registry
	Cmds    *commands.Manager
	Hub     *sse.Hub
	Metrics *diagnostics.Metrics
	Writer  *telemetry.Writer
	MQTT    MQTTStatus
	Static  fs.FS
	Log     *slog.Logger
}

type Server struct {
	Deps
	mux    *http.ServeMux
	routes []string
}

func New(d Deps) *Server {
	s := &Server{Deps: d, mux: http.NewServeMux()}
	s.register()
	return s
}

// Routes lists "METHOD /pattern" for the OpenAPI contract test.
func (s *Server) Routes() []string { return append([]string(nil), s.routes...) }

func (s *Server) Handler() http.Handler {
	return s.recoverer(s.requestID(s.secure(s.mux)))
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Addr: s.Cfg.HTTPAddr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10} // WriteTimeout is per-request (SSE streams must outlive it)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	case err := <-errc:
		return err
	}
}

// ------------------------------------------------------------- middleware

type ctxKey int

const (
	keyReqID ctxKey = iota
	keyUser
)

func reqID(r *http.Request) string { v, _ := r.Context().Value(keyReqID).(string); return v }

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := "req-" + auth.NewID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyReqID, id)))
	})
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// self-contained UI: no CDN, fonts or remote scripts (works offline)
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if !strings.HasSuffix(r.URL.Path, "/events") { // SSE manages its own deadlines
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				writeError(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// -------------------------------------------------------------- responses

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg, "details": details, "request_id": reqID(r)}})
}

// decode reads a bounded JSON body and rejects unknown fields (no mass
// assignment: clients cannot set fields the handler did not declare).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeError(w, r, 415, "VALIDATION_FAILED", "Content-Type must be application/json", nil)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, r, 400, "VALIDATION_FAILED", "invalid JSON body: "+err.Error(), nil)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, r, 400, "VALIDATION_FAILED", "trailing data after JSON body", nil)
		return false
	}
	return true
}

func (s *Server) mapErr(w http.ResponseWriter, r *http.Request, err error) {
	var ce *commands.Error
	if errors.As(err, &ce) {
		st := map[string]int{"VALIDATION_FAILED": 400, "NOT_FOUND": 404, "PROGRAM_VERSION_CONFLICT": 409, "DEVICE_OFFLINE": 409,
			"COMMAND_PENDING": 409, "IDEMPOTENCY_CONFLICT": 409}[ce.Code]
		if st == 0 {
			st = 400
		}
		writeError(w, r, st, ce.Code, ce.Message, ce.Details)
		return
	}
	if errors.Is(err, storage.ErrStorageLow) {
		writeError(w, r, 503, "STORAGE_UNAVAILABLE", "local storage is full or read-only", nil)
		return
	}
	s.Log.Error("request failed", "path", r.URL.Path, "err", err, "request_id", reqID(r))
	writeError(w, r, 500, "INTERNAL_ERROR", "internal error", nil)
}

// ----------------------------------------------------------- routing glue

type userHandler func(w http.ResponseWriter, r *http.Request, u auth.User)

func (s *Server) handle(method, pattern string, h http.HandlerFunc) {
	s.routes = append(s.routes, method+" "+pattern)
	s.mux.HandleFunc(method+" "+pattern, h)
}

// protected authenticates then authorises. need == "" means any signed-in user.
func (s *Server) protected(method, pattern string, need roles.Action, h userHandler) {
	s.handle(method, pattern, func(w http.ResponseWriter, r *http.Request) {
		u, viaCookie, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		if viaCookie && method != http.MethodGet && !csrfOK(r) {
			writeError(w, r, 403, "FORBIDDEN", "cross-site request refused", nil)
			return
		}
		if need != "" && !roles.Can(u.Role, need) {
			s.Auth.AuditStandalone(r.Context(), u.ID, "authz.denied", "route", method+" "+pattern, "")
			writeError(w, r, 403, "FORBIDDEN", "your role does not permit this action", map[string]any{"required": need})
			return
		}
		h(w, r, u)
	})
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, bool, bool) {
	tok, viaCookie := "", false
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	} else if c, err := r.Cookie(cookieName); err == nil {
		tok, viaCookie = c.Value, true
	}
	u, err := s.Auth.Authenticate(r.Context(), tok)
	if err != nil {
		writeError(w, r, 401, "UNAUTHORIZED", "sign in required", nil)
		return auth.User{}, false, false
	}
	return u, viaCookie, true
}

// csrfOK protects cookie-authenticated writes: JSON content type is required
// (checked by decode) and a browser-supplied Origin must match our Host.
func csrfOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // non-browser client
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// siteOK verifies {siteId} is THIS site; the Pi serves exactly one site.
func (s *Server) siteOK(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("siteId") != s.Cfg.SiteID {
		writeError(w, r, 404, "NOT_FOUND", "site not found", nil)
		return false
	}
	return true
}

// -------------------------------------------------------------- static UI

func (s *Server) staticHandler() http.Handler {
	var root fs.FS = s.Static
	if s.Cfg.StaticDir != "" {
		root = os.DirFS(s.Cfg.StaticDir)
	}
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, r, 405, "VALIDATION_FAILED", "method not allowed", nil)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, r, 404, "NOT_FOUND", "no such endpoint", nil)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if f, err := root.Open(p); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback so client-side routes survive a refresh
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
