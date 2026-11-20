package runs

import "context"

// releaseGroupHolds pumps runners whose multi-repo group usage-limit hold has
// expired (§10.2). Stub in Wave 0 (F1); filled by G2.
func (d *Dispatcher) releaseGroupHolds(ctx context.Context) {}
