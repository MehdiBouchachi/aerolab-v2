package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignupIsOffUnlessTheOperatorOpensIt(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"email": "grower@example.org", "password": "long-enough-password", "organization_kind": "PERSONAL"}
	if w := e.do("POST", "/api/v1/auth/signup", "", body); w.Code != 403 || !strings.Contains(w.Body.String(), "SIGNUP_DISABLED") {
		t.Fatalf("closed signup: %d %s", w.Code, w.Body)
	}
	e.srv.Cfg.Signup = "open"
	if w := e.do("POST", "/api/v1/auth/signup", "", map[string]any{"email": "x@example.org", "password": "short"}); w.Code != 400 {
		t.Fatalf("weak password accepted: %d", w.Code)
	}
	if w := e.do("POST", "/api/v1/auth/signup", "", map[string]any{"email": "g@example.org", "password": "long-enough-password", "organization_kind": "GOVERNMENT"}); w.Code != 400 {
		t.Fatalf("government org without a name: %d", w.Code)
	}
	w := e.do("POST", "/api/v1/auth/signup", "", body)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Token        string
		Organization struct{ ID, Kind string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Token == "" || out.Organization.Kind != "PERSONAL" {
		t.Fatalf("%+v", out)
	}
	if w := e.do("POST", "/api/v1/auth/signup", "", body); w.Code != 409 {
		t.Fatalf("duplicate account: %d", w.Code)
	}
	w = e.do("GET", "/api/v1/orgs", out.Token, nil)
	if !strings.Contains(w.Body.String(), `"role":"OWNER"`) {
		t.Fatalf("orgs: %s", w.Body)
	}
}

// A cooperative owner connects two Pis with one-time codes; a member only sees the Pi they were given.
func TestClaimCodesConnectPisAndFleetOverview(t *testing.T) {
	e := newEnv(t)
	owner := e.user("owner@coop.example")
	worker := e.user("worker@coop.example")
	stranger := e.user("other@farm.example")
	_ = worker
	e.exec(`INSERT INTO organizations(id,name,kind) VALUES ('org-coop','Coop','BUSINESS')`)
	e.exec(`INSERT INTO organization_members(org_id,user_id,role) VALUES ('org-coop',$1,'OWNER')`, owner)

	if w := e.do("POST", "/api/v1/orgs/org-coop/site-claims", e.tok["other@farm.example"], nil); w.Code != 403 {
		t.Fatalf("non-member created a claim code: %d", w.Code)
	}
	claim := func() string {
		w := e.do("POST", "/api/v1/orgs/org-coop/site-claims", e.tok["owner@coop.example"], nil)
		if w.Code != 201 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var c struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		return c.Code
	}
	code := claim()
	if len(code) != 14 || strings.ContainsAny(code, "01OIL") {
		t.Fatalf("code %q not readable", code)
	}
	bad := e.do("POST", "/api/v1/edge/claim", "", map[string]any{"code": "AAAA-BBBB-CCCC", "site_id": "site-north-01", "site_name": "North"})
	if bad.Code != 400 {
		t.Fatalf("wrong code: %d", bad.Code)
	}
	// typed in lower case with spaces still works
	w := e.do("POST", "/api/v1/edge/claim", "", map[string]any{"code": " " + strings.ToLower(strings.ReplaceAll(code, "-", " ")) + " ", "site_id": "site-north-01", "site_name": "North greenhouse", "edge_version": "edge-test"})
	if w.Code != 201 {
		t.Fatalf("claim: %d %s", w.Code, w.Body)
	}
	var got struct {
		Token  string `json:"token"`
		SiteID string `json:"site_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !strings.HasPrefix(got.Token, "aeroedge_") {
		t.Fatalf("token %q", got.Token)
	}
	if w := e.do("POST", "/api/v1/edge/claim", "", map[string]any{"code": code, "site_id": "site-south-01", "site_name": "South"}); w.Code != 400 {
		t.Fatalf("code reused: %d", w.Code)
	}
	// the same Pi reconnecting (disconnected / reinstalled) with a new code from ITS organisation is
	// re-linked and the old token stops working
	w2 := e.do("POST", "/api/v1/edge/claim", "", map[string]any{"code": claim(), "site_id": "site-north-01", "site_name": "North greenhouse"})
	if w2.Code != 201 {
		t.Fatalf("reconnect: %d %s", w2.Code, w2.Body)
	}
	if w := e.do("POST", "/api/v1/edge/sync/batch", got.Token, batch("site-north-01", nil)); w.Code != 401 {
		t.Fatalf("old token still accepted after re-link: %d", w.Code)
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &got)
	// another organisation can never take the site over
	e.exec(`INSERT INTO organizations(id,name,kind) VALUES ('org-other','Other','BUSINESS')`)
	e.exec(`INSERT INTO organization_members(org_id,user_id,role) VALUES ('org-other',$1,'OWNER')`, stranger)
	var oc struct{ Code string }
	_ = json.Unmarshal(e.do("POST", "/api/v1/orgs/org-other/site-claims", e.tok["other@farm.example"], nil).Body.Bytes(), &oc)
	if w := e.do("POST", "/api/v1/edge/claim", "", map[string]any{"code": oc.Code, "site_id": "site-north-01", "site_name": "Hijack"}); w.Code != 409 {
		t.Fatalf("another organisation took the site: %d %s", w.Code, w.Body)
	}
	if n := e.count(`SELECT COUNT(*) FROM site_members WHERE site_id='site-north-01' AND user_id=$1`, stranger); n != 0 {
		t.Fatal("hijack attempt granted access")
	}
	// the new token syncs like any edge token
	e.edge["site-north-01"] = got.Token
	tomato := func(id, conn string, inc int) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"id": id, "name": id, "connection": conn, "active_incidents": map[string]int{"warning": inc},
			"crop": map[string]any{"profile_id": "crop-1", "profile_name": "Grower A", "crop": "Tomato"}})
		return b
	}
	bare, _ := json.Marshal(map[string]any{"id": "unit-3", "name": "u3", "connection": "STALE", "active_incidents": map[string]int{}, "crop": nil})
	st := state(time.Now(), tomato("unit-1", "ONLINE", 0), tomato("unit-2", "ONLINE", 2), bare)
	st.UnitsComplete = true
	if w := e.do("POST", "/api/v1/edge/sync/batch", got.Token, batch("site-north-01", st)); w.Code != 200 {
		t.Fatalf("sync with claimed token: %d %s", w.Code, w.Body)
	}

	w = e.do("GET", "/api/v1/fleet", e.tok["owner@coop.example"], nil)
	var f struct {
		Organizations []struct {
			Kind  string
			Sites []fleetSite
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &f)
	if len(f.Organizations) != 1 || f.Organizations[0].Kind != "BUSINESS" || len(f.Organizations[0].Sites) != 1 {
		t.Fatalf("fleet: %s", w.Body)
	}
	s := f.Organizations[0].Sites[0]
	if s.Role != "ADMIN" || s.UnitsTotal != 3 || s.UnitsOnline != 2 || s.UnitsAttention != 2 || s.UnitsNoCrop != 1 ||
		len(s.Crops) != 1 || s.Crops[0] != (cropCount{"Tomato", 2}) || s.ActiveIncidents["warning"] != 2 || s.Stale {
		t.Fatalf("site summary %+v", s)
	}
	// a member of the org sees nothing until given a site role; a stranger never does
	for _, who := range []string{"worker@coop.example", "other@farm.example"} {
		w = e.do("GET", "/api/v1/fleet", e.tok[who], nil)
		if strings.Contains(w.Body.String(), "site-north-01") {
			t.Fatalf("%s sees a Pi without a role: %s", who, w.Body)
		}
	}
	if w := e.do("PUT", "/api/v1/orgs/org-coop/members", e.tok["other@farm.example"], map[string]any{"email": "other@farm.example", "role": "OWNER"}); w.Code != 403 {
		t.Fatalf("stranger made themselves owner: %d", w.Code)
	}
	if w := e.do("PUT", "/api/v1/orgs/org-coop/members", e.tok["owner@coop.example"], map[string]any{"email": "worker@coop.example", "role": "MEMBER"}); w.Code != 200 {
		t.Fatalf("add member: %d %s", w.Code, w.Body)
	}
}

func TestClaimAttemptsAreRateLimited(t *testing.T) {
	e := newEnv(t)
	last := 0
	for i := 0; i < 12; i++ {
		last = e.do("POST", "/api/v1/edge/claim", "", map[string]any{"code": "WRONG-CODE-XXXX", "site_id": "site-x-01", "site_name": "X"}).Code
	}
	if last != 429 {
		t.Fatalf("guessing claim codes is not rate limited (last status %d)", last)
	}
}
