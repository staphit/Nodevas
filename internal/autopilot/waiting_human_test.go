package autopilot

import (
	"context"
	"path/filepath"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

// WaitingHuman finds, across projects, the live autopilots' tasks whose
// review waits for a person.
func TestWaitingHuman(t *testing.T) {
	ctx := context.Background()
	database, err := db.OpenAt(filepath.Join(t.TempDir(), db.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewStore(database)
	create := func(project, goal string, tasks ...runnerwire.TaskInstance) Record {
		t.Helper()
		r, err := store.Create(ctx, Record{AutopilotRun: runnerwire.AutopilotRun{
			Project: project, GoalNodeID: goal, CreatedBy: "bea", CreatedByName: "Bea",
		}}, tasks)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	waiting := func(node, pipeline string) runnerwire.TaskInstance {
		return runnerwire.TaskInstance{NodeID: node, Title: node, State: runnerwire.TaskWaitingHuman, PipelineID: pipeline}
	}
	live := create("main", "g1",
		waiting("t1", "p1"),
		waiting("t2", ""), // no pipeline: nothing to review
		runnerwire.TaskInstance{NodeID: "t3", Title: "t3", State: runnerwire.TaskWaitingReview, PipelineID: "p3"},
	)
	paused := create("main", "g2", waiting("t4", "p4"))
	paused.Status = runnerwire.AutopilotPaused
	if _, err := store.Save(ctx, paused, nil); err != nil {
		t.Fatal(err)
	}
	done := create("main", "g3", waiting("t5", "p5"))
	done.Status = runnerwire.AutopilotCancelled
	if _, err := store.Save(ctx, done, nil); err != nil {
		t.Fatal(err)
	}
	other := create("other", "g1", waiting("t6", "p6"))
	create("hidden", "g1", waiting("t7", "p7"))

	rows, err := store.WaitingHuman(ctx, []string{"main", "other"})
	if err != nil {
		t.Fatal(err)
	}
	want := []WaitingHumanRow{
		{Project: "main", AutopilotID: live.ID, GoalNodeID: "g1", NodeID: "t1", PipelineID: "p1"},
		{Project: "main", AutopilotID: paused.ID, GoalNodeID: "g2", NodeID: "t4", PipelineID: "p4"},
		{Project: "other", AutopilotID: other.ID, GoalNodeID: "g1", NodeID: "t6", PipelineID: "p6"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if rows, err := store.WaitingHuman(ctx, nil); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("no projects = %v, %v", rows, err)
	}
	o := New(Options{Store: store, Runs: newFakeRuns(), Board: newBoard(nil, nil)})
	if rows, err := o.WaitingHuman(ctx, []string{"other"}); err != nil || len(rows) != 1 {
		t.Fatalf("orchestrator = %v, %v", rows, err)
	}
}
