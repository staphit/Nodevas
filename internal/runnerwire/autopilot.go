package runnerwire

// Autopilot: a goal node's tasks dispatched as a DAG. The board already
// is the graph (a task's requires edges decide when it is ready); an autopilot
// run walks it, dispatching every ready task through a pipeline (implement →
// review → revise → merge) and recording each task's stages the way an
// Airflow DAG run records its task instances. See
// docs/workstation.md, "Autopilot".

// Review modes for ReviewPolicy.Mode.
const (
	ReviewNone        = "none"         // implement, open the PR, stop
	ReviewAgents      = "agents"       // Reviewers vote; Quorum approvals pass
	ReviewHuman       = "human"        // a person approves or asks for changes
	ReviewAgentsHuman = "agents+human" // the agents' quorum first, then a person
)

// MaxReviewers bounds ReviewPolicy.Reviewers.
const MaxReviewers = 5

// ReviewPolicy is how a task's pull request gets approved.
type ReviewPolicy struct {
	Mode string `json:"mode"`
	// Reviewers each review the PR in parallel (one review run each).
	Reviewers []AgentModel `json:"reviewers,omitempty"`
	// Quorum is how many of Reviewers must approve; 0 means all of them.
	Quorum int `json:"quorum,omitempty"`
	// AnyEditor lets anyone who may change the project give the person's
	// review (human, agents+human). Without it only the autopilot's creator,
	// the runner's owner or an administrator may, as for a merge approval.
	AnyEditor bool `json:"anyEditor,omitempty"`
}

// Advisors for AutopilotPolicy.Advisor.
const (
	AdvisorRules = ""    // Implement chain, else the owner's routing rules
	AdvisorLLM   = "llm" // a fast model on the owner's runner picks per task
	// AdvisorSystemOne: a System One model (Jev, or Laya run locally) on the
	// owner's runner picks per task, from typed choices (decide.go).
	AdvisorSystemOne = "systemone"
)

// MaxAutopilotParallel bounds AutopilotPolicy.MaxParallel.
const MaxAutopilotParallel = 8

// AutopilotPolicy is how an autopilot run works its goal's tasks.
type AutopilotPolicy struct {
	// Implement is the implement step's chain (first, then fallbacks);
	// empty leaves the choice to Advisor.
	Implement []AgentModel `json:"implement,omitempty"`
	Review    ReviewPolicy `json:"review"`
	// MaxParallel is how many tasks run at once (1..MaxAutopilotParallel).
	MaxParallel int `json:"maxParallel"`
	// MaxRounds bounds review→revise rounds per task (1..MaxPipelineRounds).
	MaxRounds int `json:"maxRounds"`
	// AutoMerge merges an approved PR once its CI is green (W4: the runner
	// merges with its owner's gh login); off, the task waits at "approved".
	AutoMerge bool `json:"autoMerge"`
	// AllowAgentOnlyMerge lets AutoMerge merge a PR no person reviewed
	// (review mode none or agents). Off, AutoMerge needs mode human or
	// agents+human: agents read attacker-controllable content (the task,
	// the diff), so their approval alone is not trusted to merge.
	AllowAgentOnlyMerge bool   `json:"allowAgentOnlyMerge,omitempty"`
	Advisor             string `json:"advisor,omitempty"`
	// RunnerID and RepoID pin where tasks run; "" lets the hub pick among the
	// owner's online runners that have the goal's repo.
	RunnerID string `json:"runnerId,omitempty"`
	RepoID   string `json:"repoId,omitempty"`
}

// Autopilot run statuses.
const (
	AutopilotRunning   = "running"
	AutopilotPaused    = "paused"
	AutopilotSucceeded = "succeeded"
	AutopilotFailed    = "failed"
	AutopilotCancelled = "cancelled"
)

// AutopilotRun is one autopilot run of a goal (GET /api/autopilot/:id).
type AutopilotRun struct {
	ID            string          `json:"id"`
	Project       string          `json:"project"`
	GoalNodeID    string          `json:"goalNodeId"`
	Status        string          `json:"status"`
	Policy        AutopilotPolicy `json:"policy"`
	Reason        string          `json:"reason,omitempty"`
	Counts        TaskCounts      `json:"counts"`
	CreatedBy     string          `json:"createdBy"`
	CreatedByName string          `json:"createdByName"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
	FinishedAt    string          `json:"finishedAt,omitempty"`
}

// TaskCounts tallies an autopilot run's tasks by state.
type TaskCounts struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Running int `json:"running"`
	Waiting int `json:"waiting"` // waiting_review + waiting_human
	Failed  int `json:"failed"`  // failed + upstream_failed
	Pending int `json:"pending"` // none + scheduled + queued
	Skipped int `json:"skipped"`
}

// Task states, as Airflow names a task instance's.
const (
	TaskNone           = "none"           // not ready: an upstream task is unfinished
	TaskScheduled      = "scheduled"      // ready, waiting for a parallel slot
	TaskQueued         = "queued"         // its run is offered or queued on a runner
	TaskRunning        = "running"        // implementing or revising
	TaskWaitingReview  = "waiting_review" // reviewers are reviewing
	TaskWaitingHuman   = "waiting_human"  // a person must approve or ask for changes
	TaskSuccess        = "success"        // merged (or approved, without AutoMerge)
	TaskFailed         = "failed"
	TaskUpstreamFailed = "upstream_failed"
	TaskSkipped        = "skipped"
)

// Task stages, in order. A task instance records each one it reaches.
const (
	StageQueued    = "queued"
	StageImplement = "implement"
	StageCommit    = "commit"
	StagePush      = "push"
	StagePR        = "pr"
	StageCI        = "ci"
	StageReview    = "review"
	StageHuman     = "human_review"
	StageRevise    = "revise"
	StageApproved  = "approved"
	StageMerge     = "merge"
	StageDone      = "done"
)

// Stage statuses.
const (
	StagePending = "pending"
	StageRunning = "running"
	StageOK      = "success"
	StageFailed  = "failed"
	StageSkipped = "skipped"
	StageWaiting = "waiting"
)

// StageRecord is one stage a task instance reached.
type StageRecord struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
	At     string `json:"at"`
	RunID  string `json:"runId,omitempty"`
	// Round is the review round (1-based) for review, revise and human_review.
	Round  int    `json:"round,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ReviewVote is one reviewer's verdict on a task's PR in a round.
type ReviewVote struct {
	Agent   string `json:"agent"`
	Model   string `json:"model"`
	Verdict string `json:"verdict"` // VerdictApprove | VerdictChanges | "" (pending)
	RunID   string `json:"runId,omitempty"`
	Round   int    `json:"round"`
}

// TaskInstance is one task of an autopilot run.
type TaskInstance struct {
	NodeID     string       `json:"nodeId"`
	Title      string       `json:"title"`
	State      string       `json:"state"`
	Stage      string       `json:"stage"`
	Try        int          `json:"try"`
	Upstream   []string     `json:"upstream,omitempty"`
	RunID      string       `json:"runId,omitempty"`
	PipelineID string       `json:"pipelineId,omitempty"`
	PRURL      string       `json:"prUrl,omitempty"`
	CIStatus   string       `json:"ciStatus,omitempty"`
	Votes      []ReviewVote `json:"votes,omitempty"`
	// HumanVerdict is a person's decision in the current round, if any.
	HumanVerdict string        `json:"humanVerdict,omitempty"`
	Stages       []StageRecord `json:"stages"`
	Reason       string        `json:"reason,omitempty"`
	// Advisor says why this try's implementer was picked (the advisor's
	// reason, e.g. a System One decision), when one was given.
	Advisor string `json:"advisor,omitempty"`
	// ContentHash is the task node's content when the task joined the run;
	// a pending task whose node changed since is held (Hold) until a person
	// retries it, so an edit after start cannot slip new work in unseen.
	ContentHash string `json:"contentHash,omitempty"`
	// Hold says why the task waits for a person (State waiting_human)
	// outside its pipeline's own review: HoldChanged, HoldRemoved or
	// HoldSensitive. A person clears it (retry, or approve for sensitive).
	Hold      string `json:"hold,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// TaskInstance.Hold values.
const (
	HoldChanged   = "changed"   // the node's content changed after start
	HoldRemoved   = "removed"   // the node left the goal after start
	HoldSensitive = "sensitive" // the PR touches CI, manifests or scripts
)

// MaxAutopilotTasks bounds the tasks one autopilot run works.
const MaxAutopilotTasks = 25

// AutopilotView is GET /api/autopilot/:id: the run and its task instances in
// topological order (upstream before downstream, then by title).
type AutopilotView struct {
	Run   AutopilotRun   `json:"run"`
	Tasks []TaskInstance `json:"tasks"`
}

// AutopilotListResponse is GET /api/autopilot without ?goal=: the project's
// autopilots in the statuses asked for (running and paused by default),
// oldest first.
type AutopilotListResponse struct {
	Autopilots []AutopilotView `json:"autopilots"`
}

// CreateAutopilotRequest is POST /api/autopilot (project from ?project=).
type CreateAutopilotRequest struct {
	GoalNodeID string          `json:"goalNodeId"`
	Policy     AutopilotPolicy `json:"policy"`
}

// HumanReviewRequest is POST /api/runs/pipelines/:id/review.
type HumanReviewRequest struct {
	Verdict string `json:"verdict"` // VerdictApprove | VerdictChanges
	Comment string `json:"comment,omitempty"`
}

// JobPlan: a read-only run that breaks a goal node into task nodes with
// requires edges (create_node, link_nodes), changing no files.
const JobPlan = "plan"
