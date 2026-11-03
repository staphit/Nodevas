package autopilot

import (
	"context"
	"log/slog"
)

// QA history (agent analytics A): every goal QA round's outcome, kept, not
// only the latest (autopilot_runs.qa_round and qa_run_id).

// QA round outcomes.
const (
	QAOutcomePass = "pass"
	QAOutcomeFail = "fail"
	// QAOutcomeNone: QA failed and created no fix tasks.
	QAOutcomeNone = "none"
)

// maxQAReason caps a round's reason.
const maxQAReason = 500

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

// AddQARound records one QA round; a round already recorded is kept as it
// was.
func (s *Store) AddQARound(ctx context.Context, r QARound) error {
	reason := r.Reason
	if len(reason) > maxQAReason {
		reason = reason[:maxQAReason]
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO autopilot_qa_rounds (autopilot_id, round, run_id, outcome, fix_tasks, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (autopilot_id, round) DO NOTHING`,
		r.AutopilotID, r.Round, r.RunID, r.Outcome, r.FixTasks, reason, s.stamp())
	return err
}

// QARounds lists an autopilot's QA rounds, oldest first.
func (s *Store) QARounds(ctx context.Context, autopilotID string) ([]QARound, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT autopilot_id, round, run_id, outcome, fix_tasks, reason, created_at
  FROM autopilot_qa_rounds WHERE autopilot_id = ? ORDER BY round`, autopilotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QARound
	for rows.Next() {
		var r QARound
		var round, fix int64
		if err := rows.Scan(&r.AutopilotID, &round, &r.RunID, &r.Outcome, &fix, &r.Reason, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Round, r.FixTasks = int(round), int(fix)
		out = append(out, r)
	}
	return out, rows.Err()
}

// recordQARound keeps one QA round's outcome.
func (o *Orchestrator) recordQARound(ctx context.Context, autopilotID string, round int, runID, outcome string, fixTasks int, reason string) {
	if o.opts.Store == nil {
		return
	}
	if err := o.opts.Store.AddQARound(ctx, QARound{AutopilotID: autopilotID, Round: round, RunID: runID,
		Outcome: outcome, FixTasks: fixTasks, Reason: reason}); err != nil {
		slog.Warn("autopilot: record QA round", slog.String("autopilot", autopilotID), slog.Any("error", err))
	}
}
