// HTTP handlers for the workspace-wide workflow vocabulary.
// Routes: routes.go.
//
// Custom lifecycle states belong to the workspace, not to one project, so this
// is the single place they are written. Reading them is open to anyone who can
// read the projects; writing takes admin, like every other definition change.

package project

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

var statusShapes = map[string]bool{
	"circle": true, "square": true, "diamond": true, "triangle": true, "dash": true,
}

// hoisted maps a workspace to the project list whose local states were last
// lifted into the shared file by this process. The lift is idempotent, but it
// reads every graph.yaml, so it is skipped while the workspace still holds the
// same projects. Keying on the list rather than on the workspace is what makes
// a project imported or created later get its states — waiting for a server
// restart is not something anyone can be expected to guess.
var hoisted sync.Map

func (a *API) hoistStatuses(workspace string) {
	if workspace == "" {
		return
	}
	projects, err := a.pm.List()
	if err != nil {
		return
	}
	paths := make([]string, 0, len(projects))
	for _, info := range projects {
		if !info.IsFolder {
			paths = append(paths, filepath.Join(info.Path, "graph.yaml"))
		}
	}
	fingerprint := strings.Join(paths, "\n")
	if previous, ok := hoisted.Load(workspace); ok && previous == fingerprint {
		return
	}
	if err := store.HoistProjectStatuses(workspace, paths); err != nil {
		log.Printf("share workspace statuses: %v", err)
		// Leave the fingerprint unrecorded so a later read tries again.
		return
	}
	hoisted.Store(workspace, fingerprint)
}

func (a *API) GetWorkspaceStatuses(c *gin.Context) {
	workspace := a.pm.Workspace()
	a.hoistStatuses(workspace)
	statuses := store.LoadWorkspaceStatuses(workspace)
	if statuses == nil {
		statuses = []engine.StatusDefinition{}
	}
	c.JSON(http.StatusOK, map[string]any{"customStatuses": statuses})
}

// PutWorkspaceStatuses replaces the whole vocabulary. A full replacement keeps
// the client's list authoritative: reordering, renaming and deleting are one
// write instead of three endpoints that can disagree.
func (a *API) PutWorkspaceStatuses(c *gin.Context) {
	var body struct {
		CustomStatuses []engine.StatusDefinition `json:"customStatuses"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if len(body.CustomStatuses) > store.MaxWorkspaceStatuses {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf(
			"共用狀態過多（上限 %d）", store.MaxWorkspaceStatuses))
		return
	}
	seenID := map[string]bool{}
	seenLabel := map[string]bool{}
	for _, definition := range body.CustomStatuses {
		if !strings.HasPrefix(definition.ID, "custom-status-") ||
			!engine.ValidNodeID(definition.ID) {
			httpx.Err(c, http.StatusBadRequest,
				fmt.Errorf("狀態代號無效：%q", definition.ID))
			return
		}
		label := strings.TrimSpace(definition.Label)
		if label == "" || len(label) > 128 {
			httpx.Err(c, http.StatusBadRequest,
				fmt.Errorf("狀態「%s」的名稱空白或過長", definition.ID))
			return
		}
		if !statusShapes[definition.Shape] {
			httpx.Err(c, http.StatusBadRequest,
				fmt.Errorf("不支援的圖形：%q", definition.Shape))
			return
		}
		if seenID[definition.ID] {
			httpx.Err(c, http.StatusBadRequest,
				fmt.Errorf("重複的狀態代號：%q", definition.ID))
			return
		}
		lowered := strings.ToLower(label)
		if seenLabel[lowered] {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("重複的狀態名稱：%q", label))
			return
		}
		seenID[definition.ID] = true
		seenLabel[lowered] = true
	}
	if err := store.SaveWorkspaceStatuses(a.pm.Workspace(), body.CustomStatuses); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	// Every open project's graph now reads differently, so clients reload it.
	a.pm.Hub().Broadcast("graph-changed", "")
	c.JSON(http.StatusOK, map[string]any{
		"ok": true, "customStatuses": body.CustomStatuses,
	})
}
