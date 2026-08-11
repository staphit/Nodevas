// HTTP handlers for the workspace catalog: listing projects, adding and
// switching workspaces. Routes: routes.go (routeProjects).

package project

import (
	"fmt"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func (a *API) PostWorkspaceAdd(c *gin.Context) {
	if httpx.RefuseHostPath(c, a.pm) {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	root, err := a.pm.NormalizeWorkspaceRoot(body.Path)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}

	if err := a.pm.AddWorkspaceRoot(root); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}

	if err := a.pm.SwitchWorkspace(root); err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("開啟工作區失敗：%w", err))
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok": true, "path": root, "label": filepath.Base(root),
	})
}

func (a *API) PostWorkspaceOpen(c *gin.Context) {
	if httpx.RefuseHostPath(c, a.pm) {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	root, err := a.pm.NormalizeWorkspaceRoot(body.Path)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	roots, loadErr := a.pm.WorkspaceRoots()
	if loadErr != nil {
		httpx.Err(c, http.StatusInternalServerError, loadErr)
		return
	}
	found := false
	for _, existing := range roots {
		if project.WorkspacePathEqual(existing, root) {
			found = true
			break
		}
	}
	if !found {
		httpx.Err(c, http.StatusNotFound, fmt.Errorf("工作區尚未加入：%q", root))
		return
	}
	if project.WorkspacePathEqual(root, a.pm.Workspace()) {
		c.JSON(http.StatusOK, map[string]any{"ok": true, "path": root})
		return
	}
	if err := a.pm.SwitchWorkspace(root); err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("切換工作區失敗：%w", err))
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "path": root})
}

// PostWorkspaceRemove forgets a workspace without deleting anything from
// disk. Every workspace is equal; only the final remaining one is protected.
func (a *API) PostWorkspaceRemove(c *gin.Context) {
	if httpx.RefuseHostPath(c, a.pm) {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	raw := strings.TrimSpace(body.Path)
	if raw == "" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("請提供要移除的工作區路徑"))
		return
	}
	requested, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	// Stored roots are symlink-resolved (loadWorkspaceRoots normalizes every
	// entry), so a request naming the same directory through a symlink — e.g.
	// /var vs /private/var on macOS, or any /tmp link — must resolve too or it
	// would never match. A deleted directory cannot be resolved; fall back to
	// the cleaned path, which then simply won't match an already-dropped root.
	if resolved, resolveErr := filepath.EvalSymlinks(requested); resolveErr == nil {
		requested = resolved
	}

	defer a.pm.LockCatalog()()
	roots, err := a.pm.LoadWorkspaceRoots()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	target := ""
	for _, root := range roots {
		if project.WorkspacePathEqual(root, requested) {
			target = root
			break
		}
	}
	if target == "" {
		httpx.Err(c, http.StatusNotFound, fmt.Errorf("工作區尚未加入：%q", requested))
		return
	}
	if len(roots) <= 1 {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("最後一個工作區不能移除；請先加入另一個工作區"))
		return
	}
	if project.WorkspacePathEqual(target, a.pm.Workspace()) {
		fallback := ""
		for _, root := range roots {
			if !project.WorkspacePathEqual(root, target) {
				fallback = root
				break
			}
		}
		if err := a.pm.SwitchWorkspace(fallback); err != nil {
			httpx.Err(c, http.StatusInternalServerError, fmt.Errorf("切換到其他工作區失敗：%w", err))
			return
		}
	}
	kept := make([]string, 0, len(roots)-1)
	for _, root := range roots {
		if !project.WorkspacePathEqual(root, target) {
			kept = append(kept, root)
		}
	}
	if err := a.pm.SaveWorkspaceRoots(kept); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok": true, "workspace": a.pm.Workspace(),
	})
}

// GetProjects lists sub-projects plus the active one.
func (a *API) GetProjects(c *gin.Context) {
	projects, err := a.pm.List()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	active := ""
	root := a.pm.Store().Root()
	for _, p := range projects {
		if p.Path == root {
			active = p.Name
		}
	}
	workspaces, err := a.pm.WorkspaceInfos()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	// Host paths are implementation details, not workspace identifiers. A
	// remote account is scoped to the active workspace and must not learn what
	// else is mounted on the server or where it lives on disk. visibleHostPath
	// is the single gate every path-bearing response goes through.
	workspace := a.visibleHostPath(a.pm.Workspace())
	for index := range projects {
		projects[index].Path = a.visibleHostPath(projects[index].Path)
	}
	if a.pm.IsRemote() {
		activeWorkspace := make([]project.WorkspaceInfo, 0, 1)
		for _, info := range workspaces {
			if info.Active {
				info.Path = ""
				activeWorkspace = append(activeWorkspace, info)
				break
			}
		}
		workspaces = activeWorkspace
	}
	c.JSON(200, map[string]any{
		"workspace":  workspace,
		"workspaces": workspaces,
		"active":     active,
		"projects":   projects,
	})
}
