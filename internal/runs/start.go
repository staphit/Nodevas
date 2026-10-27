package runs

import (
	"context"
	"fmt"
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
	// LoadNode returns the node's title, content and type; found false means
	// the graph has no such node. It is called once the runner checks pass.
	LoadNode func() (NodeInfo, bool, error)
	// Agent roles: the project's resolved roles (tool allowlists for the
	// job), and the hook that locks the node's acceptance when an Executor
	// is dispatched (store.LockAcceptance). Roles nil is the built-ins.
	Roles          []engine.AgentRole
	LockAcceptance func() error
}

// NodeInfo is what a run needs to know about its node. Type is the node
// type's id and AgentMode its agent mode, which decides whether the node may
// be dispatched at all.
type NodeInfo struct {
	Title, Content, Type string
	AgentMode            engine.AgentMode
	// References are the notes the node uses as context (characters,
	// setting), already summarized; ReferencesTruncated says more exist.
	References          []Reference
	ReferencesTruncated bool
	// Agent roles: the node's acceptance criteria and its effective workflow
	// (nil: legacy).
	Acceptance *engine.Acceptance
	Workflow   *engine.Workflow
}

// Reference is one reference note as a run's prompt shows it. It mirrors the
// store's summary so this package stays free of the store.
type Reference struct {
	ID, Title, TypeLabel, Summary string
}

// Start-refusal codes (StartError.Code).
const (
	StartInvalid      = "invalid"
	StartNotFound     = "not_found"
	StartPrecondition = "precondition"
	StartBudget       = "budget"
	// StartNodeType: the node's type does not let agents work it.
	StartNodeType = "node_type"
)

// StartError is why Start refused a run, worded for a person.
type StartError struct {
	Code string
	// What is the missing thing for StartNotFound: "runner" or "node".
	What string
	Msg  string
	// Budget is the owner's budget for StartBudget.
	Budget runnerwire.BudgetStatus
	// NodeType and AgentMode name the refusing type for StartNodeType.
	NodeType  string
	AgentMode engine.AgentMode
	// Reason is the shared error code for a role or stage refusal
	// (ACCEPTANCE_REQUIRED).
	Reason string
}

func (e *StartError) Error() string { return e.Msg }

// Is lets callers test a StartError against the store's sentinel errors.
func (e *StartError) Is(target error) bool {
	switch e.Code {
	case StartInvalid:
		return target == ErrInvalid
	case StartNotFound:
		return target == ErrNotFound
	case StartPrecondition, StartBudget, StartNodeType:
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
	// A dispatch is an implement run, (W7) a plan run, which breaks the node
	// into tasks on the board, or (agent roles) a judge run, which checks
	// acceptance criteria; review, revise, test and verify runs are a
	// pipeline's steps.
	if body.Kind != runnerwire.JobImplement && body.Kind != runnerwire.JobPlan && body.Kind != runnerwire.JobJudge {
		return Record{}, refuse(StartInvalid, `kind must be "", "`+runnerwire.JobPlan+`" or "`+runnerwire.JobJudge+`"`)
	}
	if (body.Kind == runnerwire.JobPlan || body.Kind == runnerwire.JobJudge) && (body.Pipeline != nil || body.Mode != runnerwire.ModeHeadless) {
		return Record{}, refuse(StartInvalid, "a "+body.Kind+" run is one headless run, not a pipeline")
	}
	body.Role = strings.TrimSpace(body.Role)
	body.Override = strings.TrimSpace(body.Override)
	if len(body.Override) > MaxOverrideBytes || !utf8.ValidString(body.Override) || strings.ContainsAny(body.Override, "\x00\r\n") {
		return Record{}, refuse(StartInvalid, "override must be one line of at most 200 characters")
	}
	if body.Override != "" && (body.Role != "" || body.Kind != runnerwire.JobImplement || body.Pipeline != nil) {
		return Record{}, refuse(StartInvalid, "override dispatches one plain implement run: no role, kind or pipeline")
	}
	roles := req.Roles
	if len(roles) == 0 {
		roles = engine.BuiltinAgentRoles()
	}
	var role *engine.AgentRole
	if body.Role != "" {
		if role = findRole(roles, body.Role); role == nil {
			return Record{}, refuse(StartInvalid, fmt.Sprintf("role %q is not a role of this project", body.Role))
		}
	}
	if body.Kind == runnerwire.JobJudge && role == nil {
		return Record{}, refuse(StartInvalid, "a judge run needs a role (qa or reviewer)")
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

	node, found, err := req.LoadNode()
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, &StartError{Code: StartNotFound, What: "node", Msg: "node not found"}
	}
	if !AgentModeAllows(node.AgentMode, body.Kind) {
		typeName := node.Type
		if typeName == "" {
			typeName = "task"
		}
		return Record{}, &StartError{
			Code: StartNodeType, NodeType: node.Type, AgentMode: node.AgentMode,
			Msg: "a " + typeName + " node cannot be dispatched to an agent",
		}
	}

	// Agent roles and workflow stages (spec 2.6). wf is the node's effective
	// workflow; nil is legacy, today's behavior exactly.
	wf := node.Workflow.Normalized()
	if body.Override != "" && !wf.Staged() {
		return Record{}, refuse(StartInvalid, "override: this node's workflow has no stages to override")
	}
	// A staged project's Executor work runs headless (tests first, verify,
	// review); working it interactively needs a person's override reason.
	if wf.Staged() && body.Override == "" && body.Kind == runnerwire.JobImplement &&
		body.Mode == runnerwire.ModeInteractive && (role == nil || role.Slot() == engine.RoleExecutor) {
		return Record{}, &StartError{Code: StartPrecondition, Reason: CodeStagedNeedsHeadless,
			Msg: "this project's stages run headless; give an override reason to work interactively"}
	}
	story := wf != nil && wf.Judge == engine.JudgeChecklist && storyType(node.Type)
	staged := wf.Staged() && body.Override == "" && body.Kind == runnerwire.JobImplement &&
		body.Mode == runnerwire.ModeHeadless && (role == nil || role.Slot() == engine.RoleExecutor)
	if role == nil && wf != nil && body.Override == "" {
		switch body.Kind {
		case runnerwire.JobImplement:
			role = findRole(roles, engine.RoleExecutor)
		case runnerwire.JobPlan:
			role = findRole(roles, engine.RolePlanner)
		}
	}
	slot := ""
	if role != nil {
		slot = role.Slot()
	}
	worker := slot == engine.RoleExecutor || slot == engine.RoleTester
	if body.Override == "" && engine.RequiresAcceptance(wf) && node.Acceptance.Empty() && (staged || worker) {
		return Record{}, &StartError{Code: StartPrecondition, Reason: CodeAcceptanceRequired,
			Msg: "write the acceptance criteria first: this project's stages need them before a Tester or Executor runs"}
	}
	caps := runner.Info.Capabilities
	if staged {
		missing := !slices.Contains(caps, runnerwire.CapRoles) ||
			(story && !slices.Contains(caps, runnerwire.CapJudge)) ||
			(!story && wf.Stages.Tester && !slices.Contains(caps, runnerwire.CapVerify))
		if missing {
			return Record{}, refuse(StartPrecondition, "this runner cannot run staged pipelines (tests first, verify, review); update nodevas on it")
		}
		if !story && wf.Stages.Tester && !repo.Verify {
			return Record{}, refuse(StartPrecondition, fmt.Sprintf(
				"configure a test command for %s on the runner, so it can verify red and green: nodevas runner repo set %s --test-command \"go test ./...\"",
				repo.Name, repo.Name))
		}
	}
	if body.Kind == runnerwire.JobJudge && !slices.Contains(caps, runnerwire.CapJudge) {
		return Record{}, refuse(StartPrecondition, "this runner cannot run judge jobs; update nodevas on it")
	}

	title, content := node.Title, node.Content
	if title == "" {
		title = body.NodeID
	}
	name := req.ActorName
	if name == "" {
		name = req.ActorID
	}
	extra := body.Prompt
	if role != nil && !role.Builtin && strings.TrimSpace(role.Instructions) != "" {
		extra = strings.TrimSpace(extra + "\n\nProject policy for this role (" + role.Label + "):\n" + role.Instructions)
	}
	jobPrompt := BuildPrompt(title, content, ReferencesBlock(node.References, node.ReferencesTruncated), extra)
	acceptance := acceptanceItems(node.Acceptance)
	tools := roleTools(roles, node.Type)

	// The first run's role: a staged pipeline starts with its Tester (or the
	// writer, or a judge, for a story).
	first := stagedFirst{kind: body.Kind, role: "", am: runnerwire.AgentModel{Agent: body.Agent, Model: model}}
	if role != nil {
		first.role = role.ID
	}
	if staged {
		first = firstStagedStep(wf, story, content, pipeline, body.Agent, model)
	}
	if first.role != "" {
		status, err := d.store.RoleBudgetStatus(ctx, runner.OwnerID, first.role)
		if err != nil {
			return Record{}, err
		}
		if status.Exceeded != "" {
			return Record{}, &StartError{Code: StartBudget, Msg: RoleBudgetMessage(first.role, status), Budget: status}
		}
	}
	lockCriteria := req.LockAcceptance != nil && body.Override == "" && body.Kind == runnerwire.JobImplement && (staged || worker)

	pipelineID := ""
	var config *StagedConfig
	if wf != nil && body.Override == "" && (staged || pipeline != nil) {
		config = &StagedConfig{Workflow: wf, Staged: staged, Story: story, Tools: tools, Acceptance: acceptance}
	}
	if staged && pipeline == nil {
		synthesized := runnerwire.Pipeline{Implement: runnerwire.AgentModel{Agent: body.Agent, Model: model}, MaxRounds: 2}
		pipeline = &synthesized
	}
	if staged {
		if pipeline.Tester.Agent == "" {
			pipeline.Tester = pipeline.Implement
		}
		if wf.Stages.Reviewer && len(runnerwire.ReviewersOf(*pipeline)) == 0 {
			pipeline.Reviewers = []runnerwire.AgentModel{pipeline.Implement}
		}
		first = firstStagedStep(wf, story, content, pipeline, body.Agent, model)
	}
	if pipeline != nil {
		p, err := d.store.CreatePipeline(ctx, NewPipeline{
			OwnerID: runner.OwnerID, CreatedBy: req.ActorID, Project: req.Project, NodeID: body.NodeID,
			Pipeline: *pipeline, Fallbacks: body.Fallbacks, JobPrompt: jobPrompt, Prompt: body.Prompt,
			Config: config, Phase: first.phase,
		})
		if err != nil {
			return Record{}, err
		}
		pipelineID = p.ID
	}
	jobRoles := JobRoles{}
	if first.role != "" {
		jobRoles = JobRoles{Tools: tools[first.role], Acceptance: acceptance}
	}
	if body.Kind == runnerwire.JobJudge && slot == engine.RoleQA && (wf == nil || wf.Judge != engine.JudgeChecklist) && repo.Verify {
		// Goal verification runs the repo's tests first (spec 3.3).
		jobRoles.VerifySpec = &runnerwire.VerifySpec{Phase: runnerwire.VerifyGoal}
	}
	review := strings.TrimSpace(body.Revision)
	createRun := d.createRun
	if createRun == nil {
		createRun = d.store.Create
	}
	record, err := createRun(ctx, New{
		Project: req.Project, NodeID: body.NodeID, NodeTitle: title,
		RunnerID: runner.ID, RunnerName: runner.Name, OwnerID: runner.OwnerID,
		Agent: first.am.Agent, Model: first.am.Model, RepoID: repo.ID, RepoName: repo.Name, Base: body.Base,
		Prompt: body.Prompt, JobPrompt: jobPrompt,
		CreatedBy: req.ActorID, CreatedByName: name,
		PipelineID: pipelineID, Fallbacks: body.Fallbacks,
		SteppedUpAt: req.SteppedUpAt, Mode: body.Mode, Kind: first.kind, Review: review,
		Role: first.role, Override: body.Override, JobRoles: jobRoles,
		NodeType: node.Type, AcceptanceCount: len(acceptance),
	})
	if err != nil {
		if pipelineID != "" {
			_, _ = d.store.EndPipeline(ctx, pipelineID, runnerwire.PipelineFailed, err.Error())
		}
		return Record{}, err
	}
	// The criteria an Executor works against do not change under it. They
	// are locked only once the run exists; a failed lock cancels it.
	if lockCriteria {
		if err := req.LockAcceptance(); err != nil {
			_, _ = d.store.Transition(ctx, record.ID, runnerwire.RunCancelled, Change{Error: "criteria lock failed: " + err.Error()})
			if pipelineID != "" {
				_, _ = d.store.EndPipeline(ctx, pipelineID, runnerwire.PipelineFailed, "criteria lock failed")
			}
			return Record{}, fmt.Errorf("lock the acceptance criteria: %w", err)
		}
	}
	if body.RoutePick != "" {
		d.store.LinkRoutePick(ctx, body.RoutePick, req.ActorID, body.NodeID, record.ID)
	}
	if body.Override != "" {
		if err := d.store.NoteFirst(ctx, record.ID, "stage override by "+name+": "+body.Override); err != nil {
			return Record{}, err
		}
		d.record(AuditEntry{Project: req.Project, ActorID: req.ActorID, ActorName: name, Action: "run.override",
			Target: record.ID, Detail: map[string]any{"node": body.NodeID, "reason": body.Override}})
	}
	return d.Submit(ctx, record), nil
}

// CodeAcceptanceRequired is StartError.Reason when the node needs acceptance
// criteria first (shared with the web client and store.CodeAcceptanceRequired).
const CodeAcceptanceRequired = "ACCEPTANCE_REQUIRED"

// CodeStagedNeedsHeadless is StartError.Reason when a staged node's Executor
// work is asked for interactively without an override reason.
const CodeStagedNeedsHeadless = "STAGED_NEEDS_HEADLESS"

// storyType reports whether a node type is a story's (its body is the work).
func storyType(nodeType string) bool { return nodeType == "scene" || nodeType == "event" }

// findRole is the role with id among roles, or nil.
func findRole(roles []engine.AgentRole, id string) *engine.AgentRole {
	for i := range roles {
		if roles[i].ID == id {
			role := roles[i]
			return &role
		}
	}
	return nil
}

// roleTools is each role's tools for a job on a node of nodeType: the
// role's allowlist, plus update_node_body for an Executor whose node is a
// scene or event (the prose is its work).
func roleTools(roles []engine.AgentRole, nodeType string) map[string][]string {
	out := make(map[string][]string, len(roles))
	for _, role := range roles {
		tools := slices.Clone(role.Tools)
		if role.Slot() == engine.RoleExecutor && storyType(nodeType) && !slices.Contains(tools, "update_node_body") {
			tools = append(tools, "update_node_body")
		}
		out[role.ID] = tools
	}
	return out
}

// acceptanceItems is a node's criteria as job data.
func acceptanceItems(acc *engine.Acceptance) []runnerwire.AcceptanceItem {
	if acc.Empty() {
		return nil
	}
	out := make([]runnerwire.AcceptanceItem, 0, len(acc.Items))
	for _, item := range acc.Items {
		out = append(out, runnerwire.AcceptanceItem{ID: item.ID, Text: item.Text})
	}
	return out
}

// stagedFirst is the first run a dispatch records.
type stagedFirst struct {
	kind, role, phase string
	am                runnerwire.AgentModel
}

// firstStagedStep is a staged pipeline's first run: the Tester, or for a
// story the writer (an empty scene is red by definition) or a judge.
func firstStagedStep(wf *engine.Workflow, story bool, content string, p *runnerwire.Pipeline, agent, model string) stagedFirst {
	implement := runnerwire.AgentModel{Agent: agent, Model: model}
	if p != nil {
		implement = p.Implement
	}
	switch {
	case story && strings.TrimSpace(content) == "":
		return stagedFirst{kind: runnerwire.JobImplement, role: engine.RoleExecutor, phase: runnerwire.PhaseImplement, am: implement}
	case story:
		return stagedFirst{kind: runnerwire.JobJudge, role: engine.RoleReviewer, phase: runnerwire.PhaseJudge, am: judgeOf(p)}
	case wf.Stages.Tester:
		tester := implement
		if p != nil && p.Tester.Agent != "" {
			tester = p.Tester
		}
		return stagedFirst{kind: runnerwire.JobTest, role: engine.RoleTester, phase: runnerwire.PhaseTest, am: tester}
	}
	return stagedFirst{kind: runnerwire.JobImplement, role: engine.RoleExecutor, phase: runnerwire.PhaseImplement, am: implement}
}

// judgeOf is who judges a story's checklist: the first reviewer, else the
// implementer.
func judgeOf(p *runnerwire.Pipeline) runnerwire.AgentModel {
	if p == nil {
		return runnerwire.AgentModel{}
	}
	if reviewers := runnerwire.ReviewersOf(*p); len(reviewers) > 0 {
		return reviewers[0]
	}
	return p.Implement
}

// AgentModeAllows is the node-type gate on a run: an implement run needs a
// type agents are dispatched to; a plan run (breaking the node down) also
// accepts an Autopilot type such as Goal. An empty mode allows nothing, so a
// caller that forgets to resolve the type fails closed.
func AgentModeAllows(mode engine.AgentMode, kind string) bool {
	if kind == runnerwire.JobPlan || kind == runnerwire.JobJudge {
		return mode == engine.AgentDispatch || mode == engine.AgentAutopilot
	}
	return mode == engine.AgentDispatch
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
// content, its reference notes (refs, from ReferencesBlock) and the
// dispatcher's extra instructions, within MaxPromptBytes. The content gives
// way first, so the instructions typed for this run, and the notes the
// writing must agree with, are never the part that is cut.
func BuildPrompt(title, content, refs, extra string) string {
	head := "# " + strings.TrimSpace(title) + "\n\n"
	tail := ""
	if extra = strings.TrimSpace(extra); extra != "" {
		tail = "\n\nAdditional instructions:\n" + extra
	}
	content = strings.TrimSpace(content)
	room := runnerwire.MaxPromptBytes - len(head) - len(refs) - len(tail)
	if len(content) > room {
		content = Truncate(content, max(room-len(truncatedNote), 0)) + truncatedNote
	}
	return Truncate(head+content+refs+tail, runnerwire.MaxPromptBytes)
}

// ReferencesBlock renders a node's reference notes for its run prompt, or ""
// when it has none. The store caps the notes (8, 4000 runes of summary), so
// the block stays near 12 KB at most.
func ReferencesBlock(refs []Reference, truncated bool) string {
	if len(refs) == 0 && !truncated {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Reference notes\n")
	b.WriteString("Read these before writing; they are this project's source of truth (characters, setting). get_node <id> returns the full text.\n")
	for _, ref := range refs {
		label := ref.TypeLabel
		if label == "" {
			label = "Note"
		}
		b.WriteString("\n### " + oneLine(ref.Title) + " (" + ref.ID + ", " + label + ")\n")
		if summary := strings.TrimSpace(ref.Summary); summary != "" {
			b.WriteString(summary + "\n")
		}
	}
	if truncated {
		b.WriteString("\n(More references exist; get_node <node id> lists them.)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine keeps a title on its heading line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
