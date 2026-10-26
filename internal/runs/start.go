package runs

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// Starting a run: what POST /api/runs does once it knows who asks and for
// which project, shared with the autopilot (W7) so its tasks get the same
// checks — the caller's own runner, the runner's hello, the owner's budget,
// the step-up the dispatcher had — as a run a person starts.

// Limits on a run request.
const (
	// MaxExtraPrompt caps the dispatcher's extra instructions.
	MaxExtraPrompt = 8 << 10
	maxBaseLen     = 200
)

// StartRequest is one run to start.
type StartRequest struct {
	Body    runnerwire.CreateRunRequest
	Project string
	// ActorID is who starts it; the runner must be theirs. ActorName is
	// recorded as the run's dispatcher.
	ActorID, ActorName string
	// SteppedUpAt is the dispatcher's step-up (RFC3339Nano), or "".
	SteppedUpAt string
	// LoadNode returns the node's title and content; found false means the
	// graph has no such node. It is called once the runner checks pass.
	LoadNode func() (title, content string, found bool, err error)
}

// Start-refusal codes (StartError.Code).
const (
	StartInvalid      = "invalid"
	StartNotFound     = "not_found"
	StartPrecondition = "precondition"
	StartBudget       = "budget"
)

// StartError is why Start refused a run, worded for a person.
type StartError struct {
	Code string
	// What is the missing thing for StartNotFound: "runner" or "node".
	What string
	Msg  string
	// Budget is the owner's budget for StartBudget.
	Budget runnerwire.BudgetStatus
}

func (e *StartError) Error() string { return e.Msg }

// Is lets callers test a StartError against the store's sentinel errors.
func (e *StartError) Is(target error) bool {
	switch e.Code {
	case StartInvalid:
		return target == ErrInvalid
	case StartNotFound:
		return target == ErrNotFound
	case StartPrecondition, StartBudget:
		return target == ErrPrecondition
	}
	return false
}

func refuse(code, msg string) error { return &StartError{Code: code, Msg: msg} }

// Start validates a run request, resolves it against the runner's hello,
// records the run (and its pipeline) and queues it. Refusals are
// *StartError; anything else is a store failure.
func (d *Dispatcher) Start(ctx context.Context, req StartRequest) (Record, error) {
	body := req.Body
	if !engine.ValidNodeID(body.NodeID) {
		return Record{}, refuse(StartInvalid, "nodeId is not a valid node id")
	}
	if len(body.Prompt) > MaxExtraPrompt || !utf8.ValidString(body.Prompt) {
		return Record{}, refuse(StartInvalid, "prompt must be valid text of at most 8192 bytes")
	}
	if !ValidBase(body.Base) {
		return Record{}, refuse(StartInvalid, "base is not a valid branch name")
	}
	if !ValidMode(body.Mode) {
		return Record{}, refuse(StartInvalid, `mode must be "" or "`+runnerwire.ModeInteractive+`"`)
	}
	// A dispatch is an implement run or (W7) a plan run, which breaks the node
	// into tasks on the board; review and revise runs are a pipeline's steps.
	if body.Kind != runnerwire.JobImplement && body.Kind != runnerwire.JobPlan {
		return Record{}, refuse(StartInvalid, `kind must be "" or "`+runnerwire.JobPlan+`"`)
	}
	if body.Kind == runnerwire.JobPlan && (body.Pipeline != nil || body.Mode != runnerwire.ModeHeadless) {
		return Record{}, refuse(StartInvalid, "a plan run is one headless run, not a pipeline")
	}
	// A replan's revision (runnerwire.ReconcileStart) rides on a plan run
	// only, and is offered to the runner as the job's Review.
	if body.Revision != "" && body.Kind != runnerwire.JobPlan {
		return Record{}, refuse(StartInvalid, "revision is for plan runs only")
	}
	if len(body.Revision) > runnerwire.MaxRevisionBytes || !utf8.ValidString(body.Revision) {
		return Record{}, refuse(StartInvalid, "revision must be valid text of at most 16384 bytes")
	}
	if err := ValidateChain(body.Fallbacks, false); err != nil {
		return Record{}, refuse(StartInvalid, err.Error())
	}
	if body.Pipeline != nil {
		if err := ValidatePipeline(*body.Pipeline, false); err != nil {
			return Record{}, refuse(StartInvalid, err.Error())
		}
		// The pipeline's implement step is the first run.
		body.Agent, body.Model = body.Pipeline.Implement.Agent, body.Pipeline.Implement.Model
	}

	// The runner must be the caller's own, administrators included: a run
	// executes on the owner's machine with the owner's logins, and nobody
	// else gets to put work there.
	runner, err := d.reg.Get(ctx, body.RunnerID)
	if err != nil || runner.OwnerID != req.ActorID {
		return Record{}, &StartError{Code: StartNotFound, What: "runner", Msg: "runner not found"}
	}
	model, repo, problem := CheckAgainstHello(runner.Info, body)
	if problem != "" {
		return Record{}, refuse(StartInvalid, problem)
	}
	var pipeline *runnerwire.Pipeline
	if body.Pipeline != nil {
		p, err := ResolvePipeline(runner.Info, *body.Pipeline, model)
		if err != nil {
			return Record{}, refuse(StartInvalid, "pipeline "+err.Error())
		}
		pipeline = &p
	}
	if body.Kind == runnerwire.JobPlan && !slices.Contains(runner.Info.Capabilities, runnerwire.CapPlan) {
		return Record{}, refuse(StartPrecondition, "this runner cannot run plan jobs; update nodevas on it")
	}
	if !Headless(runner.Info) {
		return Record{}, refuse(StartPrecondition, "this runner does not take runs; update nodevas on it")
	}
	if !ModeSupported(runner.Info, body.Mode) {
		return Record{}, refuse(StartPrecondition, "this runner cannot run interactive sessions")
	}
	// The budget is the runner owner's: runs spend their subscription.
	budget, err := d.store.BudgetStatus(ctx, runner.OwnerID)
	if err != nil {
		return Record{}, err
	}
	if budget.Exceeded != "" {
		return Record{}, &StartError{Code: StartBudget, Msg: BudgetMessage(budget), Budget: budget}
	}

	title, content, found, err := req.LoadNode()
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, &StartError{Code: StartNotFound, What: "node", Msg: "node not found"}
	}
	if title == "" {
		title = body.NodeID
	}
	name := req.ActorName
	if name == "" {
		name = req.ActorID
	}
	jobPrompt := BuildPrompt(title, content, body.Prompt)
	pipelineID := ""
	if pipeline != nil {
		p, err := d.store.CreatePipeline(ctx, NewPipeline{
			OwnerID: runner.OwnerID, CreatedBy: req.ActorID, Project: req.Project, NodeID: body.NodeID,
			Pipeline: *pipeline, Fallbacks: body.Fallbacks, JobPrompt: jobPrompt, Prompt: body.Prompt,
		})
		if err != nil {
			return Record{}, err
		}
		pipelineID = p.ID
	}
	record, err := d.store.Create(ctx, New{
		Project: req.Project, NodeID: body.NodeID, NodeTitle: title,
		RunnerID: runner.ID, RunnerName: runner.Name, OwnerID: runner.OwnerID,
		Agent: body.Agent, Model: model, RepoID: repo.ID, RepoName: repo.Name, Base: body.Base,
		Prompt: body.Prompt, JobPrompt: jobPrompt,
		CreatedBy: req.ActorID, CreatedByName: name,
		PipelineID: pipelineID, Fallbacks: body.Fallbacks,
		SteppedUpAt: req.SteppedUpAt, Mode: body.Mode, Kind: body.Kind, Review: strings.TrimSpace(body.Revision),
	})
	if err != nil {
		if pipelineID != "" {
			_, _ = d.store.EndPipeline(ctx, pipelineID, runnerwire.PipelineFailed, err.Error())
		}
		return Record{}, err
	}
	return d.Submit(ctx, record), nil
}

// CheckAgainstHello resolves the request's agent, model and repo against
// what the runner last reported. It returns the model to record and the
// repo, or a reason the request does not fit.
func CheckAgainstHello(info *runnerwire.Info, body runnerwire.CreateRunRequest) (string, runnerwire.Repo, string) {
	model, err := ResolveAgentModel(info, runnerwire.AgentModel{Agent: body.Agent, Model: body.Model})
	if err != nil {
		return "", runnerwire.Repo{}, err.Error()
	}
	r := slices.IndexFunc(info.Repos, func(repo runnerwire.Repo) bool { return repo.ID == body.RepoID })
	if body.RepoID == "" || r < 0 {
		return "", runnerwire.Repo{}, "repoId is not one of the runner's repositories"
	}
	return model, info.Repos[r], ""
}

// ValidBase accepts "" (the repository's default) or a plausible branch
// name. The runner checks again with git; this keeps junk out of the record
// and anything that reads as an option off the runner's command line.
func ValidBase(base string) bool {
	if base == "" {
		return true
	}
	if len(base) > maxBaseLen || strings.HasPrefix(base, "-") || strings.Contains(base, "..") {
		return false
	}
	for _, r := range base {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._/-", r))) {
			return false
		}
	}
	return true
}

// ValidMode accepts the run modes a request may ask for.
func ValidMode(mode string) bool {
	return mode == runnerwire.ModeHeadless || mode == runnerwire.ModeInteractive
}

// ModeSupported reports whether a runner's hello lets it run a mode.
func ModeSupported(info *runnerwire.Info, mode string) bool {
	if mode != runnerwire.ModeInteractive {
		return true
	}
	return info != nil && slices.Contains(info.Capabilities, runnerwire.CapInteractive)
}

const truncatedNote = "\n\n[node content truncated]"

// BuildPrompt assembles the task a runner is offered: the node's title, its
// content, and the dispatcher's extra instructions, within MaxPromptBytes.
// The content gives way first, so the instructions typed for this run are
// never the part that is cut.
func BuildPrompt(title, content, extra string) string {
	head := "# " + strings.TrimSpace(title) + "\n\n"
	tail := ""
	if extra = strings.TrimSpace(extra); extra != "" {
		tail = "\n\nAdditional instructions:\n" + extra
	}
	content = strings.TrimSpace(content)
	room := runnerwire.MaxPromptBytes - len(head) - len(tail)
	if len(content) > room {
		content = Truncate(content, max(room-len(truncatedNote), 0)) + truncatedNote
	}
	return Truncate(head+content+tail, runnerwire.MaxPromptBytes)
}
