package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/store"
)

// An autopilot through the real dispatcher and a runner socket: the goal's
// two tasks run one after the other, each committed, pushed and PR'd, and
// the goal is done on the board at the end.
func TestAutopilotThroughTheDispatcher(t *testing.T) {
	f := newRunsFixture(t)
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
	conn := f.connect(t, runsHello(2))

	// cal cannot steer bea's autopilot, and an empty goal has nothing to run.
	response := f.do(t, &f.bea, http.MethodPost, "/api/autopilot", `{"goalNodeId":"`+runsTestNode+`","policy":{}}`, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("goal without tasks: %d %s", response.Code, response.Body)
	}
	response = f.do(t, &f.bea, http.MethodPost, "/api/autopilot", `{"goalNodeId":"goal","policy":{"maxParallel":2}}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/autopilot = %d %s", response.Code, response.Body)
	}
	var view runnerwire.AutopilotView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Run.Policy.RepoID != "repo-1" || len(view.Tasks) != 2 || view.Tasks[0].NodeID != "task-a" ||
		view.Tasks[0].State != runnerwire.TaskQueued || view.Tasks[1].State != runnerwire.TaskNone {
		t.Fatalf("created = %+v", view)
	}
	id := view.Run.ID
	if code := f.do(t, &f.cal, http.MethodPost, "/api/autopilot/"+id+"/pause", "", nil).Code; code != http.StatusForbidden {
		t.Fatalf("cal pausing: %d", code)
	}

	work := func(node string) {
		t.Helper()
		env := readEnvelopeForTest(t, conn)
		if env.Type != runnerwire.TypeOffer || env.Job == nil || env.Job.NodeID != node {
			t.Fatalf("got %+v, want an offer for %s", env, node)
		}
		runID := env.Job.RunID
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{
			RunID: runID, Base: "main", Branch: "agent/" + node,
		}})
		f.waitStatus(t, runID, runnerwire.RunRunning)
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeChanges, RunID: runID, Changes: testChanges()})
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePR, RunID: runID, PR: &runnerwire.PRInfo{
			URL: "https://github.com/bea/visual-editor/pull/" + node, Number: 7, State: runnerwire.PROpen,
		}})
		sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeFinish, RunID: runID, Result: &runnerwire.RunResult{
			Status: runnerwire.RunSucceeded, Summary: "Done.",
		}})
		f.waitStatus(t, runID, runnerwire.RunSucceeded)
	}
	work("task-a")
	f.waitAutopilot(t, id, func(v runnerwire.AutopilotView) bool { return v.Tasks[0].State == runnerwire.TaskSuccess })
	work("task-b")
	done := f.waitAutopilot(t, id, func(v runnerwire.AutopilotView) bool { return v.Run.Status == runnerwire.AutopilotSucceeded })
	if done.Run.Counts.Success != 2 || done.Tasks[1].PRURL == "" || len(done.Tasks[1].Stages) < 5 {
		t.Fatalf("done = %+v", done)
	}
	rs, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.LoadGraph()
	statuses := engine.ComputeStatuses(g, rs)
	if statuses["task-a"] != engine.StatusDone || statuses["goal"] != engine.StatusDone {
		t.Fatalf("board statuses = %v", statuses)
	}
	// The latest autopilot of the goal, by goal.
	response = f.do(t, &f.bea, http.MethodGet, "/api/autopilot?goal=goal", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET by goal = %d", response.Code)
	}
}

func (f *runsFixture) waitAutopilot(t *testing.T, id string, ok func(runnerwire.AutopilotView) bool) runnerwire.AutopilotView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		response := f.do(t, &f.bea, http.MethodGet, "/api/autopilot/"+id, "", nil)
		var view runnerwire.AutopilotView
		_ = json.Unmarshal(response.Body.Bytes(), &view)
		if response.Code == http.StatusOK && len(view.Tasks) > 0 && ok(view) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("autopilot %s: %d %+v", id, response.Code, view)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
