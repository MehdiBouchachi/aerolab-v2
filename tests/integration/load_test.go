package integration

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/simulator/esp32"
)

// Load tests are opt-in (they take minutes):
//
//	AEROLAB_LOAD=1 go test -run TestLoad -v -timeout 30m ./integration/
//
// AEROLAB_LOAD_DEVICES=20  AEROLAB_LOAD_SECS=60  AEROLAB_LOAD_REALRATE=1
//
// What is measured is printed and ALSO appended to AEROLAB_LOAD_REPORT if set.
// These numbers are from whatever machine runs them - they say nothing about
// a Raspberry Pi 5 until run on one.

type proc struct{ rssMiB, hwmMiB, cpuSec float64 }

func readProc(pid int) proc {
	var p proc
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && (f[0] == "VmRSS:" || f[0] == "VmHWM:") {
				kb, _ := strconv.ParseFloat(f[1], 64)
				if f[0] == "VmRSS:" {
					p.rssMiB = kb / 1024
				} else {
					p.hwmMiB = kb / 1024
				}
			}
		}
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		s := string(b)
		s = s[strings.LastIndex(s, ")")+2:]
		f := strings.Fields(s)
		if len(f) > 13 {
			u, _ := strconv.ParseFloat(f[11], 64)
			sy, _ := strconv.ParseFloat(f[12], 64)
			p.cpuSec = (u + sy) / 100 // USER_HZ = 100
		}
	}
	return p
}

func pct(xs []time.Duration, p float64) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[int(float64(len(xs)-1)*p)]
}

func runLoad(t *testing.T, n int, secs int, hb, tel time.Duration, label string) {
	s := newStack(t, n)
	s.startBroker()
	s.startEdge(map[string]string{"AEROLAB_STALE_AFTER": "15s"})
	s.startFleet(n, func(c *esp32.Config) {
		c.HeartbeatEvery, c.TelemetryEvery = hb, tel
		c.ReceiptTimeout = 15 * time.Second
		c.ReconnectMax = 5 * time.Second
	})
	for _, d := range s.fleet.All() {
		_ = d.Provision(mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 2, OffDurationSeconds: 5, MaxContinuousOnSecs: 3, RequireReservoirOK: true}, 1)
	}
	s.waitUntil(60*time.Second, fmt.Sprintf("%d units ONLINE", n), func() bool { return s.allOnline(n) })

	edgePID, brokerPID := s.edge.Process.Pid, s.broker.Process.Pid
	e0, b0 := readProc(edgePID), readProc(brokerPID)
	rows0 := s.count(`SELECT COUNT(*) FROM telemetry_snapshots`)
	start := time.Now()

	// UI/API traffic during the run: 3 "browsers" polling the 20-unit list
	var lat []time.Duration
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(500 * time.Millisecond):
				t0 := time.Now()
				var r struct{ Units []unitJSON }
				s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/units?limit=100", s.admin(), nil, &r)
				lat = append(lat, time.Since(t0))
			}
		}
	}()
	time.Sleep(time.Duration(secs) * time.Second)
	close(stop)
	<-done
	elapsed := time.Since(start)
	e1, b1 := readProc(edgePID), readProc(brokerPID)
	rows1 := s.count(`SELECT COUNT(*) FROM telemetry_snapshots`)

	// replay: take the Pi away for 20 s then bring it back
	s.killEdge()
	time.Sleep(20 * time.Second)
	t1 := time.Now()
	s.startEdge(map[string]string{"AEROLAB_STALE_AFTER": "15s"})
	s.waitUntil(60*time.Second, "units ONLINE after Pi restart", func() bool { return s.allOnline(n) })
	onlineAfter := time.Since(t1)
	recovered := s.waitNoLoss(120*time.Second, 10)
	e2 := readProc(s.edge.Process.Pid)
	b2 := readProc(brokerPID)

	var diag struct {
		Ingest    map[string]any `json:"ingest"`
		Resources map[string]any `json:"resources"`
	}
	s.call("GET", "/api/v1/diagnostics", s.admin(), nil, &diag)
	fi, _ := os.Stat(s.dbPath())
	wal, _ := os.Stat(s.dbPath() + "-wal")
	walSz := int64(0)
	if wal != nil {
		walSz = wal.Size()
	}
	msgsPerSec := float64(n) * (float64(time.Second)/float64(hb) + float64(time.Second)/float64(tel))

	rep := fmt.Sprintf(`### %s - %d simulated controllers (data_source=SIMULATOR)

| Metric | Value |
|---|---|
| Machine | %d CPUs, %s |
| Offered MQTT load | state every %s + snapshot every %s per device = **%.1f msg/s** (spec baseline: %.1f msg/s for this N) |
| Steady-state window | %.0f s |
| Telemetry rows persisted in window | %d (%.1f/s) |
| Edge RSS (start / end / peak HWM) | %.1f / %.1f / **%.1f MiB** (budget: 192 soft, 256 hard) |
| Mosquitto RSS (start / end / peak HWM) | %.1f / %.1f / **%.1f MiB** (budget 64) |
| Edge CPU (avg over window) | %.1f%% of one core |
| Mosquitto CPU (avg over window) | %.1f%% of one core |
| API p50 / p95 / max (GET units, N=%d) | %s / %s / %s |
| 20 s Pi outage (SIGKILL) -> all units ONLINE | %s |
| Pi outage -> every buffered record durable (no gaps/dups) | %s |
| Edge peak RSS after replay (HWM) | %.1f MiB |
| DB size / WAL | %.1f MiB / %.1f MiB |
| Ingest: persisted / dup-ignored / rejected / queue-drops / commit errors | %v / %v / %v / %v / %v |
| Last batch commit | %v us |
`, label, n, readCPUs(), runtimeVer(), hb, tel, msgsPerSec, float64(n)*(0.2+1.0/60), elapsed.Seconds(), rows1-rows0, float64(rows1-rows0)/elapsed.Seconds(),
		e0.rssMiB, e1.rssMiB, e2.hwmMiB, b0.rssMiB, b1.rssMiB, b2.hwmMiB, (e1.cpuSec-e0.cpuSec)/elapsed.Seconds()*100, (b1.cpuSec-b0.cpuSec)/elapsed.Seconds()*100,
		n, pct(lat, .5).Round(time.Microsecond), pct(lat, .95).Round(time.Microsecond), pct(lat, 1).Round(time.Microsecond), onlineAfter.Round(100*time.Millisecond), recovered.Round(100*time.Millisecond),
		e2.hwmMiB, float64(fi.Size())/(1<<20), float64(walSz)/(1<<20),
		diag.Ingest["records_persisted"], diag.Ingest["duplicates_ignored"], diag.Ingest["rejected"], diag.Ingest["queue_drops"], diag.Ingest["commit_errors"], diag.Ingest["last_commit_micros"])
	t.Log("\n" + rep)
	if f := os.Getenv("AEROLAB_LOAD_REPORT"); f != "" {
		if fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fh.WriteString(rep + "\n")
			fh.Close()
		}
	}
	// hard guard-rails (the documented budgets)
	if e2.hwmMiB > 256 {
		t.Errorf("edge peak RSS %.1f MiB exceeds the 256 MiB hard budget", e2.hwmMiB)
	}
	if b2.hwmMiB > 64 {
		t.Errorf("mosquitto peak RSS %.1f MiB exceeds the 64 MiB budget", b2.hwmMiB)
	}
	if v := diag.Ingest["queue_drops"]; v != nil && v.(float64) != 0 {
		t.Errorf("queue drops under load: %v", v)
	}
}

func readCPUs() int { return runtimeNumCPU() }

func TestLoad(t *testing.T) {
	if os.Getenv("AEROLAB_LOAD") != "1" {
		t.Skip("set AEROLAB_LOAD=1 to run the load test")
	}
	n, _ := strconv.Atoi(os.Getenv("AEROLAB_LOAD_DEVICES"))
	if n == 0 {
		n = 10
	}
	secs, _ := strconv.Atoi(os.Getenv("AEROLAB_LOAD_SECS"))
	if secs == 0 {
		secs = 45
	}
	if os.Getenv("AEROLAB_LOAD_REALRATE") == "1" {
		runLoad(t, n, secs, 5*time.Second, 60*time.Second, "REAL-RATE (state 5 s, snapshot 60 s)")
		return
	}
	runLoad(t, n, secs, 500*time.Millisecond, time.Second, "ACCELERATED STRESS (state 0.5 s = 10x, snapshot 1 s = 60x the spec rate)")
}
