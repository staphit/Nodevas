package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/agents"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func stepUpStatusOf(t *testing.T, s *runnerTestServer, b *browser) runnerwire.StepUpStatus {
	t.Helper()
	response := s.do(t, b, http.MethodGet, "/api/auth/step-up", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET step-up = %d %s", response.Code, response.Body)
	}
	var status runnerwire.StepUpStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

// stepUp runs the whole step-up for b: request, read the mail, verify.
func stepUp(t *testing.T, s *runnerTestServer, b browser) runnerwire.StepUpStatus {
	t.Helper()
	before := s.inbox.count()
	if response := s.do(t, &b, http.MethodPost, "/api/auth/step-up/request", `{}`, nil); response.Code != http.StatusAccepted {
		t.Fatalf("step-up request = %d %s", response.Code, response.Body)
	}
	s.inbox.waitFor(t, before+1)
	code := s.inbox.code(t)
	response := s.do(t, &b, http.MethodPost, "/api/auth/step-up", `{"code":"`+code+`"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("step-up verify = %d %s", response.Code, response.Body)
	}
	var status runnerwire.StepUpStatus
	_ = json.Unmarshal(response.Body.Bytes(), &status)
	return status
}

func TestStepUpOverHTTP(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")

	if status := stepUpStatusOf(t, s, &bea); !status.Required || status.Active || status.Until != "" {
		t.Fatalf("before = %+v", status)
	}
	// CSRF applies as to every other write.
	noCSRF := browser{cookies: bea.cookies}
	if code := s.do(t, &noCSRF, http.MethodPost, "/api/auth/step-up/request", `{}`, nil).Code; code != http.StatusForbidden {
		t.Fatalf("step-up request without CSRF = %d", code)
	}
	if code := s.do(t, nil, http.MethodGet, "/api/auth/step-up", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("step-up status signed out = %d", code)
	}

	before := s.inbox.count()
	if code := s.do(t, &bea, http.MethodPost, "/api/auth/step-up/request", "", nil).Code; code != http.StatusAccepted {
		t.Fatalf("step-up request with no body = %d", code)
	}
	s.inbox.waitFor(t, before+1)
	if mail := s.inbox.last(t); mail.to != "bea@example.test" || !strings.Contains(mail.body, "step-up") {
		t.Fatalf("mail = %+v", mail)
	}
	if code := s.do(t, &bea, http.MethodPost, "/api/auth/step-up", `{"code":"WRONGCOD"}`, nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d", code)
	}
	code := s.inbox.code(t)
	response := s.do(t, &bea, http.MethodPost, "/api/auth/step-up", `{"code":"`+strings.ToLower(code[:4])+"-"+code[4:]+`"}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("verify = %d %s", response.Code, response.Body)
	}
	if status := stepUpStatusOf(t, s, &bea); !status.Required || !status.Active || status.Until == "" {
		t.Fatalf("after = %+v", status)
	}
	// Another person's session is not stepped up.
	cal := s.signIn(t, "cal@example.test")
	if status := stepUpStatusOf(t, s, &cal); status.Active {
		t.Fatalf("cal = %+v", status)
	}

	ann := s.signIn(t, testEmail)
	trail := s.do(t, &ann, http.MethodGet, "/api/audit?scope=server&limit=200", "", nil).Body.String()
	for _, action := range []string{"auth.stepup.request", "auth.stepup.failed", `"auth.stepup"`} {
		if !strings.Contains(trail, action) {
			t.Errorf("audit lacks %s", action)
		}
	}
	if strings.Contains(trail, code) {
		t.Fatal("the passcode reached the audit trail")
	}
}

func TestStepUpOnLoopbackIsNotRequired(t *testing.T) {
	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	server := serverForTest(t, pm, nil, nil)
	server.UseRunnersDB(database)
	s := &runnerTestServer{server: server, handler: server.Handler()}
	if status := stepUpStatusOf(t, s, nil); status.Required || !status.Active {
		t.Fatalf("loopback status = %+v", status)
	}
	if code := s.do(t, nil, http.MethodPost, "/api/auth/step-up/request", `{}`, nil).Code; code != http.StatusNotFound {
		t.Fatalf("loopback request = %d", code)
	}
	if !server.runs.SteppedUp(runs.Record{}) {
		t.Fatal("a loopback offer does not vouch")
	}
}

func TestOffersCarryTheDispatchersStepUp(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(2))

	plain := f.dispatch(t, f.bea, f.runBody(""))
	if expectOffer(t, conn, plain.ID).SteppedUp {
		t.Fatal("an offer vouched before any step-up")
	}
	stepUp(t, f.runnerTestServer, f.bea)
	stepped := f.dispatch(t, f.bea, f.runBody(""))
	if !expectOffer(t, conn, stepped.ID).SteppedUp {
		t.Fatal("an offer after a step-up did not vouch")
	}
}

func TestExternalSessionChangesThroughTheRunnerProxy(t *testing.T) {
	f := newRunsFixture(t)
	bearer := map[string]string{"Authorization": "Bearer " + f.runner.Token}
	proxy := runnerwire.ProxyPathPrefix
	post := func(path, body string) map[string]any {
		t.Helper()
		response := f.do(t, nil, http.MethodPost, proxy+path, body, bearer)
		if response.Code != http.StatusOK {
			t.Fatalf("POST %s = %d %s", path, response.Code, response.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out
	}
	changes := func(b *browser) (runnerwire.SessionChangesResponse, int) {
		response := f.do(t, b, http.MethodGet, "/api/nodes/"+runsTestNode+"/session-changes", "", nil)
		var out runnerwire.SessionChangesResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out, response.Code
	}

	post("agents/heartbeat", `{"sessionId":"claude:s1","name":"bea-claude","agent":"claude"}`)
	if out := post("agents/sessions/claude:s1/changes", `{"files":["a.go"]}`); out["recorded"] != false {
		t.Fatalf("recorded without a node: %v", out)
	}

	post("nodes/"+runsTestNode+"/claim", `{"owner":"mcp:claude"}`)
	if out := post("agents/heartbeat", `{"sessionId":"claude:s1","nodeId":"`+runsTestNode+`"}`); out["claimedNodeId"] != runsTestNode {
		t.Fatalf("heartbeat = %v", out)
	}
	peer := dialHubAs(t, f.liveURL, f.bea)
	if out := post("agents/sessions/claude:s1/changes", `{"files":["a.go","b.go"]}`); out["recorded"] != true || out["nodeId"] != runsTestNode {
		t.Fatalf("changes = %v", out)
	}
	peer.await(agents.SessionChangesEvent, func(e realtime.Event) bool { return e.ID == runsTestNode })
	post("agents/sessions/claude:s1/changes",
		`{"snapshot":{"commit":"","base":"abc123","files":[{"path":"a.go","status":"modified","additions":1,"deletions":0}],"additions":1,"deletions":0,"patch":"diff"}}`)

	// Anyone who can see the node sees them, cal included.
	for _, b := range []*browser{&f.bea, &f.cal} {
		got, code := changes(b)
		if code != http.StatusOK || len(got.Changes) != 1 {
			t.Fatalf("GET = %d %+v", code, got)
		}
		c := got.Changes[0]
		if c.SessionID != "claude:s1" || c.Session != "bea-claude" || c.NodeID != runsTestNode ||
			strings.Join(c.Files, ",") != "a.go,b.go" || c.Snapshot == nil || c.Snapshot.Base != "abc123" {
			t.Fatalf("change = %+v", c)
		}
	}
	if _, code := changes(nil); code != http.StatusUnauthorized {
		t.Fatalf("signed-out GET = %d", code)
	}

	// Somebody else cannot write into bea's session.
	if code := f.do(t, &f.cal, http.MethodPost, "/api/agents/sessions/claude:s1/changes", `{"files":["x.go"]}`, nil).Code; code != http.StatusForbidden {
		t.Fatalf("cal writing bea's session = %d", code)
	}
	// Bad input is refused whole.
	if code := f.do(t, nil, http.MethodPost, proxy+"agents/sessions/claude:s1/changes", `{}`, bearer).Code; code != http.StatusBadRequest {
		t.Fatalf("empty report = %d", code)
	}
	if code := f.do(t, nil, http.MethodPost, proxy+"agents/sessions/claude:s1/changes",
		`{"snapshot":{"files":[{"path":"a.go","status":"exploded"}]}}`, bearer).Code; code != http.StatusBadRequest {
		t.Fatalf("bad snapshot = %d", code)
	}
}
