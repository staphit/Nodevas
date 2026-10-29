// Package station serves /api/station: what Station's building draws of
// every project the viewer may read, in one request. GET
// /api/station/building is permission-filtered per project, reads one
// workspace walk, a few cross-project queries and the in-memory session
// lists, and no node file or graph. It is not on the runner proxy's
// allowlist and refuses agents: it is a person's overview.
package station

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/autopilot"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/project"
	"nodevas/internal/runs"
)

// API is the handler set.
type API struct {
	pm     *project.ProjectManager
	src    Sources
	access func(*http.Request, string) bool
	now    func() time.Time
}

// New builds the handler set. access reports whether the request may read
// the project directory; nil allows every project.
func New(pm *project.ProjectManager, src Sources, access func(*http.Request, string) bool) *API {
	return &API{pm: pm, src: src, access: access, now: time.Now}
}

// Register wires /api/station.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/station/building", a.building)
}

func (a *API) building(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", locale.Errorf("agents cannot read the building", "agent 不能讀取大樓"), nil)
		return
	}
	readable := func(dir string) bool { return a.access == nil || a.access(c.Request, dir) }
	out, err := Assemble(c.Request.Context(), a.src, actor, readable, a.now())
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// sources is the server's Sources. Any of d, o and h may be nil on a server
// without runners or agents: that part of the building is then empty.
type sources struct {
	pm *project.ProjectManager
	d  *runs.Dispatcher
	o  *autopilot.Orchestrator
	h  *agents.Hub
}

// NewSources reads the building from the server's stores.
func NewSources(pm *project.ProjectManager, d *runs.Dispatcher, o *autopilot.Orchestrator, h *agents.Hub) Sources {
	return &sources{pm: pm, d: d, o: o, h: h}
}

func (s *sources) Projects() ([]project.ProjectInfo, error) { return s.pm.List() }

func (s *sources) Root(name string) (string, bool) {
	st, err := s.pm.StoreFor(name)
	if err != nil || st == nil {
		return "", false
	}
	return st.Root(), true
}

func (s *sources) Runs(ctx context.Context, names []string, viewer, since string) ([]runs.Record, error) {
	if s.d == nil {
		return nil, nil
	}
	return s.d.Store().ListBuilding(ctx, runs.BuildingFilter{Projects: names, Viewer: viewer, Since: since, Limit: runLimit})
}

func (s *sources) OthersActive(ctx context.Context, names []string, viewer string) (map[string]int, error) {
	if s.d == nil {
		return map[string]int{}, nil
	}
	return s.d.Store().CountOthersActive(ctx, names, viewer)
}

func (s *sources) Sessions(root string) []agents.Session {
	if s.h == nil {
		return nil
	}
	return s.h.ListSessions(root, "", false, nil)
}

func (s *sources) PendingAsksIn(ctx context.Context, roots []string) ([]agents.Ask, error) {
	if s.h == nil {
		return nil, nil
	}
	return s.h.PendingAsksIn(ctx, roots)
}

func (s *sources) HeldIn(ctx context.Context, roots []string, v agents.Viewer) ([]agents.Message, error) {
	if s.h == nil {
		return nil, nil
	}
	return s.h.HeldIn(ctx, roots, v)
}

func (s *sources) WaitingHuman(ctx context.Context, names []string) ([]autopilot.WaitingHumanRow, error) {
	if s.o == nil {
		return nil, nil
	}
	return s.o.WaitingHuman(ctx, names)
}
