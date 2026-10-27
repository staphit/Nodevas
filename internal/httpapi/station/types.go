package station

// The wire contract of GET /api/station/building, mirrored by
// web/src/api/station.ts. Every field has an explicit camelCase tag, and no
// array is ever sent as null.

// BuildingResponse is every project the viewer may read, busiest first.
type BuildingResponse struct {
	Projects []BuildingProject `json:"projects"`
	// Truncated: there were more readable projects than MaxProjects (or
	// than the request queries), and Total is how many.
	Truncated bool `json:"truncated"`
	Total     int  `json:"total"`
}

// BuildingProject is one project's office: who works there, who waits on
// the viewer, and how it looks.
type BuildingProject struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Layout is graph.yaml's type: "workflow", "story" or "".
	Layout string `json:"layout"`
	// OfficeStyle is resolved (the layout's default when unset).
	OfficeStyle    string `json:"officeStyle"`
	OfficeStyleSet bool   `json:"officeStyleSet"`
	// Runs are the viewer-visible runs: every one not yet ended and the
	// recent ended ones (within a day), newest first, capped.
	Runs          []BuildingRun `json:"runs"`
	RunsTruncated bool          `json:"runsTruncated"`
	// OthersActive counts the active runs the viewer may not see (someone
	// else's, on someone else's runner). Always 0 for an administrator.
	OthersActive int               `json:"othersActive"`
	Sessions     []BuildingSession `json:"sessions"`
	Waiting      BuildingWaiting   `json:"waiting"`
	Asks         []BuildingAsk     `json:"asks"`
	Reviews      []BuildingReview  `json:"reviews"`
}

// BuildingRun is the summary of a run the building draws.
type BuildingRun struct {
	ID          string            `json:"id"`
	NodeID      string            `json:"nodeId"`
	Title       string            `json:"title"`
	Agent       string            `json:"agent"`
	Model       string            `json:"model"`
	Kind        string            `json:"kind"`
	Status      string            `json:"status"`
	RunnerID    string            `json:"runnerId"`
	RunnerName  string            `json:"runnerName"`
	FailureKind string            `json:"failureKind,omitempty"`
	RetryOf     string            `json:"retryOf,omitempty"`
	PipelineID  string            `json:"pipelineId,omitempty"`
	RetryAt     string            `json:"retryAt,omitempty"`
	PRURL       string            `json:"prUrl,omitempty"`
	PRNumber    int               `json:"prNumber,omitempty"`
	PRState     string            `json:"prState,omitempty"`
	CIStatus    string            `json:"ciStatus,omitempty"`
	Merge       *BuildingRunMerge `json:"merge,omitempty"`
	CreatedAt   string            `json:"createdAt"`
	StartedAt   string            `json:"startedAt"`
	FinishedAt  string            `json:"finishedAt"`
}

// BuildingRunMerge is the status of a run's latest merge approval.
type BuildingRunMerge struct {
	Status string `json:"status"`
}

// BuildingSession is a connected agent session.
type BuildingSession struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	State    string `json:"state"`
	NodeID   string `json:"nodeId"`
	Doing    string `json:"doing"`
	RunID    string `json:"runId"`
	LastSeen int64  `json:"lastSeen"`
	Online   bool   `json:"online"`
}

// BuildingWaiting counts, completely, what waits on the viewer in the
// project and is theirs to act on.
type BuildingWaiting struct {
	Asks    int `json:"asks"`
	Held    int `json:"held"`
	Reviews int `json:"reviews"`
	Total   int `json:"total"`
}

// BuildingAsk is an agent's question waiting on the viewer.
type BuildingAsk struct {
	ID          string `json:"id"`
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	Agent       string `json:"agent"`
	Question    string `json:"question"`
	NodeID      string `json:"nodeId"`
	CreatedAt   int64  `json:"createdAt"`
}

// BuildingReview is an autopilot task waiting for a person's review.
type BuildingReview struct {
	AutopilotID string `json:"autopilotId"`
	GoalNodeID  string `json:"goalNodeId"`
	NodeID      string `json:"nodeId"`
	PipelineID  string `json:"pipelineId"`
}
