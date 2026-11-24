package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func busyGlobalHello(maxConcurrent int) runnerwire.Envelope {
	env := globalGroupHello(maxConcurrent)
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapContinue, runnerwire.CapCoord)
	env.Hello.Agents = []runnerwire.Agent{
		{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}, {ID: "opus"}}},
		{Kind: "codex", Installed: true, Models: []runnerwire.Model{{ID: "gpt-5.5", Default: true}}},
	}
	return env
}

func TestBusyRoutesPersonOnly(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, busyGlobalHello(2))
	pingPong(t, conn)

	for _, path := range []string{
		"/api/runs/busy?runnerId=" + f.runner.Runner.ID + "&repoId=repo-1&nodeId=" + runsTestNode,
		"/api/global-runs/busy?runnerId=" + f.runner.Runner.ID + "&repoIds=repo-1",
	} {
		res := f.doAsAgent(t, &f.bea, http.MethodGet, path, "")
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("agent GET %s = %d %s, want 403 PERMISSION_DENIED", path, res.Code, res.Body)
		}
	}
}

func TestGlobalBusyOwnRunnerOnly(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, busyGlobalHello(2))
	pingPong(t, conn)

	path := "/api/global-runs/busy?runnerId=" + f.runner.Runner.ID + "&repoIds=repo-1"

	// Another member cannot query bea's runner.
	if res := f.do(t, &f.cal, http.MethodGet, path, "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("cal GET %s = %d %s, want 404", path, res.Code, res.Body)
	}
	// Even an admin cannot query another member's runner on global busy.
	if res := f.do(t, &f.ann, http.MethodGet, path, "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("admin GET %s = %d %s, want 404", path, res.Code, res.Body)
	}
	// The runner's owner gets 200 with per-repo busy payload.
	res := f.do(t, &f.bea, http.MethodGet, path, "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("bea GET %s = %d %s, want 200", path, res.Code, res.Body)
	}
	if cache := res.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q", cache)
	}
	var got runnerwire.GlobalBusyResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil || len(got.Repos) != 1 || got.Repos[0].RepoID != "repo-1" {
		t.Fatalf("body = %+v, err = %v", got, err)
	}
}

func TestStartGlobalAsksWhenRepoBusy(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, busyGlobalHello(2))
	pingPong(t, conn)

	// Start first global run on repo-1.
	first := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) {
		r.RepoID = "repo-1"
		r.Agent = "claude"
		r.Model = "opus"
		r.OpenPR = true
		r.Task = "Fix auth flow"
	}))
	expectOffer(t, conn, first.ID)

	// Preflight GET /api/global-runs/busy shows the holder and continue info.
	pre := f.do(t, &f.bea, http.MethodGet, "/api/global-runs/busy?runnerId="+f.runner.Runner.ID+"&repoIds=repo-1,repo-2", "", nil)
	if pre.Code != http.StatusOK {
		t.Fatalf("preflight = %d %s", pre.Code, pre.Body)
	}
	var preBody runnerwire.GlobalBusyResponse
	if err := json.Unmarshal(pre.Body.Bytes(), &preBody); err != nil || len(preBody.Repos) != 2 {
		t.Fatalf("preflight body = %+v, %v", preBody, err)
	}
	if len(preBody.Repos[0].Holders) != 1 || preBody.Repos[0].Holders[0].RunID != first.ID ||
		preBody.Repos[0].Continue == nil || preBody.Repos[0].Continue.RunID != first.ID ||
		preBody.Repos[0].Continue.Agent != "claude" || preBody.Repos[0].Continue.Model != "opus" ||
		!preBody.Repos[0].Continue.OpenPR {
		t.Fatalf("repo-1 preflight = %+v", preBody.Repos[0])
	}
	if len(preBody.Repos[1].Holders) != 0 || preBody.Repos[1].Others != 0 {
		t.Fatalf("repo-2 preflight should be free: %+v", preBody.Repos[1])
	}

	// Second global start on repo-1 without onBusy gets 409 BUSY.
	secondBody := f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) {
		r.RepoID = "repo-1"
		r.Agent = "codex"
		r.Task = "Another task"
	})
	resp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs", secondBody, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("second global start = %d %s, want 409", resp.Code, resp.Body)
	}
	var busy struct {
		Code     string                   `json:"code"`
		Holders  []runnerwire.RunHolder   `json:"holders"`
		Others   int                      `json:"others"`
		Choices  []string                 `json:"choices"`
		Continue *runnerwire.BusyContinue `json:"continue"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &busy); err != nil {
		t.Fatal(err)
	}
	if busy.Code != "BUSY" || len(busy.Holders) != 1 || busy.Holders[0].RunID != first.ID || busy.Others != 0 {
		t.Fatalf("busy = %+v", busy)
	}
	for _, want := range []string{runnerwire.OnBusyQueue, runnerwire.OnBusyTakeOver, runnerwire.OnBusyBoth, runnerwire.OnBusyContinue} {
		if !slices.Contains(busy.Choices, want) {
			t.Errorf("choices %v missing %q", busy.Choices, want)
		}
	}
	if busy.Continue == nil || busy.Continue.RunID != first.ID || busy.Continue.Agent != "claude" || busy.Continue.Model != "opus" || !busy.Continue.OpenPR {
		t.Fatalf("continue = %+v", busy.Continue)
	}
}

func TestSimpleStartQueueIsFreshRun(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, busyGlobalHello(2))
	pingPong(t, conn)

	first := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) {
		r.RepoID = "repo-1"
		r.Agent = "claude"
		r.Model = "opus"
		r.Simple = true
		r.Task = "Make a birthday page"
	}))
	expectOffer(t, conn, first.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: first.ID, Base: "main", Branch: "agent/birthday-1",
	}})
	f.waitGlobal(t, first.ID, "running", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunRunning })

	// Queueing a Simple start creates a fresh run (not a follow-up), with its own agent and afterRun set.
	queued := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) {
		r.RepoID = "repo-1"
		r.Agent = "codex"
		r.Model = "gpt-5.5"
		r.Simple = true
		r.OpenPR = true
		r.Task = "Make a recipe page"
		r.OnBusy = runnerwire.OnBusyQueue
	}))
	if queued.Status != runnerwire.RunQueued || queued.AfterRun != first.ID || queued.BusyChoice != runnerwire.OnBusyQueue ||
		queued.ContinueOf != "" || queued.WorkRoot != "" || queued.Agent != "codex" || queued.NoPR {
		t.Fatalf("queued fresh run = %+v", queued)
	}

	// When the first finishes, the queued fresh run is offered.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: first.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: "Built the birthday page.",
	}})
	job := expectOffer(t, conn, queued.ID)
	if job.ContinueOf != "" || job.WorkRoot != "" || job.Agent != "codex" {
		t.Fatalf("offered fresh job = %+v", job)
	}
	if !strings.Contains(job.Prompt, "Built the birthday page.") {
		t.Fatalf("carry-over summary missing from prompt:\n%s", job.Prompt)
	}
}

func TestGroupBusyOffersOnlyBothAndSkip(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, busyGlobalHello(4))
	pingPong(t, conn)

	// Make repo-1 busy.
	first := f.startGlobal(t, &f.bea, f.globalBody(func(r *runnerwire.CreateGlobalRunRequest) {
		r.RepoID = "repo-1"
		r.Task = "Busy in api"
	}))
	expectOffer(t, conn, first.ID)

	// Group start on repo-1, repo-2, repo-3 without onBusy -> 409 BUSY with repos[] and only both/skip choices.
	resp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(nil), nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("group busy = %d %s, want 409", resp.Code, resp.Body)
	}
	var busy struct {
		Code  string                `json:"code"`
		Repos []runnerwire.RepoBusy `json:"repos"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &busy); err != nil {
		t.Fatal(err)
	}
	if busy.Code != "BUSY" || len(busy.Repos) != 1 || busy.Repos[0].RepoID != "repo-1" ||
		len(busy.Repos[0].Holders) != 1 || busy.Repos[0].Holders[0].RunID != first.ID {
		t.Fatalf("group busy body = %+v", busy)
	}
	if !slices.Equal(busy.Repos[0].Choices, []string{runnerwire.OnBusyBoth, runnerwire.OnBusySkip}) {
		t.Fatalf("group choices = %v, want [both skip]", busy.Repos[0].Choices)
	}

	// Group with onBusy=skip drops repo-1 and starts only repo-2 and repo-3 (budget counts only 2).
	if code, _ := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":3}`); code != http.StatusOK {
		t.Fatalf("set budget = %d", code)
	}
	skipResp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(func(r *runnerwire.CreateGlobalGroupRequest) {
		r.OnBusy = runnerwire.OnBusySkip
	}), nil)
	if skipResp.Code != http.StatusCreated {
		t.Fatalf("group skip = %d %s, want 201", skipResp.Code, skipResp.Body)
	}
	var skipped runnerwire.GlobalGroupResponse
	if err := json.Unmarshal(skipResp.Body.Bytes(), &skipped); err != nil {
		t.Fatal(err)
	}
	if skipped.Group.RepoCount != 2 || len(skipped.Runs) != 2 {
		t.Fatalf("skipped group = %+v", skipped)
	}
	for _, r := range skipped.Runs {
		if r.RepoID == "repo-1" {
			t.Fatalf("busy repo-1 was not skipped: %+v", skipped.Runs)
		}
	}
}

func TestNodeBusyTakeOverHTTPForbiddenAndAdmin(t *testing.T) {
	f := newRunsFixture(t)
	beaConn := f.connect(t, runsHello(2))
	pingPong(t, beaConn)

	// Bea starts a run on fix-login.
	beaRun := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, beaConn, beaRun.ID)

	// Cal connects his own runner and tries takeOver -> 403 NOT_YOURS_TO_STOP.
	calRunner := f.create(t, f.cal, "cal-pc")
	calConn := runnerSocket(t, f.liveURL, calRunner.Token)
	sendEnvelope(t, calConn, runsHello(2))
	if welcome := readEnvelopeForTest(t, calConn); welcome.Type != runnerwire.TypeWelcome {
		t.Fatalf("welcome = %+v", welcome)
	}
	calTakeOver, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: calRunner.Runner.ID, Agent: "claude", RepoID: "repo-1",
		OnBusy: runnerwire.OnBusyTakeOver, HolderIDs: []string{beaRun.ID},
	})
	resp := f.do(t, &f.cal, http.MethodPost, "/api/runs", string(calTakeOver), nil)
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"NOT_YOURS_TO_STOP"`) {
		t.Fatalf("cal takeOver = %d %s, want 403 NOT_YOURS_TO_STOP", resp.Code, resp.Body)
	}

	// Admin Ann connects her own runner and sees Bea's holder with takeOver allowed.
	annRunner := f.create(t, f.ann, "ann-pc")
	annConn := runnerSocket(t, f.liveURL, annRunner.Token)
	sendEnvelope(t, annConn, runsHello(2))
	if welcome := readEnvelopeForTest(t, annConn); welcome.Type != runnerwire.TypeWelcome {
		t.Fatalf("welcome = %+v", welcome)
	}
	annTakeOver, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: annRunner.Runner.ID, Agent: "claude", RepoID: "repo-1",
		OnBusy: runnerwire.OnBusyTakeOver, HolderIDs: []string{beaRun.ID},
	})
	taken := f.dispatch(t, f.ann, string(annTakeOver))
	if taken.AfterRun != beaRun.ID || taken.BusyChoice != runnerwire.OnBusyTakeOver {
		t.Fatalf("admin takeOver run = %+v", taken)
	}
	expectCancel(t, beaConn, beaRun.ID)
}
