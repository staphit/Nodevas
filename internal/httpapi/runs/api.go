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
	maxCreateBody  = 16 << 10
	listLimit      = 50
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
	api.POST("/runs/:id/merge", a.merge)
	api.GET("/runs/:id/terminal", a.terminal)
	api.GET("/runs/budget", a.budget)
	api.PUT("/runs/budget", a.setBudget)
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
	record, err := a.d.Start(c.Request.Context(), runs.StartRequest{
		Body: body, Project: projectName, ActorID: actor.ID, ActorName: actor.Name,
		SteppedUpAt: steppedUpAt,
		LoadNode: func() (string, string, bool, error) {
			node, content, err := loadNode(st, body.NodeID)
			if err != nil || node == nil {
				return "", "", false, err
			}
			return node.Title, content, true, nil
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
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", errors.New(refused.Msg), nil)
	case runs.StartBudget:
		httpx.Coded(c, http.StatusTooManyRequests, CodeBudgetExceeded,
			errors.New(refused.Msg), map[string]any{"exceeded": refused.Budget.Exceeded})
	default:
		invalid(c, "%s", refused.Msg)
	}
}

// loadNode returns the node and its body text, or a nil node when the graph
// has no such node.
func loadNode(st *store.Store, id string) (*engine.Node, string, error) {
	graph, _, err := st.LoadGraph()
	if err != nil {
		return nil, "", err
	}
	node := graph.NodeByID(id)
	if node == nil {
		return nil, "", nil
	}
	raw, _, err := st.LoadNodeContent(id)
	if errors.Is(err, fs.ErrNotExist) {
		return node, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	file, err := engine.ParseNodeFile([]byte(raw))
	if err != nil {
		// Unparseable frontmatter: the text is still the task.
		return node, raw, nil
	}
	return node, file.Body, nil
}

// BuildPrompt assembles the task a runner is offered (runs.BuildPrompt).
func BuildPrompt(title, content, extra string) string { return runs.BuildPrompt(title, content, extra) }

func (a *API) list(c *gin.Context) {
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	filter := runs.Filter{Project: projectName, NodeID: strings.TrimSpace(c.Query("nodeId")), Limit: listLimit}
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
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return runs.Record{}, false
	}
	r, err := a.d.Store().Get(c.Request.Context(), c.Param("id"))
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
	r, ok := a.visible(c)
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
