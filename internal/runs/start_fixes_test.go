package runs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// B-24 (I-1): the acceptance criteria are locked only once the run is
// recorded; a lock that then fails cancels the run it was for.
func TestCriteriaLockedOnlyAfterTheRunIsRecorded(t *testing.T) {
	f := newRoleFixture(t, true)
	locked := 0
	f.d.createRun = func(context.Context, New) (Record, error) { return Record{}, errors.New("disk full") }
	_, err := f.roleStart(t, lightStages(), criteria("GET /x returns 200"), func(req *StartRequest) {
		req.LockAcceptance = func() error { locked++; return nil }
	})
	if locked != 0 {
		t.Fatalf("the criteria were locked %d times for a run that was never recorded", locked)
	}
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("start with a failing create: %v", err)
	}
}

func TestCriteriaLockFailureCancelsTheRun(t *testing.T) {
	f := newRoleFixture(t, true)
	_, err := f.roleStart(t, lightStages(), criteria("GET /x returns 200"), func(req *StartRequest) {
		req.LockAcceptance = func() error { return errors.New("store busy") }
	})
	if err == nil {
		t.Fatal("start succeeded with a failing criteria lock")
	}
	all, listErr := f.s.List(context.Background(), Filter{Project: ".", NodeID: "n1", Limit: 10})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(all) != 1 || all[0].Status != runnerwire.RunCancelled || !strings.Contains(all[0].Error, "criteria lock failed") {
		t.Fatalf("runs after a failed lock = %+v, want one cancelled run", all)
	}
}

// B-25 (I-2): a staged project's executor work runs headless unless a
// person gives an override reason.
func TestInteractiveExecutorOnStagedNodeRefused(t *testing.T) {
	caps := append(append([]string{}, allRoleCaps...), runnerwire.CapInteractive)
	f := newRoleFixture(t, true, caps...)
	_, err := f.roleStart(t, standardStages(), criteria("GET /x returns 200"), func(req *StartRequest) {
		req.Body.Mode = runnerwire.ModeInteractive
	})
	se := wantStartRefusal(t, err, StartPrecondition, "stages run headless")
	if se.Reason != "STAGED_NEEDS_HEADLESS" {
		t.Fatalf("reason = %q, want STAGED_NEEDS_HEADLESS", se.Reason)
	}

	r, err := f.roleStart(t, standardStages(), criteria("GET /x returns 200"), func(req *StartRequest) {
		req.Body.Mode = runnerwire.ModeInteractive
		req.Body.Override = "pairing on the fix"
	})
	if err != nil || r.Mode != runnerwire.ModeInteractive {
		t.Fatalf("interactive with an override: %v (%+v)", err, r.Run)
	}

	g := newRoleFixture(t, true, caps...)
	r, err = g.roleStart(t, nil, nil, func(req *StartRequest) { req.Body.Mode = runnerwire.ModeInteractive })
	if err != nil || r.Mode != runnerwire.ModeInteractive {
		t.Fatalf("interactive on a node with no stages: %v", err)
	}
}
