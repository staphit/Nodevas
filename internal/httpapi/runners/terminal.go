package runners

import (
	"context"
	"log/slog"

	"nodevas/internal/runnerwire"
)

// termOut relays a runner's terminal output (W2.5) to the browsers watching
// the run. Output for a run that is not this runner's, not interactive or not
// running, or with a repeated seq, is dropped: a bad chunk never closes the
// socket the runner's other runs depend on. Drops are logged at debug only,
// since output arrives many times a second.
func (a *API) termOut(ctx context.Context, runnerID string, env runnerwire.Envelope) {
	if env.RunID == "" || env.Term == nil {
		slog.Debug("runner term_out malformed", slog.String("runner", runnerID))
		return
	}
	if err := a.runs.TermOutput(ctx, runnerID, env.RunID, *env.Term); err != nil {
		slog.Debug("runner term_out dropped",
			slog.String("runner", runnerID), slog.String("run", env.RunID), slog.Any("error", err))
	}
}
