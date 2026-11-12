// Package diagnostics holds process counters and resource introspection used
// by /readyz, /api/v1/diagnostics and the load tests. Counters are atomics;
// the reject-reason map is hard-bounded.
package diagnostics

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Started time.Time

	MQTTIn            atomic.Int64
	Rejected          atomic.Int64
	UnknownDevice     atomic.Int64
	SourceMismatch    atomic.Int64
	QueueDrops        atomic.Int64
	RecordsPersisted  atomic.Int64
	DuplicatesIgnored atomic.Int64
	ReceiptsSent      atomic.Int64
	CommitErrors      atomic.Int64
	LastCommitMicros  atomic.Int64
	StorageOK         atomic.Bool
	OpsDropped        atomic.Int64

	mu      sync.Mutex
	reasons map[string]int64
}

func New() *Metrics {
	m := &Metrics{Started: time.Now(), reasons: map[string]int64{}}
	m.StorageOK.Store(true)
	return m
}

// Reject counts a rejected message by stable reason code (max 64 distinct).
func (m *Metrics) Reject(code string) {
	m.Rejected.Add(1)
	m.mu.Lock()
	if _, ok := m.reasons[code]; ok || len(m.reasons) < 64 {
		m.reasons[code]++
	}
	m.mu.Unlock()
}

func (m *Metrics) Reasons() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.reasons))
	for k, v := range m.reasons {
		out[k] = v
	}
	return out
}

type Resources struct {
	GoHeapAllocMiB float64 `json:"go_heap_alloc_mib"`
	GoSysMiB       float64 `json:"go_sys_mib"`
	RSSMiB         float64 `json:"rss_mib"`
	Goroutines     int     `json:"goroutines"`
	GCCycles       uint32  `json:"gc_cycles"`
}

func ReadResources() Resources {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return Resources{GoHeapAllocMiB: float64(ms.HeapAlloc) / (1 << 20), GoSysMiB: float64(ms.Sys) / (1 << 20), RSSMiB: rssMiB(), Goroutines: runtime.NumGoroutine(), GCCycles: ms.NumGC}
}

// rssMiB reads VmRSS from /proc (Linux only; 0 elsewhere).
func rssMiB() float64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "VmRSS:") {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 {
				kb, _ := strconv.ParseFloat(fs[1], 64)
				return kb / 1024
			}
		}
	}
	return 0
}
