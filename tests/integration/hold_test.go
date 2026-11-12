package integration

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// Manual helper: AEROLAB_HOLD=60 keeps a full synced stack alive so a browser
// can be pointed at the Pi UI (:edge) and the cloud UI (:cloud).
func TestHoldStackForManualBrowsing(t *testing.T) {
	secs, _ := strconv.Atoi(os.Getenv("AEROLAB_HOLD"))
	if secs == 0 {
		t.Skip("set AEROLAB_HOLD=<seconds>")
	}
	s := newStack(t, 4)
	s.startBroker()
	c := s.startCloud("60s", "6s")
	s.startEdge(mergeEnv(c.edgeEnv(true), map[string]string{"AEROLAB_STATIC_DIR": os.Getenv("AEROLAB_WEB_LOCAL")}))
	s.startFleet(4, nil)
	factoryProgram(s, 4)
	s.waitUntil(20*time.Second, "online", func() bool { return s.allOnline(4) })
	fmt.Printf("HOLD edge=%d cloud=%d wan_cut_file=/tmp/claude-0/cutwan\nHOLD-EDGE-PW=%s\nHOLD-CLOUD=%s / %s\n", s.edgePort, c.port, s.prov.AdminPassword, c.email, c.pw)
	end := time.Now().Add(time.Duration(secs) * time.Second)
	cut := false
	for time.Now().Before(end) {
		_, err := os.Stat("/tmp/claude-0/cutwan")
		if want := err == nil; want != cut {
			cut = want
			if cut {
				c.wan.Cut()
			} else {
				c.wan.Restore()
			}
		}
		if _, err := os.Stat("/tmp/claude-0/lowwater"); err == nil {
			s.fleet.Get("esp32-sim-002").SetFaults(espFaults(true))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func mergeEnv(a, b map[string]string) map[string]string {
	for k, v := range b {
		if v != "" {
			a[k] = v
		}
	}
	return a
}
