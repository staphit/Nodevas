package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// workspaceHello is a runner that knows run workspaces, lets its owner act
// on them and browses repos.
func workspaceHello() runnerwire.Envelope {
	env := runsHello(2)
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapRunWorkspace, runnerwire.CapWorkspaceActions,
		runnerwire.CapRepoBrowse)
	return env
}

// localRun is a succeeded run with a commit and no PR: autoPR off.
func (f *runsFixture) localRun(t *testing.T, conn *websocket.Conn) runnerwire.Run {
	t.Helper()
	run := f.startedRun(t, conn)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded,
	}})
	done := f.waitRun(t, run.ID, "its commit", func(r runnerwire.Run) bool {
		return r.Status == runnerwire.RunSucceeded && r.Commit == testCommit
	})
	pingPong(t, conn)
	return done
}

// shortWorkspaceWait makes the workspace POSTs answer 202 after wait.
func shortWorkspaceWait(t *testing.T, wait time.Duration) {
	t.Helper()
	old := runsapi.WorkspaceWait
	runsapi.WorkspaceWait = wait
	t.Cleanup(func() { runsapi.WorkspaceWait = old })
}

type workspaceError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Blocked string `json:"blocked"`
}

func (f *runsFixture) workspacePost(t *testing.T, b *browser, runID, action, body string) (int, runnerwire.Run, workspaceError) {
	t.Helper()
	response := f.do(t, b, http.MethodPost, "/api/runs/"+runID+"/workspace/"+action, body, nil)
	var run runnerwire.Run
	var failure workspaceError
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	_ = json.Unmarshal(response.Body.Bytes(), &failure)
	return response.Code, run, failure
}

func mergeBody(head string) string {
	raw, _ := json.Marshal(runnerwire.WorkspaceMergeBody{HeadSHA: head})
	return string(raw)
}

func expectWorkspaceAction(t *testing.T, conn *websocket.Conn, runID string) runnerwire.WorkspaceActionJob {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeWorkspaceAction || env.WorkspaceAction == nil || env.WorkspaceAction.RunID != runID {
		t.Fatalf("got %+v, want a workspace action for %s", env, runID)
	}
	return *env.WorkspaceAction
}

type postResult struct {
	code    int
	run     runnerwire.Run
	failure workspaceError
}

// postAsync sends a workspace POST that waits for the runner's answer.
func (f *runsFixture) postAsync(t *testing.T, b *browser, runID, action, body string) chan postResult {
	t.Helper()
	out := make(chan postResult, 1)
	go func() {
		response := f.do(t, b, http.MethodPost, "/api/runs/"+runID+"/workspace/"+action, body, nil)
		var res postResult
		res.code = response.Code
		_ = json.Unmarshal(response.Body.Bytes(), &res.run)
		_ = json.Unmarshal(response.Body.Bytes(), &res.failure)
		out <- res
	}()
	return out
}

func awaitPost(t *testing.T, ch chan postResult) postResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(20 * time.Second):
		t.Fatal("the POST never answered")
	}
	return postResult{}
}

func (f *runsFixture) auditActions(t *testing.T, prefix string) map[string]string {
	t.Helper()
	events, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range events {
		out[e.Action] = e.Target
	}
	return out
}

func TestRunWorkspaceOwnerAllowed(t *testing.T) {
	shortWorkspaceWait(t, 10*time.Second)
	f := newRunsFixture(t)
	conn := f.connect(t, workspaceHello())
	run := f.localRun(t, conn)

	pending := f.postAsync(t, &f.bea, run.ID, "merge", mergeBody(testCommit))
	job := expectWorkspaceAction(t, conn, run.ID)
	if job.AccountID != f.runner.Runner.OwnerID || job.Action != runnerwire.WorkspaceMergeLocal || job.HeadSHA != testCommit ||
		job.Branch != "agent/fix-login" || job.Base != "main" || job.RepoID != "repo-1" || job.NodeID != runsTestNode || job.ID == "" {
		t.Fatalf("job = %+v", job)
	}
	merged := strings.Repeat("e", 40)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceResult, RunID: run.ID, WorkspaceResult: &runnerwire.WorkspaceOutcome{
		ID: job.ID, RunID: run.ID, Action: job.Action, MergeCommit: merged, WorktreeRemoved: true, BranchDeleted: true,
	}})
	res := awaitPost(t, pending)
	if res.code != http.StatusOK || res.run.Workspace != runnerwire.RunWorkspaceMergedLocal || res.run.WorkspaceCommit != merged ||
		res.run.WorkspaceAction == nil || res.run.WorkspaceAction.Status != runnerwire.WorkspaceDone || res.run.Status != runnerwire.RunSucceeded {
		t.Fatalf("merge = %d %+v %+v", res.code, res.run.WorkspaceAction, res.failure)
	}
	actions := f.auditActions(t, "run.workspace.")
	if actions["run.workspace.merge.request"] != run.ID || actions["run.workspace.merge.done"] != run.ID {
		t.Fatalf("audit = %+v", actions)
	}
	// Merged: nothing more to do, either way.
	if code, _, failure := f.workspacePost(t, &f.bea, run.ID, "discard", "{}"); code != http.StatusConflict || failure.Blocked != runnerwire.WorkspaceGateMergedLocal {
		t.Fatalf("discard after a merge = %d %+v", code, failure)
	}

	// A runner's refusal is a 200 whose action failed.
	other := f.localRun(t, conn)
	pending = f.postAsync(t, &f.bea, other.ID, "discard", "")
	job = expectWorkspaceAction(t, conn, other.ID)
	// The run's commit goes with a discard too: the runner deletes the
	// branch only up to it.
	if job.Action != runnerwire.WorkspaceDiscard || job.HeadSHA != testCommit {
		t.Fatalf("discard job = %+v", job)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceResult, RunID: other.ID, WorkspaceResult: &runnerwire.WorkspaceOutcome{
		ID: job.ID, RunID: other.ID, Action: job.Action, Code: runnerwire.WorkspaceErrFilesBusy, Error: "a file is open",
	}})
	res = awaitPost(t, pending)
	if res.code != http.StatusOK || res.run.Workspace != "" || res.run.WorkspaceAction.Status != runnerwire.WorkspaceFailed ||
		res.run.WorkspaceAction.Code != runnerwire.WorkspaceErrFilesBusy || res.run.WorkspaceAction.Reason != "a file is open" {
		t.Fatalf("refused discard = %d %+v", res.code, res.run.WorkspaceAction)
	}
	if f.auditActions(t, "run.workspace.discard.")["run.workspace.discard.failed"] != other.ID {
		t.Fatal("no refused-attempt audit row")
	}
}

func TestRunWorkspaceRefusals(t *testing.T) {
	shortWorkspaceWait(t, 300*time.Millisecond)
	f := newRunsFixture(t)
	conn := f.connect(t, workspaceHello())
	run := f.localRun(t, conn)

	// Someone who cannot see the run: 404. The administrator, who can, is
	// still not the runner's owner.
	if code, _, _ := f.workspacePost(t, &f.cal, run.ID, "discard", "{}"); code != http.StatusNotFound {
		t.Fatalf("cal = %d", code)
	}
	for _, path := range []string{"merge", "discard"} {
		code, _, failure := f.workspacePost(t, &f.ann, run.ID, path, mergeBody(testCommit))
		if code != http.StatusForbidden || failure.Code != "PERMISSION_DENIED" {
			t.Fatalf("administrator %s = %d %+v", path, code, failure)
		}
	}
	for _, path := range []string{"tree", "file?path=README", "git/status"} {
		if code := f.do(t, &f.ann, http.MethodGet, "/api/runs/"+run.ID+"/workspace/"+path, "", nil).Code; code != http.StatusForbidden {
			t.Fatalf("administrator GET %s = %d", path, code)
		}
	}
	// A run on bea's runner that cal dispatched: cal sees it, but it is not
	// cal's disk.
	calID := f.create(t, f.cal, "cal-pc").Runner.OwnerID
	theirs, err := f.server.runs.Store().Create(context.Background(), runs.New{
		Project: f.projectName(), NodeID: runsTestNode, NodeTitle: "Fix the login bug",
		RunnerID: f.runner.Runner.ID, RunnerName: "bea-pc", OwnerID: f.runner.Runner.OwnerID,
		Agent: "claude", Model: "default", RepoID: "repo-1", RepoName: "visual-editor",
		JobPrompt: "# Fix", CreatedBy: calID, CreatedByName: "cal",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Ended before it was offered, so no later dispatch offers it.
	if _, err := f.server.runs.Store().Transition(context.Background(), theirs.ID, runnerwire.RunCancelled, runs.Change{}); err != nil {
		t.Fatal(err)
	}
	if _, code := f.getRun(t, f.cal, theirs.ID); code != http.StatusOK {
		t.Fatalf("cal cannot see the run cal dispatched: %d", code)
	}
	for _, path := range []string{"merge", "discard"} {
		if code, _, failure := f.workspacePost(t, &f.cal, theirs.ID, path, mergeBody(testCommit)); code != http.StatusForbidden ||
			failure.Code != "PERMISSION_DENIED" {
			t.Fatalf("dispatcher, not owner, %s = %d %+v", path, code, failure)
		}
	}
	if code := f.do(t, &f.cal, http.MethodGet, "/api/runs/"+theirs.ID+"/workspace/tree", "", nil).Code; code != http.StatusForbidden {
		t.Fatalf("dispatcher, not owner, tree = %d", code)
	}
	// An agent, whoever it acts for: refused on every route.
	for _, req := range [][2]string{{http.MethodGet, "tree"}, {http.MethodGet, "file?path=README"}, {http.MethodGet, "git/status"},
		{http.MethodGet, ""}, {http.MethodPost, "merge"}, {http.MethodPost, "discard"}} {
		path := "/api/runs/" + run.ID + "/workspace"
		if req[1] != "" {
			path += "/" + req[1]
		}
		body := ""
		if req[0] == http.MethodPost {
			body = mergeBody(testCommit)
		}
		response := f.doAsAgent(t, &f.bea, req[0], path, body)
		if response.Code != http.StatusForbidden || errorCode(t, response) != "PERMISSION_DENIED" {
			t.Fatalf("agent %s %s = %d %s", req[0], path, response.Code, response.Body)
		}
	}
	// Bad heads.
	for _, head := range []string{"", "HEAD", strings.Repeat("a", 65)} {
		if code, _, _ := f.workspacePost(t, &f.bea, run.ID, "merge", mergeBody(head)); code != http.StatusBadRequest {
			t.Errorf("head %q = %d", head, code)
		}
	}
	// The branch moved on since the owner looked.
	if code, _, failure := f.workspacePost(t, &f.bea, run.ID, "merge", mergeBody(strings.Repeat("0", 40))); code != http.StatusConflict ||
		failure.Blocked != runnerwire.WorkspaceGateHeadMoved {
		t.Fatalf("stale head = %d %+v", code, failure)
	}
	// Still running.
	running := f.startedRun(t, conn)
	if code, _, failure := f.workspacePost(t, &f.bea, running.ID, "discard", "{}"); code != http.StatusConflict ||
		failure.Code != "FAILED_PRECONDITION" || failure.Blocked != runnerwire.WorkspaceGateRunning {
		t.Fatalf("running = %d %+v", code, failure)
	}
	// An open PR merges there.
	green := f.finishedGreenRun(t, conn)
	if code, _, failure := f.workspacePost(t, &f.bea, green.ID, "merge", mergeBody(testHeadSHA)); code != http.StatusConflict ||
		failure.Blocked != runnerwire.WorkspaceErrHasPR {
		t.Fatalf("open PR = %d %+v", code, failure)
	}
	view := f.workspaceView(t, &f.ann, green.ID)
	if view.Owner || view.Merge.Mode != runnerwire.WorkspaceMergeModePR || view.Discard.Allowed || view.Discard.Blocked != runnerwire.WorkspaceGateNotOwner ||
		view.Files.Blocked != runnerwire.WorkspaceFilesNotOwner || view.InfoError != runnerwire.WorkspaceInfoNotOwner || view.Info != nil {
		t.Fatalf("non-owner view = %+v", view)
	}

	// A runner that turned actions off; one too old to know them.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	off := workspaceHello()
	off.Hello.Capabilities = []string{runnerwire.CapHeadless, runnerwire.CapRunWorkspace}
	conn = f.connect(t, off)
	pingPong(t, conn)
	if code, _, failure := f.workspacePost(t, &f.bea, run.ID, "merge", mergeBody(testCommit)); code != http.StatusConflict ||
		failure.Blocked != runnerwire.WorkspaceGateActionsOff {
		t.Fatalf("actions off = %d %+v", code, failure)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	conn = f.connect(t, runsHello(1))
	pingPong(t, conn)
	if code, _, failure := f.workspacePost(t, &f.bea, run.ID, "merge", mergeBody(testCommit)); code != http.StatusPreconditionFailed ||
		failure.Code != "RUNNER_OUTDATED" {
		t.Fatalf("outdated = %d %+v", code, failure)
	}
	if response := f.do(t, &f.bea, http.MethodGet, "/api/runs/"+run.ID+"/workspace/tree", "", nil); response.Code != http.StatusPreconditionFailed ||
		errorCode(t, response) != "RUNNER_OUTDATED" {
		t.Fatalf("outdated tree = %d %s", response.Code, response.Body)
	}
	// Offline, last seen able to act.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	conn = f.connect(t, workspaceHello())
	pingPong(t, conn)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	if code, _, failure := f.workspacePost(t, &f.bea, run.ID, "discard", "{}"); code != http.StatusServiceUnavailable ||
		failure.Code != "RUNNER_OFFLINE" {
		t.Fatalf("offline = %d %+v", code, failure)
	}
	if view := f.workspaceView(t, &f.bea, run.ID); !view.Owner || view.RunnerOnline || view.InfoError != runnerwire.WorkspaceInfoOffline ||
		view.Files.Blocked != runnerwire.WorkspaceFilesOffline || view.Discard.Blocked != runnerwire.WorkspaceGateOffline {
		t.Fatalf("offline view = %+v", view)
	}
}

// The runner proxy never reaches the workspace: an agent's board calls are
// an allowlist, and these are not on it.
func TestRunWorkspaceAgentProxyCannotReach(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, workspaceHello())
	run := f.localRun(t, conn)
	for _, path := range []string{"runs/" + run.ID + "/workspace/discard", "runs/" + run.ID + "/workspace/merge"} {
		if code, body := f.proxied(t, run.ID, http.MethodPost, path, mergeBody(testCommit), nil); code == http.StatusOK || code == http.StatusAccepted {
			t.Fatalf("proxied %s = %d %s", path, code, body)
		}
	}
	for _, path := range []string{"runs/" + run.ID + "/workspace", "runs/" + run.ID + "/workspace/tree"} {
		if code, body := f.proxied(t, run.ID, http.MethodGet, path, "", nil); code == http.StatusOK {
			t.Fatalf("proxied GET %s = %d %s", path, code, body)
		}
	}
	if runnerProxyAllows(http.MethodPost, "/api/runs/"+run.ID+"/workspace/discard") {
		t.Fatal("the proxy allowlist names the workspace")
	}
	pingPong(t, conn)
	if got, _ := f.getRun(t, f.bea, run.ID); got.WorkspaceAction != nil {
		t.Fatalf("a proxied request was recorded: %+v", got.WorkspaceAction)
	}
}

// A run that never had a worktree is discarded on the hub alone.
func TestRunWorkspaceDiscardHubOnly(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	pingPong(t, conn)
	run := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeDecline, RunID: run.ID, Reason: "no"})
	f.waitStatus(t, run.ID, runnerwire.RunDeclined)
	code, got, failure := f.workspacePost(t, &f.bea, run.ID, "discard", "{}")
	if code != http.StatusOK || got.Workspace != runnerwire.RunWorkspaceDiscarded || got.WorkspaceAction == nil ||
		got.WorkspaceAction.Status != runnerwire.WorkspaceDone || got.Status != runnerwire.RunDeclined {
		t.Fatalf("discard = %d %+v %+v", code, got, failure)
	}
	// Nothing went to the runner: the next message is the pong.
	pingPong(t, conn)
	// Again: the same run, no new action.
	if code, again, _ := f.workspacePost(t, &f.bea, run.ID, "discard", "{}"); code != http.StatusOK || again.WorkspaceAction.ID != got.WorkspaceAction.ID {
		t.Fatalf("again = %d %+v", code, again.WorkspaceAction)
	}
}

func TestRunWorkspaceWaitsThenPending(t *testing.T) {
	shortWorkspaceWait(t, 200*time.Millisecond)
	f := newRunsFixture(t)
	conn := f.connect(t, workspaceHello())
	run := f.localRun(t, conn)

	code, pending, failure := f.workspacePost(t, &f.bea, run.ID, "discard", "{}")
	if code != http.StatusAccepted || pending.WorkspaceAction == nil || pending.WorkspaceAction.Status != runnerwire.WorkspacePending ||
		pending.WorkspaceAction.RequestedBy != "bea" {
		t.Fatalf("discard = %d %+v %+v", code, pending.WorkspaceAction, failure)
	}
	job := expectWorkspaceAction(t, conn, run.ID)
	if job.ID != pending.WorkspaceAction.ID {
		t.Fatalf("job = %+v", job)
	}
	// One at a time.
	if code, _, failure := f.workspacePost(t, &f.bea, run.ID, "merge", mergeBody(testCommit)); code != http.StatusConflict ||
		failure.Blocked != runnerwire.WorkspaceGatePending {
		t.Fatalf("second action = %d %+v", code, failure)
	}
	// The runner drops before answering: it gets the request again.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	again := f.connect(t, workspaceHello())
	if resent := expectWorkspaceAction(t, again, run.ID); resent.ID != job.ID || resent.AccountID != f.runner.Runner.OwnerID {
		t.Fatalf("resent = %+v", resent)
	}
	sendEnvelope(t, again, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceResult, RunID: run.ID, WorkspaceResult: &runnerwire.WorkspaceOutcome{
		ID: job.ID, RunID: run.ID, Action: job.Action, WorktreeRemoved: true,
	}})
	f.waitRun(t, run.ID, "discarded", func(r runnerwire.Run) bool { return r.Workspace == runnerwire.RunWorkspaceDiscarded })

	// Unanswered past the TTL: expired, not sent again.
	other := f.localRun(t, again)
	if code, _, _ := f.workspacePost(t, &f.bea, other.ID, "discard", "{}"); code != http.StatusAccepted {
		t.Fatalf("second discard = %d", code)
	}
	expectWorkspaceAction(t, again, other.ID)
	_ = again.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	later := time.Now().Add(runs.WorkspaceActionTTL + time.Minute)
	f.server.runs.Store().SetClock(func() time.Time { return later })
	third := f.connect(t, workspaceHello())
	pingPong(t, third)
	got, _ := f.getRun(t, f.bea, other.ID)
	if got.WorkspaceAction == nil || got.WorkspaceAction.Status != runnerwire.WorkspaceExpired || got.Workspace != "" {
		t.Fatalf("after the TTL = %+v", got.WorkspaceAction)
	}
	if f.auditActions(t, "run.workspace.discard.")["run.workspace.discard.expired"] != other.ID {
		t.Fatal("no expiry audit row")
	}
}

func (f *runsFixture) workspaceView(t *testing.T, b *browser, runID string) runnerwire.RunWorkspaceView {
	t.Helper()
	response := f.do(t, b, http.MethodGet, "/api/runs/"+runID+"/workspace", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET workspace = %d %s", response.Code, response.Body)
	}
	var view runnerwire.RunWorkspaceView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestRunWorkspaceBrowse(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, workspaceHello())
	run := f.localRun(t, conn)
	base := "/api/runs/" + run.ID + "/workspace"

	// The panel's view, with a live look at the worktree.
	views := make(chan runnerwire.RunWorkspaceView, 1)
	go func() { views <- f.workspaceView(t, &f.bea, run.ID) }()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeRepoRequest || env.Repo == nil || env.Repo.Op != runnerwire.RepoOpRunInfo || env.Repo.RunID != run.ID ||
		env.Repo.Branch != "agent/fix-login" || env.Repo.Base != "main" || env.Repo.NodeID != runsTestNode || env.Repo.RepoID != "repo-1" ||
		env.Repo.AccountID != f.runner.Runner.OwnerID {
		t.Fatalf("runner got %+v", env.Repo)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID,
		RunInfo: &runnerwire.RunWorkspaceInfo{Exists: true, Branch: "agent/fix-login", Uncommitted: 3, BaseCheckout: runnerwire.BaseCheckoutNone}}})
	view := <-views
	if !view.Owner || !view.RunnerOnline || !view.Files.Allowed || view.Merge.Mode != runnerwire.WorkspaceMergeModeLocal ||
		!view.Discard.Allowed || view.Info == nil || view.Info.Uncommitted != 3 || view.InfoError != "" {
		t.Fatalf("view = %+v", view)
	}

	// The tree, relayed with the run.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.do(t, &f.bea, http.MethodGet, base+"/tree?path=src", "", nil) }()
	env = readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeRepoRequest || env.Repo.Op != runnerwire.RepoOpRunList || env.Repo.Path != "src" || env.Repo.RunID != run.ID {
		t.Fatalf("runner got %+v", env.Repo)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID,
		Listing: &runnerwire.RepoListing{Path: "src", Entries: []runnerwire.RepoEntry{{Name: "login.ts", Type: "file", Size: 9}}}}})
	if response := <-done; response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "login.ts") {
		t.Fatalf("tree = %d %s", response.Code, response.Body)
	}
	// A worktree that is gone.
	go func() { done <- f.do(t, &f.bea, http.MethodGet, base+"/file?path=src/login.ts", "", nil) }()
	env = readEnvelopeForTest(t, conn)
	if env.Repo.Op != runnerwire.RepoOpRunRead || env.Repo.Path != "src/login.ts" {
		t.Fatalf("runner got %+v", env.Repo)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID,
		Code: runnerwire.RepoErrNoWorktree, Error: "gone"}})
	if response := <-done; response.Code != http.StatusNotFound || errorCode(t, response) != "NO_WORKTREE" {
		t.Fatalf("no worktree = %d %s", response.Code, response.Body)
	}
	// Status, and a busy runner.
	go func() { done <- f.do(t, &f.bea, http.MethodGet, base+"/git/status", "", nil) }()
	env = readEnvelopeForTest(t, conn)
	if env.Repo.Op != runnerwire.RepoOpRunStatus {
		t.Fatalf("runner got %+v", env.Repo)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID,
		Code: runnerwire.RepoErrBusy, Error: "busy"}})
	if response := <-done; response.Code != http.StatusTooManyRequests || errorCode(t, response) != "BUSY" || response.Header().Get("Retry-After") == "" {
		t.Fatalf("busy = %d %s", response.Code, response.Body)
	}
	if code := f.do(t, &f.bea, http.MethodGet, base+"/tree?path="+strings.Repeat("a", runnerwire.MaxRepoPathBytes+1), "", nil).Code; code != http.StatusBadRequest {
		t.Fatalf("long path = %d", code)
	}
	// The plain repo browse never carries a run op.
	go func() {
		done <- f.do(t, &f.bea, http.MethodGet, "/api/explorer/repos/"+f.runner.Runner.ID+"/repo-1/tree", "", nil)
	}()
	env = readEnvelopeForTest(t, conn)
	if env.Repo.Op != runnerwire.RepoOpList || env.Repo.RunID != "" {
		t.Fatalf("explorer sent %+v", env.Repo)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &runnerwire.RepoDone{ID: env.Repo.ID,
		Listing: &runnerwire.RepoListing{Entries: []runnerwire.RepoEntry{}}}})
	<-done
}

func TestPRMergeRefusedWhileWorkspaceBusy(t *testing.T) {
	shortWorkspaceWait(t, 200*time.Millisecond)
	f := newRunsFixture(t)
	conn := f.connect(t, workspaceHello())
	run := f.finishedGreenRun(t, conn)
	pingPong(t, conn)
	code, _, failure := f.workspacePost(t, &f.bea, run.ID, "discard", "{}")
	if code != http.StatusAccepted {
		t.Fatalf("discard with an open PR = %d %+v", code, failure)
	}
	if code, _, failure := f.approve(t, &f.bea, run.ID, testHeadSHA); code != http.StatusConflict || failure.Error != runs.MsgWorkspaceBusy {
		t.Fatalf("PR merge while discarding = %d %+v", code, failure)
	}
}
