package runs

import (
	"context"

	"nodevas/internal/runnerwire"
)

// Pipeline evidence rows (agent analytics A): every verify, guard, CI,
// review and judge outcome a staged pipeline records, tied to its run.

// Evidence outcomes.
const (
	EvidencePass = "pass"
	EvidenceFail = "fail"
	EvidenceSkip = "skip"
	EvidenceInfo = "info"
)

// Evidence is one run_evidence row.
type Evidence struct {
	RunID      string `json:"runId"`
	PipelineID string `json:"pipelineId"`
	Project    string `json:"project"`
	NodeID     string `json:"nodeId"`
	Kind       string `json:"kind"`
	Outcome    string `json:"outcome"`
	Summary    string `json:"summary"`
	At         string `json:"at"`
}

// MaxEvidenceSummary caps an evidence row's summary.
const MaxEvidenceSummary = 2000

// AddEvidence stores one evidence row for run r.
func (s *Store) AddEvidence(ctx context.Context, r Record, kind, outcome, summary string) error {
	switch outcome {
	case EvidencePass, EvidenceFail, EvidenceSkip, EvidenceInfo:
	default:
		outcome = EvidenceInfo
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO run_evidence (run_id, pipeline_id, project, node_id, kind, outcome, summary, at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.PipelineID, r.Project, r.NodeID, Truncate(kind, maxShortField),
		outcome, Truncate(summary, MaxEvidenceSummary), s.stamp())
	return err
}

// EvidenceForRun lists a run's evidence, oldest first.
func (s *Store) EvidenceForRun(ctx context.Context, runID string) ([]Evidence, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT run_id, pipeline_id, project, node_id, kind, outcome, summary, at
  FROM run_evidence WHERE run_id = ? ORDER BY at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.RunID, &e.PipelineID, &e.Project, &e.NodeID, &e.Kind, &e.Outcome, &e.Summary, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// passFail is an evidence outcome from a boolean.
func passFail(ok bool) string {
	if ok {
		return EvidencePass
	}
	return EvidenceFail
}

// ciOutcome is a CI status's evidence outcome.
func ciOutcome(status string) string {
	switch status {
	case runnerwire.CISuccess:
		return EvidencePass
	case runnerwire.CIFailure:
		return EvidenceFail
	}
	return EvidenceInfo
}
