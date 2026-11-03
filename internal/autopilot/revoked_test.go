package autopilot

import (
	"context"
	"errors"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// An autopilot works as its creator. Once the creator may no longer change
// the project (removed from it, or made a viewer), it pauses with the reason
// and neither dispatches, merges what its runs report nor writes the board,
// and it cannot be resumed until they may again.
func TestAutopilotPausesWhenItsCreatorLosesTheProject(t *testing.T) {
	board := newBoard([]string{"goal", "a", "b"}, []string{"a>b", "b>goal"})
	h := newHarness(t, board)
	allowed := true
	asked := ""
	h.o = New(Options{Store: h.store, Runs: h.runs, Runners: fakeRunners{list: []runnerwire.Runner{onlineRunner("bea")}},
		Board: board, MayWork: func(_ context.Context, project, accountID string) (bool, error) {
			asked = project + "/" + accountID
			return allowed, nil
		}})
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	if task(v, "a").RunID == "" {
		t.Fatalf("a was not dispatched: %s", states(v))
	}

	allowed = false
	h.finishWithPR(task(v, "a").RunID)
	v = h.view(v.Run.ID)
	if asked != "main/bea" {
		t.Errorf("MayWork asked about %q, want main/bea", asked)
	}
	if v.Run.Status != runnerwire.AutopilotPaused || v.Run.Reason != ReasonCreatorLostAccess {
		t.Fatalf("run = %s %q, want paused for its creator", v.Run.Status, v.Run.Reason)
	}
	if start := h.runs.lastStart("b"); start.Body.NodeID != "" {
		t.Fatalf("b was dispatched for a creator who lost the project: %+v", start)
	}
	if status := board.status("a"); status == engine.StatusDone {
		t.Fatal("a's run was merged onto the board for a creator who lost the project")
	}
	if err := h.o.Resume(h.ctx, v.Run.ID); err == nil {
		t.Fatal("resumed for a creator who lost the project")
	}
	if v = h.view(v.Run.ID); v.Run.Status != runnerwire.AutopilotPaused {
		t.Fatalf("run = %s after a refused resume", v.Run.Status)
	}

	// Given the project back, its creator resumes it and it carries on.
	allowed = true
	if err := h.o.Resume(h.ctx, v.Run.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	v = h.view(v.Run.ID)
	if board.status("a") != engine.StatusDone || h.runs.lastStart("b").Body.NodeID != "b" {
		t.Fatalf("after resuming: a %s, states %s", board.status("a"), states(v))
	}
}

// A check that could not be made (the account store failing for a moment)
// is not a refusal: the autopilot keeps its status and does nothing on that
// pass, and carries on at the next once the check answers.
func TestAutopilotWaitsOutAFailedAccessCheck(t *testing.T) {
	board := newBoard([]string{"goal", "a", "b"}, []string{"a>b", "b>goal"})
	h := newHarness(t, board)
	failing := false
	h.o = New(Options{Store: h.store, Runs: h.runs, Runners: fakeRunners{list: []runnerwire.Runner{onlineRunner("bea")}},
		Board: board, MayWork: func(context.Context, string, string) (bool, error) {
			if failing {
				return false, errors.New("read accounts: connection reset")
			}
			return true, nil
		}})
	v := h.create(runnerwire.AutopilotPolicy{MaxParallel: 1})
	if task(v, "a").RunID == "" {
		t.Fatalf("a was not dispatched: %s", states(v))
	}

	failing = true
	h.finishWithPR(task(v, "a").RunID)
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotRunning {
		t.Fatalf("run = %s %q after a failed check, want running", v.Run.Status, v.Run.Reason)
	}
	if start := h.runs.lastStart("b"); start.Body.NodeID != "" {
		t.Fatalf("b was dispatched without a check: %+v", start)
	}
	if status := board.status("a"); status == engine.StatusDone {
		t.Fatal("a's run was merged without a check")
	}

	failing = false
	v = h.view(v.Run.ID)
	if v.Run.Status != runnerwire.AutopilotRunning || board.status("a") != engine.StatusDone ||
		h.runs.lastStart("b").Body.NodeID != "b" {
		t.Fatalf("after the check answered: run %s, a %s, states %s", v.Run.Status, board.status("a"), states(v))
	}
}
