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
	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

const testHeadSHA = "89abcdef0123456789abcdef0123456789abcdef"

func greenPR(ci string) *runnerwire.PRInfo {
	return &runnerwire.PRInfo{
		URL: "https://github.com/bea/visual-editor/pull/42", Number: 42, State: runnerwire.PROpen,
		CI: ci, HeadSHA: testHeadSHA,
	}
}

// finishedGreenRun is a succeeded run whose open PR passed CI at testHeadSHA.
func (f *runsFixture) finishedGreenRun(t *testing.T, conn *websocket.Conn) runnerwire.Run {
	t.Helper()
	run := f.startedRun(t, conn)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: greenPR(runnerwire.CISuccess)})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded,
	}})
	return f.waitRun(t, run.ID, "a green PR", func(r runnerwire.Run) bool {
		return r.Status == runnerwire.RunSucceeded && r.PRHeadSHA == testHeadSHA && r.CIStatus == runnerwire.CISuccess
	})
}

type apiError struct {
	Error    string `json:"error"`
	Code     string `json:"code"`
	Exceeded string `json:"exceeded"`
}

func (f *runsFixture) approve(t *testing.T, b *browser, runID, head string) (int, runnerwire.Run, apiError) {
	t.Helper()
	body, _ := json.Marshal(runnerwire.MergeRequestBody{HeadSHA: head})
	response := f.do(t, b, http.MethodPost, "/api/runs/"+runID+"/merge", string(body), nil)
	var run runnerwire.Run
	var failure apiError
	_ = json.Unmarshal(response.Body.Bytes(), &run)
	_ = json.Unmarshal(response.Body.Bytes(), &failure)
	return response.Code, run, failure
}

func expectMerge(t *testing.T, conn *websocket.Conn, runID string) runnerwire.MergeJob {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeMerge || env.Merge == nil || env.Merge.RunID != runID {
		t.Fatalf("got %+v, want a merge job for %s", env, runID)
	}
	return *env.Merge
}

func TestRunMergeApproval(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	peer := dialHubAs(t, f.liveURL, f.bea)
	run := f.finishedGreenRun(t, conn)
	// The finish is a notification; the PR going green is not.
	peer.await(runnerwire.RunNotifyEvent, func(e realtime.Event) bool { return e.ID == run.ID })
	if run.Merge != nil {
		t.Fatalf("fresh run has a merge: %+v", run.Merge)
	}

	code, approved, failure := f.approve(t, &f.bea, run.ID, testHeadSHA)
	if code != http.StatusAccepted {
		t.Fatalf("approve = %d %+v", code, failure)
	}
	if m := approved.Merge; m == nil || m.Status != runnerwire.MergePending || m.HeadSHA != testHeadSHA ||
		m.RequestedBy != "bea" || m.ID == "" {
		t.Fatalf("approved run's merge = %+v", approved.Merge)
	}
	job := expectMerge(t, conn, run.ID)
	if job.ID != approved.Merge.ID || job.HeadSHA != testHeadSHA {
		t.Fatalf("job = %+v", job)
	}
	peer.awaitNone(runnerwire.RunNotifyEvent, 200*time.Millisecond)

	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{
		ID: job.ID, RunID: run.ID,
	}})
	merged := f.waitRun(t, run.ID, "merged", func(r runnerwire.Run) bool {
		return r.Merge != nil && r.Merge.Status == runnerwire.MergeDone
	})
	if merged.Merge.FinishedAt == "" || merged.Merge.Reason != "" {
		t.Fatalf("merged = %+v", merged.Merge)
	}
	peer.await(runnerwire.RunNotifyEvent, func(e realtime.Event) bool { return e.ID == run.ID })
	// A repeat answer changes nothing and announces nothing.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{
		ID: job.ID, RunID: run.ID, Error: "again",
	}})
	pingPong(t, conn)
	peer.awaitNone(runnerwire.RunNotifyEvent, 200*time.Millisecond)

	events, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "run.merge."})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, e := range events {
		actions[e.Action] = e.Target == run.ID
	}
	if !actions["run.merge.approve"] || !actions["run.merge.merged"] {
		t.Fatalf("audit trail = %+v", events)
	}

	// Its runner reports the merged PR as usual; a new approval is refused.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: &runnerwire.PRInfo{
		URL: greenPR("").URL, Number: 42, State: runnerwire.PRMerged, CI: runnerwire.CISuccess, HeadSHA: testHeadSHA,
	}})
	f.waitRun(t, run.ID, "PR merged", func(r runnerwire.Run) bool { return r.PRState == runnerwire.PRMerged })
	if code, _, failure := f.approve(t, &f.bea, run.ID, testHeadSHA); code != http.StatusConflict || failure.Error != runs.MsgNoOpenPR {
		t.Fatalf("approving a merged PR = %d %+v", code, failure)
	}
}

func TestRunMergeRefusals(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(2))
	run := f.finishedGreenRun(t, conn)

	// No PR yet.
	bare := f.startedRun(t, conn)
	if code, _, failure := f.approve(t, &f.bea, bare.ID, testHeadSHA); code != http.StatusConflict ||
		failure.Code != "FAILED_PRECONDITION" || failure.Error != runs.MsgNoOpenPR {
		t.Fatalf("no PR = %d %+v", code, failure)
	}

	// Someone who cannot see the run gets a 404; anyone may not.
	if code, _, _ := f.approve(t, &f.cal, run.ID, testHeadSHA); code != http.StatusNotFound {
		t.Fatalf("cal approving bea's merge = %d", code)
	}
	if code, _, _ := f.approve(t, nil, run.ID, testHeadSHA); code != http.StatusUnauthorized {
		t.Fatalf("anonymous approval = %d", code)
	}
	if code, _, _ := f.approve(t, &f.bea, "nope", testHeadSHA); code != http.StatusNotFound {
		t.Fatalf("approving no run = %d", code)
	}
	for _, head := range []string{"", "HEAD", strings.Repeat("a", 65)} {
		if code, _, _ := f.approve(t, &f.bea, run.ID, head); code != http.StatusBadRequest {
			t.Errorf("head %q = %d, want 400", head, code)
		}
	}

	// An agent may not approve, whichever way it is recognised.
	body, _ := json.Marshal(runnerwire.MergeRequestBody{HeadSHA: testHeadSHA})
	request := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/merge", strings.NewReader(string(body)))
	request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
	request.Header.Set("Content-Type", "application/json")
	withCookies(request, f.bea.cookies)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("agent approval = %d %s", response.Code, response.Body)
	}

	// The head moved on since the approver looked.
	if code, _, failure := f.approve(t, &f.bea, run.ID, "0000000000000000000000000000000000000000"); code != http.StatusConflict ||
		failure.Error != runs.MsgHeadMoved {
		t.Fatalf("stale head = %d %+v", code, failure)
	}
	// CI not green: a new push is pending again.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: greenPR(runnerwire.CIPending)})
	f.waitRun(t, run.ID, "CI pending", func(r runnerwire.Run) bool { return r.CIStatus == runnerwire.CIPending })
	if code, _, failure := f.approve(t, &f.bea, run.ID, testHeadSHA); code != http.StatusConflict || failure.Error != runs.MsgCINotGreen {
		t.Fatalf("CI pending = %d %+v", code, failure)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: greenPR(runnerwire.CISuccess)})
	f.waitRun(t, run.ID, "CI green", func(r runnerwire.Run) bool { return r.CIStatus == runnerwire.CISuccess })

	// The administrator may approve; a second approval while one waits may not.
	if code, approved, failure := f.approve(t, &f.ann, run.ID, testHeadSHA); code != http.StatusAccepted || approved.Merge == nil {
		t.Fatalf("administrator approval = %d %+v", code, failure)
	}
	job := expectMerge(t, conn, run.ID)
	if code, _, failure := f.approve(t, &f.bea, run.ID, testHeadSHA); code != http.StatusConflict || failure.Error != runs.MsgMergePending {
		t.Fatalf("second approval = %d %+v", code, failure)
	}

	// Another runner cannot answer for bea's run, whatever IDs it sends.
	other := f.create(t, f.cal, "cal-pc")
	intruder := runnerSocket(t, f.liveURL, other.Token)
	sendEnvelope(t, intruder, runsHello(1))
	readEnvelopeForTest(t, intruder)
	sendEnvelope(t, intruder, runnerwire.Envelope{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{
		ID: job.ID, RunID: run.ID,
	}})
	pingPong(t, intruder)
	// Nor can bea's runner answer for a merge with the wrong run, or a malformed one.
	for _, env := range []runnerwire.Envelope{
		{Type: runnerwire.TypeMergeResult},
		{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{ID: job.ID}},
		{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{ID: job.ID, RunID: bare.ID}},
	} {
		sendEnvelope(t, conn, env)
	}
	pingPong(t, conn)
	if got, _ := f.getRun(t, f.bea, run.ID); got.Merge == nil || got.Merge.Status != runnerwire.MergePending {
		t.Fatalf("a foreign or malformed result moved the merge: %+v", got.Merge)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{
		ID: job.ID, RunID: run.ID, Error: "required review missing",
	}})
	failed := f.waitRun(t, run.ID, "merge failed", func(r runnerwire.Run) bool {
		return r.Merge != nil && r.Merge.Status == runnerwire.MergeFailed
	})
	if failed.Merge.Reason != "required review missing" || failed.Merge.RequestedBy != "ann" {
		t.Fatalf("failed merge = %+v", failed.Merge)
	}
}

func TestRunMergeWaitsForAnOfflineRunner(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.finishedGreenRun(t, conn)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)

	code, approved, failure := f.approve(t, &f.bea, run.ID, testHeadSHA)
	if code != http.StatusAccepted || approved.Merge == nil || approved.Merge.Status != runnerwire.MergePending {
		t.Fatalf("approve with the runner offline = %d %+v %+v", code, approved.Merge, failure)
	}
	again := f.connect(t, runsHello(1))
	job := expectMerge(t, again, run.ID)
	if job.ID != approved.Merge.ID || job.HeadSHA != testHeadSHA {
		t.Fatalf("job on reconnect = %+v", job)
	}
	sendEnvelope(t, again, runnerwire.Envelope{Type: runnerwire.TypeMergeResult, MergeResult: &runnerwire.MergeOutcome{
		ID: job.ID, RunID: run.ID,
	}})
	f.waitRun(t, run.ID, "merged", func(r runnerwire.Run) bool { return r.Merge != nil && r.Merge.Status == runnerwire.MergeDone })
}

func TestRunMergeExpiresWhileTheRunnerIsAway(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	run := f.finishedGreenRun(t, conn)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)

	if code, _, failure := f.approve(t, &f.bea, run.ID, testHeadSHA); code != http.StatusAccepted {
		t.Fatalf("approve = %d %+v", code, failure)
	}
	later := time.Now().Add(runs.MergeTTL + time.Minute)
	f.server.runs.Store().SetClock(func() time.Time { return later })

	// Back after the TTL: the approval is not sent; the next message is the
	// pong.
	again := f.connect(t, runsHello(1))
	pingPong(t, again)
	got, _ := f.getRun(t, f.bea, run.ID)
	if got.Merge == nil || got.Merge.Status != runnerwire.MergeExpired || got.Merge.Reason == "" {
		t.Fatalf("merge after the TTL = %+v", got.Merge)
	}
	// A fresh approval goes straight out.
	if code, _, failure := f.approve(t, &f.bea, run.ID, testHeadSHA); code != http.StatusAccepted {
		t.Fatalf("fresh approval = %d %+v", code, failure)
	}
	expectMerge(t, again, run.ID)
}

// run-notify: a red CI and a failed run are notifications; a cancel is not.
func TestRunNotifyEvents(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(1))
	peer := dialHubAs(t, f.liveURL, f.bea)

	run := f.startedRun(t, conn)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: greenPR(runnerwire.CIPending)})
	pingPong(t, conn)
	peer.awaitNone(runnerwire.RunNotifyEvent, 200*time.Millisecond)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: run.ID, PR: greenPR(runnerwire.CIFailure)})
	peer.await(runnerwire.RunNotifyEvent, func(e realtime.Event) bool { return e.ID == run.ID })
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: run.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunFailed, Error: "tests failed",
	}})
	peer.await(runnerwire.RunNotifyEvent, func(e realtime.Event) bool { return e.ID == run.ID })

	cancelled := f.startedRun(t, conn)
	if _, code := f.cancel(t, f.bea, cancelled.ID); code != http.StatusOK {
		t.Fatalf("cancel = %d", code)
	}
	expectCancel(t, conn, cancelled.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: cancelled.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunCancelled,
	}})
	f.waitStatus(t, cancelled.ID, runnerwire.RunCancelled)
	peer.awaitNone(runnerwire.RunNotifyEvent, 300*time.Millisecond)

	// A run its runner lost while away fails, and that is a notification.
	lost := f.startedRun(t, conn)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	f.waitOffline(t)
	f.connect(t, runsHello(1))
	peer.await(runnerwire.RunNotifyEvent, func(e realtime.Event) bool { return e.ID == lost.ID })
	f.waitStatus(t, lost.ID, runnerwire.RunFailed)
}

func (f *runsFixture) budget(t *testing.T, b *browser, method, body string) (int, runnerwire.BudgetStatus) {
	t.Helper()
	response := f.do(t, b, method, "/api/runs/budget", body, nil)
	var out runnerwire.BudgetStatus
	_ = json.Unmarshal(response.Body.Bytes(), &out)
	return response.Code, out
}

func TestRunBudgets(t *testing.T) {
	f := newRunsFixture(t)
	f.connect(t, runsHello(4))

	if code, st := f.budget(t, &f.bea, http.MethodGet, ""); code != http.StatusOK || st.Budget != (runnerwire.Budget{}) ||
		st.Used.Runs != 0 || st.Exceeded != "" {
		t.Fatalf("fresh budget = %d %+v", code, st)
	}
	for _, body := range []string{`{"dailyRuns":-1}`, `{"dailyCostUsd":-2}`, `{"dailyTokens":-3}`,
		`{"dailyRuns":10001}`, `{"dailyRuns":"many"}`, `{"dailyRuns":1,"surprise":true}`} {
		if code, _ := f.budget(t, &f.bea, http.MethodPut, body); code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", body, code)
		}
	}
	if code, _ := f.budget(t, nil, http.MethodGet, ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous budget = %d", code)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/runs/budget", strings.NewReader(`{"dailyRuns":0}`))
	request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
	request.Header.Set("Content-Type", "application/json")
	withCookies(request, f.bea.cookies)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("agent lifting the budget = %d %s", response.Code, response.Body)
	}

	code, st := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":2,"dailyCostUsd":1.5,"dailyTokens":0}`)
	if code != http.StatusOK || st.Budget.DailyRuns != 2 || st.Budget.DailyCostUSD != 1.5 || st.Exceeded != "" {
		t.Fatalf("PUT budget = %d %+v", code, st)
	}
	f.dispatch(t, f.bea, f.runBody(""))
	f.dispatch(t, f.bea, f.runBody(""))
	response = f.do(t, &f.bea, http.MethodPost, "/api/runs", f.runBody(""), nil)
	var failure apiError
	_ = json.Unmarshal(response.Body.Bytes(), &failure)
	if response.Code != http.StatusTooManyRequests || failure.Code != "RESOURCE_EXHAUSTED" || failure.Exceeded != "runs" ||
		!strings.Contains(failure.Error, "2 of 2 runs") {
		t.Fatalf("third run over budget = %d %+v", response.Code, failure)
	}
	if _, st := f.budget(t, &f.bea, http.MethodGet, ""); st.Used.Runs != 2 || st.Exceeded != "runs" {
		t.Fatalf("budget after two runs = %+v", st)
	}
	// Budgets are per person: cal's is untouched.
	if _, st := f.budget(t, &f.cal, http.MethodGet, ""); st.Used.Runs != 0 || st.Budget.DailyRuns != 0 {
		t.Fatalf("cal's budget = %+v", st)
	}
	// Zero lifts the limit.
	if code, st := f.budget(t, &f.bea, http.MethodPut, `{"dailyRuns":0,"dailyCostUsd":0,"dailyTokens":0}`); code != http.StatusOK || st.Exceeded != "" {
		t.Fatalf("lifted budget = %d %+v", code, st)
	}
	f.dispatch(t, f.bea, f.runBody(""))
}
