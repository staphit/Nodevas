package runs

import "context"

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

// EvidenceForRun lists a run's evidence, oldest first.
func (s *Store) EvidenceForRun(ctx context.Context, runID string) ([]Evidence, error) {
	// TODO(green): implement.
	return nil, nil
}
