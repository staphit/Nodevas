package copilot

import (
	"context"
	"errors"
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
		opts.MayWork = func(_ context.Context, project, accountID string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, project+"/"+accountID)
			return false, nil
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

// A check that could not be made (the account store failing for a moment)
// is not a refusal: the follow-up stays pending, and is delivered when it
// is next looked at and the check answers.
func TestFollowupWaitsOutAFailedAccessCheck(t *testing.T) {
	runs := newFakeRuns()
	var mu sync.Mutex
	failing := true
	h := newHarness(t, func(opts *Options) {
		withFollowups(runs)(opts)
		opts.MayWork = func(context.Context, string, string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if failing {
				return false, errors.New("read accounts: connection reset")
			}
			return true, nil
		}
	})
	conversation := h.conversation(alice, ModeDefault)
	addFollowup(t, h, conversation, "en")
	run := planRun(runnerwire.RunSucceeded)
	runs.set(run)
	h.service.RunChanged(run)
	h.service.background.Wait()

	if entries := entriesOf(t, h, conversation); len(entries) != 0 {
		t.Fatalf("delivered without a check: %+v", entries)
	}
	if pending := pendingFollowups(t, h); len(pending) != 1 {
		t.Fatalf("pending after a failed check = %+v, want the follow-up kept", pending)
	}

	mu.Lock()
	failing = false
	mu.Unlock()
	h.service.RunChanged(run)
	h.service.background.Wait()
	if entries := entriesOf(t, h, conversation); len(entries) == 0 {
		t.Fatal("not delivered once the check answered")
	}
	if pending := pendingFollowups(t, h); len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}
}
