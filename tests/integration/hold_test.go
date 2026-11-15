package integration

import (
	"fmt"
	"os"
	"path/filepath"
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

// AEROLAB_HOLD_FLEET=<seconds>: a Pi NOT yet connected to the cloud, with self-service units enabled,
// and a cloud with sign-up open and one organisation owner, to walk through the fleet screens.
func TestHoldFleetForManualBrowsing(t *testing.T) {
	secs, _ := strconv.Atoi(os.Getenv("AEROLAB_HOLD_FLEET"))
	if secs == 0 {
		t.Skip("set AEROLAB_HOLD_FLEET=<seconds>")
	}
	s := newStack(t, 2)
	s.startBroker()
	c := s.startCloudWithoutSite()
	c.stop()
	c.extra = []string{"AEROLAB_CLOUD_SIGNUP=open"}
	c.startProc("60s", "6s")
	mdir := filepath.Join(s.dir, "dev", "mosquitto")
	s.startEdge(map[string]string{"AEROLAB_STATIC_DIR": os.Getenv("AEROLAB_WEB_LOCAL"), "AEROLAB_CLOUD_ALLOW_INSECURE": "true", "AEROLAB_CLOUD_SYNC_INTERVAL": "1s",
		"AEROLAB_BROKER_PASSWD_FILE": filepath.Join(mdir, "passwd"), "AEROLAB_BROKER_PID_FILE": filepath.Join(mdir, "mosquitto.pid")})
	s.startFleet(2, nil)
	factoryProgram(s, 2)
	s.waitUntil(20*time.Second, "online", func() bool { return s.allOnline(2) })
	fmt.Printf("HOLD edge=%d cloud=%d broker=%s\nHOLD-EDGE-PW=%s\nHOLD-CLOUD=%s / %s\n", s.edgePort, c.port, s.prov.BrokerURL, s.prov.AdminPassword, c.email, c.pw)
	time.Sleep(time.Duration(secs) * time.Second)
}
