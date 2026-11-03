package autopilot

import (
	"context"
	"testing"

	"nodevas/internal/runnerwire"
	"nodevas/internal/testpg"
)

// InProject lists one project's autopilots in the statuses asked for, oldest
// first; List views each.
func TestListLiveAutopilotsOfAProject(t *testing.T) {
	ctx := context.Background()
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	store := NewStore(database)
	create := func(project, goal string) Record {
		t.Helper()
		r, err := store.Create(ctx, Record{AutopilotRun: runnerwire.AutopilotRun{
			Project: project, GoalNodeID: goal, CreatedBy: "bea", CreatedByName: "Bea",
		}}, []runnerwire.TaskInstance{{NodeID: goal + "-task", Title: "Task", State: runnerwire.TaskQueued}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := create("main", "g1")
	paused := create("main", "g2")
	paused.Status = runnerwire.AutopilotPaused
	if _, err := store.Save(ctx, paused, nil); err != nil {
		t.Fatal(err)
	}
	done := create("main", "g3")
	done.Status = runnerwire.AutopilotSucceeded
	if _, err := store.Save(ctx, done, nil); err != nil {
		t.Fatal(err)
	}
	create("other", "g1")

	ids, err := store.InProject(ctx, "main", runnerwire.AutopilotRunning, runnerwire.AutopilotPaused)
	if err != nil || len(ids) != 2 || ids[0] != first.ID || ids[1] != paused.ID {
		t.Fatalf("live in main = %v, %v", ids, err)
	}
	if ids, _ := store.InProject(ctx, "main", runnerwire.AutopilotPaused); len(ids) != 1 || ids[0] != paused.ID {
		t.Fatalf("paused in main = %v", ids)
	}
	if ids, _ := store.InProject(ctx, "main"); len(ids) != 0 {
		t.Fatalf("no statuses = %v", ids)
	}

	o := New(Options{Store: store, Runs: newFakeRuns(), Board: newBoard([]string{"g1-task:task"}, nil)})
	views, err := o.List(ctx, "main", runnerwire.AutopilotRunning, runnerwire.AutopilotPaused)
	if err != nil || len(views) != 2 || views[0].Run.ID != first.ID || len(views[0].Tasks) != 1 ||
		views[1].Run.Status != runnerwire.AutopilotPaused {
		t.Fatalf("views = %+v, %v", views, err)
	}
	if views, _ := o.List(ctx, "elsewhere", runnerwire.AutopilotRunning); len(views) != 0 {
		t.Fatalf("a project without autopilots = %+v", views)
	}
}
