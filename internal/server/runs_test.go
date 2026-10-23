package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// runsFixture is an accounts server with a node to work on, bea's runner, and
// a live listener for that runner's socket.
type runsFixture struct {
	*runnerTestServer
	bea, cal, ann browser
	runner        runnerwire.CreateResponse
	liveURL       string
}

const runsTestNode = "fix-login"

func newRunsFixture(t *testing.T) *runsFixture {
	t.Helper()
	s := newRunnerTestServer(t)
	if _, err := s.server.pm.Store().CreateNode(&engine.Node{
		ID: runsTestNode, Title: "Fix the login bug",
	}, "The login button does nothing on Safari."); err != nil {
		t.Fatalf("create node: %v", err)
	}
	f := &runsFixture{
		runnerTestServer: s,
		bea:              s.signIn(t, "bea@example.test"),
		cal:              s.signIn(t, "cal@example.test"),
		ann:              s.signIn(t, testEmail),
	}
	f.runner = s.create(t, f.bea, "bea-pc")
	live := httptest.NewServer(s.handler)
	t.Cleanup(live.Close)
	f.liveURL = live.URL
	return f
}

func runsHello(maxConcurrent int, active ...string) runnerwire.Envelope {
	return runnerwire.Envelope{Type: runnerwire.TypeHello, Active: active, Hello: &runnerwire.Info{
		Version: "test", Hostname: "desk", OS: "windows", Arch: "amd64",
		Capabilities:  []string{runnerwire.CapHeadless},
		MaxConcurrent: maxConcurrent,
		Agents: []runnerwire.Agent{{Kind: "claude", Installed: true, Models: []runnerwire.Model{
			{ID: "default", Default: true}, {ID: "opus"},
		}}, {Kind: "codex", Installed: false}},
		Repos: []runnerwire.Repo{{ID: "repo-1", Name: "visual-editor", Path: `H:\visual-editor`}},
	}}
}

// connect opens bea's runner socket, says hello and reads the welcome.
func (f *runsFixture) connect(t *testing.T, hello runnerwire.Envelope) *websocket.Conn {
	t.Helper()
	conn := runnerSocket(t, f.liveURL, f.runner.Token)
	sendEnvelope(t, conn, hello)
	if welcome := readEnvelopeForTest(t, conn); welcome.Type != runnerwire.TypeWelcome {
		t.Fatalf("welcome = %+v", welcome)
	}
	return conn
}

func (f *runsFixture) runBody(extra string) string {
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1",
		Base: "main", Prompt: extra,
	})
	return string(body)
}

func (f *runsFixture) dispatch(t *testing.T, b browser, body string) runnerwire.Run {
	t.Helper()
	response := f.do(t, &b, http.MethodPost, "/api/runs", body, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/runs = %d, body = %s", response.Code, response.Body)
	}
	var run runnerwire.Run
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func (f *runsFixture) getRun(t *testing.T, b browser, id string) (runnerwire.Run, int) {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/runs/"+id, "", nil)
	var run runnerwire.Run
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	return run, response.Code
}

// waitStatus polls until the run reaches status: the socket is processed on
// its own goroutine, so what a runner sent lands a moment later.
func (f *runsFixture) waitStatus(t *testing.T, id, status string) runnerwire.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, code := f.getRun(t, f.bea, id)
		if code == http.StatusOK && run.Status == status {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: status %q (HTTP %d), want %q", id, run.Status, code, status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *runsFixture) cancel(t *testing.T, b browser, id string) (runnerwire.Run, int) {
	t.Helper()
	response := f.do(t, &b, http.MethodPost, "/api/runs/"+id+"/cancel", "", nil)
	var run runnerwire.Run
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	return run, response.Code
}

func expectOffer(t *testing.T, conn *websocket.Conn, runID string) runnerwire.Job {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeOffer || env.Job == nil || env.Job.RunID != runID {
		t.Fatalf("got %+v, want an offer for %s", env, runID)
	}
	return *env.Job
}

func expectCancel(t *testing.T, conn *websocket.Conn, runID string) {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeCancel || env.RunID != runID || env.Reason == "" {
		t.Fatalf("got %+v, want a cancel for %s", env, runID)
	}
}

func TestRunLifecycle(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))

	run := f.dispatch(t, f.bea, f.runBody("Add a regression test."))
	if run.Status != runnerwire.RunOffered || run.Model != "default" || run.RepoName != "visual-editor" ||
		run.RunnerName != "bea-pc" || run.CreatedByName != "bea" || run.Prompt != "Add a regression test." {
		t.Fatalf("created run = %+v", run)
	}
	job := expectOffer(t, conn, run.ID)
	for _, want := range []string{"Fix the login bug", "does nothing on Safari", "Additional instructions:\nAdd a regression test."} {
		if !strings.Contains(job.Prompt, want) {
			t.Errorf("job prompt %q lacks %q", job.Prompt, want)
		}
	}
	if job.RequestedBy != "bea" || job.Agent != "claude" || job.RepoID != "repo-1" || job.Base != "main" ||
		job.NodeID != runsTestNode || job.NodeTitle != "Fix the login bug" || job.Project == "" {
		t.Fatalf("job = %+v", job)
	}

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: run.ID, Base: "main", Branch: "agent/fix-login-" + run.ID,
	}})
	running := f.waitStatus(t, run.ID, runnerwire.RunRunning)
	if running.StartedAt == "" || running.Branch == "" {
		t.Fatalf("running = %+v", running)
	}

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventText, Text: "Looking at the button."},
		{Seq: 2, Type: runnerwire.EventToolCall, Tool: "Edit", ToolID: "t1", Input: "{}"},
		{Seq: 3, Type: runnerwire.EventUsage, Usage: &runnerwire.Usage{InputTokens: 100, OutputTokens: 20}},
	}})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: "Fixed it.", Usage: &runnerwire.Usage{InputTokens: 120, OutputTokens: 30, CostUSD: 0.01},
	}})
	done := f.waitStatus(t, run.ID, runnerwire.RunSucceeded)
	if done.Summary != "Fixed it." || done.Usage == nil || done.Usage.InputTokens != 120 || done.FinishedAt == "" {
		t.Fatalf("done = %+v", done)
	}

	page := func(query string) runnerwire.RunEventsResponse {
		response := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+run.ID+"/events"+query, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("events%s = %d %s", query, response.Code, response.Body)
		}
		var out runnerwire.RunEventsResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out
	}
	if first := page("?limit=2"); len(first.Events) != 2 || !first.More || first.Events[1].Seq != 2 {
		t.Fatalf("first page = %+v", first)
	}
	if rest := page("?after=2"); len(rest.Events) != 1 || rest.More || rest.Events[0].Usage == nil {
		t.Fatalf("second page = %+v", rest)
	}

	list := func(b browser) []runnerwire.Run {
		response := f.do(t, &b, http.MethodGet, "/api/runs?nodeId="+runsTestNode, "", nil)
		var out runnerwire.RunListResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out.Runs
	}
	if got := list(f.bea); len(got) != 1 || got[0].ID != run.ID {
		t.Fatalf("bea's list = %+v", got)
	}
	if got := list(f.ann); len(got) != 1 {
		t.Fatalf("the administrator's list = %+v", got)
	}
	if got := list(f.cal); len(got) != 0 {
		t.Fatalf("cal sees %+v", got)
	}
	if _, code := f.getRun(t, f.cal, run.ID); code != http.StatusNotFound {
		t.Fatalf("cal reading bea's run: %d", code)
	}
	if _, code := f.getRun(t, f.ann, run.ID); code != http.StatusOK {
		t.Fatalf("the administrator reading bea's run: %d", code)
	}
	if code := f.do(t, &f.cal, http.MethodGet, "/api/runs/"+run.ID+"/events", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("cal reading bea's events: %d", code)
	}
}

func TestRunDeclineAndCancel(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))

	declined := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, declined.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: declined.ID, Reason: "not now"})
	if got := f.waitStatus(t, declined.ID, runnerwire.RunDeclined); got.Error != "not now" || got.FinishedAt == "" {
		t.Fatalf("declined = %+v", got)
	}

	// Cancelled while offered: ends at once, and the runner is told.
	offered := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, offered.ID)
	if got, code := f.cancel(t, f.cal, offered.ID); code != http.StatusNotFound {
		t.Fatalf("cal cancelling bea's run: %d %+v", code, got)
	}
	if got, code := f.cancel(t, f.bea, offered.ID); code != http.StatusOK || got.Status != runnerwire.RunCancelled {
		t.Fatalf("cancel offered = %d %+v", code, got)
	}
	expectCancel(t, conn, offered.ID)
	// An accept that crosses the cancel is answered with another cancel.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: offered.ID}})
	expectCancel(t, conn, offered.ID)

	// Cancelled while running: the runner is asked, and the run ends when it
	// reports the finish.
	running := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, running.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: running.ID, Branch: "b"}})
	f.waitStatus(t, running.ID, runnerwire.RunRunning)
	if got, code := f.cancel(t, f.ann, running.ID); code != http.StatusOK || got.Status != runnerwire.RunRunning {
		t.Fatalf("cancel running = %d %+v", code, got)
	}
	expectCancel(t, conn, running.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: running.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunCancelled, ExitCode: -1,
	}})
	if got := f.waitStatus(t, running.ID, runnerwire.RunCancelled); got.ExitCode != -1 {
		t.Fatalf("cancelled = %+v", got)
	}

	// A queued run (the runner is full) is cancelled without the runner.
	busy := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, busy.ID)
	queued := f.dispatch(t, f.bea, f.runBody(""))
	if queued.Status != runnerwire.RunQueued {
		t.Fatalf("second run with one slot = %+v", queued)
	}
	if got, code := f.cancel(t, f.bea, queued.ID); code != http.StatusOK || got.Status != runnerwire.RunCancelled {
		t.Fatalf("cancel queued = %d %+v", code, got)
	}
}

func TestRunnerReconnectReconcilesRuns(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(2))

	lost := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, lost.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: lost.ID}})
	f.waitStatus(t, lost.ID, runnerwire.RunRunning)
	unanswered := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, unanswered.ID)

	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	waiting := f.dispatch(t, f.bea, f.runBody(""))
	if waiting.Status != runnerwire.RunQueued {
		t.Fatalf("run for an offline runner = %+v", waiting)
	}

	// Back, running nothing: the running run is lost, the unanswered offer
	// and the waiting run are offered (oldest first, two slots).
	again := f.connect(t, runsHello(2))
	expectOffer(t, again, unanswered.ID)
	expectOffer(t, again, waiting.ID)
	if got := f.waitStatus(t, lost.ID, runnerwire.RunFailed); got.Error != runs.ReasonLost {
		t.Fatalf("lost run = %+v", got)
	}
}

func TestRunnerReconnectKeepsActiveRuns(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: run.ID}})
	f.waitStatus(t, run.ID, runnerwire.RunRunning)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)

	// Cancelled while the runner was away: there is nobody to ask, so it
	// ends here, and the runner is told when it comes back still running it.
	if got, code := f.cancel(t, f.bea, run.ID); code != http.StatusOK || got.Status != runnerwire.RunCancelled {
		t.Fatalf("cancel with the runner offline = %d %+v", code, got)
	}
	again := f.connect(t, runsHello(1, run.ID))
	expectCancel(t, again, run.ID)
}

func (f *runsFixture) waitOffline(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := f.do(t, &f.bea, http.MethodGet, "/api/runners", "", nil)
		var out runnerwire.ListResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		if len(out.Runners) == 1 && !out.Runners[0].Online {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("runner still online")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunDispatchRules(t *testing.T) {
	f := newRunsFixture(t)
	// Before the runner has ever said hello, nothing about it is known.
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs", f.runBody(""), nil).Code; code != http.StatusBadRequest {
		t.Fatalf("dispatch to a runner that never connected: %d", code)
	}
	f.connect(t, runsHello(1))

	// Someone else's runner does not exist, for members and administrators
	// alike: a run executes on its owner's machine.
	for name, b := range map[string]browser{"cal": f.cal, "ann": f.ann} {
		if code := f.do(t, &b, http.MethodPost, "/api/runs", f.runBody(""), nil).Code; code != http.StatusNotFound {
			t.Errorf("%s dispatching to bea's runner: %d, want 404", name, code)
		}
	}

	body := func(mutate func(*runnerwire.CreateRunRequest)) string {
		req := runnerwire.CreateRunRequest{NodeID: runsTestNode, RunnerID: f.runner.Runner.ID, Agent: "claude", RepoID: "repo-1"}
		mutate(&req)
		raw, _ := json.Marshal(req)
		return string(raw)
	}
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"uninstalled agent": {body(func(r *runnerwire.CreateRunRequest) { r.Agent = "codex" }), http.StatusBadRequest},
		"unknown agent":     {body(func(r *runnerwire.CreateRunRequest) { r.Agent = "gemini" }), http.StatusBadRequest},
		"unknown model":     {body(func(r *runnerwire.CreateRunRequest) { r.Model = "gpt-9" }), http.StatusBadRequest},
		"unknown repo":      {body(func(r *runnerwire.CreateRunRequest) { r.RepoID = "elsewhere" }), http.StatusBadRequest},
		"unknown node":      {body(func(r *runnerwire.CreateRunRequest) { r.NodeID = "nope" }), http.StatusNotFound},
		"option as base":    {body(func(r *runnerwire.CreateRunRequest) { r.Base = "--upload-pack=x" }), http.StatusBadRequest},
		"long prompt": {body(func(r *runnerwire.CreateRunRequest) {
			r.Prompt = strings.Repeat("x", 8<<10+1)
		}), http.StatusBadRequest},
		"unknown runner": {body(func(r *runnerwire.CreateRunRequest) { r.RunnerID = "nope" }), http.StatusNotFound},
	} {
		if code := f.do(t, &f.bea, http.MethodPost, "/api/runs", tc.body, nil).Code; code != tc.want {
			t.Errorf("%s: %d, want %d", name, code, tc.want)
		}
	}
	explicit := f.dispatch(t, f.bea, body(func(r *runnerwire.CreateRunRequest) { r.Model = "opus" }))
	if explicit.Model != "opus" {
		t.Fatalf("explicit model = %+v", explicit)
	}
	if code := f.do(t, nil, http.MethodGet, "/api/runs", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", code)
	}
	// Writes are CSRF-protected like any other.
	noCSRF := browser{cookies: f.bea.cookies}
	if code := f.do(t, &noCSRF, http.MethodPost, "/api/runs", f.runBody(""), nil).Code; code != http.StatusForbidden {
		t.Fatalf("dispatch without a CSRF token: %d", code)
	}
}

// An agent may not dispatch agents, whichever way it is recognised.
func TestAgentsCannotDispatchRuns(t *testing.T) {
	f := newRunsFixture(t)
	f.connect(t, runsHello(1))
	request := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(f.runBody("")))
	request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
	request.Header.Set("Content-Type", "application/json")
	withCookies(request, f.bea.cookies)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("in-process agent dispatch: %d %s", response.Code, response.Body)
	}

	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	local := serverForTest(t, pm, nil, nil)
	local.UseRunnersDB(database)
	handler := local.Handler()
	request = httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"nodeId":"x","runnerId":"y","agent":"claude","repoId":"r"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(auth.AgentRoleHeaderName, "worker")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("loopback worker dispatch: %d %s", response.Code, response.Body)
	}
}

func TestRunEventsLimits(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: run.ID}})
	f.waitStatus(t, run.ID, runnerwire.RunRunning)

	// A second runner of cal's cannot touch bea's run, whatever ID it sends.
	other := f.create(t, f.cal, "cal-pc")
	intruder := runnerSocket(t, f.liveURL, other.Token)
	sendEnvelope(t, intruder, runsHello(1))
	readEnvelopeForTest(t, intruder)
	sendEnvelope(t, intruder, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{{Seq: 1, Type: "text", Text: "evil"}}})
	sendEnvelope(t, intruder, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunFailed}})

	// A batch over the per-message cap keeps the first MaxEventsPerMessage.
	batch := make([]runnerwire.RunEvent, runnerwire.MaxEventsPerMessage+5)
	for i := range batch {
		batch[i] = runnerwire.RunEvent{Seq: int64(i + 1), Type: runnerwire.EventLog, Text: "line " + strconv.Itoa(i)}
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: batch})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunSucceeded}})
	f.waitStatus(t, run.ID, runnerwire.RunSucceeded)

	response := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+run.ID+"/events?limit=5000", "", nil)
	var out runnerwire.RunEventsResponse
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	if len(out.Events) != runnerwire.MaxEventsPerMessage || out.Events[0].Text == "evil" {
		t.Fatalf("stored %d events, first %+v", len(out.Events), out.Events[0])
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+run.ID+"/events?after=-1", "", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("negative after: %d", code)
	}

	// Past hello the socket takes messages far over the hello limit.
	big := strings.Repeat("x", runnerwire.MaxHelloBytes*2)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{{Seq: 999, Type: "text", Text: big}}})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePing})
	if env := readEnvelopeForTest(t, conn); env.Type != runnerwire.TypePong {
		t.Fatalf("after a large message: %+v", env)
	}
}
