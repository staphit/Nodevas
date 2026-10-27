// Package runs serves /api/runs: dispatching a board node to an agent on one
// of the caller's runners, following the run, and cancelling it. The hub only
// records and relays; the runner executes, after someone there accepts.
package runs

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

// Limits on POST /api/runs.
const (
	// MaxExtraPrompt caps the dispatcher's extra instructions.
	MaxExtraPrompt = runs.MaxExtraPrompt
	// Room for the extra prompt and a replan's revision (MaxRevisionBytes).
	maxCreateBody = 48 << 10
	listLimit     = 50
)

// API exposes runs over HTTP.
type API struct {
	pm  *project.ProjectManager
	reg *runners.Registry
	d   *runs.Dispatcher
	// steppedUpAt reports the dispatcher's live step-up (W6); nil on a server
	// without accounts, where the dispatcher vouches for everyone anyway.
	steppedUpAt func(*http.Request) (time.Time, bool)
}

// New builds the handler set.
func New(pm *project.ProjectManager, reg *runners.Registry, d *runs.Dispatcher) *API {
	return &API{pm: pm, reg: reg, d: d}
}

// UseStepUp tells run creation how to read the dispatcher's step-up, which
// each run records so its offers can vouch for the dispatcher while it lasts.
func (a *API) UseStepUp(steppedUpAt func(*http.Request) (time.Time, bool)) {
	a.steppedUpAt = steppedUpAt
}

// Register wires /api/runs. Every route takes a session as usual; none is
// reachable through the runner proxy.
func (a *API) Register(api *gin.RouterGroup) {
	api.POST("/runs", a.create)
	api.GET("/runs", a.list)
	api.GET("/runs/:id", a.get)
	api.GET("/runs/:id/events", a.events)
	api.GET("/runs/:id/changes", a.changes)
	api.POST("/runs/:id/cancel", a.cancel)
	api.POST("/runs/:id/retry-now", a.retryNow)
	api.POST("/runs/:id/merge", a.merge)
	api.GET("/runs/:id/terminal", a.terminal)
	api.GET("/runs/budget", a.budget)
	api.PUT("/runs/budget", a.setBudget)
	api.GET("/runs/roles", a.roleSettings)
	api.PUT("/runs/roles", a.setRoleSettings)
	api.GET("/runs/routing", a.routing)
	api.PUT("/runs/routing", a.setRouting)
	api.GET("/runs/route", a.route)
	api.GET("/runs/pipelines/:id", a.pipeline)
	api.POST("/runs/pipelines/:id/review", a.review)
}

func notFound(c *gin.Context, what string) {
	httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New(what+" not found"), nil)
}

func invalid(c *gin.Context, format string, args ...any) {
	httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Errorf(format, args...), nil)
}

func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, runs.ErrNotFound):
		notFound(c, "run")
	case errors.Is(err, runs.ErrInvalid):
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
	case errors.Is(err, runs.ErrPrecondition), errors.Is(err, runs.ErrTransition):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

// ProjectName is the name ?project= takes for a store: its path below the
// workspace, slash-separated, or "." for the workspace root. Runs record the
// name, not the path, because the name is what the runner's agent sends back.
func ProjectName(pm *project.ProjectManager, st *store.Store) string {
	if pm == nil || st == nil {
		return ""
	}
	rel, err := filepath.Rel(pm.Workspace(), st.Root())
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// requestProject is the store and project name this request targets.
func (a *API) requestProject(c *gin.Context) (*store.Store, string, bool) {
	st := httpx.StoreFor(c.Request, a.pm)
	name := ProjectName(a.pm, st)
	if st == nil || name == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
		return nil, "", false
	}
	return st, name, true
}

// canSee is who may see and cancel a run: whoever dispatched it, the owner of
// the runner it went to, and administrators.
func canSee(actor identity.Actor, r runs.Record) bool {
	return actor.IsAdmin() || r.CreatedBy == actor.ID || r.OwnerID == actor.ID
}

func (a *API) create(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	// A run executes on someone's machine. An agent dispatching agents is a
	// loop nobody approved, so only a person may.
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot dispatch runs"), nil)
		return
	}
	var body runnerwire.CreateRunRequest
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	st, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	record, err := a.d.Start(c.Request.Context(), runs.StartRequest{
		Body: body, Project: projectName, ActorID: actor.ID, ActorName: actor.Name,
		SteppedUpAt: steppedUpAt,
		// The project's roles (tool allowlists, custom roles) and the
		// hook that locks the node's criteria when an Executor starts.
		Roles:          graph.ResolvedAgentRoles(),
		LockAcceptance: func() error { return st.LockAcceptance(body.NodeID) },
		LoadNode: func() (runs.NodeInfo, bool, error) {
			return LoadNodeInfo(st, body.NodeID)
		},
	})
	if err != nil {
		startFailed(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, record.Run)
}

// startFailed answers a refused or failed run start.
func startFailed(c *gin.Context, err error) {
	var refused *runs.StartError
	if !errors.As(err, &refused) {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	switch refused.Code {
	case runs.StartNotFound:
		notFound(c, refused.What)
	case runs.StartPrecondition:
		code := "FAILED_PRECONDITION"
		if refused.Reason != "" {
			code = refused.Reason
		}
		httpx.Coded(c, http.StatusConflict, code, errors.New(refused.Msg), nil)
	case runs.StartBudget:
		httpx.Coded(c, http.StatusTooManyRequests, CodeBudgetExceeded,
			errors.New(refused.Msg), map[string]any{"exceeded": refused.Budget.Exceeded})
	case runs.StartNodeType:
		httpx.Coded(c, http.StatusConflict, store.CodeNodeTypeForbidsAgent, errors.New(refused.Msg),
			map[string]any{"nodeType": refused.NodeType, "agentMode": refused.AgentMode})
	default:
		invalid(c, "%s", refused.Msg)
	}
}

// LoadNodeInfo is what a run needs to know about a node: its title, body,
// type, references and, for agent roles, its acceptance criteria and its
// effective workflow (its Goal's override, else the project's; nil legacy).
// found is false when the graph has no such node.
func LoadNodeInfo(st *store.Store, id string) (runs.NodeInfo, bool, error) {
	node, nodeType, content, err := loadNode(st, id)
	if err != nil || node == nil {
		return runs.NodeInfo{}, false, err
	}
	refs, truncated, err := loadReferences(st, id)
	if err != nil {
		return runs.NodeInfo{}, false, err
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		return runs.NodeInfo{}, false, err
	}
	goal := graph.GoalOf(node)
	if nodeType.AgentMode == engine.AgentAutopilot {
		goal = node.ID
	}
	return runs.NodeInfo{
		Title: node.Title, Content: content,
		Type: nodeType.ID, AgentMode: nodeType.AgentMode,
		References: refs, ReferencesTruncated: truncated,
		Acceptance: node.Acceptance, Workflow: engine.EffectiveWorkflow(graph, goal),
	}, true, nil
}

// loadNode returns the node, its resolved type and its body text, or a nil
// node when the graph has no such node.
func loadNode(st *store.Store, id string) (*engine.Node, engine.NodeType, string, error) {
	graph, _, err := st.LoadGraph()
	if err != nil {
		return nil, engine.NodeType{}, "", err
	}
	node := graph.NodeByID(id)
	if node == nil {
		return nil, engine.NodeType{}, "", nil
	}
	nodeType := graph.TypeOf(node)
	raw, _, err := st.LoadNodeContent(id)
	if errors.Is(err, fs.ErrNotExist) {
		return node, nodeType, "", nil
	}
	if err != nil {
		return nil, engine.NodeType{}, "", err
	}
	file, err := engine.ParseNodeFile([]byte(raw))
	if err != nil {
		// Unparseable frontmatter: the text is still the task.
		return node, nodeType, raw, nil
	}
	return node, nodeType, file.Body, nil
}

// loadReferences summarizes the notes a node uses as context, for its prompt.
func loadReferences(st *store.Store, id string) ([]runs.Reference, bool, error) {
	graph, _, err := st.LoadGraph()
	if err != nil {
		return nil, false, err
	}
	summaries, truncated, err := st.ReferenceSummaries(graph, id)
	if err != nil {
		return nil, false, err
	}
	return RunReferences(summaries), truncated, nil
}

// RunReferences maps the store's reference summaries onto a run's.
func RunReferences(summaries []store.ReferenceSummary) []runs.Reference {
	if len(summaries) == 0 {
		return nil
	}
	out := make([]runs.Reference, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, runs.Reference{
			ID: summary.ID, Title: summary.Title, TypeLabel: summary.TypeLabel, Summary: summary.Summary,
		})
	}
	return out
}

// BuildPrompt assembles the task a runner is offered (runs.BuildPrompt); refs
// is a runs.ReferencesBlock.
func BuildPrompt(title, content, refs, extra string) string {
	return runs.BuildPrompt(title, content, refs, extra)
}

func (a *API) list(c *gin.Context) {
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	filter := runs.Filter{Project: projectName, NodeID: strings.TrimSpace(c.Query("nodeId")), Limit: listLimit}
	// ?active=1: every run that has not ended, uncapped (Filter.Active).
	switch strings.TrimSpace(c.Query("active")) {
	case "", "0", "false":
	case "1", "true":
		filter.Active = true
	default:
		invalid(c, "active must be 1 or 0")
		return
	}
	if !actor.IsAdmin() {
		filter.Viewer = actor.ID
	}
	records, err := a.d.Store().List(c.Request.Context(), filter)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.RunListResponse{Runs: make([]runnerwire.Run, 0, len(records))}
	for _, r := range records {
		out.Runs = append(out.Runs, r.Run)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// visible loads the run named in the path, if the caller may see it in the
// project the request targets. Anything else is a 404: probing IDs teaches
// nothing.
func (a *API) visible(c *gin.Context) (runs.Record, bool) {
	return a.visibleTo(c, false)
}

// reviewable is visible, widened for reading a run's work to whoever may
// review its pipeline: with humanAnyEditor, any editor of the project, who
// cannot give a review without seeing what it is of.
func (a *API) reviewable(c *gin.Context) (runs.Record, bool) {
	return a.visibleTo(c, true)
}

func (a *API) visibleTo(c *gin.Context, reviewers bool) (runs.Record, bool) {
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return runs.Record{}, false
	}
	r, err := a.d.Store().Get(c.Request.Context(), c.Param("id"))
	if err == nil && r.Project == projectName && !canSee(auth.ActorFrom(c.Request), r) &&
		reviewers && a.anyEditorReviews(c, r.PipelineID) {
		return r, true
	}
	if errors.Is(err, runs.ErrNotFound) || (err == nil && (r.Project != projectName || !canSee(auth.ActorFrom(c.Request), r))) {
		notFound(c, "run")
		return runs.Record{}, false
	}
	if err != nil {
		fail(c, err)
		return runs.Record{}, false
	}
	return r, true
}

func (a *API) get(c *gin.Context) {
	r, ok := a.visible(c)
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, r.Run)
}

func (a *API) events(c *gin.Context) {
	r, ok := a.visible(c)
	if !ok {
		return
	}
	after, err := queryInt(c, "after", 0)
	if err != nil || after < 0 {
		invalid(c, "after must be a non-negative integer")
		return
	}
	limit, err := queryInt(c, "limit", runs.DefaultEventPage)
	if err != nil || limit <= 0 {
		invalid(c, "limit must be a positive integer")
		return
	}
	events, more, err := a.d.Store().Events(c.Request.Context(), r.ID, after, int(min(limit, runs.MaxEventPage)))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RunEventsResponse{Events: events, More: more})
}

// changes returns what the run committed: files, counts and the patch. Changes
// is null until the runner reports them, and stays null for a run that never
// got that far.
func (a *API) changes(c *gin.Context) {
	r, ok := a.reviewable(c)
	if !ok {
		return
	}
	changes, err := a.d.Store().Changes(c.Request.Context(), r.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RunChangesResponse{Changes: changes})
}

// anyEditorReviews reports whether pipeline id lets any editor of its project
// give the person's review (humanAnyEditor). The members gate has already
// checked the caller may read the project this request targets.
func (a *API) anyEditorReviews(c *gin.Context, id string) bool {
	if id == "" || auth.ActorFrom(c.Request).IsAgent() {
		return false
	}
	p, err := a.d.Store().Pipeline(c.Request.Context(), id)
	return err == nil && p.Pipeline.Human && p.Pipeline.HumanAnyEditor
}

func queryInt(c *gin.Context, key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func (a *API) cancel(c *gin.Context) {
	r, ok := a.visible(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	// Cancelling any run of a pipeline cancels the pipeline, and with it
	// whichever of its runs has not ended.
	if r.PipelineID != "" {
		if err := a.d.CancelPipeline(c.Request.Context(), r.PipelineID, "cancelled by "+name); err != nil {
			fail(c, err)
			return
		}
	}
	updated, err := a.d.Cancel(c.Request.Context(), r.ID, "cancelled by "+name)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, updated.Run)
}

// retryNow ends a run's wait for its agent's usage limit to reset: the run
// is offered as soon as its runner has room, still subject to the budget and
// to the runner's own acceptance. Only a person may: it starts code on
// someone's machine, as a dispatch does.
func (a *API) retryNow(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot retry runs"), nil)
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	updated, err := a.d.RetryNow(c.Request.Context(), r.ID, name)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, updated.Run)
}
