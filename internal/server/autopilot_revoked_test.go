package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/members"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/store"
)

// An autopilot works on its creator's behalf. bea, an editor of restricted
// main, starts one and is then removed from main: the task already offered
// to her runner may finish, but nothing more is offered to it, what it
// reports is not written onto the board as "autopilot (bea)", and the
// autopilot pauses with the reason.
func TestAutopilotPausesWhenItsCreatorLeavesTheProject(t *testing.T) {
	f := newRunsFixture(t)
	f.server.UseMembersDB(f.server.database)
	ctx := context.Background()
	st := f.server.pm.Store()
	for _, n := range []*engine.Node{
		{ID: "goal", Title: "Ship the login fix"},
		{ID: "task-a", Title: "Fix the button", Kind: "task"},
		{ID: "task-b", Title: "Add a regression test", Kind: "task"},
	} {
		if _, err := st.CreateNode(identity.Local, n, "Do "+n.Title+"."); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{
		{Kind: "add-edge", From: "task-a", To: "task-b"},
		{Kind: "add-edge", From: "task-b", To: "goal"},
	}); err != nil {
		t.Fatal(err)
	}
	id := func(name string) string {
		t.Helper()
		accountID, _, ok := f.server.users.FindAccount(ctx, name)
		if !ok {
			t.Fatalf("no account %s", name)
		}
		return accountID
	}
	ann, _, ok := f.server.users.ActorRevision(ctx, id("ann"))
	if !ok || !ann.IsAdmin() {
		t.Fatalf("ann = %+v", ann)
	}
	key := project.Key(st.Root())
	if err := f.server.members.Set(ctx, ann, key, id("cal"), members.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := f.server.members.Set(ctx, ann, key, id("bea"), members.RoleEditor); err != nil {
		t.Fatal(err)
	}

	conn := f.connect(t, runsHello(2))
	response := f.do(t, &f.bea, http.MethodPost, "/api/autopilot", `{"goalNodeId":"goal","policy":{"maxParallel":1}}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/autopilot = %d %s", response.Code, response.Body)
	}
	var view runnerwire.AutopilotView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	offer := readEnvelopeForTest(t, conn)
	if offer.Type != runnerwire.TypeOffer || offer.Job == nil || offer.Job.NodeID != "task-a" {
		t.Fatalf("got %+v, want an offer for task-a", offer)
	}
	runID := offer.Job.RunID
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
		RunID: runID, Base: "main", Branch: "agent/task-a",
	}})
	f.waitRunAs(t, f.ann, runID, runnerwire.RunRunning)

	if err := f.server.members.Remove(ctx, ann, key, id("bea")); err != nil {
		t.Fatal(err)
	}
	if code := f.do(t, &f.bea, http.MethodGet, "/api/graph", "", nil).Code; code != http.StatusForbidden {
		t.Fatalf("bea reading main after her removal: %d", code)
	}
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: runID, Changes: testChanges()})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: runID, PR: &runnerwire.PRInfo{
		URL: "https://github.com/bea/visual-editor/pull/1", Number: 1, State: runnerwire.PROpen,
	}})
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: runID, Result: &runnerwire.RunResult{
		Status: runnerwire.RunSucceeded, Summary: "Done.",
	}})
	f.waitRunAs(t, f.ann, runID, runnerwire.RunSucceeded)

	deadline := time.Now().Add(10 * time.Second)
	for {
		response := f.do(t, &f.ann, http.MethodGet, "/api/autopilot/"+view.Run.ID, "", nil)
		var got runnerwire.AutopilotView
		_ = json.Unmarshal(response.Body.Bytes(), &got)
		if response.Code == http.StatusOK && got.Run.Status == runnerwire.AutopilotPaused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("autopilot for a removed creator: %d %s", response.Code, response.Body)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// No more work reaches her runner.
	readCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			break
		}
		var env runnerwire.Envelope
		if json.Unmarshal(data, &env) == nil && env.Type == runnerwire.TypeOffer {
			t.Fatalf("an offer after the creator's removal: %+v", env.Job)
		}
	}
	rs, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.LoadGraph()
	statuses := engine.ComputeStatuses(g, rs)
	if statuses["task-a"] == engine.StatusDone || statuses["task-b"] == engine.StatusDone || statuses["goal"] == engine.StatusDone {
		t.Fatalf("board written for a removed creator: %v", statuses)
	}
}

// waitRunAs waits for a run's status, read by b.
func (f *runsFixture) waitRunAs(t *testing.T, b browser, id, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, code := f.getRun(t, b, id)
		if code == http.StatusOK && run.Status == status {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: status %q (HTTP %d), want %q", id, run.Status, code, status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
