package runs

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// /api/global-runs: global agents (agent workspace, phase 2;
// runnerwire/global.go), runs in one of the caller's own runners'
// repositories for no project and no node. The prefix is exempt from the
// per-request project and its members gate (httpx.projectScopedPathExempt):
// a person must never lose their own global agent because of which board
// is open. Instead every route here is a person's only, and each run is
// shown to who may see it (canSee) and only under this prefix: a global
// run's id under /api/runs/:id is a 404, and a node's run's id here too.

// maxGlobalBody is room for the task (runnerwire.MaxGlobalTaskBytes) as
// JSON, where a quote, a backslash or a line break takes two bytes (a
// pasted log, Windows paths), and the rest of the request. The task's own
// limit is checked once it is decoded.
const maxGlobalBody = 2*runnerwire.MaxGlobalTaskBytes + 8<<10

// globalPrefix is the route prefix whose per-run routes resolve global runs.
const globalPrefix = "/api/global-runs/"

// registerGlobal wires /api/global-runs. The per-run routes are the
// /api/runs/:id handlers themselves; visibleTo picks the rule by prefix.
func (a *API) registerGlobal(api *gin.RouterGroup) {
	g := api.Group("/global-runs", personOnly)
	g.POST("", a.createGlobal)
	g.GET("", a.listGlobal)
	g.POST("/cancel-all", a.cancelAllGlobal)
	g.GET("/:id", a.get)
	g.GET("/:id/events", a.events)
	g.GET("/:id/asks", a.globalAsks)
	g.GET("/:id/changes", a.changes)
	g.GET("/:id/terminal", a.terminal)
	g.POST("/:id/cancel", a.cancel)
	g.POST("/:id/offer", a.offer)
	g.POST("/:id/merge", a.merge)
	g.GET("/:id/workspace", a.workspace)
	g.GET("/:id/workspace/tree", a.workspaceTree)
	g.GET("/:id/workspace/file", a.workspaceFile)
	g.GET("/:id/workspace/git/status", a.workspaceStatus)
	g.POST("/:id/workspace/merge", a.workspaceMerge)
	g.POST("/:id/workspace/discard", a.workspaceDiscard)
}

// personOnly refuses agents on every global agents route: an agent starting,
// stopping or reading another agent outside any board is a loop nobody
// approved, and a global agent has no board to answer to.
func personOnly(c *gin.Context) {
	if auth.ActorFrom(c.Request).IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("agents cannot use global agents", "agent 不能使用全域 agent"), nil)
		return
	}
	c.Next()
}

// globalRoute reports whether the request came in under /api/global-runs/.
func globalRoute(c *gin.Context) bool {
	return strings.HasPrefix(c.FullPath(), globalPrefix)
}

func (a *API) createGlobal(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	var body runnerwire.CreateGlobalRunRequest
	if !httpx.DecodeJSONLimit(c, &body, maxGlobalBody) {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	record, err := a.d.StartGlobal(c.Request.Context(), runs.GlobalStartRequest{
		Body: body, ActorID: actor.ID, ActorName: actor.Name, SteppedUpAt: steppedUpAt,
	})
	if err != nil {
		startFailed(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, a.forReader(c, record.Run))
}

// listGlobal is the caller's global agents tab: their global runs (made by
// them or on their runners; administrators too, it is a personal tab) and
// their own runners' load.
func (a *API) listGlobal(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	limit, err := queryInt(c, "limit", runs.DefaultGlobalPage)
	if err != nil || limit <= 0 || limit > runs.MaxGlobalPage {
		invalid(c, "limit must be between 1 and %d", "limit 須介於 1 到 %d", runs.MaxGlobalPage)
		return
	}
	before := strings.TrimSpace(c.Query("before"))
	if len(before) > 64 {
		invalid(c, "before must be a run's createdAt", "before 須為某次執行的 createdAt")
		return
	}
	ctx := c.Request.Context()
	records, more, err := a.d.Store().ListGlobal(ctx, runs.GlobalFilter{Viewer: actor.ID, Limit: int(limit), Before: before})
	if err != nil {
		fail(c, err)
		return
	}
	loads, err := a.runnerLoads(ctx, actor)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.GlobalRunsResponse{Runs: make([]runnerwire.Run, 0, len(records)), More: more, Runners: loads}
	for _, r := range records {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// runnerLoads is the load line of each of the viewer's own runners. An
// administrator's runner list holds everyone's; only their own count here.
func (a *API) runnerLoads(ctx context.Context, viewer identity.Actor) ([]runnerwire.RunnerLoad, error) {
	out := []runnerwire.RunnerLoad{}
	if a.reg == nil {
		return out, nil
	}
	all, err := a.reg.List(ctx, viewer)
	if err != nil {
		return nil, err
	}
	own := make([]runnerwire.Runner, 0, len(all))
	ids := make([]string, 0, len(all))
	for _, runner := range all {
		if runner.OwnerID == viewer.ID {
			own = append(own, runner)
			ids = append(ids, runner.ID)
		}
	}
	counts, err := a.d.Store().RunnerLoads(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, runner := range own {
		c := counts[runner.ID]
		load := runnerwire.RunnerLoad{
			RunnerID: runner.ID, Name: runner.Name, Online: runner.Online,
			MaxConcurrent: runs.Capacity(runner.Info), Settable: runners.Settable(runner.Online, runner.Info), GlobalRuns: globalRunsBlock(runner),
			Busy: c.Busy, Queued: c.Queued, GlobalActive: c.GlobalActive, Today: c.Today,
		}
		if runner.Info != nil {
			load.Ceiling = runner.Info.MaxConcurrentCeiling
		}
		out = append(out, load)
	}
	return out, nil
}

func hasCap(runner runnerwire.Runner, capability string) bool {
	return runner.Info != nil && slices.Contains(runner.Info.Capabilities, capability)
}

// globalRunsBlock is why a global run cannot start on the runner now, or "".
func globalRunsBlock(runner runnerwire.Runner) string {
	switch {
	case !runner.Online:
		return runnerwire.RunnerBlockedOffline
	case !runs.Headless(runner.Info):
		return runnerwire.RunnerBlockedHeadless
	case !hasCap(runner, runnerwire.CapGlobalRuns):
		return runnerwire.RunnerBlockedOutdated
	case !hasCap(runner, runnerwire.CapGlobalRunsOn):
		return runnerwire.RunnerBlockedOff
	}
	return ""
}

// cancelAllGlobal stops every global run of the caller's that has not
// ended, as POST …/cancel does for each.
func (a *API) cancelAllGlobal(c *gin.Context) {
	var body struct{}
	if !httpx.DecodeJSONLimit(c, &body, 1<<10) {
		return
	}
	actor := auth.ActorFrom(c.Request)
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	records, err := a.d.CancelAllGlobal(c.Request.Context(), actor.ID, name)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.CancelAllResponse{Cancelled: len(records), Runs: make([]runnerwire.Run, 0, len(records))}
	for _, r := range records {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// globalAsks is GET /api/global-runs/:id/asks: a global agent has no board
// MCP, so it never asks anything.
func (a *API) globalAsks(c *gin.Context) {
	if _, ok := a.visible(c); !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"asks": []any{}})
}
