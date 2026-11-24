package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func globalGroupContinueHello(maxConcurrent int) runnerwire.Envelope {
	env := globalGroupHello(maxConcurrent)
	env.Hello.Capabilities = append(env.Hello.Capabilities,
		runnerwire.CapContinue,
		runnerwire.CapRunWorkspace,
		runnerwire.CapWorkspaceActions,
	)
	return env
}

func TestGroupFollowUpRoutePersonOnlyAndOwnRunnerOnly(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, globalGroupContinueHello(3))
	pingPong(t, conn)
	beaPeer := dialHubAs(t, f.liveURL, f.bea)
	annPeer := dialHubAs(t, f.liveURL, f.ann)
	calPeer := dialHubAs(t, f.liveURL, f.cal)

	// Start a 3-repo group on bea's runner.
	resp := f.do(t, &f.bea, http.MethodPost, "/api/global-runs/groups", f.groupBody(nil), nil)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/global-runs/groups = %d %s", resp.Code, resp.Body)
	}
	var created runnerwire.GlobalGroupResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Runs) != 3 {
		t.Fatalf("created %d runs, want 3", len(created.Runs))
	}

	// Drain the three initial offers and finish them:
	// run[0] succeeded, run[1] failed (open), run[2] failed (then workspace discarded -> WORK_GONE).
	for range 3 {
		env := readEnvelopeForTest(t, conn)
		if env.Type != runnerwire.TypeOffer {
			t.Fatalf("got %s, want offer", env.Type)
		}
	}
	statuses := []string{runnerwire.RunSucceeded, runnerwire.RunFailed, runnerwire.RunFailed}
	for i, r := range created.Runs {
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
			RunID: r.ID, Base: "main", Branch: "agent/bump-deps-" + r.ID[:8],
		}})
		f.waitGlobal(t, r.ID, "running", func(cur runnerwire.Run) bool { return cur.Status == runnerwire.RunRunning })
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: r.ID, Changes: testChanges()})
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: r.ID, Result: &runnerwire.RunResult{
			Status: statuses[i], Summary: statuses[i],
		}})
		f.waitGlobal(t, r.ID, statuses[i], func(cur runnerwire.Run) bool { return cur.Status == statuses[i] })
	}
	// Discard run[2] via the workspace discard route so it is skipped as WORK_GONE.
	discardDone := make(chan globalAnswer, 1)
	go func() {
		discardDone <- f.global(t, &f.bea, http.MethodPost, "/api/global-runs/"+created.Runs[2].ID+"/workspace/discard", "{}", nil)
	}()
	action := expectWorkspaceAction(t, conn, created.Runs[2].ID)
	sendEnvelope(t, conn, runnerwire.Envelope{
		Type:  runnerwire.TypeWorkspaceResult,
		RunID: created.Runs[2].ID,
		WorkspaceResult: &runnerwire.WorkspaceOutcome{
			ID: action.ID, RunID: created.Runs[2].ID, Action: action.Action,
			WorktreeRemoved: true, BranchDeleted: true,
		},
	})
	select {
	case ans := <-discardDone:
		if ans.code != http.StatusOK || ans.run.Workspace != runnerwire.RunWorkspaceDiscarded {
			t.Fatalf("discard run[2] = %d %s", ans.code, ans.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("discard run[2] timed out")
	}

	route := "/api/global-runs/groups/" + created.Group.ID + "/follow-up"
	reqBody, _ := json.Marshal(runnerwire.GroupFollowUpRequest{
		Message: "Fix the failing repos",
		Only:    "failed",
	})

	// 1. Person-only: agents are refused with 403 PERMISSION_DENIED.
	if agentRes := f.doAsAgent(t, &f.bea, http.MethodPost, route, string(reqBody)); agentRes.Code != http.StatusForbidden ||
		!strings.Contains(agentRes.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("agent POST %s = %d %s, want 403 PERMISSION_DENIED", route, agentRes.Code, agentRes.Body)
	}

	// 2. Own-runner-only: another member (cal) gets 404.
	if calRes := f.do(t, &f.cal, http.MethodPost, route, string(reqBody), nil); calRes.Code != http.StatusNotFound {
		t.Fatalf("cal POST %s = %d %s, want 404", route, calRes.Code, calRes.Body)
	}

	// 3. Own-runner-only: even admin ann (who can GET the group) cannot follow up on bea's runner -> 404.
	if getRes := f.do(t, &f.ann, http.MethodGet, "/api/global-runs/groups/"+created.Group.ID, "", nil); getRes.Code != http.StatusOK {
		t.Fatalf("admin GET group = %d %s, want 200", getRes.Code, getRes.Body)
	}
	if annRes := f.do(t, &f.ann, http.MethodPost, route, string(reqBody), nil); annRes.Code != http.StatusNotFound {
		t.Fatalf("admin POST %s = %d %s, want 404", route, annRes.Code, annRes.Body)
	}

	// 4. Invalid request body ("only" and "message") -> 400 with field.
	badOnly, _ := json.Marshal(runnerwire.GroupFollowUpRequest{Message: "Retry", Only: "invalid"})
	if badRes := f.do(t, &f.bea, http.MethodPost, route, string(badOnly), nil); badRes.Code != http.StatusBadRequest ||
		!strings.Contains(badRes.Body.String(), `"field":"only"`) {
		t.Fatalf("bad only = %d %s, want 400 field only", badRes.Code, badRes.Body)
	}
	badMsg, _ := json.Marshal(runnerwire.GroupFollowUpRequest{Message: "  ", Only: "all"})
	if badRes := f.do(t, &f.bea, http.MethodPost, route, string(badMsg), nil); badRes.Code != http.StatusBadRequest ||
		!strings.Contains(badRes.Body.String(), `"field":"message"`) {
		t.Fatalf("bad message = %d %s, want 400 field message", badRes.Code, badRes.Body)
	}

	// 5. Owner bea succeeds: 200 OK with Cache-Control: no-store, started=[follow-up of run[1]], skipped=[{run[2], WORK_GONE}].
	okRes := f.do(t, &f.bea, http.MethodPost, route, string(reqBody), nil)
	if okRes.Code != http.StatusOK {
		t.Fatalf("bea POST %s = %d %s, want 200", route, okRes.Code, okRes.Body)
	}
	if cc := okRes.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	var out runnerwire.GroupFollowUpResponse
	if err := json.Unmarshal(okRes.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Started) != 1 || out.Started[0].ContinueOf != created.Runs[1].ID || out.Started[0].GroupID != created.Group.ID {
		t.Fatalf("started = %+v, want follow-up of %s", out.Started, created.Runs[1].ID)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RunID != created.Runs[2].ID || out.Skipped[0].Code != runs.CodeWorkGone {
		t.Fatalf("skipped = %+v, want [{%s %s}]", out.Skipped, created.Runs[2].ID, runs.CodeWorkGone)
	}
	expectOffer(t, conn, out.Started[0].ID)

	// Realtime notification goes to bea only, never ann or cal.
	beaPeer.await(runnerwire.GlobalRunsChangedEvent, func(e realtime.Event) bool { return e.ID == out.Started[0].ID })
	for _, peer := range []*wsPeer{annPeer, calPeer} {
		awaitNoneOf(t, peer, 200*time.Millisecond,
			runnerwire.GlobalRunsChangedEvent, runnerwire.GlobalRunEventsEvent, runnerwire.GlobalRunNotifyEvent,
			runnerwire.RunsChangedEvent, runnerwire.RunEventsEvent, runnerwire.RunNotifyEvent)
	}

	// 6. When runner drops CapContinue, group follow-up returns 412 RUNNER_OUTDATED.
	sendEnvelope(t, conn, globalGroupHello(3))
	pingPong(t, conn)
	outdatedRes := f.do(t, &f.bea, http.MethodPost, route, string(reqBody), nil)
	if outdatedRes.Code != http.StatusPreconditionFailed || !strings.Contains(outdatedRes.Body.String(), runs.CodeRunnerOutdated) {
		t.Fatalf("outdated runner = %d %s, want 412 RUNNER_OUTDATED", outdatedRes.Code, outdatedRes.Body)
	}
}
