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
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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
	MaxExtraPrompt = 8 << 10
	maxCreateBody  = 16 << 10
	maxBaseLen     = 200
	listLimit      = 50
)

// API exposes runs over HTTP.
type API struct {
	pm  *project.ProjectManager
	reg *runners.Registry
	d   *runs.Dispatcher
}

// New builds the handler set.
func New(pm *project.ProjectManager, reg *runners.Registry, d *runs.Dispatcher) *API {
	return &API{pm: pm, reg: reg, d: d}
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
	api.GET("/runs/budget", a.budget)
	api.PUT("/runs/budget", a.setBudget)
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
	if !engine.ValidNodeID(body.NodeID) {
		invalid(c, "nodeId is not a valid node id")
		return
	}
	if len(body.Prompt) > MaxExtraPrompt || !utf8.ValidString(body.Prompt) {
		invalid(c, "prompt must be valid text of at most %d bytes", MaxExtraPrompt)
		return
	}
	if !validBase(body.Base) {
		invalid(c, "base is not a valid branch name")
		return
	}

	// The runner must be the caller's own, administrators included: a run
	// executes on the owner's machine with the owner's logins, and nobody
	// else gets to put work there.
	runner, err := a.reg.Get(c.Request.Context(), body.RunnerID)
	if err != nil || runner.OwnerID != actor.ID {
		notFound(c, "runner")
		return
	}
	model, repo, problem := checkAgainstHello(runner.Info, body)
	if problem != "" {
		invalid(c, "%s", problem)
		return
	}
	if !runs.Headless(runner.Info) {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION",
			errors.New("this runner does not take runs; update nodevas on it"), nil)
		return
	}
	// The budget is the runner owner's: runs spend their subscription.
	budget, err := a.d.Store().BudgetStatus(c.Request.Context(), runner.OwnerID)
	if err != nil {
		fail(c, err)
		return
	}
	if budget.Exceeded != "" {
		httpx.Coded(c, http.StatusTooManyRequests, CodeBudgetExceeded,
			errors.New(runs.BudgetMessage(budget)), map[string]any{"exceeded": budget.Exceeded})
		return
	}

	node, content, err := loadNode(st, body.NodeID)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	if node == nil {
		notFound(c, "node")
		return
	}
	title := node.Title
	if title == "" {
		title = node.ID
	}
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	record, err := a.d.Store().Create(c.Request.Context(), runs.New{
		Project: projectName, NodeID: node.ID, NodeTitle: title,
		RunnerID: runner.ID, RunnerName: runner.Name, OwnerID: runner.OwnerID,
		Agent: body.Agent, Model: model, RepoID: repo.ID, RepoName: repo.Name, Base: body.Base,
		Prompt: body.Prompt, JobPrompt: BuildPrompt(title, content, body.Prompt),
		CreatedBy: actor.ID, CreatedByName: name,
	})
	if err != nil {
		fail(c, err)
		return
	}
	record = a.d.Submit(c.Request.Context(), record)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, record.Run)
}

// checkAgainstHello resolves the request's agent, model and repo against what
// the runner last reported. It returns the model to record and the repo, or a
// reason the request does not fit.
func checkAgainstHello(info *runnerwire.Info, body runnerwire.CreateRunRequest) (string, runnerwire.Repo, string) {
	if info == nil {
		return "", runnerwire.Repo{}, "the runner has not connected yet, so its agents are unknown"
	}
	i := slices.IndexFunc(info.Agents, func(agent runnerwire.Agent) bool { return agent.Kind == body.Agent })
	if i < 0 || !info.Agents[i].Installed {
		return "", runnerwire.Repo{}, fmt.Sprintf("agent %q is not installed on the runner", body.Agent)
	}
	agent := info.Agents[i]
	model := body.Model
	if model == "" {
		model = "default"
		for _, m := range agent.Models {
			if m.Default {
				model = m.ID
				break
			}
		}
	} else if !slices.ContainsFunc(agent.Models, func(m runnerwire.Model) bool { return m.ID == model }) {
		return "", runnerwire.Repo{}, fmt.Sprintf("model %q is not offered by %s on the runner", model, agent.Kind)
	}
	r := slices.IndexFunc(info.Repos, func(repo runnerwire.Repo) bool { return repo.ID == body.RepoID })
	if body.RepoID == "" || r < 0 {
		return "", runnerwire.Repo{}, "repoId is not one of the runner's repositories"
	}
	return model, info.Repos[r], ""
}

// validBase accepts "" (the repository's default) or a plausible branch
// name. The runner checks again with git; this keeps junk out of the record
// and anything that reads as an option off the runner's command line.
func validBase(base string) bool {
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
		content = runs.Truncate(content, max(room-len(truncatedNote), 0)) + truncatedNote
	}
	return runs.Truncate(head+content+tail, runnerwire.MaxPromptBytes)
}

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
	updated, err := a.d.Cancel(c.Request.Context(), r.ID, "cancelled by "+name)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, updated.Run)
}
