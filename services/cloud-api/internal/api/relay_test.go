package api

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	sc "github.com/mehdibouchachi/aerolab-v2/contracts/sync"
)

// fakePi answers one relayed call the way the Pi's relay loop does.
func fakePi(t *testing.T, e *env, site string, answer func(sc.RelayRequest) sc.RelayResponse) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			w := e.do("GET", "/api/v1/edge/relay/next", e.edge[site], nil)
			if w.Code == 204 {
				continue
			}
			if w.Code != 200 {
				t.Errorf("next: %d %s", w.Code, w.Body)
				return
			}
			var q sc.RelayRequest
			_ = json.Unmarshal(w.Body.Bytes(), &q)
			if r := e.do("POST", "/api/v1/edge/relay/results", e.edge[site], answer(q)); r.Code != 204 {
				t.Errorf("result: %d %s", r.Code, r.Body)
			}
			return
		}
		t.Error("no relayed request arrived")
	}()
	return &wg
}

func TestRelayForwardsToThePiAndReturnsItsAnswer(t *testing.T) {
	e := newEnv(t)
	u := e.user("owner@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", u, "OPERATOR")
	e.exec(`UPDATE sites SET last_edge_seen_at=now() WHERE id='site-a-0001'`)

	var got sc.RelayRequest
	wg := fakePi(t, e, "site-a-0001", func(q sc.RelayRequest) sc.RelayResponse {
		got = q
		return sc.RelayResponse{ID: q.ID, Status: 201, Body: json.RawMessage(`{"id":"crop-1"}`)}
	})
	w := e.do("POST", "/api/v1/sites/site-a-0001/edge/sites/site-a-0001/crop-profiles?x=1", e.tok["owner@x.test"], map[string]any{"name": "Basil"})
	wg.Wait()
	if w.Code != 201 || !strings.Contains(w.Body.String(), "crop-1") {
		t.Fatalf("answer not passed through: %d %s", w.Code, w.Body)
	}
	if got.Method != "POST" || got.Path != "/api/v1/sites/site-a-0001/crop-profiles?x=1" || got.Role != "OPERATOR" || got.RequestedBy != "owner@x.test" || !strings.Contains(string(got.Body), "Basil") {
		t.Fatalf("forwarded wrongly: %+v", got)
	}
	// delivered bodies are not kept (they can hold one-time setup codes)
	if n := e.count(`SELECT COUNT(*) FROM relay_requests WHERE body IS NOT NULL OR response_body IS NOT NULL`); n != 0 {
		t.Fatalf("%d relay rows still hold bodies", n)
	}
}

func TestRelayIsOnlyForMembersAndOnlyToTheirSite(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@x.test")
	e.user("stranger@x.test")
	e.site("site-a-0001")
	e.site("site-b-0001")
	e.member("site-a-0001", u, "ADMIN")
	e.exec(`UPDATE sites SET last_edge_seen_at=now()`)
	if w := e.do("GET", "/api/v1/sites/site-a-0001/edge/sites/site-a-0001/storage", e.tok["stranger@x.test"], nil); w.Code != 404 {
		t.Fatalf("non-member: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/sites/site-a-0001/edge/sites/site-a-0001/storage", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	// site B's Pi never receives site A's request
	done := make(chan struct{})
	go func() {
		e.do("GET", "/api/v1/sites/site-a-0001/edge/sites/site-a-0001/storage", e.tok["a@x.test"], nil)
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	if w := e.do("GET", "/api/v1/edge/relay/next", e.edge["site-b-0001"], nil); w.Code != 204 {
		t.Fatalf("site B got another site's request: %d %s", w.Code, w.Body)
	}
	<-done // site A's request expires unanswered
}

func TestRelaySaysOfflineAtOnceAndNeverRunsLate(t *testing.T) {
	e := newEnv(t)
	u := e.user("a@x.test")
	e.site("site-a-0001")
	e.member("site-a-0001", u, "ADMIN")
	// never connected: immediate 503, nothing stored
	if w := e.do("PATCH", "/api/v1/sites/site-a-0001/edge/units/u1", e.tok["a@x.test"], map[string]any{"name": "x"}); w.Code != 503 || !strings.Contains(w.Body.String(), "SITE_OFFLINE") {
		t.Fatalf("offline site: %d %s", w.Code, w.Body)
	}
	if n := e.count(`SELECT COUNT(*) FROM relay_requests`); n != 0 {
		t.Fatalf("offline request stored: %d", n)
	}
	// connected but the Pi does not pick it up: 504 after the pickup window, and the row can no longer be claimed
	e.exec(`UPDATE sites SET last_edge_seen_at=now() WHERE id='site-a-0001'`)
	w := e.do("PATCH", "/api/v1/sites/site-a-0001/edge/units/u1", e.tok["a@x.test"], map[string]any{"name": "x"})
	if w.Code != 504 || !strings.Contains(w.Body.String(), "SITE_UNREACHABLE") {
		t.Fatalf("unpicked request: %d %s", w.Code, w.Body)
	}
	if n := e.count(`SELECT COUNT(*) FROM relay_requests WHERE status='EXPIRED'`); n != 1 {
		t.Fatalf("not expired: %d", n)
	}
	if w := e.do("GET", "/api/v1/edge/relay/next", e.edge["site-a-0001"], nil); w.Code != 204 {
		t.Fatalf("an expired request was handed out: %d %s", w.Code, w.Body)
	}
}

func TestMetaSaysCloud(t *testing.T) {
	e := newEnv(t)
	if w := e.do("GET", "/api/v1/meta", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"cloud"`) {
		t.Fatal(w.Code, w.Body)
	}
}
