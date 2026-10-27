// Package autopilot serves /api/autopilot: starting an autopilot on a goal
// node, following its task instances, and steering it (pause, resume, adopt
// a revised goal, cancel, retry or skip a task, a person's review). See
// internal/autopilot.
package autopilot

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	ap "nodevas/internal/autopilot"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/store"
)

const maxBody = 16 << 10

// API exposes the orchestrator over HTTP.
type API struct {
	pm *project.ProjectManager
	o  *ap.Orchestrator
	// steppedUpAt reports the creator's live step-up, recorded on the
	// autopilot's runs as POST /api/runs records the dispatcher's.
	steppedUpAt func(*http.Request) (time.Time, bool)
}

// New builds the handler set.
func New(pm *project.ProjectManager, o *ap.Orchestrator) *API { return &API{pm: pm, o: o} }

// UseStepUp tells creation how to read the creator's step-up.
func (a *API) UseStepUp(steppedUpAt func(*http.Request) (time.Time, bool)) {
	a.steppedUpAt = steppedUpAt
}

// Register wires /api/autopilot. None of it is reachable through the runner
// proxy: an agent does not start or steer autopilots.
func (a *API) Register(api *gin.RouterGroup) {
	api.POST("/autopilot", a.create)
	api.GET("/autopilot", a.latest)
	api.GET("/autopilot/:id", a.get)
	api.POST("/autopilot/:id/pause", a.pause)
	api.POST("/autopilot/:id/resume", a.resume)
	api.POST("/autopilot/:id/adopt", a.adopt)
	api.POST("/autopilot/:id/cancel", a.cancel)
	api.POST("/autopilot/:id/tasks/:node/retry", a.retry)
	api.POST("/autopilot/:id/tasks/:node/skip", a.skip)
	api.POST("/autopilot/:id/tasks/:node/review", a.review)
}

func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ap.ErrNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", err, nil)
	case errors.Is(err, ap.ErrInvalid):
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
	case errors.Is(err, ap.ErrConflict):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, nil)
	case errors.Is(err, ap.ErrNodeType):
		httpx.Coded(c, http.StatusConflict, store.CodeNodeTypeForbidsAgent, err, nil)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

func (a *API) project(c *gin.Context) (string, bool) {
	name := runsapi.ProjectName(a.pm, httpx.StoreFor(c.Request, a.pm))
	if name == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
		return "", false
	}
	return name, true
}

// person refuses agents: an autopilot dispatches work onto someone's machine,
// and an agent starting or steering one is a loop nobody approved.
func person(c *gin.Context) (identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot start or steer autopilots"), nil)
		return actor, false
	}
	return actor, true
}

func (a *API) create(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	var body runnerwire.CreateAutopilotRequest
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	name, ok := a.project(c)
	if !ok {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	view, err := a.o.Create(c.Request.Context(), ap.CreateInput{
		Project: name, GoalNodeID: strings.TrimSpace(body.GoalNodeID), Policy: body.Policy,
		Actor: actor, SteppedUpAt: steppedUpAt,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, view)
}

func (a *API) latest(c *gin.Context) {
	name, ok := a.project(c)
	if !ok {
		return
	}
	goal := strings.TrimSpace(c.Query("goal"))
	if _, named := c.GetQuery("goal"); !named {
		a.list(c, name)
		return
	}
	if !engine.ValidNodeID(goal) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("goal is not a valid node id"), nil)
		return
	}
	view, err := a.o.Latest(c.Request.Context(), name, goal)
	c.Header("Cache-Control", "no-store")
	// A goal that never had an autopilot is the ordinary answer, not a
	// missing resource: null, so the browser does not log a failed request
	// for every goal it looks at. An unknown id under /api/autopilot/:id is
	// still a 404.
	if errors.Is(err, ap.ErrNotFound) {
		c.JSON(http.StatusOK, nil)
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// listable is what GET /api/autopilot?status= may ask for: the live
// statuses, of which a goal has at most one autopilot at a time, so the list
// stays as short as the goals being worked. Ended ones are read by goal.
var listable = map[string]bool{
	runnerwire.AutopilotRunning: true, runnerwire.AutopilotPaused: true,
}

// list answers GET /api/autopilot without ?goal=: the project's autopilots
// in the statuses asked for. Anyone who may read the project sees them, as
// they may see any one of them (visible).
func (a *API) list(c *gin.Context, project string) {
	statuses := []string{runnerwire.AutopilotRunning, runnerwire.AutopilotPaused}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		statuses = statuses[:0]
		seen := map[string]bool{}
		for _, part := range strings.Split(raw, ",") {
			status := strings.TrimSpace(part)
			if !listable[status] {
				httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("unknown autopilot status "+strconv.Quote(status)), nil)
				return
			}
			if !seen[status] {
				seen[status] = true
				statuses = append(statuses, status)
			}
		}
	}
	views, err := a.o.List(c.Request.Context(), project, statuses...)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.AutopilotListResponse{Autopilots: views})
}

// visible loads the autopilot in the path, in the request's project.
func (a *API) visible(c *gin.Context) (ap.Record, bool) {
	name, ok := a.project(c)
	if !ok {
		return ap.Record{}, false
	}
	rec, err := a.o.Get(c.Request.Context(), c.Param("id"))
	if errors.Is(err, ap.ErrNotFound) || (err == nil && rec.Project != name) {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("autopilot not found"), nil)
		return ap.Record{}, false
	}
	if err != nil {
		fail(c, err)
		return ap.Record{}, false
	}
	return rec, true
}

// steerable loads the autopilot for an action: only its creator (whose
// runners its tasks run on) or an administrator steers it.
func (a *API) steerable(c *gin.Context) (ap.Record, bool) {
	actor, ok := person(c)
	if !ok {
		return ap.Record{}, false
	}
	rec, ok := a.visible(c)
	if !ok {
		return rec, false
	}
	if !actor.IsAdmin() && actor.ID != rec.CreatedBy {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			errors.New("only the person who started this autopilot can steer it"), nil)
		return rec, false
	}
	return rec, true
}

func (a *API) get(c *gin.Context) {
	rec, ok := a.visible(c)
	if !ok {
		return
	}
	a.respond(c, rec.ID, http.StatusOK)
}

func (a *API) respond(c *gin.Context, id string, status int) {
	view, err := a.o.View(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, view)
}

func (a *API) act(c *gin.Context, fn func(ap.Record) error) {
	rec, ok := a.steerable(c)
	if !ok {
		return
	}
	if err := fn(rec); err != nil {
		fail(c, err)
		return
	}
	a.respond(c, rec.ID, http.StatusOK)
}

func (a *API) pause(c *gin.Context) {
	a.act(c, func(rec ap.Record) error { return a.o.Pause(c.Request.Context(), rec.ID) })
}

func (a *API) resume(c *gin.Context) {
	a.act(c, func(rec ap.Record) error { return a.o.Resume(c.Request.Context(), rec.ID) })
}

// adopt resumes a paused autopilot with its goal's tasks as they stand now,
// after a revision of the goal (Orchestrator.Adopt).
func (a *API) adopt(c *gin.Context) {
	a.act(c, func(rec ap.Record) error { return a.o.Adopt(c.Request.Context(), rec.ID) })
}

func (a *API) cancel(c *gin.Context) {
	a.act(c, func(rec ap.Record) error { return a.o.Cancel(c.Request.Context(), rec.ID, "cancelled by a person") })
}

func (a *API) node(c *gin.Context) (string, bool) {
	node := c.Param("node")
	if !engine.ValidNodeID(node) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("not a valid node id"), nil)
		return "", false
	}
	return node, true
}

func (a *API) retry(c *gin.Context) {
	node, ok := a.node(c)
	if !ok {
		return
	}
	a.act(c, func(rec ap.Record) error { return a.o.Retry(c.Request.Context(), rec.ID, node) })
}

func (a *API) skip(c *gin.Context) {
	node, ok := a.node(c)
	if !ok {
		return
	}
	a.act(c, func(rec ap.Record) error { return a.o.Skip(c.Request.Context(), rec.ID, node) })
}

// review is a person's verdict on a task waiting for one: the autopilot's
// creator, the runner's owner or an administrator may give it, and so may
// anyone who may write the project (the members gate has checked that) when
// the policy lets any editor review (Orchestrator.Review decides).
func (a *API) review(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	node, ok := a.node(c)
	if !ok {
		return
	}
	var body runnerwire.HumanReviewRequest
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	rec, ok := a.visible(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	who := runs.Reviewer{ID: actor.ID, Name: actor.Name}
	if err := a.o.Review(c.Request.Context(), rec.ID, node, who, actor.IsAdmin(), body.Verdict, body.Comment); err != nil {
		fail(c, err)
		return
	}
	a.respond(c, rec.ID, http.StatusOK)
}
