package runs

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// maxRoleSettingsBody bounds PUT /api/runs/roles.
const maxRoleSettingsBody = 64 << 10

// roleSettings answers GET /api/runs/roles: the caller's per-role max
// concurrent runs and daily budgets. Who runs a role (agent, model,
// fallbacks) is a routing rule with match "role" (GET /api/runs/routing).
func (a *API) roleSettings(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	settings, err := a.d.Store().RoleSettings(c.Request.Context(), actor.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RoleSettingsResponse{Roles: settings})
}

// setRoleSettings replaces the caller's role settings. Like a budget, it is
// a brake on agents, so no agent may change it.
func (a *API) setRoleSettings(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", locale.Errorf("agents cannot change role settings", "agent 不能更改角色設定"), nil)
		return
	}
	var body runnerwire.RoleSettingsResponse
	if !httpx.DecodeJSONLimit(c, &body, maxRoleSettingsBody) {
		return
	}
	settings, err := a.d.Store().SetRoleSettings(c.Request.Context(), actor.ID, body.Roles)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RoleSettingsResponse{Roles: settings})
}
