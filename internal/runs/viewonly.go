package runs

import (
	"context"

	"nodevas/internal/runnerwire"
)

// acceptViewOnly records an interactive run's view-only state from Accept and
// turns any attached terminal viewers read-only. Stub in Wave 0 (F1); filled by G1.
func (d *Dispatcher) acceptViewOnly(ctx context.Context, r Record, accept runnerwire.Accept) {}
