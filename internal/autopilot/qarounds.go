package autopilot

import "context"

// QA history (agent analytics A): every goal QA round's outcome, kept, not
// only the latest (autopilot_runs.qa_round and qa_run_id).

// QARound is one recorded QA round.
type QARound struct {
	AutopilotID string `json:"autopilotId"`
	Round       int    `json:"round"`
	RunID       string `json:"runId"`
	// Outcome is pass, fail or none (failed with no fix tasks).
	Outcome   string `json:"outcome"`
	FixTasks  int    `json:"fixTasks"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"createdAt"`
}

// QARounds lists an autopilot's QA rounds, oldest first.
func (s *Store) QARounds(ctx context.Context, autopilotID string) ([]QARound, error) {
	// TODO(green): implement.
	return nil, nil
}
