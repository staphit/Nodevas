package agents

import "context"

// RunAskPreviewRunes caps a timeline entry's one-line preview.
const RunAskPreviewRunes = 120

// RunAsk is one ask of a run as the run's timeline shows it to a viewer.
type RunAsk struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Decision    string `json:"decision,omitempty"`
	Question    string `json:"question,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Note        string `json:"note,omitempty"`
	SessionName string `json:"sessionName,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	DecidedAt   int64  `json:"decidedAt,omitempty"`
	CanAnswer   bool   `json:"canAnswer"`
}

// RunAsks is not built yet.
func (h *Hub) RunAsks(ctx context.Context, project, runID string, v Viewer) ([]RunAsk, error) {
	return nil, nil
}
