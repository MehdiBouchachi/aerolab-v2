package esp32

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

// Fleet runs N independent devices (each with its own identity, programme,
// boot, journal, seed and reconnect jitter).
type Fleet struct {
	mu      sync.RWMutex
	devices map[string]*Device
	order   []string
}

func NewFleet() *Fleet { return &Fleet{devices: map[string]*Device{}} }

func (f *Fleet) Add(d *Device) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[d.cfg.DeviceID] = d
	f.order = append(f.order, d.cfg.DeviceID)
}

func (f *Fleet) Get(id string) *Device {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.devices[id]
}

func (f *Fleet) All() []*Device {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*Device, 0, len(f.order))
	for _, id := range f.order {
		out = append(out, f.devices[id])
	}
	return out
}

func (f *Fleet) Start(ctx context.Context) {
	for _, d := range f.All() {
		d.Start(ctx)
	}
}

func (f *Fleet) Stop() {
	var wg sync.WaitGroup
	for _, d := range f.All() {
		wg.Add(1)
		go func(d *Device) { defer wg.Done(); d.Stop() }(d)
	}
	wg.Wait()
}

// SetNetwork toggles the "WAN/Wi-Fi/broker" reachability of all devices.
func (f *Fleet) SetNetwork(up bool) {
	for _, d := range f.All() {
		d.SetNetwork(up)
	}
}

// ControlHandler exposes fault injection for scenario scripts. It is meant to
// be bound to loopback only; it does not exist on real firmware.
//
//	GET  /devices
//	POST /network                    {"up":bool}              all devices
//	POST /devices/{id}/network       {"up":bool}
//	POST /devices/{id}/faults        {"low_water":true,...}
//	POST /devices/{id}/recover       local physical recovery confirmation
//	POST /devices/{id}/reboot
//	POST /devices/{id}/drop-results  {"n":2}
func (f *Fleet) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/devices", func(w http.ResponseWriter, r *http.Request) {
		var out []Status
		for _, d := range f.All() {
			out = append(out, d.Status())
		}
		writeJSON(w, 200, map[string]any{"data_source": mc.SourceSimulator, "devices": out})
	})
	mux.HandleFunc("/network", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Up bool }
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b) != nil {
			writeJSON(w, 400, map[string]string{"error": "POST {\"up\":bool}"})
			return
		}
		f.SetNetwork(b.Up)
		writeJSON(w, 200, map[string]bool{"up": b.Up})
	})
	mux.HandleFunc("/devices/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/devices/"), "/"), "/")
		if len(parts) != 2 || r.Method != http.MethodPost {
			writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		d := f.Get(parts[0])
		if d == nil {
			writeJSON(w, 404, map[string]string{"error": "unknown device"})
			return
		}
		body := http.MaxBytesReader(w, r.Body, 4096)
		switch parts[1] {
		case "network":
			var b struct{ Up bool }
			if json.NewDecoder(body).Decode(&b) != nil {
				writeJSON(w, 400, map[string]string{"error": "bad body"})
				return
			}
			d.SetNetwork(b.Up)
		case "faults":
			var fl Faults
			if json.NewDecoder(body).Decode(&fl) != nil {
				writeJSON(w, 400, map[string]string{"error": "bad body"})
				return
			}
			d.SetFaults(fl)
		case "recover":
			if !d.ConfirmRecovery() {
				writeJSON(w, 409, map[string]string{"error": "not in PROTECTED_STOP or fault still present"})
				return
			}
		case "reboot":
			d.Reboot()
		case "drop-results":
			var b struct{ N int }
			if json.NewDecoder(body).Decode(&b) != nil {
				writeJSON(w, 400, map[string]string{"error": "bad body"})
				return
			}
			d.DropNextResults(b.N)
		default:
			writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, 200, d.Status())
	})
	return mux
}

// WaitFor polls cond until true or timeout (helper for scenarios / tests).
func WaitFor(timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within %s", timeout)
}
