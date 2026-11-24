package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
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
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: running.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunCancelled}})
	f.waitStatus(t, running.ID, runnerwire.RunCancelled)
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

// previewHello is the built-in runner's hello once it offers previews.
func previewHello() runnerwire.Envelope {
	env := globalRunsHello()
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapRunPreview)
	return env
}

const previewToken = "0123456789abcdef0123456789abcdef"

type previewAnswer struct {
	Web       bool   `json:"web"`
	URL       string `json:"url"`
	Folder    string `json:"folder"`
	ExpiresAt string `json:"expiresAt"`
	Code      string `json:"code"`
	Blocked   string `json:"blocked"`
}

// previewPost is POST …/workspace/preview from a browser at host.
func (f *runsFixture) previewPost(t *testing.T, b *browser, path, host string, header map[string]string) (*httptest.ResponseRecorder, previewAnswer) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	if b != nil {
		withCookies(request, b.cookies)
		request.Header.Set(auth.CSRFHeaderName, b.csrf)
	}
	for key, value := range header {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	var answer previewAnswer
	_ = json.Unmarshal(response.Body.Bytes(), &answer)
	return response, answer
}

// previewAsync sends previewPost and answers the run_preview the runner gets
// with done (its ID filled in).
func (f *runsFixture) previewAnswered(t *testing.T, conn *websocket.Conn, path, host string, done runnerwire.RepoDone) (*httptest.ResponseRecorder, previewAnswer) {
	t.Helper()
	type result struct {
		response *httptest.ResponseRecorder
		answer   previewAnswer
	}
	out := make(chan result, 1)
	go func() {
		response, answer := f.previewPost(t, &f.bea, path, host, nil)
		out <- result{response, answer}
	}()
	// Wait for the runner's request with the slack a loaded machine needs,
	// and say what the hub answered if it answered without asking.
	envs := make(chan runnerwire.Envelope, 1)
	readErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			readErr <- err
			return
		}
		var env runnerwire.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			readErr <- err
			return
		}
		envs <- env
	}()
	var env runnerwire.Envelope
	select {
	case env = <-envs:
	case err := <-readErr:
		t.Fatalf("runner read: %v", err)
	case res := <-out:
		t.Fatalf("the hub answered without asking the runner: %d %s", res.response.Code, res.response.Body)
	}
	if env.Type != runnerwire.TypeRepoRequest || env.Repo == nil || env.Repo.Op != runnerwire.RepoOpRunPreview ||
		env.Repo.AccountID != f.runner.Runner.OwnerID || env.Repo.RepoID != "repo-1" {
		t.Fatalf("runner got %+v", env)
	}
	done.ID = env.Repo.ID
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeRepoDone, RepoDone: &done})
	select {
	case res := <-out:
		return res.response, res.answer
	case <-time.After(20 * time.Second):
		t.Fatal("the preview POST never answered")
	}
	return nil, previewAnswer{}
}

func webPreview() runnerwire.RepoDone {
	return runnerwire.RepoDone{Preview: &runnerwire.RunPreview{Web: true, Port: 43210, Token: previewToken, Entry: "site pages/index.html",
		Path: `C:\data\worktrees\run`, ExpiresAt: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}}
}

func TestWorkspacePreviewRoute(t *testing.T) {
	shortWorkspaceWait(t, 10*time.Second)
	f := newRunsFixture(t)
	conn := f.connect(t, previewHello())
	f.server.runners.SetWelcomed(f.runner.Runner.ID, true)
	run := f.localRun(t, conn)
	path := "/api/runs/" + run.ID + "/workspace/preview"

	// The host swap: the page never shares the app's loopback name.
	for _, c := range [][2]string{{"127.0.0.1:8080", "localhost"}, {"localhost:8080", "127.0.0.1"}, {"[::1]:8080", "localhost"}} {
		response, answer := f.previewAnswered(t, conn, path, c[0], webPreview())
		want := "http://" + c[1] + ":43210/p/" + previewToken + "/site%20pages/index.html"
		if response.Code != http.StatusOK || !answer.Web || answer.URL != want || answer.Folder != `C:\data\worktrees\run` ||
			answer.ExpiresAt != "2026-11-17T12:00:00Z" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("Host %s: %d %s", c[0], response.Code, response.Body)
		}
	}
	// No page: the folder to open instead.
	response, answer := f.previewAnswered(t, conn, path, "127.0.0.1:8080", runnerwire.RepoDone{Preview: &runnerwire.RunPreview{Path: `C:\data\worktrees\run`}})
	if response.Code != http.StatusOK || answer.Web || answer.URL != "" || answer.Folder != `C:\data\worktrees\run` || answer.ExpiresAt != "" {
		t.Fatalf("no page = %d %s", response.Code, response.Body)
	}
	// The runner's refusals.
	for code, blocked := range map[string]string{runnerwire.RepoErrNoWorktree: runnerwire.RepoErrNoWorktree,
		runnerwire.RepoErrPreviewOff: runnerwire.RepoErrPreviewOff, runnerwire.RepoErrOff: runnerwire.RepoErrPreviewOff} {
		response, answer := f.previewAnswered(t, conn, path, "127.0.0.1:8080", runnerwire.RepoDone{Code: code, Error: "no"})
		if response.Code != http.StatusConflict || answer.Blocked != blocked {
			t.Fatalf("runner %s = %d %s", code, response.Code, response.Body)
		}
	}
	// A runner answer the hub cannot vouch for is never handed on.
	for _, bad := range []runnerwire.RunPreview{
		{Web: true, Port: 43210, Token: previewToken, Entry: "../x.html"},
		{Web: true, Port: 43210, Token: previewToken, Entry: "/etc/x.html"},
		{Web: true, Port: 43210, Token: "short", Entry: "index.html"},
		{Web: true, Port: 0, Token: previewToken, Entry: "index.html"},
		{Web: true, Port: 43210, Token: previewToken, Entry: `a\b.html`},
		{Web: true, Port: 43210, Token: previewToken, Entry: ""},
		{Web: true, Port: 70000, Token: previewToken, Entry: "index.html"},
	} {
		response, _ := f.previewAnswered(t, conn, path, "127.0.0.1:8080", runnerwire.RepoDone{Preview: &bad})
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "43210") {
			t.Fatalf("bad answer %+v = %d %s", bad, response.Code, response.Body)
		}
	}

	// A Host that is not this machine: off, and nothing asked of the runner.
	response, answer = f.previewPost(t, &f.bea, path, "nodevas.example.test", nil)
	if response.Code != http.StatusConflict || answer.Blocked != runnerwire.RepoErrPreviewOff {
		t.Fatalf("remote Host = %d %s", response.Code, response.Body)
	}
	response, answer = f.previewPost(t, &f.bea, path, "127.0.0.1:8080", map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if response.Code != http.StatusConflict || answer.Blocked != runnerwire.RepoErrPreviewOff {
		t.Fatalf("forwarded for elsewhere = %d %s", response.Code, response.Body)
	}
	// Someone else: the administrator is not the owner; cal cannot see it.
	if response, answer := f.previewPost(t, &f.ann, path, "127.0.0.1:8080", nil); response.Code != http.StatusForbidden || answer.Code != "PERMISSION_DENIED" {
		t.Fatalf("administrator = %d %s", response.Code, response.Body)
	}
	if response, _ := f.previewPost(t, &f.cal, path, "127.0.0.1:8080", nil); response.Code != http.StatusNotFound {
		t.Fatalf("cal = %d %s", response.Code, response.Body)
	}
	if response := f.doAsAgent(t, &f.bea, http.MethodPost, path, "{}"); response.Code != http.StatusForbidden {
		t.Fatalf("agent = %d %s", response.Code, response.Body)
	}
	// A page on another origin cannot ask for it.
	if response, _ := f.previewPost(t, &f.bea, path, "127.0.0.1:8080", map[string]string{"Origin": "http://evil.example.test"}); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin = %d %s", response.Code, response.Body)
	}
	if response, _ := f.previewPost(t, &f.bea, path, "127.0.0.1:8080", map[string]string{"Sec-Fetch-Site": "cross-site"}); response.Code != http.StatusForbidden {
		t.Fatalf("cross-site = %d %s", response.Code, response.Body)
	}
	// Only a POST mints.
	if response := f.do(t, &f.bea, http.MethodGet, path, "", nil); response.Code == http.StatusOK {
		t.Fatalf("GET = %d %s", response.Code, response.Body)
	}
	if response := f.do(t, &f.bea, http.MethodGet, "/api/unknown-run-id/workspace/preview", "", nil); response.Code == http.StatusOK {
		t.Fatalf("GET elsewhere = %d", response.Code)
	}
	if response, _ := f.previewPost(t, &f.bea, "/api/runs/no-such-run/workspace/preview", "127.0.0.1:8080", nil); response.Code != http.StatusNotFound {
		t.Fatalf("unknown run = %d %s", response.Code, response.Body)
	}

	// Applied already.
	pending := f.postAsync(t, &f.bea, run.ID, "merge", mergeBody(testCommit))
	job := expectWorkspaceAction(t, conn, run.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeWorkspaceResult, RunID: run.ID, WorkspaceResult: &runnerwire.WorkspaceOutcome{
		ID: job.ID, RunID: run.ID, Action: job.Action, MergeCommit: strings.Repeat("e", 40), WorktreeRemoved: true, BranchDeleted: true,
	}})
	if res := awaitPost(t, pending); res.code != http.StatusOK {
		t.Fatalf("merge = %d %+v", res.code, res.failure)
	}
	if response, answer := f.previewPost(t, &f.bea, path, "127.0.0.1:8080", nil); response.Code != http.StatusConflict ||
		answer.Blocked != runnerwire.WorkspaceGateMergedLocal {
		t.Fatalf("merged = %d %s", response.Code, response.Body)
	}
	// Still running.
	running := f.startedRun(t, conn)
	if response, answer := f.previewPost(t, &f.bea, "/api/runs/"+running.ID+"/workspace/preview", "127.0.0.1:8080", nil); response.Code != http.StatusConflict ||
		answer.Blocked != runnerwire.WorkspaceGateRunning {
		t.Fatalf("running = %d %s", response.Code, response.Body)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: running.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunCancelled}})
	f.waitStatus(t, running.ID, runnerwire.RunCancelled)

	// A team runner (not this hub's built-in one), even claiming the
	// capability: off, and nothing sent.
	other := f.localRun(t, conn)
	otherPath := "/api/runs/" + other.ID + "/workspace/preview"
	f.server.runners.SetWelcomed(f.runner.Runner.ID, false)
	if response, answer := f.previewPost(t, &f.bea, otherPath, "127.0.0.1:8080", nil); response.Code != http.StatusConflict ||
		answer.Blocked != runnerwire.RepoErrPreviewOff {
		t.Fatalf("team runner = %d %s", response.Code, response.Body)
	}
	pingPong(t, conn)
	// Offline.
	f.server.runners.SetWelcomed(f.runner.Runner.ID, true)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	if response, answer := f.previewPost(t, &f.bea, otherPath, "127.0.0.1:8080", nil); response.Code != http.StatusConflict ||
		answer.Blocked != runnerwire.WorkspaceGateOffline {
		t.Fatalf("offline = %d %s", response.Code, response.Body)
	}
}

func TestWorkspacePreviewRouteGlobal(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, previewHello())
	f.server.runners.SetWelcomed(f.runner.Runner.ID, true)
	run, _ := f.startedGlobal(t, conn, nil)
	path := "/api/global-runs/" + run.ID + "/workspace/preview"
	// Still running.
	if response, answer := f.previewPost(t, &f.bea, path, "127.0.0.1:8080", nil); response.Code != http.StatusConflict ||
		answer.Blocked != runnerwire.WorkspaceGateRunning {
		t.Fatalf("running = %d %s", response.Code, response.Body)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: run.ID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{Status: runnerwire.RunSucceeded}})
	f.waitGlobal(t, run.ID, "its commit", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunSucceeded && r.Commit == testCommit })
	pingPong(t, conn)
	response, answer := f.previewAnswered(t, conn, path, "localhost:8080", webPreview())
	if response.Code != http.StatusOK || !answer.Web || !strings.HasPrefix(answer.URL, "http://127.0.0.1:43210/p/"+previewToken+"/") {
		t.Fatalf("global preview = %d %s", response.Code, response.Body)
	}
	// Not under the project prefix, and the other way round.
	if response, _ := f.previewPost(t, &f.bea, "/api/runs/"+run.ID+"/workspace/preview", "127.0.0.1:8080", nil); response.Code != http.StatusNotFound {
		t.Fatalf("project route for a global run = %d %s", response.Code, response.Body)
	}
	if response, _ := f.previewPost(t, &f.ann, path, "127.0.0.1:8080", nil); response.Code == http.StatusOK {
		t.Fatalf("administrator = %d %s", response.Code, response.Body)
	}
	if response := f.do(t, &f.bea, http.MethodGet, path, "", nil); response.Code == http.StatusOK {
		t.Fatalf("GET = %d %s", response.Code, response.Body)
	}
}

// lockedBuffer collects log output written from the server's goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The token goes to its owner's browser and nowhere else: not the log, not
// the audit trail.
func TestWorkspacePreviewTokenNotLogged(t *testing.T) {
	logs := &lockedBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	f := newRunsFixture(t)
	conn := f.connect(t, previewHello())
	f.server.runners.SetWelcomed(f.runner.Runner.ID, true)
	run := f.localRun(t, conn)
	response, answer := f.previewAnswered(t, conn, "/api/runs/"+run.ID+"/workspace/preview", "127.0.0.1:8080", webPreview())
	if response.Code != http.StatusOK || !strings.Contains(answer.URL, previewToken) {
		t.Fatalf("preview = %d %s", response.Code, response.Body)
	}
	pingPong(t, conn)
	if !strings.Contains(logs.String(), "/workspace/preview") {
		t.Fatalf("the request was not logged here, so this proves nothing:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), previewToken) {
		t.Fatalf("the token is in the log:\n%s", logs.String())
	}
	events, err := f.server.audit.Query(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), previewToken) {
		t.Fatal("the token is in the audit trail")
	}
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
