package runs

import (
	"context"

	"nodevas/internal/runnerwire"
)

// acceptViewOnly records an interactive run's view-only state from Accept and
// turns any attached terminal viewers read-only (§7.3).
func (d *Dispatcher) acceptViewOnly(ctx context.Context, r Record, accept runnerwire.Accept) {
	if !accept.ViewOnly {
		return
	}
	_ = d.store.SetViewOnly(ctx, r.ID, true)
	if t := d.Terminals().Get(r.ID); t != nil {
		t.setViewOnly()
	}
}
