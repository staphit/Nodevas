package runs

import (
	"context"

	"nodevas/internal/runnerwire"
)

// coordHooks holds coordination callbacks wired by the server and optional
// test seams used by F1 unit tests (nil in production). This file belongs to
// F1 alone so later waves replacing the stub files keep every hook intact.
type coordHooks struct {
	releaseRunClaim func(ctx context.Context, project, nodeID, runID string)

	// Test seams (nil outside tests).
	onWaitsBehind       func(ctx context.Context, r Record) bool
	onHeldByOther       func(ctx context.Context, r Record) bool
	onAcceptViewOnly    func(ctx context.Context, r Record, accept runnerwire.Accept)
	onEnded             func(ctx context.Context, r Record)
	onReleaseWaiting    func(ctx context.Context)
	onReleaseGroupHolds func(ctx context.Context)
}

// SetRunClaimRelease registers the callback that releases a run's own board
// claim when the run is stopped by take-over or ends with a queued busy run
// waiting behind it (§C.2.5). Call it before serving.
func (d *Dispatcher) SetRunClaimRelease(fn func(ctx context.Context, project, nodeID, runID string)) {
	d.coord.releaseRunClaim = fn
}

func (d *Dispatcher) releaseClaim(ctx context.Context, project, nodeID, runID string) {
	if d.coord.releaseRunClaim != nil {
		d.coord.releaseRunClaim(ctx, project, nodeID, runID)
	}
}

// ended is deferred at the top of afterEnd (after the RunFinal check and
// before the global early return), so it runs for every final run after any
// fallback, usage-limit successor or pipeline step has been created (§C.2.3).
func (d *Dispatcher) ended(ctx context.Context, r Record) {
	d.busyEnded(ctx, r)
	d.contestEnded(ctx, r)
	if d.coord.onEnded != nil {
		d.coord.onEnded(ctx, r)
	}
}

func (d *Dispatcher) pumpWaitsBehind(ctx context.Context, r Record) bool {
	if d.coord.onWaitsBehind != nil {
		return d.coord.onWaitsBehind(ctx, r)
	}
	return d.waitsBehind(ctx, r)
}

func (d *Dispatcher) pumpHeldByOther(ctx context.Context, r Record) bool {
	if d.coord.onHeldByOther != nil {
		return d.coord.onHeldByOther(ctx, r)
	}
	return d.heldByOther(ctx, r)
}

func (d *Dispatcher) callAcceptViewOnly(ctx context.Context, r Record, accept runnerwire.Accept) {
	d.acceptViewOnly(ctx, r, accept)
	if d.coord.onAcceptViewOnly != nil {
		d.coord.onAcceptViewOnly(ctx, r, accept)
	}
}

func (d *Dispatcher) tickReleaseWaiting(ctx context.Context) {
	d.releaseWaiting(ctx)
	if d.coord.onReleaseWaiting != nil {
		d.coord.onReleaseWaiting(ctx)
	}
}

func (d *Dispatcher) tickReleaseGroupHolds(ctx context.Context) {
	d.releaseGroupHolds(ctx)
	if d.coord.onReleaseGroupHolds != nil {
		d.coord.onReleaseGroupHolds(ctx)
	}
}
