package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/coder/websocket"

	"nodevas/internal/audit"
	"nodevas/internal/engine"
	controlapi "nodevas/internal/httpapi/control"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/store"
)

// startGoal makes goal ← task and starts b's autopilot on it, returning the
// autopilot's id and the run its task was offered as on conn.
func (f *runsFixture) startGoal(t *testing.T, b browser, conn *websocket.Conn, goal, task string) (string, string) {
	t.Helper()
	st := f.server.pm.Store()
	for _, n := range []*engine.Node{{ID: goal, Title: "Goal " + goal}, {ID: task, Title: "Task " + task, Kind: "task"}} {
		if _, err := st.CreateNode(identity.Local, n, "Do "+n.Title+"."); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.ApplyGraphOps(identity.Local, []store.GraphOp{{Kind: "add-edge", From: task, To: goal}}); err != nil {
		t.Fatal(err)
	}
	response := f.do(t, &b, http.MethodPost, "/api/autopilot", `{"goalNodeId":"`+goal+`","policy":{}}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/autopilot = %d %s", response.Code, response.Body)
	}
	var view runnerwire.AutopilotView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	env := readEnvelopeForTest(t, conn)
	if env.Type != runnerwire.TypeOffer || env.Job == nil || env.Job.NodeID != task {
		t.Fatalf("got %+v, want an offer for %s", env, task)
	}
	return view.Run.ID, env.Job.RunID
}

func (f *runsFixture) pauseAll(t *testing.T, b browser, body string) controlapi.PauseAllResponse {
	t.Helper()
	response := f.do(t, &b, http.MethodPost, "/api/control/pause-all", body, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/control/pause-all = %d %s", response.Code, response.Body)
	}
	var out controlapi.PauseAllResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *runsFixture) autopilotStatus(t *testing.T, id string) string {
	t.Helper()
	response := f.do(t, &f.bea, http.MethodGet, "/api/autopilot/"+id, "", nil)
	var view runnerwire.AutopilotView
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		t.Fatalf("GET autopilot %s = %d %s", id, response.Code, response.Body)
	}
	return view.Run.Status
}

func byID(targets []controlapi.Target) map[string]controlapi.Target {
	out := map[string]controlapi.Target{}
	for _, target := range targets {
		if target.ID != "" {
			out[target.ID] = target
		}
	}
	return out
}

// Pause everything pauses the viewer's autopilots and withdraws their
// pending runs, leaves their running runs alone unless asked, and touches
// nothing of anyone else's: that is reported as skipped, and the whole act
// is one audit row.
func TestPauseAll(t *testing.T) {
	f := newRunsFixture(t)
	conn := f.connect(t, runsHello(4))

	// bea: a running run, an offered one, and an autopilot with an offered task.
	running := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, running.ID)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypeAccept, Accept: &runnerwire.Accept{RunID: running.ID}})
	f.waitStatus(t, running.ID, runnerwire.RunRunning)
	beaPilot, beaTaskRun := f.startGoal(t, f.bea, conn, "goal-bea", "task-bea")
	pending := f.dispatch(t, f.bea, f.runBody(""))
	expectOffer(t, conn, pending.ID)

	// cal: his own runner, run and autopilot.
	calRunner := f.create(t, f.cal, "cal-pc")
	calConn := runnerSocket(t, f.liveURL, calRunner.Token)
	sendEnvelope(t, calConn, runsHello(4))
	if welcome := readEnvelopeForTest(t, calConn); welcome.Type != runnerwire.TypeWelcome {
		t.Fatalf("welcome = %+v", welcome)
	}
	body, _ := json.Marshal(runnerwire.CreateRunRequest{
		NodeID: runsTestNode, RunnerID: calRunner.Runner.ID, Agent: "claude", RepoID: "repo-1",
	})
	calRun := f.dispatch(t, f.cal, string(body))
	expectOffer(t, calConn, calRun.ID)
	calPilot, calTaskRun := f.startGoal(t, f.cal, calConn, "goal-cal", "task-cal")

	// The body is checked.
	if code := f.do(t, &f.bea, http.MethodPost, "/api/control/pause-all", `{"cancelRunning":"yes"}`, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", code)
	}

	out := f.pauseAll(t, f.bea, `{"cancelRunning":false}`)
	paused := byID(out.Paused)
	if len(out.Paused) != 1 || paused[beaPilot].Kind != controlapi.KindAutopilot {
		t.Fatalf("paused = %+v", out.Paused)
	}
	cancelled := byID(out.Cancelled)
	if len(out.Cancelled) != 2 || cancelled[pending.ID].Status != runnerwire.RunOffered || cancelled[beaTaskRun].Kind != controlapi.KindRun {
		t.Fatalf("cancelled = %+v", out.Cancelled)
	}
	skipped := byID(out.Skipped)
	if skipped[running.ID].Reason != controlapi.SkipRunning {
		t.Fatalf("bea's running run: %+v", out.Skipped)
	}
	if got := skipped[calPilot]; got.Kind != controlapi.KindAutopilot || got.Reason != controlapi.SkipNotPermitted {
		t.Fatalf("cal's autopilot: %+v", out.Skipped)
	}
	hidden := 0
	for _, target := range out.Skipped {
		if target.Kind == controlapi.KindRun && target.Reason == controlapi.SkipNotPermitted {
			if target.ID != "" || target.NodeID != "" {
				t.Fatalf("someone else's run named: %+v", target)
			}
			hidden++
		}
	}
	if hidden != 2 || len(out.Skipped) != 4 {
		t.Fatalf("skipped = %+v", out.Skipped)
	}

	// What it did, and what it left.
	if got := f.autopilotStatus(t, beaPilot); got != runnerwire.AutopilotPaused {
		t.Fatalf("bea's autopilot is %s", got)
	}
	if got := f.autopilotStatus(t, calPilot); got != runnerwire.AutopilotRunning {
		t.Fatalf("cal's autopilot is %s", got)
	}
	f.waitStatus(t, pending.ID, runnerwire.RunCancelled)
	f.waitStatus(t, running.ID, runnerwire.RunRunning)
	for _, id := range []string{calRun.ID, calTaskRun} {
		if run, code := f.getRun(t, f.cal, id); code != http.StatusOK || run.Status != runnerwire.RunOffered {
			t.Fatalf("cal's run %s = %d %s", id, code, run.Status)
		}
	}

	// One audit row for the whole act.
	events, err := f.server.audit.Query(context.Background(), audit.Filter{ActionPrefix: "POST control/pause-all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("audit rows = %+v", events)
	}
	detail := events[0].Detail
	if detail["autopilotsPaused"] != float64(1) || detail["runsCancelled"] != float64(2) ||
		detail["skipped"] != float64(4) || detail["cancelRunning"] != false {
		t.Fatalf("audit detail = %+v", detail)
	}

	// cancelRunning stops the running run too; cal's still stand.
	out = f.pauseAll(t, f.bea, `{"cancelRunning":true}`)
	if got := byID(out.Cancelled); len(out.Cancelled) != 1 || got[running.ID].Status != runnerwire.RunRunning {
		t.Fatalf("second pause-all cancelled = %+v", out.Cancelled)
	}
	if len(out.Paused) != 0 {
		t.Fatalf("second pause-all paused = %+v", out.Paused)
	}
	if run, _ := f.getRun(t, f.cal, calRun.ID); run.Status != runnerwire.RunOffered {
		t.Fatalf("cal's run after the second pause-all = %s", run.Status)
	}
}
