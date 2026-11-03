package autopilot

import (
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// B-11: every QA round is kept: two failing rounds, then a pass.
func TestQARoundsKeptPerRound(t *testing.T) {
	h, v := throughTasks(t, qaOnly(func(wf *engine.Workflow) { wf.MaxQARounds = 3 }))
	var qaRuns []string
	for round := 1; round <= 2; round++ {
		qa := h.qaRun(t)
		qaRuns = append(qaRuns, qa.ID)
		fix := "fix" + string(rune('0'+round))
		h.board.addMember(fix, qa.ID, "qa-fix")
		h.finishQA(qa.ID, runnerwire.VerdictFail, false)
		v = h.view(v.Run.ID)
		h.finishWithPR(task(v, fix).RunID)
		v = h.view(v.Run.ID)
	}
	last := h.qaRun(t)
	qaRuns = append(qaRuns, last.ID)
	h.finishQA(last.ID, runnerwire.VerdictPass, true)
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotSucceeded {
		t.Fatalf("after the passing round: %s", v.Run.Status)
	}

	rounds, err := h.store.QARounds(h.ctx, v.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 3 {
		t.Fatalf("QA rounds = %+v, want 3", rounds)
	}
	for i, want := range []struct {
		outcome  string
		fixTasks int
	}{{"fail", 1}, {"fail", 1}, {"pass", 0}} {
		r := rounds[i]
		if r.Round != i+1 || r.RunID != qaRuns[i] || r.Outcome != want.outcome || r.FixTasks != want.fixTasks || r.CreatedAt == "" {
			t.Errorf("round %d = %+v, want outcome %s, %d fix tasks, run %s", i+1, r, want.outcome, want.fixTasks, qaRuns[i])
		}
	}
}
