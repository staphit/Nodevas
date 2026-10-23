package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

// routingHello is a runner with two agents and several models, for fallback
// and pipelines.
func routingHello(maxConcurrent int) runnerwire.Envelope {
	env := runsHello(maxConcurrent)
	env.Hello.Agents = []runnerwire.Agent{
		{Kind: "claude", Installed: true, Models: []runnerwire.Model{
			{ID: "default", Default: true}, {ID: "opus"}, {ID: "sonnet"},
		}},
		{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5", Default: true}}},
		{Kind: "gemini", Installed: false},
	}
	return env
}

func (f *runsFixture) createBody(edit func(*runnerwire.CreateRunRequest)) string {
	req := runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: f.runner.Runner.ID, Agent: "claude", Model: "opus",
		RepoID: "repo-1", Base: "main",
	}
	edit(&req)
	body, _ := json.Marshal(req)
	return string(body)
}

func accept(t *testing.T, conn *websocket.Conn, runID, branch string) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: runID, Base: "main", Branch: branch,
	}})
}

func finish(t *testing.T, conn *websocket.Conn, runID string, result runnerwire.RunResult) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: runID, Result: &result})
}

// nextOffer reads the next message, which must be an offer, and returns its
// job.
func nextOffer(t *testing.T, conn *websocket.Conn) runnerwire.Job {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeOffer || env.Job == nil {
		t.Fatalf("got %+v, want an offer", env)
	}
	return *env.Job
}

func (f *runsFixture) runEvents(t *testing.T, id string) string {
	t.Helper()
	response := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+id+"/events", "", nil)
	var out runnerwire.RunEventsResponse
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	var lines []string
	for _, e := range out.Events {
		lines = append(lines, e.Text)
	}
	return strings.Join(lines, "\n")
}

func (f *runsFixture) waitEvents(t *testing.T, id, want string) string {
	t.Helper()
	var got string
	f.waitRun(t, id, "event "+want, func(runnerwire.Run) bool {
		got = f.runEvents(t, id)
		return strings.Contains(got, want)
	})
	return got
}

func (f *runsFixture) routingCall(t *testing.T, b *browser, method, body string) (int, runnerwire.RoutingConfig) {
	t.Helper()
	response := f.do(t, b, method, "/api/runs/routing", body, nil)
	var out runnerwire.RoutingConfig
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return response.Code, out
}

func (f *runsFixture) suggest(t *testing.T, nodeID string) (int, runnerwire.RouteSuggestion) {
	t.Helper()
	response := f.do(t, &f.bea, http.MethodGet, "/api/runs/route?nodeId="+nodeID, "", nil)
	var out runnerwire.RouteSuggestion
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return response.Code, out
}

func (f *runsFixture) pipeline(t *testing.T, b browser, id string) (int, runnerwire.PipelineState) {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/runs/pipelines/"+id, "", nil)
	var out runnerwire.PipelineState
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return response.Code, out
}

func (f *runsFixture) waitPipeline(t *testing.T, id, status string) runnerwire.PipelineState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, got := f.pipeline(t, f.bea, id)
		if got.Status == status {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipeline %s: %+v, want %s", id, got, status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunRoutingRulesAndSuggestion(t *testing.T) {
	f := newRunsFixture(t)
	st := f.server.pm.Store()
	for _, node := range []*engine.Node{
		{ID: "ui-gate", Title: "UI gate", Kind: "gate", Tags: []string{"frontend", "UI"}},
		{ID: "plain-gate", Title: "Plain gate", Kind: "gate"},
	} {
		if _, err := st.CreateNode(node, ""); err != nil {
			t.Fatal(err)
		}
	}

	if code, got := f.routingCall(t, &f.bea, http.MethodGet, ""); code != http.StatusOK || len(got.Rules) != 0 {
		t.Fatalf("fresh routing = %d %+v", code, got)
	}
	if code, got := f.suggest(t, runsTestNode); code != http.StatusOK || got.RuleID != "" {
		t.Fatalf("suggestion without rules = %d %+v", code, got)
	}
	for _, body := range []string{
		`{"rules":[{"match":"weekday","target":{"agent":"claude","model":"opus"}}]}`,
		`{"rules":[{"match":"default","target":{"agent":"","model":"opus"}}]}`,
		`{"rules":[{"match":"tag","target":{"agent":"claude","model":"opus"}}]}`,
		`{"rules":[{"match":"default","target":{"agent":"claude","model":"opus"},"pipeline":{"implement":{"agent":"a","model":"b"},"review":{"agent":"c","model":"d"},"maxRounds":4}}]}`,
		`{"rules":[{"match":"default","target":{"agent":"claude","model":"opus"},"fallbacks":[{"agent":"a","model":"1"},{"agent":"a","model":"2"},{"agent":"a","model":"3"},{"agent":"a","model":"4"},{"agent":"a","model":"5"},{"agent":"a","model":"6"}]}]}`,
		`{"rules":"all of them"}`,
	} {
		if code, _ := f.routingCall(t, &f.bea, http.MethodPut, body); code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", body, code)
		}
	}
	request := httptest.NewRequest(http.MethodPut, "/api/runs/routing", strings.NewReader(`{"rules":[]}`))
	request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
	request.Header.Set("Content-Type", "application/json")
	withCookies(request, f.bea.cookies)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("agent changing routing = %d %s", response.Code, response.Body)
	}

	rules := runnerwire.RoutingConfig{Rules: []runnerwire.RouteRule{
		{Match: runnerwire.RouteDefault, Target: runnerwire.AgentModel{Agent: "claude", Model: "sonnet"}},
		{ID: "this-project", Project: runsapi.ProjectName(f.server.pm, st), Match: runnerwire.RouteProject,
			Target:    runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Fallbacks: []runnerwire.AgentModel{{Agent: "codex", Model: "gpt-5"}}},
		{ID: "other-project", Project: "elsewhere", Match: runnerwire.RouteTag, Value: "ui",
			Target: runnerwire.AgentModel{Agent: "codex", Model: "gpt-5"}},
		{ID: "gates", Match: runnerwire.RouteKind, Value: "gate", Target: runnerwire.AgentModel{Agent: "codex", Model: "gpt-5"}},
		{ID: "ui", Match: runnerwire.RouteTag, Value: "ui", RunnerID: f.runner.Runner.ID,
			Target: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Pipeline: &runnerwire.Pipeline{Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
				Review: runnerwire.AgentModel{Agent: "codex", Model: "gpt-5"}, MaxRounds: 2}},
	}}
	raw, _ := json.Marshal(rules)
	code, stored := f.routingCall(t, &f.bea, http.MethodPut, string(raw))
	if code != http.StatusOK || len(stored.Rules) != 5 || stored.Rules[0].ID == "" || stored.Rules[1].ID != "this-project" {
		t.Fatalf("PUT routing = %d %+v", code, stored)
	}
	if _, got := f.routingCall(t, &f.bea, http.MethodGet, ""); len(got.Rules) != 5 || got.Rules[0].ID != stored.Rules[0].ID {
		t.Fatalf("GET routing = %+v", got)
	}
	if _, got := f.routingCall(t, &f.cal, http.MethodGet, ""); len(got.Rules) != 0 {
		t.Fatalf("cal sees bea's rules: %+v", got)
	}

	// Tag beats kind beats project beats default; another project's rule
	// never applies.
	if _, got := f.suggest(t, "ui-gate"); got.RuleID != "ui" || got.Match != runnerwire.RouteTag ||
		got.Pipeline == nil || got.RunnerID != f.runner.Runner.ID {
		t.Fatalf("ui-gate = %+v", got)
	}
	if _, got := f.suggest(t, "plain-gate"); got.RuleID != "gates" || got.Target.Agent != "codex" {
		t.Fatalf("plain-gate = %+v", got)
	}
	if _, got := f.suggest(t, runsTestNode); got.RuleID != "this-project" || len(got.Fallbacks) != 1 {
		t.Fatalf("fix-login = %+v", got)
	}
	raw, _ = json.Marshal(runnerwire.RoutingConfig{Rules: rules.Rules[:1]})
	_, only := f.routingCall(t, &f.bea, http.MethodPut, string(raw))
	if _, got := f.suggest(t, runsTestNode); len(only.Rules) != 1 || got.RuleID != only.Rules[0].ID || got.Match != runnerwire.RouteDefault {
		t.Fatalf("default = %+v", got)
	}
	if code, _ := f.suggest(t, "no-such-node"); code != http.StatusNotFound {
		t.Fatalf("missing node = %d", code)
	}
	if code, _ := f.suggest(t, "../x"); code != http.StatusBadRequest {
		t.Fatalf("bad node id = %d", code)
	}
}

func TestRunFallbackChain(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(4))

	// A fallback list that breaks the limits is refused.
	tooMany := f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Fallbacks = make([]runnerwire.AgentModel, runnerwire.MaxFallbacks+1)
		for i := range r.Fallbacks {
			r.Fallbacks[i] = runnerwire.AgentModel{Agent: "codex"}
		}
	})
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs", tooMany, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("six fallbacks = %d", code)
	}

	first := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Fallbacks = []runnerwire.AgentModel{
			{Agent: "claude", Model: "haiku"}, // not on the runner: skipped
			{Agent: "gemini", Model: "pro"},   // not installed: skipped
			{Agent: "codex", Model: "gpt-5"},
			{Agent: "claude", Model: "sonnet"},
		}
	}))
	expectOffer(t, conn, first.ID)
	accept(t, conn, first.ID, "agent/a")
	f.waitStatus(t, first.ID, runnerwire.RunRunning)
	finish(t, conn, first.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "429", FailureKind: runnerwire.FailureRateLimit})

	job := nextOffer(t, conn)
	if job.Agent != "codex" || job.Model != "gpt-5" || job.RunID == first.ID || job.Prompt == "" {
		t.Fatalf("fallback job = %+v", job)
	}
	second, _ := f.getRun(t, f.bea, job.RunID)
	if second.RetryOf != first.ID || second.Status != runnerwire.RunOffered {
		t.Fatalf("fallback run = %+v", second)
	}
	failed, _ := f.getRun(t, f.bea, first.ID)
	if failed.FailureKind != runnerwire.FailureRateLimit || failed.Status != runnerwire.RunFailed {
		t.Fatalf("failed run = %+v", failed)
	}
	notes := f.waitEvents(t, first.ID, "falling back to codex/gpt-5")
	for _, want := range []string{"skipping fallback claude/haiku", "skipping fallback gemini/pro"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes %q lack %q", notes, want)
		}
	}

	// A task failure does not fall back, whatever chain is left.
	accept(t, conn, second.ID, "agent/b")
	f.waitStatus(t, second.ID, runnerwire.RunRunning)
	finish(t, conn, second.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "tests fail", FailureKind: runnerwire.FailureTask})
	pingPong(t, conn)
	if got := f.waitStatus(t, second.ID, runnerwire.RunFailed); got.FailureKind != runnerwire.FailureTask {
		t.Fatalf("task failure = %+v", got)
	}

	// The chain runs out.
	last := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Fallbacks = []runnerwire.AgentModel{{Agent: "claude", Model: "haiku"}}
	}))
	expectOffer(t, conn, last.ID)
	accept(t, conn, last.ID, "agent/c")
	f.waitStatus(t, last.ID, runnerwire.RunRunning)
	finish(t, conn, last.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureQuota})
	pingPong(t, conn)
	f.waitEvents(t, last.ID, "fallback chain exhausted")
}

func TestRunFallbackStopsAtTheBudget(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(4))
	if code, _ := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":1}`); code != http.StatusOK {
		t.Fatalf("set budget = %d", code)
	}
	run := f.dispatch(t, f.bea, f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Fallbacks = []runnerwire.AgentModel{{Agent: "codex"}}
	}))
	expectOffer(t, conn, run.ID)
	accept(t, conn, run.ID, "agent/a")
	f.waitStatus(t, run.ID, runnerwire.RunRunning)
	finish(t, conn, run.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureModel})
	pingPong(t, conn)
	f.waitEvents(t, run.ID, "not falling back: daily run budget reached")
}

func pipelineBody(f *runsFixture, rounds int) string {
	return f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Agent, r.Model = "", ""
		r.Prompt = "Keep it small."
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Review:    runnerwire.AgentModel{Agent: "codex"},
			MaxRounds: rounds,
		}
	})
}

const testPRURL = "https://github.com/bea/visual-editor/pull/7"

func sendPR(t *testing.T, conn *websocket.Conn, runID string) {
	t.Helper()
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: runID, PR: &runnerwire.PRInfo{
		URL: testPRURL, Number: 7, State: runnerwire.PROpen, CI: runnerwire.CIPending, HeadSHA: testHeadSHA,
	}})
}

// implement runs a pipeline's implement step to success: changes, then the
// PR, then the finish, as the runner sends them.
func implement(t *testing.T, conn *websocket.Conn, runID string) {
	t.Helper()
	accept(t, conn, runID, "agent/fix-login-"+runID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: runID, Changes: testChanges()})
	sendPR(t, conn, runID)
	finish(t, conn, runID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: "Fixed."})
}

func review(t *testing.T, conn *websocket.Conn, runID, verdict, summary string) {
	t.Helper()
	accept(t, conn, runID, "review-"+runID)
	finish(t, conn, runID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: summary, Verdict: verdict})
}

func TestRunPipelineFullCycle(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(1))
	peer := dialHubAs(t, f.liveURL, f.bea)

	impl := f.dispatch(t, f.bea, pipelineBody(f, 2))
	if impl.PipelineID == "" || impl.Kind != runnerwire.JobImplement || impl.Agent != "claude" || impl.Model != "opus" {
		t.Fatalf("implement run = %+v", impl)
	}
	if code, p := f.pipeline(t, f.bea, impl.PipelineID); code != http.StatusOK || p.Status != runnerwire.PipelineRunning ||
		p.Round != 1 || len(p.RunIDs) != 1 || p.Pipeline.Review.Model != "gpt-5" || p.NodeID != runsTestNode {
		t.Fatalf("pipeline = %d %+v", code, p)
	}
	if code, _ := f.pipeline(t, f.cal, impl.PipelineID); code != http.StatusNotFound {
		t.Fatalf("cal reading bea's pipeline = %d", code)
	}
	if job := nextOffer(t, conn); job.RunID != impl.ID || job.Kind != runnerwire.JobImplement {
		t.Fatalf("implement job = %+v", job)
	}
	implement(t, conn, impl.ID)

	job := nextOffer(t, conn)
	branch := "agent/fix-login-" + impl.ID
	if job.Kind != runnerwire.JobReview || job.Agent != "codex" || job.Model != "gpt-5" || job.Branch != branch ||
		job.PRURL != testPRURL || !strings.Contains(job.Prompt, "Fix the login bug") || job.Review != "" {
		t.Fatalf("review job = %+v", job)
	}
	review(t, conn, job.RunID, runnerwire.VerdictChanges, "Rename the helper.\nVERDICT: changes")

	revise := nextOffer(t, conn)
	if revise.Kind != runnerwire.JobRevise || revise.Agent != "claude" || revise.Model != "opus" || revise.Branch != branch ||
		revise.PRURL != testPRURL || revise.Review != "Rename the helper.\nVERDICT: changes" ||
		!strings.Contains(revise.Prompt, "Additional instructions:\nKeep it small.") ||
		!strings.Contains(revise.Prompt, "Review to address:\nRename the helper.") {
		t.Fatalf("revise job = %+v", revise)
	}
	if _, p := f.pipeline(t, f.bea, impl.PipelineID); p.Round != 2 {
		t.Fatalf("round after changes = %+v", p)
	}
	accept(t, conn, revise.RunID, branch)
	finish(t, conn, revise.RunID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Summary: "Renamed."})

	again := nextOffer(t, conn)
	if again.Kind != runnerwire.JobReview || again.Branch != branch {
		t.Fatalf("second review job = %+v", again)
	}
	review(t, conn, again.RunID, runnerwire.VerdictApprove, "Looks good.")
	p := f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineApproved)
	if p.Round != 2 || len(p.RunIDs) != 4 || p.RunIDs[0] != impl.ID || p.RunIDs[3] != again.RunID {
		t.Fatalf("approved pipeline = %+v", p)
	}
	last, _ := f.getRun(t, f.bea, again.RunID)
	if last.PipelineID != impl.PipelineID || last.Kind != runnerwire.JobReview || last.Verdict != runnerwire.VerdictApprove {
		t.Fatalf("last run = %+v", last)
	}
	f.waitEvents(t, again.RunID, "pipeline approved")
	peer.await(runnerwire.RunsChangedEvent, func(e realtime.Event) bool { return e.ID == runsTestNode })
}

func TestRunPipelineRoundsRunOut(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(1))
	impl := f.dispatch(t, f.bea, pipelineBody(f, 1))
	expectOffer(t, conn, impl.ID)

	// The PR arrives after the finish: the pipeline waits for it.
	accept(t, conn, impl.ID, "agent/x")
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: impl.ID, Changes: testChanges()})
	finish(t, conn, impl.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded})
	pingPong(t, conn)
	sendPR(t, conn, impl.ID)
	job := nextOffer(t, conn)
	if job.Kind != runnerwire.JobReview || job.Branch != "agent/x" {
		t.Fatalf("review job = %+v", job)
	}
	// A later PR report (CI moving) starts nothing more.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: impl.ID, PR: &runnerwire.PRInfo{
		URL: testPRURL, Number: 7, State: runnerwire.PROpen, CI: runnerwire.CISuccess, HeadSHA: testHeadSHA,
	}})
	review(t, conn, job.RunID, runnerwire.VerdictChanges, "Still wrong.")
	pingPong(t, conn)
	if p := f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineChangesLeft); len(p.RunIDs) != 2 || p.Round != 1 {
		t.Fatalf("changes_left pipeline = %+v", p)
	}
}

func TestRunPipelineFailures(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(1))

	// The implement run fails: no fallback, so the pipeline fails.
	failed := f.dispatch(t, f.bea, pipelineBody(f, 2))
	expectOffer(t, conn, failed.ID)
	accept(t, conn, failed.ID, "agent/a")
	finish(t, conn, failed.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, Error: "boom", FailureKind: runnerwire.FailureTask})
	pingPong(t, conn)
	f.waitPipeline(t, failed.PipelineID, runnerwire.PipelineFailed)
	f.waitEvents(t, failed.ID, "pipeline failed: run "+failed.ID+" failed: boom")

	// The implement run changed nothing: nothing to review.
	empty := f.dispatch(t, f.bea, pipelineBody(f, 2))
	expectOffer(t, conn, empty.ID)
	accept(t, conn, empty.ID, "agent/b")
	finish(t, conn, empty.ID, runnerwire.RunResult{Status: runnerwire.RunSucceeded})
	pingPong(t, conn)
	f.waitPipeline(t, empty.PipelineID, runnerwire.PipelineFailed)
	f.waitEvents(t, empty.ID, "changed nothing")

	// The review fails with a quota: the review step has no fallbacks.
	withFallback := f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude", Model: "opus"},
			Review:    runnerwire.AgentModel{Agent: "codex"}, MaxRounds: 1,
		}
		r.Fallbacks = []runnerwire.AgentModel{{Agent: "claude", Model: "sonnet"}}
	})
	run := f.dispatch(t, f.bea, withFallback)
	expectOffer(t, conn, run.ID)
	// The implement step falls back within the pipeline.
	accept(t, conn, run.ID, "agent/c")
	finish(t, conn, run.ID, runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureRateLimit})
	retry := nextOffer(t, conn)
	if retry.Model != "sonnet" || retry.Kind != runnerwire.JobImplement {
		t.Fatalf("pipeline fallback job = %+v", retry)
	}
	if got, _ := f.getRun(t, f.bea, retry.RunID); got.PipelineID != run.PipelineID || got.RetryOf != run.ID {
		t.Fatalf("pipeline fallback run = %+v", got)
	}
	implement(t, conn, retry.RunID)
	rev := nextOffer(t, conn)
	accept(t, conn, rev.RunID, "r")
	finish(t, conn, rev.RunID, runnerwire.RunResult{Status: runnerwire.RunFailed, FailureKind: runnerwire.FailureQuota})
	pingPong(t, conn)
	if p := f.waitPipeline(t, run.PipelineID, runnerwire.PipelineFailed); len(p.RunIDs) != 3 {
		t.Fatalf("failed pipeline = %+v", p)
	}

	// A review step whose agent the runner no longer has fails the pipeline.
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs", f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude"}, Review: runnerwire.AgentModel{Agent: "gemini"}, MaxRounds: 1,
		}
	}), nil).Code; code != http.StatusBadRequest {
		t.Fatalf("pipeline with an absent review agent = %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs", f.createBody(func(r *runnerwire.CreateRunRequest) {
		r.Pipeline = &runnerwire.Pipeline{
			Implement: runnerwire.AgentModel{Agent: "claude"}, Review: runnerwire.AgentModel{Agent: "codex"}, MaxRounds: 4,
		}
	}), nil).Code; code != http.StatusBadRequest {
		t.Fatalf("four rounds = %d", code)
	}
}

func TestRunPipelineCancel(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, routingHello(1))
	impl := f.dispatch(t, f.bea, pipelineBody(f, 2))
	expectOffer(t, conn, impl.ID)
	implement(t, conn, impl.ID)
	rev := nextOffer(t, conn)
	accept(t, conn, rev.RunID, "r")
	f.waitStatus(t, rev.RunID, runnerwire.RunRunning)

	// Cancelling any of its runs, even one that ended, cancels the pipeline
	// and stops the run in progress.
	if got, code := f.cancel(t, f.bea, impl.ID); code != http.StatusOK || got.Status != runnerwire.RunSucceeded {
		t.Fatalf("cancel = %d %+v", code, got)
	}
	expectCancel(t, conn, rev.RunID)
	f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineCancelled)
	finish(t, conn, rev.RunID, runnerwire.RunResult{Status: runnerwire.RunSucceeded, Verdict: runnerwire.VerdictChanges})
	pingPong(t, conn)
	if p := f.waitPipeline(t, impl.PipelineID, runnerwire.PipelineCancelled); len(p.RunIDs) != 2 {
		t.Fatalf("cancelled pipeline = %+v", p)
	}
}
