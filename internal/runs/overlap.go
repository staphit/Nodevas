package runs

import (
	"context"

	"nodevas/internal/runnerwire"
)

// changesTouched adds a run's committed files to Layer 2 touched-file overlap
// tracking (§C.3.2). Stub in Wave 0 (F1); filled by C2.
func (d *Dispatcher) changesTouched(ctx context.Context, r Record, files []runnerwire.FileChange) {}

// overlapNote returns a short prompt note about active overlaps for a global
// follow-up (§C.3.3). Stub in Wave 0 (F1); filled by C2.
func (d *Dispatcher) overlapNote(ctx context.Context, prior Record) string { return "" }
