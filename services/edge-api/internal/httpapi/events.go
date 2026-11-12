package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/sse"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
)

// events streams typed server-sent events. Clients must refetch a snapshot
// after (re)connecting: the stream carries no replay and a dropped slow
// client simply reconnects (bounded memory on the Pi).
func (s *Server) events(w http.ResponseWriter, r *http.Request, u auth.User) {
	if !s.siteOK(w, r) {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, 500, "INTERNAL_ERROR", "streaming unsupported", nil)
		return
	}
	sub, err := s.Hub.Subscribe(u.ID)
	if err != nil {
		writeError(w, r, 429, "RATE_LIMITED", "too many open event streams", nil)
		return
	}
	defer sub.Close()
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	write := func(format string, a ...any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(w, format, a...); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !write("retry: 3000\nevent: hello\ndata: {\"source\":\"local\",\"site_id\":%q,\"server_time\":%q,\"last_event_id\":%d,\"note\":\"fetch a fresh snapshot now; the stream does not replay\"}\n\n",
		s.Cfg.SiteID, storage.Now(), s.Hub.LastID()) {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-sub.C:
			if !open { // dropped for being too slow
				return
			}
			if !write("id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Data) {
				return
			}
		case <-ping.C:
			if !write(": ping\n\n") {
				return
			}
		}
	}
}

var _ = sse.QueueLen
