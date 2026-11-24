package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
)

// globalRunsHello is a runner that takes global runs, works interactively
// and knows run workspaces.
func globalRunsHello() runnerwire.Envelope {
	env := workspaceHello()
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapInteractive,
		runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn)
	return env
}

const globalTask = "Fix the login bug\n\nThe secret detail: Safari drops the click."

func (f *runsFixture) globalBody(edit func(*runnerwire.CreateGlobalRunRequest)) string {
	body := runnerwire.CreateGlobalRunRequest{RunnerID: f.runner.Runner.ID, RepoID: "repo-1", Agent: "claude",
		Base: "main", Task: globalTask}
	if edit != nil {
		edit(&body)
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

type globalError struct {
	Code     string `json:"code"`
	Blocked  string `json:"blocked"`
	Field    string `json:"field"`
	Exceeded string `json:"exceeded"`
}

type globalAnswer struct {
	code    int
	run     runnerwire.Run
	failure globalError
	body    string
}

func (f *runsFixture) global(t *testing.T, b *browser, method, path, body string, header map[string]string) globalAnswer {
	t.Helper()
	response := f.do(t, b, method, path, body, header)
	out := globalAnswer{code: response.Code, body: response.Body.String()}
	_ = json.Unmarshal(response.Body.Bytes(), &out.run)
	_ = json.Unmarshal(response.Body.Bytes(), &out.failure)
	return out
}

func (f *runsFixture) startGlobal(t *testing.T, b *browser, body string) runnerwire.Run {
	t.Helper()
	answer := f.global(t, b, http.MethodPost, "/api/global-runs", body, nil)
	if answer.code != http.StatusCreated {
		t.Fatalf("POST /api/global-runs = %d %s", answer.code, answer.body)
	}
	return answer.run
}

func (f *runsFixture) listGlobal(t *testing.T, b *browser, query string) runnerwire.GlobalRunsResponse {
	t.Helper()
	response := f.do(t, b, http.MethodGet, "/api/global-runs"+query, "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/global-runs = %d %s", response.Code, response.Body)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q", cache)
	}
	var out runnerwire.GlobalRunsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// awaitNoneOf fails if an event of any of kinds reaches peer within window.
func awaitNoneOf(t *testing.T, peer *wsPeer, window time.Duration, kinds ...string) {
	t.Helper()
	timeout := time.After(window)
	for {
		select {
		case event, ok := <-peer.events:
			if !ok {
				return
			}
			for _, kind := range kinds {
				if event.Type == kind {
					t.Fatalf("received an unwanted %q event: %+v", kind, event)
				}
			}
		case <-timeout:
			return
		}
	}
}

// waitGlobal polls bea's view of a global run until ok holds.
func (f *runsFixture) waitGlobal(t *testing.T, id, what string, ok func(runnerwire.Run) bool) runnerwire.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		answer := f.global(t, &f.bea, http.MethodGet, "/api/global-runs/"+id, "", nil)
		if answer.code == http.StatusOK && ok(answer.run) {
			return answer.run
		}
		if time.Now().After(deadline) {
			t.Fatalf("global run %s never got %s: %d %s", id, what, answer.code, answer.body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startedGlobal starts a global run and has the runner accept it.
func (f *runsFixture) startedGlobal(t *testing.T, conn *websocket.Conn, edit func(*runnerwire.CreateGlobalRunRequest)) (runnerwire.Run, runnerwire.Job) {
	t.Helper()
	pingPong(t, conn)
	run := f.startGlobal(t, &f.bea, f.globalBody(edit))
	job := expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: run.ID, Base: "main", Branch: "agent/" + job.Slug + "-" + run.ID[:8],
	}})
	return f.waitGlobal(t, run.ID, "running", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunRunning }), job
}

func TestGlobalRunStartsOffersAndTellsOnlyItsPeople(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	bea := dialHubAs(t, f.liveURL, f.bea)
	ann := dialHubAs(t, f.liveURL, f.ann)
	cal := dialHubAs(t, f.liveURL, f.cal)

	run, job := f.startedGlobal(t, conn, nil)
	if !run.Global || run.Project != "" || run.NodeID != "" || run.Title != "Fix the login bug" || !run.NoPR ||
		run.Prompt != "" || run.RepoName != "visual-editor" || run.Base != "main" {
		t.Fatalf("run = %+v", run)
	}
	if !job.Global || job.Slug != "fix-the-login-bug" || !job.NoPR || job.Project != "" || job.NodeID != "" ||
		job.NodeTitle != "Fix the login bug" || !strings.Contains(job.Prompt, "Safari drops the click") {
		t.Fatalf("offer = %+v", job)
	}
	bea.await(runnerwire.GlobalRunsChangedEvent, func(e realtime.Event) bool { return e.ID == run.ID })

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeEvents, RunID: run.ID, Events: []runnerwire.RunEvent{
		{Seq: 1, Type: runnerwire.EventText, At: "2026-11-14T00:00:00.000Z", Text: "working"},
	}})
	bea.await(runnerwire.GlobalRunEventsEvent, func(e realtime.Event) bool { return e.ID == run.ID })
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: "fixed",
	}})
	bea.await(runnerwire.GlobalRunNotifyEvent, func(e realtime.Event) bool { return e.ID == run.ID })
	done := f.waitGlobal(t, run.ID, "its commit", func(r runnerwire.Run) bool {
		return r.Status == runnerwire.RunSucceeded && r.Commit == testCommit
	})
	if done.Summary != "fixed" {
		t.Fatalf("done = %+v", done)
	}
	// Nobody else hears of it, not even through a project room.
	for _, peer := range []*wsPeer{ann, cal} {
		awaitNoneOf(t, peer, 300*time.Millisecond, runnerwire.GlobalRunsChangedEvent, runnerwire.GlobalRunEventsEvent,
			runnerwire.GlobalRunNotifyEvent, runnerwire.RunsChangedEvent, runnerwire.RunEventsEvent, runnerwire.RunNotifyEvent)
	}

	// Its events, changes and asks read under the global prefix.
	events := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/"+run.ID+"/events", "", nil)
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), "working") {
		t.Fatalf("events = %d %s", events.Code, events.Body)
	}
	changes := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/"+run.ID+"/changes", "", nil)
	if changes.Code != http.StatusOK || !strings.Contains(changes.Body.String(), "web/login.ts") {
		t.Fatalf("changes = %d %s", changes.Code, changes.Body)
	}
	asks := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/"+run.ID+"/asks", "", nil)
	if asks.Code != http.StatusOK || strings.TrimSpace(asks.Body.String()) != `{"asks":[]}` {
		t.Fatalf("asks = %d %s", asks.Code, asks.Body)
	}

	// Audited without the task's words.
	audited, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "run.global."})
	if err != nil {
		t.Fatal(err)
	}
	if len(audited) != 1 || audited[0].Action != "run.global.start" || audited[0].Target != run.ID {
		t.Fatalf("audit = %+v", audited)
	}
	if detail := fmt.Sprint(audited[0].Detail); strings.Contains(detail, "secret") || strings.Contains(detail, "Safari") ||
		!strings.Contains(detail, "fix-the-login-bug") {
		t.Fatalf("audit detail = %s", detail)
	}
	// The request's own row belongs to no project either, whichever one
	// the hub has open.
	writes, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "POST global-runs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 1 || writes[0].Project != "" || audited[0].Project != "" {
		t.Fatalf("global run writes filed under a project: %+v, %+v", writes, audited)
	}

	// The list: bea's run and bea's runner's load.
	list := f.listGlobal(t, &f.bea, "")
	if len(list.Runs) != 1 || list.Runs[0].ID != run.ID || list.More || len(list.Runners) != 1 {
		t.Fatalf("list = %+v", list)
	}
	load := list.Runners[0]
	if load.RunnerID != f.runner.Runner.ID || load.Name != "bea-pc" || !load.Online || load.MaxConcurrent != 2 ||
		load.GlobalRuns != "" || load.Settable != runnerwire.RunnerBlockedSettingsOff || load.Busy != 0 ||
		load.Today.Runs != 1 {
		t.Fatalf("load = %+v", load)
	}
}

func TestGlobalRunsAreAPersonsOwn(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	run, _ := f.startedGlobal(t, conn, nil)
	nodeRun := f.dispatch(t, f.bea, f.runBodyBoth(""))
	expectOffer(t, conn, nodeRun.ID)

	// An agent acting for bea gets nothing on any global route.
	for _, route := range [][2]string{
		{http.MethodPost, "/api/global-runs"}, {http.MethodGet, "/api/global-runs"},
		{http.MethodPost, "/api/global-runs/cancel-all"}, {http.MethodGet, "/api/global-runs/" + run.ID},
		{http.MethodGet, "/api/global-runs/" + run.ID + "/events"}, {http.MethodPost, "/api/global-runs/" + run.ID + "/cancel"},
		{http.MethodGet, "/api/global-runs/" + run.ID + "/terminal"}, {http.MethodGet, "/api/global-runs/" + run.ID + "/workspace"},
		{http.MethodPost, "/api/global-runs/" + run.ID + "/workspace/discard"},
	} {
		body := ""
		if route[0] == http.MethodPost {
			body = f.globalBody(nil)
		}
		if response := f.doAsAgent(t, &f.bea, route[0], route[1], body); response.Code != http.StatusForbidden ||
			!strings.Contains(response.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("agent %s %s = %d %s", route[0], route[1], response.Code, response.Body)
		}
	}

	// cal sees neither the run nor its list entry, and cannot use bea's runner.
	for _, path := range []string{"", "/events", "/changes", "/asks", "/workspace"} {
		if code := f.do(t, &f.cal, http.MethodGet, "/api/global-runs/"+run.ID+path, "", nil).Code; code != http.StatusNotFound {
			t.Fatalf("cal GET %s = %d", path, code)
		}
	}
	if code := f.do(t, &f.cal, http.MethodPost, "/api/global-runs/"+run.ID+"/cancel", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("cal cancel = %d", code)
	}
	if list := f.listGlobal(t, &f.cal, ""); len(list.Runs) != 0 || len(list.Runners) != 0 {
		t.Fatalf("cal's list = %+v", list)
	}
	if answer := f.global(t, &f.cal, http.MethodPost, "/api/global-runs", f.globalBody(nil), nil); answer.code != http.StatusNotFound {
		t.Fatalf("cal on bea's runner = %d %s", answer.code, answer.body)
	}
	// The administrator may open it by id; the tab is still their own.
	if answer := f.global(t, &f.ann, http.MethodGet, "/api/global-runs/"+run.ID, "", nil); answer.code != http.StatusOK || answer.run.ID != run.ID {
		t.Fatalf("ann GET = %d %s", answer.code, answer.body)
	}
	if list := f.listGlobal(t, &f.ann, ""); len(list.Runs) != 0 || len(list.Runners) != 0 {
		t.Fatalf("ann's list = %+v", list)
	}

	// Each prefix shows its own runs only.
	if _, code := f.getRun(t, f.bea, run.ID); code != http.StatusNotFound {
		t.Fatalf("GET /api/runs/<global> = %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodPost, "/api/runs/"+run.ID+"/cancel", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("POST /api/runs/<global>/cancel = %d", code)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/"+nodeRun.ID, "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("GET /api/global-runs/<node run> = %d", code)
	}
	if list := f.do(t, &f.bea, http.MethodGet, "/api/runs?nodeId="+runsTestNode, "", nil); list.Code != http.StatusOK ||
		strings.Contains(list.Body.String(), run.ID) {
		t.Fatalf("the node's run list = %d %s", list.Code, list.Body)
	}

	// Stop it, then stop all: the queued second run goes too.
	cancelled := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+run.ID+"/cancel", "", nil)
	if cancelled.code != http.StatusOK || cancelled.run.ID != run.ID {
		t.Fatalf("cancel = %d %s", cancelled.code, cancelled.body)
	}
	expectCancel(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunCancelled}})
	f.waitGlobal(t, run.ID, "cancelled", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunCancelled })
	second := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) { r.Slug = "second"; r.OnBusy = runnerwire.OnBusyBoth }))
	third := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) { r.Slug = "third"; r.OnBusy = runnerwire.OnBusyBoth }))
	response := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/cancel-all", "{}", nil)
	var all runnerwire.CancelAllResponse
	if err := json.Unmarshal(response.Body.Bytes(), &all); err != nil || response.Code != http.StatusOK || all.Cancelled != 2 {
		t.Fatalf("cancel-all = %d %s", response.Code, response.Body)
	}
	for _, id := range []string{second.ID, third.ID} {
		f.waitGlobal(t, id, "cancelled", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunCancelled })
	}
	if node, code := f.getRun(t, f.bea, nodeRun.ID); code != http.StatusOK || node.Status == runnerwire.RunCancelled {
		t.Fatalf("cancel-all stopped the node's run: %d %s", code, node.Status)
	}
}

func TestGlobalRunRefusals(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	pingPong(t, conn)
	for _, c := range []struct {
		edit  func(*runnerwire.CreateGlobalRunRequest)
		field string
	}{
		{func(r *runnerwire.CreateGlobalRunRequest) { r.Task = "  " }, "task"},
		{func(r *runnerwire.CreateGlobalRunRequest) {
			r.Task = strings.Repeat("x", runnerwire.MaxGlobalTaskBytes+1)
		}, "task"},
		{func(r *runnerwire.CreateGlobalRunRequest) { r.Slug = "Bad Slug" }, "slug"},
		{func(r *runnerwire.CreateGlobalRunRequest) { r.Base = "--upload-pack=x" }, "base"},
		{func(r *runnerwire.CreateGlobalRunRequest) { r.Mode = "tty" }, "mode"},
		{func(r *runnerwire.CreateGlobalRunRequest) { r.Agent = "gemini" }, "agent"},
		{func(r *runnerwire.CreateGlobalRunRequest) { r.Model = "nope" }, "model"},
		{func(r *runnerwire.CreateGlobalRunRequest) { r.RepoID = "repo-2" }, "repoId"},
	} {
		answer := f.global(t, &f.bea, http.MethodPost, "/api/global-runs", f.globalBody(c.edit), nil)
		if answer.code != http.StatusBadRequest || answer.failure.Code != "INVALID_ARGUMENT" || answer.failure.Field != c.field {
			t.Errorf("want 400 on %s: %d %s", c.field, answer.code, answer.body)
		}
	}
	// A body past 72 KB (twice the task, for its JSON escapes, and room), or
	// with a field the API does not know.
	if answer := f.global(t, &f.bea, http.MethodPost, "/api/global-runs", `{"task":"`+strings.Repeat("x", 73<<10)+`"}`, nil); answer.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d", answer.code)
	}
	if answer := f.global(t, &f.bea, http.MethodPost, "/api/global-runs", `{"task":"x","nodeId":"fix-login"}`, nil); answer.code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", answer.code, answer.body)
	}

	// Outdated runner, then one whose owner turned global runs off.
	outdated := runsHello(2)
	sendEnvelope(t, conn, outdated)
	pingPong(t, conn)
	if answer := f.global(t, &f.bea, http.MethodPost, "/api/global-runs", f.globalBody(nil), nil); answer.code != http.StatusPreconditionFailed ||
		answer.failure.Code != "RUNNER_OUTDATED" || answer.failure.Blocked != runnerwire.RunnerBlockedOutdated {
		t.Fatalf("outdated = %d %s", answer.code, answer.body)
	}
	off := runsHello(2)
	off.Hello.Capabilities = append(off.Hello.Capabilities, runnerwire.CapGlobalRuns)
	sendEnvelope(t, conn, off)
	pingPong(t, conn)
	if answer := f.global(t, &f.bea, http.MethodPost, "/api/global-runs", f.globalBody(nil), nil); answer.code != http.StatusConflict ||
		answer.failure.Code != "GLOBAL_RUNS_OFF" || answer.failure.Blocked != runnerwire.RunnerBlockedOff {
		t.Fatalf("turned off = %d %s", answer.code, answer.body)
	}
	if load := f.listGlobal(t, &f.bea, "").Runners[0]; load.GlobalRuns != runnerwire.RunnerBlockedOff || load.Settable != runnerwire.RunnerBlockedSettingsOff {
		t.Fatalf("load of a runner with global runs off = %+v", load)
	}

	// The owner's budget holds.
	sendEnvelope(t, conn, globalRunsHello())
	pingPong(t, conn)
	if code, _ := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":1}`); code != http.StatusOK {
		t.Fatalf("set budget = %d", code)
	}
	f.startGlobal(t, &f.bea, f.globalBody(nil))
	if answer := f.global(t, &f.bea, http.MethodPost, "/api/global-runs", f.globalBody(nil), nil); answer.code != http.StatusTooManyRequests ||
		answer.failure.Code != "RESOURCE_EXHAUSTED" || answer.failure.Exceeded != "runs" {
		t.Fatalf("over budget = %d %s", answer.code, answer.body)
	}
	for _, query := range []string{"?limit=0", "?limit=201", "?limit=x", "?before=" + strings.Repeat("9", 65)} {
		if code := f.do(t, &f.bea, http.MethodGet, "/api/global-runs"+query, "", nil).Code; code != http.StatusBadRequest {
			t.Errorf("GET %s = %d", query, code)
		}
	}
}

// The 32 KB limit is the task's own: the same task as JSON, where every
// quote, backslash and line break takes two bytes (Windows paths, a pasted
// log), still fits the body.
func TestGlobalRunTaskLimitIsTheTasksNotItsJSON(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	pingPong(t, conn)
	task := "Fix the paths\n" + strings.Repeat(`C:\a "b"`+"\n", (runnerwire.MaxGlobalTaskBytes-64)/9)
	if len(task) > runnerwire.MaxGlobalTaskBytes {
		t.Fatalf("test task is %d bytes", len(task))
	}
	run := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) { r.Task = task }))
	if run.Title != "Fix the paths" {
		t.Fatalf("run = %+v", run)
	}
}

// The members gate is a project's: a global agent keeps working whichever
// project the request names, even one the person may only read or not at all.
func TestGlobalRunsIgnoreTheProjectGate(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restrict other: %d %s", response.Code, response.Body)
	}
	ctx := context.Background()
	for _, who := range []struct {
		b      browser
		name   string
		header map[string]string
		query  string
	}{
		{f.cal, "cal-pc", nil, "?project=other"},                               // read-only member of other
		{f.dan, "dan-pc", map[string]string{"X-Nodevas-Project": "other"}, ""}, // not a member at all
		{f.bea, "bea-pc", nil, ""},                                             // no project named
	} {
		runner := f.create(t, who.b, who.name)
		hello := globalRunsHello()
		if err := f.server.runners.SetHello(ctx, runner.Runner.ID, *hello.Hello); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(runnerwire.CreateGlobalRunRequest{RunnerID: runner.Runner.ID, RepoID: "repo-1", Agent: "claude", Task: "Tidy up"})
		b := who.b
		response := f.do(t, &b, http.MethodPost, "/api/global-runs"+who.query, string(body), who.header)
		if response.Code != http.StatusCreated {
			t.Fatalf("%s POST = %d %s", who.name, response.Code, response.Body)
		}
		var run runnerwire.Run
		_ = json.Unmarshal(response.Body.Bytes(), &run)
		if run.Status != runnerwire.RunQueued || !run.Global {
			t.Fatalf("%s run = %+v", who.name, run)
		}
		list := f.do(t, &b, http.MethodGet, "/api/global-runs"+who.query, "", who.header)
		if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), run.ID) {
			t.Fatalf("%s list = %d %s", who.name, list.Code, list.Body)
		}
		get := f.do(t, &b, http.MethodGet, "/api/global-runs/"+run.ID+who.query, "", who.header)
		if get.Code != http.StatusOK {
			t.Fatalf("%s GET = %d %s", who.name, get.Code, get.Body)
		}
		cancel := f.do(t, &b, http.MethodPost, "/api/global-runs/"+run.ID+"/cancel"+who.query, "", who.header)
		if cancel.Code != http.StatusOK {
			t.Fatalf("%s cancel = %d %s", who.name, cancel.Code, cancel.Body)
		}
	}
	// The gate itself still stands for project routes.
	if response := f.do(t, &f.dan, http.MethodGet, "/api/runs?project=other", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("dan's project runs = %d", response.Code)
	}
}

func TestGlobalRunTerminal(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/global-runs/abc123/terminal":   true,
		"/api/global-runs//terminal":         false,
		"/api/global-runs/a/b/terminal":      false,
		"/api/global-runs/abc123/events":     false,
		"/api/global-runs/abc123/terminal/x": false,
		"/api/global-runs/terminal":          false,
	} {
		if got := runTerminalPath(path); got != want {
			t.Errorf("runTerminalPath(%q) = %v", path, got)
		}
	}
	if class, _ := ClassifyRoute(http.MethodGet, "/api/global-runs/abc123/terminal"); class != ClassWSUpgrade {
		t.Fatalf("global terminal socket class = %s", class)
	}

	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	run, job := f.startedGlobal(t, conn, func(r *runnerwire.CreateGlobalRunRequest) { r.Mode = runnerwire.ModeInteractive })
	if job.Mode != runnerwire.ModeInteractive || run.Mode != runnerwire.ModeInteractive {
		t.Fatalf("mode = %q / %q", job.Mode, run.Mode)
	}
	// A cross-site page cannot open it, with or without an Origin.
	request := httptest.NewRequest(http.MethodGet, "/api/global-runs/"+run.ID+"/terminal", nil)
	withCookies(request, f.bea.cookies)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site GET = %d %s", response.Code, response.Body)
	}
	dial := func(path string, extra http.Header) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		header := http.Header{"Cookie": {cookieHeader(f.bea.cookies)}}
		for key, values := range extra {
			header[key] = values
		}
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.liveURL, "http")+path, &websocket.DialOptions{HTTPHeader: header})
	}
	if _, response, err := dial("/api/global-runs/"+run.ID+"/terminal", http.Header{"Origin": {"https://evil.example"}}); err == nil ||
		response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin dial: %v %+v", err, response)
	}
	// Its own page opens it; the project route does not know it.
	term, _, err := dial("/api/global-runs/"+run.ID+"/terminal", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = term.CloseNow()
	if _, response, err := dial("/api/runs/"+run.ID+"/terminal", nil); err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("project terminal route: %v %+v", err, response)
	}
}

// Phase-1 workspace actions work on a global run through its prefix, naming
// its slug for the runner's own-branch check.
func TestGlobalRunWorkspaceDiscard(t *testing.T) {
	shortWorkspaceWait(t, 10*time.Second)
	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	run, job := f.startedGlobal(t, conn, func(r *runnerwire.CreateGlobalRunRequest) { r.Slug = "tidy" })
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunSucceeded}})
	f.waitGlobal(t, run.ID, "its commit", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunSucceeded && r.Commit == testCommit })
	pingPong(t, conn)

	// The view asks the runner for run_info, naming the slug.
	viewed := make(chan globalAnswer, 1)
	go func() {
		viewed <- f.global(t, &f.bea, http.MethodGet, "/api/global-runs/"+run.ID+"/workspace", "", nil)
	}()
	info := readEnvelopeForTest(t, conn)
	if info.Type != runnerwire.TypeRepoRequest || info.Repo == nil || info.Repo.Op != runnerwire.RepoOpRunInfo ||
		info.Repo.Slug != "tidy" || info.Repo.NodeID != "" || info.Repo.RunID != run.ID {
		t.Fatalf("run_info request = %+v", info)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{
		ID: info.Repo.ID, RunInfo: &runnerwire.RunWorkspaceInfo{Exists: true, Branch: "agent/tidy-" + run.ID[:8], OwnBranch: true},
	}})
	select {
	case view := <-viewed:
		if view.code != http.StatusOK || !strings.Contains(view.body, `"owner":true`) || !strings.Contains(view.body, `"ownBranch":true`) {
			t.Fatalf("workspace view = %d %s", view.code, view.body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the workspace view never answered")
	}

	out := make(chan globalAnswer, 1)
	go func() {
		out <- f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+run.ID+"/workspace/discard", "{}", nil)
	}()
	action := expectWorkspaceAction(t, conn, run.ID)
	if action.Action != runnerwire.WorkspaceDiscard || action.Slug != job.Slug || action.NodeID != "" ||
		action.Branch != "agent/tidy-"+run.ID[:8] || action.AccountID != f.runner.Runner.OwnerID {
		t.Fatalf("workspace action = %+v", action)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceResult, RunID: run.ID, WorkspaceResult: &runnerwire.WorkspaceOutcome{
		ID: action.ID, RunID: run.ID, Action: action.Action, WorktreeRemoved: true, BranchDeleted: true,
	}})
	var answer globalAnswer
	select {
	case answer = <-out:
	case <-time.After(20 * time.Second):
		t.Fatal("the discard never answered")
	}
	if answer.code != http.StatusOK || answer.run.Workspace != runnerwire.RunWorkspaceDiscarded {
		t.Fatalf("discard = %d %s", answer.code, answer.body)
	}
	// The project prefix refuses the same run.
	if code, _, _ := f.workspacePost(t, &f.bea, run.ID, "discard", "{}"); code != http.StatusNotFound {
		t.Fatalf("project workspace route = %d", code)
	}
}

// A global agent has no board: should anything on its runner speak for it,
// the proxy lets it check the hub is there and nothing more.
func TestProxyRefusesAGlobalRunsBoardCalls(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalRunsHello())
	run, _ := f.startedGlobal(t, conn, nil)
	for _, call := range [][2]string{
		{http.MethodGet, "graph"}, {http.MethodGet, "nodes/" + runsTestNode}, {http.MethodPost, "agents/asks"},
		{http.MethodPost, "agents/heartbeat"}, {http.MethodGet, "projects"},
	} {
		code, body := f.proxied(t, run.ID, call[0], call[1], "{}", nil)
		if code != http.StatusForbidden || !strings.Contains(body, "GLOBAL_RUN_NO_BOARD") {
			t.Fatalf("%s %s = %d %s", call[0], call[1], code, body)
		}
	}
	for _, path := range []string{"auth/status", "ready"} {
		if code, body := f.proxied(t, run.ID, http.MethodGet, path, "", nil); code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, body)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, target := range []string{"/api/global-runs", "/api/global-runs/x", "/api/global-runs/cancel-all"} {
			if runnerProxyAllows(method, target) {
				t.Fatalf("the runner proxy allows %s %s", method, target)
			}
		}
	}
}
