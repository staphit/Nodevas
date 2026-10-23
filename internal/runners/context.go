package runners

import (
	"context"

	"nodevas/internal/runnerwire"
)

type runnerKey struct{}

// WithRunner records the runner a request authenticated as. The key is
// unexported, so only code that checked a token can put one there.
func WithRunner(ctx context.Context, runner runnerwire.Runner) context.Context {
	return context.WithValue(ctx, runnerKey{}, runner)
}

// RunnerFrom returns the runner the request authenticated as, if any.
func RunnerFrom(ctx context.Context) (runnerwire.Runner, bool) {
	runner, ok := ctx.Value(runnerKey{}).(runnerwire.Runner)
	return runner, ok
}
