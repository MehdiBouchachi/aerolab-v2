package integration

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/simulator/esp32"
)

// A24: a reset changes boot_id and restarts seq; both boots are stored with no
// loss; a controller WITHOUT a trusted clock never gets an invented timestamp,
// one WITH an RTC is stored as DEVICE_CLOCK.
func TestA24_RebootNewBootIdNoFabricatedTimestamps(t *testing.T) {
	s := newStack(t, 2)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(2, func(c *esp32.Config) { c.RTC = c.DeviceID == "esp32-sim-002" })
	factoryProgram(s, 2)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(2) })
	s.waitUntil(10*time.Second, "telemetry from both", func() bool { return s.count(`SELECT COUNT(DISTINCT device_id) FROM telemetry_snapshots`) == 2 })

	d1 := s.fleet.Get("esp32-sim-001")
	oldBoot := d1.Status().BootID
	d1.Reboot()
	if d1.Status().BootID == oldBoot || d1.Status().PumpOn {
		t.Fatal("reboot must change boot_id and leave outputs off")
	}
	s.waitUntil(20*time.Second, "unit 1 RUNNING again with its saved programme", func() bool {
		u := s.unit("unit-sim-001")
		return u.Connection == "ONLINE" && strOr(u.ControlState, "") == "RUNNING"
	})
	s.waitUntil(10*time.Second, "records from the new boot", func() bool {
		return s.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE boot_id=?`, d1.Status().BootID) >= 2
	})
	t.Logf("recovered %s after reboot", s.waitNoLoss(30*time.Second, 8))
	if n := s.count(`SELECT COUNT(DISTINCT boot_id) FROM telemetry_snapshots WHERE device_id='esp32-sim-001'`); n != 2 {
		t.Fatalf("expected both boots stored, got %d", n)
	}
	// no-RTC device: nothing ever claims DEVICE_CLOCK or carries a sampled_at
	if n := s.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE device_id='esp32-sim-001' AND (time_quality='DEVICE_CLOCK' OR sampled_at_utc IS NOT NULL OR clock_quality<>'monotonic_only')`); n != 0 {
		t.Fatalf("%d rows of a clockless controller carry an absolute time", n)
	}
	// a record from the PREVIOUS boot can only be placed by receive time, and says so
	if n := s.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE device_id='esp32-sim-001' AND boot_id=? AND time_quality='DEVICE_CLOCK'`, oldBoot); n != 0 {
		t.Fatal("old-boot data labelled as device-clock time")
	}
	// RTC device: device time is preserved and labelled
	if n := s.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE device_id='esp32-sim-002' AND time_quality<>'DEVICE_CLOCK'`); n != 0 {
		t.Fatalf("%d RTC rows lost their device timestamp", n)
	}
	if n := s.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE device_id='esp32-sim-002' AND sampled_at_utc IS NULL`); n != 0 {
		t.Fatal("RTC rows missing sampled_at_utc")
	}
	// the BOOT event of each boot is durable history
	s.waitUntil(10*time.Second, "a BOOT event durable for each of the two boots", func() bool {
		return s.count(`SELECT COUNT(DISTINCT boot_id) FROM device_events WHERE device_id='esp32-sim-001' AND event_type='BOOT'`) == 2
	})
}

// Live updates over the real stack: a browser holding the SSE stream sees a
// low-water fault as typed events, without polling.
func TestSSELiveEventsOverRealStack(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()
	s.startEdge(nil)
	s.startFleet(3, nil)
	factoryProgram(s, 3)
	s.waitUntil(15*time.Second, "online", func() bool { return s.allOnline(3) })

	req, _ := http.NewRequest("GET", s.url("/api/v1/sites/"+s.prov.SiteID+"/events"), nil)
	req.Header.Set("Authorization", "Bearer "+s.admin())
	resp, err := (&http.Client{}).Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stream: %v %v", err, resp)
	}
	defer resp.Body.Close()
	type ev struct{ typ, data string }
	evs := make(chan ev, 256)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 64<<10)
		var typ string
		for sc.Scan() {
			l := sc.Text()
			switch {
			case strings.HasPrefix(l, "event: "):
				typ = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				evs <- ev{typ, strings.TrimPrefix(l, "data: ")}
			}
		}
		close(evs)
	}()
	time.Sleep(500 * time.Millisecond) // past the hello event
	s.fleet.Get("esp32-sim-002").SetFaults(esp32.Faults{LowWater: true})

	var sawIncident, sawProtected, sawReading bool
	deadline := time.After(12 * time.Second)
	for !(sawIncident && sawProtected && sawReading) {
		select {
		case e, ok := <-evs:
			if !ok {
				t.Fatal("stream closed")
			}
			switch e.typ {
			case "incident.updated":
				sawIncident = sawIncident || (strings.Contains(e.data, `"LOW_WATER"`) && strings.Contains(e.data, `"unit-sim-002"`))
			case "unit.state":
				sawProtected = sawProtected || (strings.Contains(e.data, `"PROTECTED_STOP"`) && strings.Contains(e.data, `"unit-sim-002"`))
			case "unit.reading":
				sawReading = sawReading || strings.Contains(e.data, `"SIMULATOR"`)
			}
		case <-deadline:
			t.Fatalf("incident=%v protected=%v reading=%v: events did not arrive", sawIncident, sawProtected, sawReading)
		}
	}
}
