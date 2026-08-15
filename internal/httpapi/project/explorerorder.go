// HTTP handlers for the workspace-wide explorer arrangement.
// Routes: routes.go.
//
// Where a project sits in the tree is a shared decision, so this is the single
// place the order is written. Reading it is open to anyone who can read the
// projects; writing takes admin, like every other workspace-wide change.

package project

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

func (a *API) GetProjectOrder(c *gin.Context) {
	order := store.LoadProjectOrder(a.pm.Workspace())
	if order == nil {
		order = []string{}
	}
	c.JSON(http.StatusOK, map[string]any{"projectOrder": order})
}

// PutProjectOrder replaces the whole arrangement, which keeps the client's
// list authoritative: one drag is one write, and there is no partial state a
// second client could read halfway through.
//
// Names are only checked for shape, never for existence. The order outlives the
// projects it mentions — a project deleted elsewhere, or created since this
// client last loaded, must not make the write fail — so resolving names against
// the real tree is left to whoever draws it.
func (a *API) PutProjectOrder(c *gin.Context) {
	var body struct {
		ProjectOrder []string `json:"projectOrder"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.ProjectOrder) > store.MaxExplorerOrderEntries {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf(
			"排序項目過多（上限 %d）", store.MaxExplorerOrderEntries))
		return
	}
	seen := make(map[string]bool, len(body.ProjectOrder))
	for _, name := range body.ProjectOrder {
		if !engine.ValidProjectRef(name) {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("專案路徑無效：%q", name))
			return
		}
		// Case matters nowhere else in the tree, so two entries differing only
		// in case would be one project listed twice.
		lowered := strings.ToLower(name)
		if seen[lowered] {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("重複的專案路徑：%q", name))
			return
		}
		seen[lowered] = true
	}
	if err := store.SaveProjectOrder(a.pm.Workspace(), body.ProjectOrder); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	// Everyone else's explorer is now out of date, so they reload the catalog.
	a.pm.Hub().Broadcast("project-changed", "")
	order := body.ProjectOrder
	if order == nil {
		order = []string{}
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "projectOrder": order})
}
