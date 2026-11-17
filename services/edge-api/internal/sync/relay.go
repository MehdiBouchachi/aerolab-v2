package sync

// Relay loop: the Pi keeps one outbound call open to the cloud (GET /api/v1/edge/relay/next, held up to
// ~10 s), runs whatever dashboard call arrives through its own HTTP handlers (httpapi.ServeRelay, which
// applies the allow-list and the member's role), and posts the answer back. It runs beside the sync loop,
// never blocks it, and stops with it. Losing the cloud only means remote users see "site offline".

import (
	"context"
	"time"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
)

// RelayFunc executes one forwarded call (httpapi.Server.ServeRelay).
type RelayFunc func(context.Context, sc.RelayRequest) sc.RelayResponse

// SetRelay enables the relay loop. Call before Run; nil leaves it off.
func (w *Worker) SetRelay(f RelayFunc) { w.relay = f }

func (w *Worker) runRelay(ctx context.Context) {
	failures := 0
	for ctx.Err() == nil {
		started := time.Now()
		var req sc.RelayRequest
		if err := w.call(ctx, "GET", "/api/v1/edge/relay/next", nil, &req); err != nil {
			failures++
			if failures == 1 {
				w.log.Warn("cloud relay unavailable; remote dashboard users will see the site as offline", "err", err)
			}
			if !wait(ctx, backoff(w.opt.Interval, failures)) {
				return
			}
			continue
		}
		if failures > 0 {
			w.log.Info("cloud relay recovered", "after_failures", failures)
		}
		failures = 0
		if req.ID == "" { // 204: nothing waiting
			if time.Since(started) < time.Second && !wait(ctx, time.Second) { // the cloud did not hold the call open
				return
			}
			continue
		}
		resp := w.relay(ctx, req)
		w.log.Info("relayed dashboard call", "id", req.ID, "method", req.Method, "path", req.Path, "by", req.RequestedBy, "status", resp.Status)
		if err := w.call(ctx, "POST", "/api/v1/edge/relay/results", resp, nil); err != nil {
			w.log.Warn("could not return a relayed answer; the remote user will see 'outcome unknown'", "id", req.ID, "err", err)
		}
	}
}

func wait(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
