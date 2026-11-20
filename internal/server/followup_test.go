package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func continueRunsHello() runnerwire.Envelope {
	env := globalRunsHello()
	env.Hello.Capabilities = append(env.Hello.Capabilities, runnerwire.CapContinue)
	return env
}

func expectEnd(t *testing.T, conn *websocket.Conn, runID string) runnerwire.Envelope {
	t.Helper()
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeEnd || env.RunID != runID {
		t.Fatalf("got %+v, want TypeEnd for %s", env, runID)
	}
	return env
}

func TestFollowUpThreadAndEndRoutes(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, continueRunsHello())
	beaPeer := dialHubAs(t, f.liveURL, f.bea)
	annPeer := dialHubAs(t, f.liveURL, f.ann)
	calPeer := dialHubAs(t, f.liveURL, f.cal)

	// Start and finish turn 1 on bea's runner.
	t1, _ := f.startedGlobal(t, conn, nil)
	beaPeer.await(runnerwire.GlobalRunsChangedEvent, func(e realtime.Event) bool { return e.ID == t1.ID })
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: t1.ID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: t1.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: "turn 1 done",
	}})
	f.waitGlobal(t, t1.ID, "turn 1 succeeded", func(r runnerwire.Run) bool {
		return r.Status == runnerwire.RunSucceeded && r.Commit == testCommit
	})

	// Invalid follow-up message -> 400 field "message".
	badMsg, _ := json.Marshal(runnerwire.FollowUpRequest{Message: "   "})
	badAns := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t1.ID+"/follow-up", string(badMsg), nil)
	if badAns.code != http.StatusBadRequest || badAns.failure.Field != "message" {
		t.Fatalf("empty message follow-up = %d %s, want 400 field message", badAns.code, badAns.body)
	}

	// Follow-up creates turn 2 and notifies only dispatcher/owner (bea), never ann or cal.
	fuBody, _ := json.Marshal(runnerwire.FollowUpRequest{
		Message: "Make the button bigger",
		Mode:    runnerwire.ModeInteractive,
	})
	ans := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t1.ID+"/follow-up", string(fuBody), nil)
	if ans.code != http.StatusCreated {
		t.Fatalf("POST follow-up = %d %s", ans.code, ans.body)
	}
	t2 := ans.run
	if t2.ContinueOf != t1.ID || t2.WorkRoot != t1.ID || t2.Mode != runnerwire.ModeInteractive {
		t.Fatalf("t2 = %+v", t2)
	}
	job2 := expectOffer(t, conn, t2.ID)
	if job2.ContinueOf != t1.ID || job2.WorkRoot != t1.ID {
		t.Fatalf("job2 = %+v", job2)
	}
	beaPeer.await(runnerwire.GlobalRunsChangedEvent, func(e realtime.Event) bool { return e.ID == t2.ID })
	for _, peer := range []*wsPeer{annPeer, calPeer} {
		awaitNoneOf(t, peer, 250*time.Millisecond,
			runnerwire.GlobalRunsChangedEvent, runnerwire.GlobalRunEventsEvent, runnerwire.GlobalRunNotifyEvent,
			runnerwire.RunsChangedEvent, runnerwire.RunEventsEvent, runnerwire.RunNotifyEvent)
	}

	// Following up t1 again gives 409 CONTINUED with latestId = t2.ID.
	dup := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t1.ID+"/follow-up", string(fuBody), nil)
	var dupBody struct {
		Code     string `json:"code"`
		LatestID string `json:"latestId"`
	}
	_ = json.Unmarshal([]byte(dup.body), &dupBody)
	if dup.code != http.StatusConflict || dupBody.Code != runs.CodeContinued || dupBody.LatestID != t2.ID {
		t.Fatalf("dup follow-up = %d %s", dup.code, dup.body)
	}

	// Older turn t1's workspace actions are blocked with "continued".
	wsRes := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/"+t1.ID+"/workspace/discard", "{}", nil)
	var wsErr workspaceError
	_ = json.Unmarshal(wsRes.Body.Bytes(), &wsErr)
	if wsRes.Code != http.StatusConflict || wsErr.Blocked != runnerwire.WorkspaceGateContinued {
		t.Fatalf("discard older turn = %d %s, want 409 continued", wsRes.Code, wsRes.Body)
	}

	// GET /api/global-runs/:id/thread returns [t1, t2] oldest first for bea and admin ann, 404 for cal, 403 for agent.
	for _, who := range []*browser{&f.bea, &f.ann} {
		res := f.do(t, who, http.MethodGet, "/api/global-runs/"+t2.ID+"/thread", "", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("GET thread = %d %s", res.Code, res.Body)
		}
		var thread runnerwire.RunThreadResponse
		if err := json.Unmarshal(res.Body.Bytes(), &thread); err != nil || len(thread.Runs) != 2 ||
			thread.Runs[0].ID != t1.ID || thread.Runs[1].ID != t2.ID {
			t.Fatalf("thread = %+v, %v", thread, err)
		}
	}
	if code := f.do(t, &f.cal, http.MethodGet, "/api/global-runs/"+t2.ID+"/thread", "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("cal GET thread = %d, want 404", code)
	}
	// Admin ann can see t2, but cannot follow it up (runner belongs to bea).
	if annFU := f.global(t, &f.ann, http.MethodPost, "/api/global-runs/"+t2.ID+"/follow-up", string(fuBody), nil); annFU.code != http.StatusNotFound {
		t.Fatalf("ann POST follow-up on bea's run = %d %s, want 404", annFU.code, annFU.body)
	}
	if calFU := f.global(t, &f.cal, http.MethodPost, "/api/global-runs/"+t2.ID+"/follow-up", string(fuBody), nil); calFU.code != http.StatusNotFound {
		t.Fatalf("cal POST follow-up on bea's run = %d %s, want 404", calFU.code, calFU.body)
	}

	// Person-only: agents are refused with 403 PERMISSION_DENIED on follow-up, thread, and end.
	for _, route := range [][2]string{
		{http.MethodPost, "/api/global-runs/" + t2.ID + "/follow-up"},
		{http.MethodGet, "/api/global-runs/" + t2.ID + "/thread"},
		{http.MethodPost, "/api/global-runs/" + t2.ID + "/end"},
		{http.MethodPost, "/api/runs/" + t2.ID + "/end"},
	} {
		body := ""
		if route[0] == http.MethodPost {
			body = string(fuBody)
		}
		res := f.doAsAgent(t, &f.bea, route[0], route[1], body)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("agent %s %s = %d %s, want 403", route[0], route[1], res.Code, res.Body)
		}
	}

	// End before t2 is running -> 409.
	if endEarly := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/"+t2.ID+"/end", "", nil); endEarly.Code != http.StatusConflict {
		t.Fatalf("End while offered = %d %s, want 409", endEarly.Code, endEarly.Body)
	}

	// Accept t2 as running interactive.
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: t2.ID, Base: "main", Branch: "agent/" + job2.Slug + "-" + t1.ID[:8],
	}})
	f.waitGlobal(t, t2.ID, "t2 running", func(r runnerwire.Run) bool { return r.Status == runnerwire.RunRunning })

	// Follow-up while t2 is running interactively -> 409 RUN_LIVE.
	liveFU := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t2.ID+"/follow-up", string(fuBody), nil)
	if liveFU.code != http.StatusConflict || liveFU.failure.Code != runs.CodeRunLive {
		t.Fatalf("follow-up while live interactive = %d %s, want 409 RUN_LIVE", liveFU.code, liveFU.body)
	}

	// Admin ann (outside mayType) gets 403 on End; stranger cal gets 404.
	if annEnd := f.do(t, &f.ann, http.MethodPost, "/api/global-runs/"+t2.ID+"/end", "", nil); annEnd.Code != http.StatusForbidden {
		t.Fatalf("admin End = %d %s, want 403", annEnd.Code, annEnd.Body)
	}
	if calEnd := f.do(t, &f.cal, http.MethodPost, "/api/global-runs/"+t2.ID+"/end", "", nil); calEnd.Code != http.StatusNotFound {
		t.Fatalf("cal End = %d %s, want 404", calEnd.Code, calEnd.Body)
	}

	// Owner bea calls End -> 202 and runner receives TypeEnd.
	beaEnd := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/"+t2.ID+"/end", "", nil)
	if beaEnd.Code != http.StatusAccepted {
		t.Fatalf("bea End = %d %s, want 202", beaEnd.Code, beaEnd.Body)
	}
	expectEnd(t, conn, t2.ID)
}

func TestDeclinedFollowUpFreesTheSlot(t *testing.T) {
	shortWorkspaceWait(t, 10*time.Second)
	f := newRunsFixture(t)
	conn := f.connect(t, continueRunsHello())

	t1, _ := f.startedGlobal(t, conn, nil)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: t1.ID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: t1.ID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded,
	}})
	f.waitGlobal(t, t1.ID, "t1 commit", func(r runnerwire.Run) bool {
		return r.Status == runnerwire.RunSucceeded && r.Commit == testCommit
	})

	// Follow-up t1 -> t2, then runner declines t2 with branch_moved.
	fuBody, _ := json.Marshal(runnerwire.FollowUpRequest{Message: "Turn 2"})
	ans := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t1.ID+"/follow-up", string(fuBody), nil)
	if ans.code != http.StatusCreated {
		t.Fatalf("follow-up = %d %s", ans.code, ans.body)
	}
	t2 := ans.run
	expectOffer(t, conn, t2.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type: runnerwire.TypeDecline, RunID: t2.ID, Reason: "branch moved", Code: runnerwire.DeclineBranchMoved,
	})
	f.waitGlobal(t, t2.ID, "t2 declined", func(r runnerwire.Run) bool {
		return r.Status == runnerwire.RunDeclined && r.DeclineCode == runnerwire.DeclineBranchMoved
	})

	// Naming the declined run t2 in a follow-up gets 409 CONTINUED with latestId = t1.ID.
	onDeclined := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t2.ID+"/follow-up", string(fuBody), nil)
	var errBody struct {
		Code     string `json:"code"`
		LatestID string `json:"latestId"`
	}
	_ = json.Unmarshal([]byte(onDeclined.body), &errBody)
	if onDeclined.code != http.StatusConflict || errBody.Code != runs.CodeContinued || errBody.LatestID != t1.ID {
		t.Fatalf("follow-up on declined = %d %s, want 409 CONTINUED %s", onDeclined.code, onDeclined.body, t1.ID)
	}

	// A new follow-up of t1 is accepted.
	retry := f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+t1.ID+"/follow-up", string(fuBody), nil)
	if retry.code != http.StatusCreated || retry.run.ContinueOf != t1.ID {
		t.Fatalf("retry follow-up = %d %s", retry.code, retry.body)
	}
}

func TestViewOnlyAndOldRunnerEndOverHTTP(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, continueRunsHello())

	// Create a node interactive run dispatched by cal on bea's runner.
	calID := f.create(t, f.cal, "cal-pc").Runner.OwnerID
	r, err := f.server.runs.Store().Create(context.Background(), runs.New{
		Project: f.projectName(), NodeID: runsTestNode, NodeTitle: "View-only run",
		RunnerID: f.runner.Runner.ID, RunnerName: "bea-pc", OwnerID: f.runner.Runner.OwnerID,
		Agent: "claude", Model: "default", RepoID: "repo-1", RepoName: "visual-editor",
		JobPrompt: "# Interactive", CreatedBy: calID, CreatedByName: "cal",
		Mode: runnerwire.ModeInteractive,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.server.runs.Submit(context.Background(), r)
	expectOffer(t, conn, r.ID)

	// Attach cal's terminal before Accept; then runner accepts with ViewOnly: true.
	calTerm := f.terminal(t, f.cal, r.ID)
	expectControl(t, calTerm, runnerwire.TermControl{Type: "status", Status: runnerwire.RunOffered})

	sendEnvelope(t, conn, runnerwire.Envelope{
		Type:   runnerwire.TypeAccept,
		Accept: &runnerwire.Accept{RunID: r.ID, Base: "main", Branch: "agent/vo", ViewOnly: true},
	})
	f.waitStatus(t, r.ID, runnerwire.RunRunning)

	// cal's socket receives readonly control frame (alongside running status).
	sawReadOnly := false
	for range 2 {
		kind, data := readFrame(t, calTerm)
		var ctrl runnerwire.TermControl
		if kind == websocket.MessageText && json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "readonly" {
			sawReadOnly = true
		}
	}
	if !sawReadOnly {
		t.Fatal("attached terminal did not receive readonly frame on ViewOnly accept")
	}

	// Dispatcher cal cannot End a view-only run on bea's runner (403); owner bea can (202).
	if calEnd := f.do(t, &f.cal, http.MethodPost, "/api/runs/"+r.ID+"/end", "", nil); calEnd.Code != http.StatusForbidden {
		t.Fatalf("cal End on view-only run = %d %s, want 403", calEnd.Code, calEnd.Body)
	}
	if beaEnd := f.do(t, &f.bea, http.MethodPost, "/api/runs/"+r.ID+"/end", "", nil); beaEnd.Code != http.StatusAccepted {
		t.Fatalf("bea End on view-only run = %d %s, want 202", beaEnd.Code, beaEnd.Body)
	}
	expectEnd(t, conn, r.ID)

	// When runner downgrades hello without CapContinue, End returns 412 RUNNER_OUTDATED.
	sendEnvelope(t, conn, globalRunsHello())
	pingPong(t, conn)
	if oldEnd := f.do(t, &f.bea, http.MethodPost, "/api/runs/"+r.ID+"/end", "", nil); oldEnd.Code != http.StatusPreconditionFailed ||
		!strings.Contains(oldEnd.Body.String(), "RUNNER_OUTDATED") {
		t.Fatalf("End on old runner = %d %s, want 412 RUNNER_OUTDATED", oldEnd.Code, oldEnd.Body)
	}
}
