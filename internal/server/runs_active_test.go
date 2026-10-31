package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/agents"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/store"
)

func (f *runsFixture) activeRuns(t *testing.T, b browser) []runnerwire.Run {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/runs?active=1", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/runs?active=1 = %d %s", response.Code, response.Body)
	}
	var out runnerwire.RunListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Runs
}

// ?active=1 lists every run that has not ended, several on one node
// included, to the same people the plain list shows them to.
func TestActiveRunsRoute(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(3))
	var beas []runnerwire.Run
	for i := 0; i < 3; i++ {
		beas = append(beas, f.dispatch(t, f.bea, f.runBody("")))
	}
	first := expectOffer(t, conn, beas[0].ID)

	// cal's own runner and run.
	calRunner := f.create(t, f.cal, "cal-pc")
	calConn := runnerSocket(t, f.liveURL, calRunner.Token)
	sendEnvelope(t, calConn, runsHello(1))
	if welcome := readEnvelopeForTest(t, calConn); welcome.Type != runnerwire.TypeWelcome {
		t.Fatalf("welcome = %+v", welcome)
	}
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: calRunner.Runner.ID, Agent: "claude", RepoID: "repo-1",
	})
	cals := f.dispatch(t, f.cal, string(body))

	ids := func(list []runnerwire.Run) map[string]bool {
		out := map[string]bool{}
		for _, r := range list {
			out[r.ID] = true
		}
		return out
	}
	mine := ids(f.activeRuns(t, f.bea))
	if len(mine) != 3 || !mine[beas[0].ID] || !mine[beas[1].ID] || !mine[beas[2].ID] {
		t.Fatalf("bea's active runs = %v", mine)
	}
	if got := ids(f.activeRuns(t, f.cal)); len(got) != 1 || !got[cals.ID] {
		t.Fatalf("cal's active runs = %v", got)
	}
	if got := f.activeRuns(t, f.ann); len(got) != 4 {
		t.Fatalf("the administrator's active runs = %d, want 4", len(got))
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/runs?active=maybe", "", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("active=maybe: %d", code)
	}

	// An ended run leaves the list.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: first.RunID}})
	f.waitStatus(t, first.RunID, runnerwire.RunRunning)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: first.RunID,
		Result: &runnerwire.RunResult{Status: runnerwire.RunSucceeded}})
	f.waitStatus(t, first.RunID, runnerwire.RunSucceeded)
	if got := ids(f.activeRuns(t, f.bea)); len(got) != 2 || got[first.RunID] {
		t.Fatalf("after one finished, bea's active runs = %v", got)
	}
}

// While a run runs, the usage its runner reports shows on the run itself.
func TestRunShowsLiveUsage(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: run.ID}})
	f.waitStatus(t, run.ID, runnerwire.RunRunning)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventUsage, Usage: &runnerwire.Usage{InputTokens: 1200, OutputTokens: 34}},
	}})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePing})
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypePong {
		t.Fatalf("after events: %+v", env)
	}
	got, _ := f.getRun(t, f.bea, run.ID)
	if got.Status != runnerwire.RunRunning || got.Usage == nil || got.Usage.InputTokens != 1200 || got.Usage.OutputTokens != 34 {
		t.Fatalf("running run = %+v, usage %+v", got, got.Usage)
	}
	if active := f.activeRuns(t, f.bea); len(active) != 1 || active[0].Usage == nil || active[0].Usage.InputTokens != 1200 {
		t.Fatalf("active list = %+v", active)
	}
}

// A run's agent session records its run when the run's runner says so
// through its proxy, and only then.
func TestProxiedHeartbeatRecordsRunID(t *testing.T) {
	f := newRunsFixture(t)
	f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	proxy := runnerwire.ProxyPathPrefix
	heartbeat := func(b *browser, path, body string, header map[string]string) agents.Session {
		t.Helper()
		response := f.do(t, b, http.MethodPost, path, body, header)
		if response.Code != http.StatusOK {
			t.Fatalf("heartbeat %s: %d %s", path, response.Code, response.Body)
		}
		var out struct {
			Session agents.Session `json:"session"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Session
	}
	beaRunner := map[string]string{"Authorization": "Bearer " + f.runner.Token, runnerwire.RunIDHeader: run.ID}

	got := heartbeat(nil, proxy+"agents/heartbeat", `{"sessionId":"s1","name":"repo-claude-default-abc123"}`, beaRunner)
	if got.RunID != run.ID || !got.Run {
		t.Fatalf("run session = %+v", got)
	}
	// Set once: another run on the same runner cannot take the session over.
	other := f.dispatch(t, f.bea, f.runBody(""))
	refused := f.do(t, nil, http.MethodPost, proxy+"agents/heartbeat", `{"sessionId":"s1","state":"busy"}`,
		map[string]string{"Authorization": "Bearer " + f.runner.Token, runnerwire.RunIDHeader: other.ID})
	if refused.Code != http.StatusForbidden {
		t.Fatalf("another run's heartbeat on s1: %d %s", refused.Code, refused.Body)
	}
	got = heartbeat(nil, proxy+"agents/heartbeat", `{"sessionId":"s1","state":"busy"}`, beaRunner)
	if got.RunID != run.ID {
		t.Fatalf("run session after another run's attempt = %+v", got)
	}

	// cal's runner cannot claim bea's run.
	calRunner := f.create(t, f.cal, "cal-pc")
	got = heartbeat(nil, proxy+"agents/heartbeat", `{"sessionId":"s2"}`,
		map[string]string{"Authorization": "Bearer " + calRunner.Token, runnerwire.RunIDHeader: run.ID})
	if got.RunID != "" {
		t.Fatalf("another runner's claim was believed: %+v", got)
	}
	// Nor can a browser, whatever header it sends.
	got = heartbeat(&f.bea, "/api/agents/heartbeat", `{"sessionId":"s3"}`, map[string]string{runnerwire.RunIDHeader: run.ID})
	if got.RunID != "" || got.Run {
		t.Fatalf("a browser's claim was believed: %+v", got)
	}

	// The roster carries it.
	response := f.do(t, &f.bea, http.MethodGet, "/api/agents", "", nil)
	var roster struct {
		Sessions []agents.Session `json:"sessions"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &roster)
	found := false
	for _, s := range roster.Sessions {
		if s.ID == "s1" {
			found = s.RunID == run.ID
		}
	}
	if !found {
		t.Fatalf("roster = %s", response.Body)
	}
}

// GET /api/autopilot without ?goal= lists the project's live autopilots.
func TestAutopilotListRoute(t *testing.T) {
	f := newRunsFixture(t)
	st := f.server.pm.Store()
	for _, n := range []*engine.Node{
		{ID: "goal", Title: "Ship the login fix"},
		{ID: "task-a", Title: "Fix the button", Kind: "task"},
	} {
		if _, err := st.CreateNode(identity.Local, n, "Do "+n.Title+"."); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{{Kind: "add-edge", From: "task-a", To: "goal"}}); err != nil {
		t.Fatal(err)
	}
	f.connect(t, runsHello(1))
	response := f.do(t, &f.bea, http.MethodPost, "/api/autopilot", `{"goalNodeId":"goal","policy":{}}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/autopilot = %d %s", response.Code, response.Body)
	}
	var created runnerwire.AutopilotView
	_ = json.Unmarshal(response.Body.Bytes(), &created)

	list := func(b browser, query string) ([]runnerwire.AutopilotView, int) {
		t.Helper()
		response := f.do(t, &b, http.MethodGet, "/api/autopilot"+query, "", nil)
		var out runnerwire.AutopilotListResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out.Autopilots, response.Code
	}
	// Anyone who may read the project sees it, as they may open it by id.
	for name, b := range map[string]browser{"bea": f.bea, "cal": f.cal} {
		views, code := list(b, "")
		if code != http.StatusOK || len(views) != 1 || views[0].Run.ID != created.Run.ID || len(views[0].Tasks) != 1 {
			t.Fatalf("%s's list: %d %+v", name, code, views)
		}
	}
	if views, code := list(f.bea, "?status=paused"); code != http.StatusOK || len(views) != 0 {
		t.Fatalf("paused: %d %+v", code, views)
	}
	if _, code := list(f.bea, "?status=succeeded"); code != http.StatusBadRequest {
		t.Fatalf("an ended status: %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/autopilot?goal=", "", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("an empty goal is still refused: %d", code)
	}
	// A goal that never had an autopilot is null, not a 404; an unknown id
	// still is one.
	if response := f.do(t, &f.bea, http.MethodGet, "/api/autopilot?goal=task-a", "", nil); response.Code != http.StatusOK ||
		strings.TrimSpace(response.Body.String()) != "null" {
		t.Fatalf("a goal with no autopilot: %d %s", response.Code, response.Body)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/autopilot/no-such-autopilot", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("an unknown id: %d, want 404", code)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/autopilot/"+created.Run.ID+"/pause", "", nil).Code; code != http.StatusOK {
		t.Fatalf("pause: %d", code)
	}
	if views, _ := list(f.bea, "?status=paused,running"); len(views) != 1 || views[0].Run.Status != runnerwire.AutopilotPaused {
		t.Fatalf("after pause: %+v", views)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/autopilot/"+created.Run.ID+"/cancel", "", nil).Code; code != http.StatusOK {
		t.Fatalf("cancel: %d", code)
	}
	if views, code := list(f.bea, ""); code != http.StatusOK || len(views) != 0 || views == nil {
		t.Fatalf("after cancel: %d %+v", code, views)
	}
}

// A restricted project's runs and autopilots are refused to non-members like
// the rest of it.
func TestActiveListsFollowProjectMembership(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea adding cal: %d %s", response.Code, response.Body)
	}
	for _, path := range []string{"/api/runs?active=1&project=other", "/api/autopilot?project=other"} {
		if code := f.code(t, f.dan, http.MethodGet, path, ""); code != http.StatusForbidden {
			t.Errorf("dan GET %s: %d, want 403", path, code)
		}
		if code := f.code(t, f.cal, http.MethodGet, path, ""); code != http.StatusOK {
			t.Errorf("cal GET %s: %d, want 200", path, code)
		}
	}
}
