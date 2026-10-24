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

// Pipeline is implement → review (→ revise with the review, up to MaxRounds).
// Each review step reads the implementer's PR diff and leaves a PR comment;
// the verdicts decide whether another round runs.
//
// W7: a round can have several reviewers, who review the same PR head in
// parallel; Quorum of them must approve (0: all). With Human, a person then
// approves or asks for changes (POST /api/runs/pipelines/:id/review). Review
// alone, as in W5, is one reviewer. No reviewers and no Human (Review left
// zero) is an implement-only pipeline: approved once its PR is open.
type Pipeline struct {
	Implement AgentModel `json:"implement"`
	Review    AgentModel `json:"review"`
	// MaxRounds bounds implement→review cycles; 1 means implement once and
	// review once. At most MaxPipelineRounds.
	MaxRounds int `json:"maxRounds"`
	// Reviewers replaces Review when set (at most MaxReviewers).
	Reviewers []AgentModel `json:"reviewers,omitempty"`
	// Quorum is how many reviewers must approve; 0 means all of them.
	Quorum int `json:"quorum,omitempty"`
	// Human has a person approve after the reviewers' quorum (or alone).
	Human bool `json:"human,omitempty"`
	// HumanAnyEditor lets anyone who may change the pipeline's project give
	// that person's review. Without it only the pipeline's creator, the
	// runner's owner or an administrator may: an approval can have the
	// owner's runner merge, the way a merge approval does (W4).
	HumanAnyEditor bool `json:"humanAnyEditor,omitempty"`
}

// ReviewersOf is a pipeline's reviewers: Reviewers, else Review when set.
func ReviewersOf(p Pipeline) []AgentModel {
	if len(p.Reviewers) > 0 {
		return p.Reviewers
	}
	if p.Review.Agent != "" {
		return []AgentModel{p.Review}
	}
	return nil
}

// QuorumOf is how many approvals a round needs from ReviewersOf(p).
func QuorumOf(p Pipeline) int {
	n := len(ReviewersOf(p))
	if p.Quorum <= 0 || p.Quorum > n {
		return n
	}
	return p.Quorum
}

// MaxPipelineRounds caps Pipeline.MaxRounds.
const MaxPipelineRounds = 3

// Review verdicts, parsed by the runner from the review agent's summary (a
// final line "VERDICT: approve" or "VERDICT: changes").
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
	// VerdictAbstain is a ReviewVote whose review run failed or gave no
	// verdict: it counts toward neither side.
	VerdictAbstain = "abstain"
)

// HumanReviewer is ReviewVote.Agent for a person's vote; Model is then the
// person's name.
const HumanReviewer = "human"

// CapPlan is the runner capability for plan jobs (JobPlan).
const CapPlan = "plan"

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
	ID       string   `json:"id"`
	NodeID   string   `json:"nodeId"`
	Pipeline Pipeline `json:"pipeline"`
	Round    int      `json:"round"`
	// Status: running | waiting_human | approved | changes_left | failed |
	// cancelled.
	Status string   `json:"status"`
	RunIDs []string `json:"runIds"`
	// Votes are every round's review votes, oldest round first; a vote with
	// Verdict "" is a review still running (W7).
	Votes []ReviewVote `json:"votes,omitempty"`
	// HumanVerdict is a person's verdict in the current round, if any.
	HumanVerdict string `json:"humanVerdict,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// Pipeline statuses.
const (
	PipelineRunning = "running"
	// PipelineWaitingHuman: the reviewers' quorum approved (or there are
	// none); a person decides next.
	PipelineWaitingHuman = "waiting_human"
	PipelineApproved     = "approved"
	PipelineChangesLeft  = "changes_left" // rounds ran out with changes still requested
	PipelineFailed       = "failed"
	PipelineCancelled    = "cancelled"
)
