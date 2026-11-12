package integration

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	cloudBin  string
	cloudOnce sync.Once
	cloudErr  error
)

func buildCloud(t *testing.T) string {
	cloudOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aerolab-cloud-bin-")
		if err != nil {
			cloudErr = err
			return
		}
		cloudBin = filepath.Join(dir, "cloud")
		cmd := exec.Command("go", "build", "-o", cloudBin, "./cmd/cloud")
		cmd.Dir = filepath.Join(repoRoot(), "services", "cloud-api")
		if out, err := cmd.CombinedOutput(); err != nil {
			cloudErr = fmt.Errorf("build cloud: %v\n%s", err, out)
		}
	})
	if cloudErr != nil {
		t.Fatal(cloudErr)
	}
	return cloudBin
}

// wan is a TCP proxy between the Pi and the cloud whose link can be cut and
// restored: the cloud stays up and keeps serving its own users while the
// site "loses its internet".
type wan struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	cut    bool
	conns  []net.Conn
}

func newWAN(t *testing.T, target string) *wan {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w := &wan{ln: ln, target: target}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			w.mu.Lock()
			if w.cut {
				w.mu.Unlock()
				c.Close()
				continue
			}
			up, err := net.DialTimeout("tcp", w.target, 2*time.Second)
			if err != nil {
				w.mu.Unlock()
				c.Close()
				continue
			}
			w.conns = append(w.conns, c, up)
			w.mu.Unlock()
			go func() { io.Copy(up, c); up.Close(); c.Close() }()
			go func() { io.Copy(c, up); up.Close(); c.Close() }()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return w
}

func (w *wan) Cut() {
	w.mu.Lock()
	w.cut = true
	for _, c := range w.conns {
		c.Close()
	}
	w.conns = nil
	w.mu.Unlock()
}

func (w *wan) Restore()    { w.mu.Lock(); w.cut = false; w.mu.Unlock() }
func (w *wan) URL() string { return "http://" + w.ln.Addr().String() }

// cloudSite is a running cloud API + PostgreSQL + one registered site.
type cloudSite struct {
	s       *stack
	dsn     string
	port    int
	cmd     *exec.Cmd
	edgeTok string
	wan     *wan
	tok     string // cloud admin session
	email   string
	pw      string
}

func (s *stack) cloudEnv(dsn string, extra ...string) []string {
	env := append(os.Environ(), "AEROLAB_CLOUD_DATABASE_URL="+dsn)
	return append(env, extra...)
}

func (s *stack) startCloud(intentTTL, staleAfter string) *cloudSite {
	s.t.Helper()
	dsn := newPGDSN(s.t)
	c := &cloudSite{s: s, dsn: dsn, port: freePort(s.t), email: "remote-admin@example.test", pw: "remote-admin-password-1"}
	bin := buildCloud(s.t)
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = s.cloudEnv(dsn, "AEROLAB_NEW_PASSWORD="+c.pw)
		out, err := cmd.CombinedOutput()
		if err != nil {
			s.t.Fatalf("cloud %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("create-user", "-email", c.email)
	out := run("create-site", "-id", s.prov.SiteID, "-name", "IT-SITE-SIMULATED", "-admin", c.email)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	c.edgeTok = strings.TrimSpace(lines[len(lines)-1])
	c.startProc(intentTTL, staleAfter)
	c.wan = newWAN(s.t, fmt.Sprintf("127.0.0.1:%d", c.port))
	s.t.Cleanup(c.stop)
	return c
}

func (c *cloudSite) startProc(intentTTL, staleAfter string) {
	s := c.s
	cmd := exec.Command(buildCloud(s.t), "serve")
	cmd.Env = s.cloudEnv(c.dsn, fmt.Sprintf("AEROLAB_CLOUD_HTTP_ADDR=127.0.0.1:%d", c.port), "AEROLAB_CLOUD_COOKIE_SECURE=false",
		"AEROLAB_CLOUD_INTENT_TTL="+intentTTL, "AEROLAB_CLOUD_STALE_AFTER="+staleAfter, "AEROLAB_CLOUD_STATIC_DIR="+os.Getenv("AEROLAB_WEB_CLOUD"))
	logf, _ := os.Create(filepath.Join(s.dir, "cloud.log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	c.cmd = cmd
	waitTCP(s.t, c.port)
}

func (c *cloudSite) stop() {
	if c.cmd != nil {
		_ = c.cmd.Process.Signal(syscall.SIGKILL)
		_, _ = c.cmd.Process.Wait()
		c.cmd = nil
	}
}

func (c *cloudSite) call(method, path string, body, out any) int {
	c.s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", c.port, path), rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tok != "" {
		req.Header.Set("Authorization", "Bearer "+c.tok)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		c.s.t.Fatalf("cloud %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil && len(b) > 0 {
		_ = json.Unmarshal(b, out)
	}
	return resp.StatusCode
}

func (c *cloudSite) login() {
	var r struct{ Token string }
	if code := c.call("POST", "/api/v1/auth/login", map[string]string{"email": c.email, "password": c.pw}, &r); code != 200 {
		c.s.t.Fatalf("cloud login: %d", code)
	}
	c.tok = r.Token
}

func (c *cloudSite) pg() *sql.DB {
	d, err := sql.Open("pgx", c.dsn)
	if err != nil {
		c.s.t.Fatal(err)
	}
	c.s.t.Cleanup(func() { d.Close() })
	return d
}

func (c *cloudSite) count(q string, a ...any) (n int) {
	if err := c.pg().QueryRow(q, a...).Scan(&n); err != nil {
		c.s.t.Fatalf("%s: %v", q, err)
	}
	return
}

// edge env that turns on sync through the cuttable WAN proxy
func (c *cloudSite) edgeEnv(remote bool) map[string]string {
	rp := "false"
	if remote {
		rp = "true"
	}
	return map[string]string{"AEROLAB_CLOUD_SYNC_ENABLED": "true", "AEROLAB_CLOUD_URL": c.wan.URL(), "AEROLAB_CLOUD_TOKEN": c.edgeTok,
		"AEROLAB_CLOUD_ALLOW_INSECURE": "true", "AEROLAB_CLOUD_SYNC_INTERVAL": "1s", "AEROLAB_REMOTE_PROGRAMS": rp}
}

func newPGDSN(t *testing.T) string { return pgNew(t) }
