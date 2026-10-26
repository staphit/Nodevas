package autopilot

import (
	"errors"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// After a revision of the goal (revise_goal), resuming with Adopt takes the
// goal as it stands: a task the replan added is worked, a pending task it
// skipped on the board is skipped (and settles what waits on it), a pending
// task whose content it changed is worked as changed rather than held, and
// the task with a try going keeps its record.
func TestAdoptARevisedGoal(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b", "c"}, []string{"a>goal", "a>b", "b>goal", "c>goal"}))
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	a := task(v, "a")
	if a.State != runnerwire.TaskQueued {
		t.Fatalf("after create: %s", states(v))
	}
	if err := h.o.Adopt(h.ctx, v.Run.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("adopt on a running autopilot: %v", err)
	}
	if err := h.o.Pause(h.ctx, v.Run.ID); err != nil {
		t.Fatal(err)
	}
	// The replan: c is obsolete, b changes, x is new and waits on a.
	if err := h.board.SetStatus("main", "c", engine.StatusSkipped, "copilot revision", "dropped"); err != nil {
		t.Fatal(err)
	}
	h.board.edit("b", "Do b. Also the SSO button.")
	h.board.addNode("x", "goal")
	h.board.mu.Lock()
	h.board.graph.Edges = append(h.board.graph.Edges, &engine.Edge{From: "a", To: "x"})
	h.board.mu.Unlock()

	// Paused, the frozen set holds b for its change and leaves x out.
	v = h.view(v.Run.ID)
	if task(v, "x").NodeID != "" || task(v, "b").Hold != runnerwire.HoldChanged {
		t.Fatalf("paused: %s, b %+v", states(v), task(v, "b"))
	}
	if err := h.o.Adopt(h.ctx, v.Run.ID); err != nil {
		t.Fatal(err)
	}
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotRunning {
		t.Fatalf("run = %+v", v.Run)
	}
	if c := task(v, "c"); c.State != runnerwire.TaskSkipped || !strings.Contains(c.Reason, "skipped on the board") {
		t.Fatalf("c: %+v", c)
	}
	if b := task(v, "b"); b.Hold != "" || b.State != runnerwire.TaskNone {
		t.Fatalf("b: %+v", b)
	}
	if x := task(v, "x"); x.NodeID != "x" || x.State != runnerwire.TaskNone || x.ContentHash == "" {
		t.Fatalf("x: %+v", x)
	}
	if got := task(v, "a"); got.RunID != a.RunID || got.State != runnerwire.TaskQueued {
		t.Fatalf("a lost its try: %+v", got)
	}
	// a done: b (as changed) and x go out in turn.
	h.finishWithPR(a.RunID)
	v = h.view(v.Run.ID)
	if h.runs.startsFor("b")+h.runs.startsFor("x") != 1 {
		t.Fatalf("after a: %s", states(v))
	}
	if h.runs.startsFor("b") == 1 {
		if _, body, _, _ := h.runs.lastStart("b").LoadNode(); !strings.Contains(body, "SSO button") {
			t.Fatalf("b runs its old content: %q", body)
		}
	}
	if h.runs.startsFor("c") != 0 {
		t.Fatal("the skipped task was dispatched")
	}
}

// A task skipped on the board before it started shows as skipped, not done.
func TestSkippedOnTheBoardIsSkipped(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "b>goal"}))
	if err := h.board.SetStatus("main", "b", engine.StatusSkipped, "bea", ""); err != nil {
		t.Fatal(err)
	}
	v := h.create(runnerwire.AutopilotPolicy{})
	if b := task(v, "b"); b.State != runnerwire.TaskSkipped {
		t.Fatalf("b: %+v", b)
	}
}
