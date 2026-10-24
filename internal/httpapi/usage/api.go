// Package usage serves token usage and the prices that cost it:
//
//	GET /api/usage?days=N         → usage.Report for the request's project
//	GET /api/usage/prices         → {prices, asOf}
//	PUT /api/usage/prices {prices} → replaces every custom price (administrators)
package usage

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/usage"
)

// maxPricesBody fits usage.MaxCustomPrices entries.
const maxPricesBody = 64 << 10

// API exposes a usage.Store over HTTP.
type API struct {
	pm    *project.ProjectManager
	store *usage.Store
}

// New builds the usage handlers.
func New(pm *project.ProjectManager, store *usage.Store) *API {
	return &API{pm: pm, store: store}
}

// Register wires `/api/usage*` onto the router group.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/usage", a.getUsage)
	api.GET("/usage/prices", a.getPrices)
	api.PUT("/usage/prices", a.putPrices)
}

// projectName is the request's project as ?project= takes it, as runs and
// session usage record it; "" when there is none.
func (a *API) projectName(r *http.Request) string {
	if a.pm == nil {
		if name := httpx.RequestProjectName(r); name != "" {
			return name
		}
		return "."
	}
	st := httpx.StoreFor(r, a.pm)
	if st == nil {
		return ""
	}
	rel, err := filepath.Rel(a.pm.Workspace(), st.Root())
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (a *API) getUsage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	days := usage.DefaultDays
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > usage.MaxDays {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("days is between 1 and 365"), nil)
			return
		}
		days = n
	}
	name := a.projectName(c.Request)
	if name == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
		return
	}
	actor := auth.ActorFrom(c.Request)
	q := usage.Query{Project: name, Days: days}
	// Runs as the runs list shows them: an administrator sees every run, anyone
	// else the runs they dispatched or that ran on their runners.
	if !actor.IsAdmin() {
		q.Viewer = actor.ID
	}
	report, err := a.store.Report(c.Request.Context(), q)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

func (a *API) getPrices(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	table, err := a.store.Table(c.Request.Context())
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"prices": table.Prices(), "asOf": usage.PricesAsOf})
}

// putPrices replaces the custom prices. Prices apply to every project and
// every account's figures, so they are a server setting: administrators only,
// and never an agent.
func (a *API) putPrices(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() || !actor.IsAdmin() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			errors.New("changing prices requires an owner or administrator"), nil)
		return
	}
	var body struct {
		Prices []usage.Price `json:"prices"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxPricesBody) {
		return
	}
	if body.Prices == nil {
		body.Prices = []usage.Price{}
	}
	if err := a.store.SetCustomPrices(c.Request.Context(), body.Prices, actor.ID); err != nil {
		if errors.Is(err, usage.ErrInvalid) {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.getPrices(c)
}
