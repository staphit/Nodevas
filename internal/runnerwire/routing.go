package runnerwire

// Routing, fallback and pipelines (W5). All of it is the owner's own
// configuration: there are no built-in routes. The hub decides which agent and
// model a run asks for; the runner still decides whether to take it.

// AgentModel names one agent CLI and one of its models.
type AgentModel struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

// Route match kinds, tried in this order: a node tag, the node's kind, the
// project's default, the owner's global default.
const (
	RouteTag     = "tag"
	RouteKind    = "kind"
	RouteProject = "project"
	RouteDefault = "default"
)

// RouteRule is one routing rule. Rules are per owner; Project "" applies in
// every project.
type RouteRule struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Match is one of the Route* kinds; Value is the tag or kind it matches
	// (unused for project and default).
	Match string `json:"match"`
	Value string `json:"value,omitempty"`
	// RunnerID is optional: "" means whichever runner the dispatcher picks.
	RunnerID string     `json:"runnerId,omitempty"`
	Target   AgentModel `json:"target"`
	// Fallbacks are tried in order when a run fails for a reason a different
	// model could fix (see FailureKind).
	Fallbacks []AgentModel `json:"fallbacks,omitempty"`
	// Pipeline, when set, makes a dispatch through this rule a pipeline
	// instead of a single run.
	Pipeline *Pipeline `json:"pipeline,omitempty"`
}

// RoutingConfig is GET/PUT /api/runs/routing (the caller's rules, in order).
type RoutingConfig struct {
	Rules []RouteRule `json:"rules"`
}

// MaxRouteRules bounds one owner's rules; MaxFallbacks one rule's chain.
const (
	MaxRouteRules = 200
	MaxFallbacks  = 5
)

// RouteSuggestion is GET /api/runs/route?nodeId=: what the rules pick for a
// node, for the dispatch dialog to pre-fill. RuleID "" means no rule matched.
type RouteSuggestion struct {
	RuleID    string       `json:"ruleId"`
	Match     string       `json:"match"`
	RunnerID  string       `json:"runnerId,omitempty"`
	Target    AgentModel   `json:"target"`
	Fallbacks []AgentModel `json:"fallbacks,omitempty"`
	Pipeline  *Pipeline    `json:"pipeline,omitempty"`
}

// Failure kinds a runner reports in RunResult.FailureKind, so the hub can tell
// "try another model" from "the task itself failed".
const (
	FailureRateLimit   = "rate_limit"        // provider throttled; another model/provider may work
	FailureQuota       = "quota"             // usage limit or credit exhausted
	FailureModel       = "model_unavailable" // the model is unknown or not allowed for this login
	FailureAgentAbsent = "agent_unavailable" // CLI missing or not signed in
	FailureTask        = "task"              // anything else: do not fall back
)

// FallbackWorthy reports whether a failure kind should move down the chain.
func FallbackWorthy(kind string) bool {
	switch kind {
	case FailureRateLimit, FailureQuota, FailureModel, FailureAgentAbsent:
		return true
	}
	return false
}

// Pipeline step roles.
const (
	StepImplement = "implement"
	StepReview    = "review"
)

// Pipeline is implement → review (→ implement again with the review, up to
// MaxRounds). The review step reads the implementer's PR diff and leaves a PR
// comment; its verdict decides whether another implement round runs.
type Pipeline struct {
	Implement AgentModel `json:"implement"`
	Review    AgentModel `json:"review"`
	// MaxRounds bounds implement→review cycles; 1 means implement once and
	// review once. At most MaxPipelineRounds.
	MaxRounds int `json:"maxRounds"`
}

// MaxPipelineRounds caps Pipeline.MaxRounds.
const MaxPipelineRounds = 3

// Review verdicts, parsed by the runner from the review agent's summary (a
// final line "VERDICT: approve" or "VERDICT: changes").
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
)

// Job kinds. A headless implement run is the default (""), as in W1.
const (
	JobImplement = ""
	// JobReview: check out the PR head read-only, run the agent with the PR
	// diff in its prompt, post its summary as a PR comment, never commit.
	JobReview = "review"
	// JobRevise: continue on an existing PR branch (Job.Branch) with the
	// review as the prompt, push to that branch, open no new PR.
	JobRevise = "revise"
)

// PipelineState is a pipeline as the API shows it (Run.Pipeline on each of
// its runs, and GET /api/runs/pipelines/:id).
type PipelineState struct {
	ID        string   `json:"id"`
	NodeID    string   `json:"nodeId"`
	Pipeline  Pipeline `json:"pipeline"`
	Round     int      `json:"round"`
	Status    string   `json:"status"` // running | approved | changes_left | failed | cancelled
	RunIDs    []string `json:"runIds"`
	CreatedAt string   `json:"createdAt"`
}

// Pipeline statuses.
const (
	PipelineRunning     = "running"
	PipelineApproved    = "approved"
	PipelineChangesLeft = "changes_left" // rounds ran out with changes still requested
	PipelineFailed      = "failed"
	PipelineCancelled   = "cancelled"
)
