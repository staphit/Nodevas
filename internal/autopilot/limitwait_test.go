package autopilot

import (
	"context"
	"testing"

	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// A task whose run stopped at a usage limit and now waits, queued, for the
// limit to reset is waiting, not failed.
func TestLimitWaitCountsAsWaiting(t *testing.T) {
	failed := runs.Record{Run: runnerwire.Run{ID: "r1", Status: runnerwire.RunFailed,
		FailureKind: runnerwire.FailureQuota, Error: "usage limit", Agent: "claude", Model: "opus",
		CreatedAt: "2026-10-27T10:00:00.000000Z", StartedAt: "2026-10-27T10:00:01.000000Z",
		FinishedAt: "2026-10-27T10:30:00.000000Z", HandoffBranch: "nodevas/handoff/r1"}}
	waiting := runs.Record{Run: runnerwire.Run{ID: "r2", RetryOf: "r1", Status: runnerwire.RunQueued,
		Agent: "claude", Model: "opus", CreatedAt: "2026-10-27T10:30:00.000100Z",
		ContinuesFrom: "nodevas/handoff/r1", RetryAt: "2026-10-27T15:01:00.000000Z"}}
	d := derive(observation{runs: []runs.Record{failed, waiting}}, "2026-10-27T10:31:00.000000Z")
	if d.phase != phaseActive || !d.queued || d.retryAt != waiting.RetryAt {
		t.Fatalf("derived = %+v", d)
	}
	for _, s := range d.stages {
		if s.Status == runnerwire.StageFailed {
			t.Errorf("a stage failed: %+v", s)
		}
	}
	task := &runnerwire.TaskInstance{}
	var stages []runnerwire.StageRecord
	(&Orchestrator{}).act(context.Background(), &work{}, task, d, &stages)
	if task.State != runnerwire.TaskQueued || task.Reason != "waiting for the usage limit to reset; retry at "+waiting.RetryAt {
		t.Fatalf("task = %+v", task)
	}
	if final1(task.State) {
		t.Fatal("a waiting task is final")
	}

	// Once the wait is cancelled, the task has failed.
	waiting.Status, waiting.FinishedAt = runnerwire.RunCancelled, "2026-10-27T11:00:00.000000Z"
	if d := derive(observation{runs: []runs.Record{failed, waiting}}, "2026-10-27T11:00:01.000000Z"); d.phase != phaseFailed {
		t.Fatalf("cancelled wait = %+v", d)
	}
}
