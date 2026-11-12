package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/simulator/esp32"
)

func espFaults(lowWater bool) esp32.Faults { return esp32.Faults{LowWater: lowWater} }

func (s *stack) allOnlineExcept(skip string) bool {
	for _, u := range s.units() {
		if u.ID != skip && u.Connection != "ONLINE" {
			return false
		}
	}
	return true
}

type cloudUnit struct {
	ID           string  `json:"id"`
	Connection   string  `json:"connection"`
	ControlState *string `json:"control_state"`
	Pump         *string `json:"pump_commanded"`
	SiteStale    bool    `json:"site_stale"`
	Applied      *int    `json:"applied_program_version"`
	Note         string  `json:"control_state_note"`
}

func (c *cloudSite) units() ([]cloudUnit, bool) {
	var r struct {
		Units     []cloudUnit
		SiteStale bool `json:"site_stale"`
	}
	if code := c.call("GET", "/api/v1/sites/"+c.s.prov.SiteID+"/units?limit=100", nil, &r); code != 200 {
		c.s.t.Fatalf("cloud units: %d", code)
	}
	return r.Units, r.SiteStale
}

func (c *cloudSite) intent(id string) (status, reason string, applied *int) {
	var r struct {
		Status    string
		Reason    *string `json:"reason_code"`
		Applied   *int    `json:"applied_version"`
		Confirmed bool    `json:"applied_confirmed"`
	}
	c.call("GET", "/api/v1/sites/"+c.s.prov.SiteID+"/remote-commands/"+id, nil, &r)
	if r.Reason != nil {
		reason = *r.Reason
	}
	return r.Status, reason, r.Applied
}

func (c *cloudSite) request(id string, unit string, expected int) int {
	return c.call("POST", "/api/v1/sites/"+c.s.prov.SiteID+"/remote-program-requests", map[string]any{"command_id": id, "unit_id": unit, "expected_program_version": expected, "program": prog1s}, nil)
}

func (s *stack) edgeSummaryCloud() map[string]any {
	var sum struct{ Connectivity map[string]any }
	s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/summary", s.admin(), nil, &sum)
	return sum.Connectivity
}

func (s *stack) edgeOutbox() int {
	var st struct {
		CloudSync struct {
			Pending   int   `json:"outbox_pending"`
			Reachable *bool `json:"reachable"`
		} `json:"cloud_sync"`
	}
	s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/storage", s.admin(), nil, &st)
	return st.CloudSync.Pending
}

func syncedStack(t *testing.T, n int, remote bool, intentTTL, staleAfter string) (*stack, *cloudSite) {
	s := newStack(t, n)
	s.startBroker()
	c := s.startCloud(intentTTL, staleAfter)
	s.startEdge(c.edgeEnv(remote))
	s.startFleet(n, nil)
	factoryProgram(s, n)
	s.waitUntil(20*time.Second, "units online on the Pi", func() bool { return s.allOnline(n) })
	c.login()
	return s, c
}

// A01: online - the cloud mirrors the site (state, history, incidents) and says when it last synced.
func TestCloud_A01_OnlineCloudMirrorsTheSite(t *testing.T) {
	s, c := syncedStack(t, 4, false, "30s", "10s")
	s.waitUntil(25*time.Second, "cloud shows 4 units RUNNING and fresh", func() bool {
		us, stale := c.units()
		if len(us) != 4 || stale {
			return false
		}
		for _, u := range us {
			if u.Connection != "ONLINE" || strOr(u.ControlState, "") != "RUNNING" {
				return false
			}
		}
		return true
	})
	s.waitUntil(30*time.Second, "all Pi telemetry mirrored (outbox empty, same row count)", func() bool {
		return s.edgeOutbox() == 0 && c.count(`SELECT COUNT(*) FROM telemetry_snapshots`) >= s.count(`SELECT COUNT(*) FROM telemetry_snapshots`)-8
	})
	// fresh and honest about the path
	var sum struct {
		Connectivity map[string]any
		Source       string
		SiteStale    bool `json:"site_stale"`
	}
	c.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/summary", nil, &sum)
	if sum.Source != "cloud" || sum.Connectivity["connection_path"] != "CLOUD" || sum.SiteStale || sum.Connectivity["last_cloud_sync_at"] == nil {
		t.Fatalf("%+v", sum)
	}
	ec := s.edgeSummaryCloud()
	if ec["cloud_api_reachable"] != true || ec["last_cloud_sync_at"] == nil {
		t.Fatalf("the Pi UI does not show the observed cloud link: %v", ec)
	}
	// history served from PostgreSQL
	var h struct {
		Points []any
		Sim    bool `json:"contains_simulated_data"`
	}
	if code := c.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/units/unit-sim-001/readings/history?resolution=raw", nil, &h); code != 200 || len(h.Points) == 0 || !h.Sim {
		t.Fatalf("history %d points=%d sim=%v", code, len(h.Points), h.Sim)
	}
	// duplicate-free by construction: unique keys in PostgreSQL
	if d := c.count(`SELECT COUNT(*) FROM (SELECT device_id,boot_id,sequence_number FROM telemetry_snapshots GROUP BY 1,2,3 HAVING COUNT(*)>1) x`); d != 0 {
		t.Fatalf("%d duplicate keys in the cloud", d)
	}
}

// A18/A19 + demo step 9: the site loses its internet. Local operation is
// untouched; the cloud says "stale"; on return the critical incident and the
// whole backlog arrive with their ORIGINAL times, with no duplicates or gaps.
func TestCloud_A18_A19_WANLossLocalWorksCloudStaleThenFullIdempotentRecovery(t *testing.T) {
	s, c := syncedStack(t, 4, false, "30s", "4s")
	s.waitUntil(25*time.Second, "initial sync", func() bool { us, st := c.units(); return len(us) == 4 && !st })

	cutAt := time.Now()
	c.wan.Cut()
	// local site: fully functional, controllers cycling
	before := cycles(s)
	s.fleet.Get("esp32-sim-002").SetFaults(espFaults(true))
	s.waitUntil(10*time.Second, "unit 2 PROTECTED_STOP on the Pi", func() bool { return strOr(s.unit("unit-sim-002").ControlState, "") == "PROTECTED_STOP" })
	s.waitUntil(15*time.Second, "outbox grows while offline", func() bool { return s.edgeOutbox() >= 15 })
	time.Sleep(3 * time.Second)
	for id, n := range cycles(s) {
		if id != "esp32-sim-002" && n < before[id]+2 {
			t.Errorf("%s stopped irrigating during the WAN outage", id)
		}
	}
	if !s.allOnlineExcept("unit-sim-002") {
		t.Fatal("the Pi's own view degraded because the internet is down")
	}
	var edgeSum struct{ Connectivity map[string]any }
	s.waitUntil(15*time.Second, "Pi observes the cloud as unreachable", func() bool {
		s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/summary", s.admin(), nil, &edgeSum)
		return edgeSum.Connectivity["cloud_api_reachable"] == false
	})
	// the cloud (still up for ITS users) must not pretend to be live
	s.waitUntil(15*time.Second, "cloud marks the site stale", func() bool { us, st := c.units(); return st && len(us) == 4 })
	us, _ := c.units()
	for _, u := range us {
		if u.Connection != "STALE" || strOr(u.ControlState, "") != "UNKNOWN_STALE" || u.Pump != nil || !strings.Contains(u.Note, "does not prove the pump stopped") {
			t.Fatalf("stale cloud view shows live-looking data: %+v", u)
		}
	}
	if n := c.count(`SELECT COUNT(*) FROM incidents WHERE status LIKE 'ACTIVE%' AND code='LOW_WATER'`); n != 0 {
		t.Fatalf("the cloud knows about an incident it cannot have received (%d)", n)
	}

	restoreAt := time.Now()
	c.wan.Restore()
	s.waitUntil(40*time.Second, "critical incident reaches the cloud", func() bool {
		return c.count(`SELECT COUNT(*) FROM incidents WHERE status='ACTIVE_UNACKNOWLEDGED' AND code='LOW_WATER' AND unit_id='unit-sim-002'`) == 1
	})
	s.waitUntil(60*time.Second, "backlog fully synced", func() bool { return s.edgeOutbox() == 0 })
	s.waitUntil(15*time.Second, "cloud fresh again", func() bool { _, st := c.units(); return !st })

	// nothing lost, nothing duplicated, original times preserved
	edgeRows := s.count(`SELECT COUNT(*) FROM telemetry_snapshots`)
	s.waitUntil(20*time.Second, "cloud telemetry == Pi telemetry", func() bool {
		return c.count(`SELECT COUNT(*) FROM telemetry_snapshots`) >= edgeRows-8
	})
	if d := c.count(`SELECT COUNT(*) FROM (SELECT device_id,boot_id,sequence_number FROM telemetry_snapshots GROUP BY 1,2,3 HAVING COUNT(*)>1) x`); d != 0 {
		t.Fatalf("%d duplicates", d)
	}
	// backlog rows were recorded by the Pi DURING the outage but ingested by the cloud AFTER it:
	// the two times are kept separately and the source time was not rewritten.
	late := c.count(`SELECT COUNT(*) FROM telemetry_snapshots WHERE edge_recorded_at BETWEEN $1 AND $2 AND cloud_ingested_at > $2`, cutAt.Add(time.Second), restoreAt)
	if late < 10 {
		t.Fatalf("only %d backlog rows carry an outage-time recorded_at with a later cloud ingest time", late)
	}
	s.waitUntil(15*time.Second, "a SECOND incident view is consistent (acknowledge on the Pi flows up)", func() bool {
		var list struct{ Incidents []map[string]any }
		s.call("GET", "/api/v1/sites/"+s.prov.SiteID+"/incidents?state=active", s.admin(), nil, &list)
		for _, i := range list.Incidents {
			if i["code"] == "LOW_WATER" {
				s.call("POST", "/api/v1/incidents/"+i["id"].(string)+"/acknowledge", s.admin(), nil, nil)
				return true
			}
		}
		return false
	})
	s.waitUntil(20*time.Second, "acknowledgement mirrored to the cloud", func() bool {
		return c.count(`SELECT COUNT(*) FROM incidents WHERE code='LOW_WATER' AND status='ACTIVE_ACKNOWLEDGED'`) == 1
	})
}

// Demo step 10 + A06 + A21: remote requests are intents; APPLIED only after
// the ESP32 confirms; stale and expired requests never execute.
func TestCloud_RemoteProgramRequestFlowStaleAndExpired(t *testing.T) {
	s, c := syncedStack(t, 3, true, "10s", "30s")
	s.waitUntil(25*time.Second, "cloud has the units", func() bool { us, st := c.units(); return len(us) == 3 && !st })
	dev := s.fleet.Get("esp32-sim-003")
	if v := dev.Status().ProgramVersion; v != 1 {
		t.Fatalf("precondition: factory programme v%d", v)
	}

	// 1) valid request: PENDING_SITE -> PENDING_DEVICE -> APPLIED, in that order
	if code := c.request("cmd-remote-it-0001", "unit-sim-003", 1); code != 202 {
		t.Fatalf("request: %d", code)
	}
	if st, _, ap := c.intent("cmd-remote-it-0001"); st != "PENDING_SITE" || ap != nil {
		t.Fatalf("right after the request the cloud must say PENDING_SITE, got %s", st)
	}
	seen := []string{"PENDING_SITE"}
	s.waitUntil(20*time.Second, "APPLIED", func() bool {
		st, _, _ := c.intent("cmd-remote-it-0001")
		if seen[len(seen)-1] != st {
			seen = append(seen, st)
		}
		// never APPLIED before the controller really runs v2
		if st == "APPLIED" && dev.Status().ProgramVersion != 2 {
			t.Fatalf("cloud says APPLIED but the ESP32 runs v%d", dev.Status().ProgramVersion)
		}
		return st == "APPLIED"
	})
	if strings.Join(seen, ">") != "PENDING_SITE>PENDING_DEVICE>APPLIED" && strings.Join(seen, ">") != "PENDING_SITE>APPLIED" {
		t.Fatalf("status path %v", seen)
	}
	if _, _, ap := c.intent("cmd-remote-it-0001"); ap == nil || *ap != 2 {
		t.Fatalf("applied version %v", ap)
	}
	if by := s.count(`SELECT COUNT(*) FROM commands WHERE id='cmd-remote-it-0001' AND requested_by='cloud:remote-admin@example.test'`); by != 1 {
		t.Fatal("remote requester not attributed on the Pi")
	}
	if s.fleet.Get("esp32-sim-001").Status().ProgramVersion != 1 || s.fleet.Get("esp32-sim-002").Status().ProgramVersion != 1 {
		t.Fatal("a remote change to unit 3 touched other units")
	}

	// 2) A06: the cloud still thinks the controller runs v1, but it runs v2 -> CONFLICT, nothing changes
	if code := c.request("cmd-remote-it-0002", "unit-sim-003", 1); code != 202 {
		t.Fatal(code)
	}
	s.waitUntil(15*time.Second, "CONFLICT", func() bool { st, _, _ := c.intent("cmd-remote-it-0002"); return st == "CONFLICT" })
	if v := dev.Status().ProgramVersion; v != 2 {
		t.Fatalf("a stale remote request changed the controller to v%d", v)
	}

	// 3) A21: WAN is cut when the request is made; it expires and must NEVER run late
	c.wan.Cut()
	time.Sleep(1500 * time.Millisecond)
	if code := c.request("cmd-remote-it-0003", "unit-sim-003", 2); code != 202 {
		t.Fatal(code)
	}
	time.Sleep(13 * time.Second) // > the 10 s intent TTL
	c.wan.Restore()
	s.waitUntil(20*time.Second, "EXPIRED", func() bool { st, _, _ := c.intent("cmd-remote-it-0003"); return st == "EXPIRED" })
	time.Sleep(3 * time.Second)
	if v := dev.Status().ProgramVersion; v != 2 {
		t.Fatalf("an expired request executed late: controller runs v%d", v)
	}
	if n := s.count(`SELECT COUNT(*) FROM commands WHERE id='cmd-remote-it-0003'`); n != 0 {
		t.Fatal("the Pi created a command from an expired request")
	}
}

func TestCloud_SiteThatDidNotOptInRefusesRemoteChanges(t *testing.T) {
	s, c := syncedStack(t, 2, false, "10s", "30s")
	s.waitUntil(25*time.Second, "synced", func() bool { us, st := c.units(); return len(us) == 2 && !st })
	if code := c.request("cmd-remote-it-0009", "unit-sim-001", 1); code != 202 {
		t.Fatal(code)
	}
	s.waitUntil(15*time.Second, "REJECTED", func() bool {
		st, r, _ := c.intent("cmd-remote-it-0009")
		return st == "REJECTED" && r == "REMOTE_CHANGES_DISABLED_ON_SITE"
	})
	if s.fleet.Get("esp32-sim-001").Status().ProgramVersion != 1 {
		t.Fatal("controller changed")
	}
}

// A12: a permanently offline site is a complete product: no cloud config, no outbox growth,
// reachability honestly unknown (null), everything local works.
func TestCloud_A12_PermanentlyOfflineSiteHasNoCloudDependency(t *testing.T) {
	s := newStack(t, 3)
	s.startBroker()
	s.startEdge(nil) // no cloud settings at all
	s.startFleet(3, nil)
	factoryProgram(s, 3)
	s.waitUntil(20*time.Second, "online", func() bool { return s.allOnline(3) })
	s.fleet.Get("esp32-sim-002").SetFaults(espFaults(true))
	s.waitUntil(10*time.Second, "incident", func() bool { return len(s.incidents("active")) == 1 })
	time.Sleep(3 * time.Second)
	if n := s.count(`SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Fatalf("%d outbox rows on a site with sync disabled: unbounded growth waiting for a cloud that will never come", n)
	}
	ec := s.edgeSummaryCloud()
	if ec["cloud_api_reachable"] != nil || ec["site_wan_reachable"] != nil || ec["last_cloud_sync_at"] != nil {
		t.Fatalf("an offline-by-design site must report 'unknown', not a made-up value: %v", ec)
	}
	var sess struct{ Cloud map[string]any }
	s.call("GET", "/api/v1/session", s.admin(), nil, &sess)
	if sess.Cloud["enabled"] != false {
		t.Fatalf("%v", sess.Cloud)
	}
	// the edge refuses to start sync with no credentials rather than half-working
	_, logs := s.runEdgeOnce(map[string]string{"AEROLAB_CLOUD_SYNC_ENABLED": "true", "AEROLAB_PROVISION_FILE": ""})
	if !strings.Contains(logs, "AEROLAB_CLOUD_URL") {
		t.Fatalf("misconfigured sync was accepted:\n%s", logs)
	}
	_, logs = s.runEdgeOnce(map[string]string{"AEROLAB_CLOUD_SYNC_ENABLED": "true", "AEROLAB_CLOUD_URL": "http://cloud.example.org", "AEROLAB_CLOUD_TOKEN": "x", "AEROLAB_PROVISION_FILE": ""})
	if !strings.Contains(logs, "must be https") {
		t.Fatalf("plain-http cloud URL to a remote host was accepted:\n%s", logs)
	}
}
