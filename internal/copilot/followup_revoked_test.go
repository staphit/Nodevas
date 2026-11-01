package copilot

import (
	"context"
	"sync"
	"testing"

	"nodevas/internal/runnerwire"
)

// A follow-up reads the goal's tasks, and a revision's writes the board, as
// the conversation's owner, after the run that owner started has ended. An
// owner who may no longer change the project by then (removed from it, or
// made a viewer) gets neither: the follow-up is dropped.
func TestFollowupDroppedWhenTheOwnerLostTheProject(t *testing.T) {
	runs := newFakeRuns()
	var mu sync.Mutex
	var asked []string
	goalReads := 0
	h := newHarness(t, func(opts *Options) {
		withFollowups(runs)(opts)
		goalTasks := opts.GoalTasks
		opts.GoalTasks = func(ctx context.Context, project, goal string) (Goal, error) {
			mu.Lock()
			goalReads++
			mu.Unlock()
			return goalTasks(ctx, project, goal)
		}
		opts.MayWork = func(_ context.Context, project, accountID string) bool {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, project+"/"+accountID)
			return false
		}
	})
	conversation := h.conversation(alice, ModeDefault)
	addFollowup(t, h, conversation, "en")
	run := planRun(runnerwire.RunSucceeded)
	runs.set(run)
	h.service.RunChanged(run)
	h.service.background.Wait()

	if entries := entriesOf(t, h, conversation); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
	if len(runs.notifications()) != 0 || len(pendingFollowups(t, h)) != 0 {
		t.Fatalf("notified %v; pending %+v", runs.notifications(), pendingFollowups(t, h))
	}
	mu.Lock()
	defer mu.Unlock()
	if goalReads != 0 {
		t.Fatalf("the goal's tasks were read %d times for an owner who lost the project", goalReads)
	}
	if len(asked) == 0 || asked[0] != "demo/"+alice.ID {
		t.Fatalf("MayWork asked %v, want demo/%s", asked, alice.ID)
	}
}
