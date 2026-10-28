// HTTP handlers for creating, moving, copying and removing projects.
// Routes: routes.go (routeProjects).

package project

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/store"
)

// PostProjectOpen switches to a project, creating it first with create.
//
// On loopback it moves the server's active project, and every window follows.
// With per-account projects it moves only the caller's current project (and
// their own other windows): anyone who may read a project may open it, while
// creating one stays an administrator's.
func (a *API) PostProjectOpen(c *gin.Context) {
	var body struct {
		Name     string `json:"name"`
		Create   bool   `json:"create"`
		Template string `json:"template"` // "", "empty", "eisenhower"
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if body.Name == "" {
		httpx.Err(c, 400, fmt.Errorf("body must be {name, create?, template?}"))
		return
	}
	if a.current != nil {
		// Registered outside the administrators' group (routes.go).
		if body.Create {
			if err := auth.RequireAdmin(c.Request); err != nil {
				httpx.Err(c, http.StatusForbidden, errors.New("administrator access required"))
				return
			}
		}
		a.openForCaller(c, body.Name, body.Create, body.Template)
		return
	}
	isNew := body.Create && !a.projectExists(body.Name)
	if err := a.pm.Activate(body.Name, body.Create, body.Template); err != nil {
		httpx.Err(c, 400, err)
		return
	}
	if isNew {
		a.defaultWorkflow(c, body.Name)
	}
	a.pm.Hub().Broadcast("project-changed", body.Name)
	c.JSON(200, map[string]string{
		"root": a.visibleHostPath(a.pm.Store().Root()), "active": body.Name,
	})
}

// openForCaller is PostProjectOpen with per-account projects.
func (a *API) openForCaller(c *gin.Context, name string, create bool, template string) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if create {
		if err := a.pm.Create(name, template); err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		a.defaultWorkflow(c, name)
		// A new project in everyone's tree; nobody else's current one moves.
		a.pm.Hub().Broadcast("project-changed", name)
	}
	if err := a.current.Open(c.Request, name); err != nil {
		switch {
		case errors.Is(err, ErrNotReadable):
			// As the member routes do: probing names teaches nothing.
			httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", ErrNotReadable, nil)
		case errors.Is(err, ErrAgentSwitch):
			httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", err, nil)
		default:
			httpx.Err(c, http.StatusBadRequest, err)
		}
		return
	}
	st, err := a.pm.StoreFor(name)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"root": a.visibleHostPath(st.Root()), "active": name,
	})
}

// PostProjectFolder creates a plain grouping directory in the workspace. It
// shows in the tree as a folder; projects can be created in or moved into it.
func (a *API) PostProjectFolder(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	name := strings.Trim(strings.TrimSpace(body.Name), "/")
	if err := a.pm.CreateFolder(name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, project.ErrInvalidName) || errors.Is(err, project.ErrPathTaken) {
			status = http.StatusBadRequest
		}
		httpx.Err(c, status, err)
		return
	}
	a.pm.Hub().Broadcast("project-changed", name)
	c.JSON(http.StatusOK, map[string]any{"ok": true, "name": name})
}

func (a *API) PostProjectImportPath(c *gin.Context) {
	if httpx.RefuseHostPath(c, a.pm) {
		return
	}
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	src, err := filepath.Abs(strings.TrimSpace(body.Path))
	if err != nil || strings.TrimSpace(body.Path) == "" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("請提供資料夾路徑"))
		return
	}
	info, err := os.Stat(src)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("找不到路徑 %q", src))
		return
	}
	if !info.IsDir() {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("%q 不是資料夾", src))
		return
	}
	if err := project.CheckImportSource(src, a.pm.Workspace()); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = filepath.Base(src)
	}
	if !project.ProjectNamePattern.MatchString(name) || name == "." || name == ".." {
		httpx.Err(c, http.StatusBadRequest,
			fmt.Errorf("專案名稱 %q 無效，請在表單指定名稱", name))
		return
	}
	defer a.pm.LockImport()()
	dest, err := a.pm.Resolve(name)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if _, err := store.StatProjectPath(a.pm.Workspace(), dest); err == nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("工作區已有 %q", name))
		return
	}

	// A half-written import leaves a directory nothing can open, so the failure
	// paths below take it away again — along with the Store that was cached for
	// it, which would otherwise outlive the directory it points at.
	cleanupOnError := func() {
		a.pm.EvictStores(dest)
		_ = store.RemoveAllProjectPath(a.pm.Workspace(), dest)
	}
	copied := 0
	if _, err := os.Stat(filepath.Join(src, "graph.yaml")); err == nil {
		if err := project.CopyDirTree(src, dest, &copied); err != nil {
			cleanupOnError()
			httpx.Err(c, http.StatusInternalServerError, fmt.Errorf("複製失敗：%w", err))
			return
		}
	} else {
		// Markdown import: every *.md (recursive) becomes a node document.
		docs, collectErr := project.CollectMarkdownImport(src)
		if collectErr != nil {
			httpx.Err(c, http.StatusBadRequest, collectErr)
			return
		}
		st, storeErr := a.pm.NewProjectStore(name)
		if storeErr != nil {
			httpx.Err(c, http.StatusBadRequest, storeErr)
			return
		}
		copied, err = st.ImportDocuments(&engine.Graph{Version: 1, Type: "workflow", Workflow: engine.NewProjectWorkflow("workflow")}, docs)
		if err != nil {
			cleanupOnError()
			httpx.Err(c, http.StatusInternalServerError, fmt.Errorf("匯入失敗：%w", err))
			return
		}
	}

	if err := a.pm.OpenForCaller(name); err != nil {
		cleanupOnError()
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("匯入後開啟失敗：%w", err))
		return
	}
	a.follow(c.Request, name)
	a.pm.Hub().Broadcast("project-changed", name)
	c.JSON(http.StatusOK, map[string]any{"ok": true, "name": name, "files": copied})
}

// PostProjectCopy duplicates a project under a new name — the "save as" of a
// workspace. The copy skips .vised, so drafts, history and trash stay with the
// original; only the committed files travel.
func (a *API) PostProjectCopy(c *gin.Context) {
	var body struct {
		Name      string `json:"name"`
		NewName   string `json:"newName"`
		NewParent string `json:"newParent"`
		Open      bool   `json:"open"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	name := strings.Trim(strings.TrimSpace(body.Name), "/")
	if name == "" || name == "." || !project.ValidProjectPath(name) {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("來源專案名稱無效"))
		return
	}
	newBase := strings.TrimSpace(body.NewName)
	if newBase == "" || newBase == "." || strings.Contains(newBase, "/") ||
		!project.ValidProjectPath(newBase) {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("新名稱 %q 無效", body.NewName))
		return
	}
	parent := strings.Trim(strings.TrimSpace(body.NewParent), "/")
	if parent == "." {
		parent = ""
	}
	if parent != "" && !project.ValidProjectPath(parent) {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("目標父層 %q 無效", parent))
		return
	}

	defer a.pm.LockImport()()

	src, err := a.pm.Resolve(name)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if info, statErr := store.StatProjectPath(a.pm.Workspace(), src); statErr != nil || !info.IsDir() {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("專案 %q 不存在", name))
		return
	}
	if info, statErr := store.StatProjectPath(src, filepath.Join(src, "graph.yaml")); statErr != nil || !info.Mode().IsRegular() {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("%q 不是專案（沒有 graph.yaml）", name))
		return
	}

	newName := newBase
	if parent != "" {
		newName = parent + "/" + newBase
	}
	// A copy into its own subtree would recurse forever.
	if newName == name || strings.HasPrefix(newName+"/", name+"/") {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("不能複製到自己的子層"))
		return
	}
	parentDir := a.pm.Workspace()
	if parent != "" {
		parentDir, err = a.pm.Resolve(parent)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		if info, statErr := store.StatProjectPath(a.pm.Workspace(), parentDir); statErr != nil || !info.IsDir() {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("目標父層 %q 不存在", parent))
			return
		}
	}
	dest := filepath.Join(parentDir, newBase)
	if _, statErr := store.StatProjectPath(a.pm.Workspace(), dest); statErr == nil {
		httpx.Err(c, http.StatusConflict, fmt.Errorf("工作區已有 %q", newName))
		return
	} else if !os.IsNotExist(statErr) {
		httpx.Err(c, http.StatusInternalServerError, statErr)
		return
	}

	copied := 0
	if err := project.CopyDirTree(src, dest, &copied); err != nil {
		_ = store.RemoveAllProjectPath(a.pm.Workspace(), dest)
		httpx.Err(c, http.StatusInternalServerError, fmt.Errorf("複製失敗：%w", err))
		return
	}
	if a.membership != nil {
		// A copy is as restricted as what it copies.
		a.membership.Copied(c.Request.Context(), src, dest)
	}

	active := ""
	if body.Open {
		if err := a.pm.OpenForCaller(newName); err != nil {
			httpx.Err(c, http.StatusInternalServerError,
				fmt.Errorf("已複製，但開啟失敗：%w", err))
			return
		}
		a.follow(c.Request, newName)
		active = newName
	}
	a.pm.Hub().Broadcast("project-changed", newName)
	c.JSON(http.StatusOK, map[string]any{
		"ok": true, "name": newName, "active": active, "files": copied,
	})
}

// PostProjectMove relocates or renames a project/folder. NewParent "" or "."
// means the workspace root; NewName is optional and defaults to the current
// base name. The active project follows moves of itself or any parent folder.
func (a *API) PostProjectMove(c *gin.Context) {
	var body struct {
		Name         string `json:"name"`
		NewParent    string `json:"newParent"`
		NewName      string `json:"newName"`
		NewWorkspace string `json:"newWorkspace"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	name := strings.Trim(strings.TrimSpace(body.Name), "/")
	if name == "" || name == "." {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("無法搬移工作區根目錄"))
		return
	}
	parent := strings.Trim(strings.TrimSpace(body.NewParent), "/")
	if parent == "." {
		parent = ""
	}
	if a.pm.IsRemote() && strings.TrimSpace(body.NewWorkspace) != "" {
		httpx.Err(c, http.StatusForbidden,
			fmt.Errorf("cross-workspace moves are unavailable on a remote server"))
		return
	}
	targetWorkspace := a.pm.Workspace()
	if strings.TrimSpace(body.NewWorkspace) != "" {
		var err error
		targetWorkspace, err = a.pm.NormalizeWorkspaceRoot(body.NewWorkspace)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		roots, loadErr := a.pm.WorkspaceRoots()
		if loadErr != nil {
			httpx.Err(c, http.StatusInternalServerError, loadErr)
			return
		}
		known := false
		for _, root := range roots {
			if project.WorkspacePathEqual(root, targetWorkspace) {
				known = true
				break
			}
		}
		if !known {
			httpx.Err(c, http.StatusNotFound,
				fmt.Errorf("目標工作區尚未加入：%q", targetWorkspace))
			return
		}
	}
	changesWorkspace := !project.WorkspacePathEqual(targetWorkspace, a.pm.Workspace())
	if changesWorkspace && parent != "" {
		httpx.Err(c, http.StatusBadRequest,
			fmt.Errorf("跨工作區搬移目前只接受工作區根目錄"))
		return
	}
	base := name
	if index := strings.LastIndex(name, "/"); index >= 0 {
		base = name[index+1:]
	}
	newBase := strings.TrimSpace(body.NewName)
	if newBase == "" {
		newBase = base
	}
	if newBase == "." || !project.ValidProjectPath(newBase) || strings.Contains(newBase, "/") {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("新名稱 %q 無效", newBase))
		return
	}
	newName := newBase
	if parent != "" {
		newName = parent + "/" + newBase
	}
	if newName == name && !changesWorkspace {
		c.JSON(http.StatusOK, map[string]any{
			"ok": true, "name": name, "workspace": a.visibleHostPath(a.pm.Workspace()),
		})
		return
	}
	if parent == name || strings.HasPrefix(parent+"/", name+"/") {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("不能把專案搬進自己的子層"))
		return
	}
	src, err := a.pm.Resolve(name)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if info, statErr := store.StatProjectPath(a.pm.Workspace(), src); statErr != nil || !info.IsDir() {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("專案或資料夾 %q 不存在", name))
		return
	}
	parentDir := targetWorkspace
	if parent != "" {
		parentDir, err = a.pm.Resolve(parent)
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		if info, statErr := store.StatProjectPath(targetWorkspace, parentDir); statErr != nil || !info.IsDir() {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("目標父層 %q 不存在", parent))
			return
		}
	}
	dst := filepath.Join(parentDir, newBase)
	if _, err := store.StatProjectPath(targetWorkspace, dst); err == nil {
		httpx.Err(c, http.StatusConflict, fmt.Errorf("目標位置已有同名資料夾 %q", newBase))
		return
	} else if !os.IsNotExist(err) {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}

	active, activeErr := a.pm.ActiveProject()
	movesActive := activeErr == nil &&
		(active.Name == name || strings.HasPrefix(active.Name, name+"/"))
	stopsWatcher := movesActive || changesWorkspace
	if stopsWatcher {
		// Release the watcher's directory handles before renaming (Windows
		// keeps renames blocked while handles are open).
		a.pm.StopWatcher()
	}
	// The cached stores under src point at a path that is about to stop
	// existing.
	a.pm.EvictStores(src)
	if err := store.RenameProjectPath(a.pm.Workspace(), src, targetWorkspace, dst); err != nil {
		if stopsWatcher && activeErr == nil {
			_ = a.pm.Activate(active.Name, false, "")
		}
		httpx.Err(c, http.StatusInternalServerError, fmt.Errorf("搬移失敗：%w", err))
		return
	}
	if a.membership != nil {
		a.membership.Moved(c.Request.Context(), src, dst)
	}
	newActive := ""
	if changesWorkspace {
		a.pm.SetWorkspace(targetWorkspace)
		_, destinationGraphErr := store.StatProjectPath(dst, filepath.Join(dst, "graph.yaml"))
		switch {
		case movesActive:
			newActive = newName + strings.TrimPrefix(active.Name, name)
		case destinationGraphErr == nil:
			newActive = newName
		}
		var openErr error
		if newActive != "" {
			openErr = a.pm.Activate(newActive, false, "")
		} else {
			openErr = a.pm.OpenWorkspace()
			if current, currentErr := a.pm.ActiveProject(); currentErr == nil {
				newActive = current.Name
			}
		}
		if openErr != nil {
			httpx.Err(c, http.StatusInternalServerError,
				fmt.Errorf("已搬移，但開啟目標工作區失敗：%w", openErr))
			return
		}
		if err := a.pm.SaveActiveWorkspace(); err != nil {
			log.Printf("save active workspace: %v", err)
		}
	} else if movesActive {
		newActive = newName + strings.TrimPrefix(active.Name, name)
		if err := a.pm.Activate(newActive, false, ""); err != nil {
			httpx.Err(c, http.StatusInternalServerError,
				fmt.Errorf("已搬移，但重新開啟失敗：%w", err))
			return
		}
	}
	if a.current != nil && !changesWorkspace {
		// Whoever was working in the moved project stays in it, and the
		// caller is told where they are rather than the server's default.
		a.current.Moved(c.Request.Context(), name, newName)
		newActive = a.currentName(c.Request)
	}
	a.pm.Hub().Broadcast("project-changed", newName)
	c.JSON(http.StatusOK, map[string]any{
		"ok":        true,
		"name":      newName,
		"active":    newActive,
		"workspace": a.visibleHostPath(a.pm.Workspace()),
	})
}

func (a *API) PostProjectRemove(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
		Mode string `json:"mode"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if body.Name == "" || (body.Mode != "detach" && body.Mode != "disk") {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("body must be {name, mode: detach|disk}"))
		return
	}
	// Resolved before the directory goes, for the member list to follow it.
	removedDir, resolveErr := a.pm.Resolve(body.Name)
	active, err := a.pm.RemoveProject(body.Name, body.Mode == "disk")
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	// A detached project keeps its members for when it is added back; one
	// deleted from disk loses them, so a new project there starts open.
	if a.membership != nil && body.Mode == "disk" && resolveErr == nil {
		a.membership.Removed(c.Request.Context(), removedDir)
	}
	if a.current != nil {
		// The caller's own current project, which falls back to a default
		// if it was the one removed.
		active = a.currentName(c.Request)
	}
	a.pm.Hub().Broadcast("project-changed", active)
	c.JSON(http.StatusOK, map[string]any{
		"ok":           true,
		"active":       active,
		"deletedFiles": body.Mode == "disk",
	})
}

// projectExists reports whether name already has a graph.
func (a *API) projectExists(name string) bool {
	dir, err := a.pm.Resolve(strings.Trim(strings.TrimSpace(name), "/"))
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "graph.yaml"))
	return err == nil
}

// defaultWorkflow gives a project a person just created the Standard
// workflow stages (Tester, Executor, Reviewer; a story layout judges its
// acceptance checklists instead of running tests). Existing projects keep
// none (legacy) until a person picks a preset. Best effort: the project is
// made either way.
func (a *API) defaultWorkflow(c *gin.Context, name string) {
	st, err := a.pm.StoreFor(strings.Trim(strings.TrimSpace(name), "/"))
	if err != nil {
		return
	}
	g, rev, err := st.LoadGraph()
	if err != nil || g.Workflow != nil {
		return
	}
	// LoadGraph's graph is shared with every reader; edit a copy.
	g = g.Clone()
	g.Workflow = engine.NewProjectWorkflow(g.Type)
	if _, err := st.SaveGraph(auth.ActorFrom(c.Request), g, rev); err != nil {
		log.Printf("new project %s: default workflow: %v", name, err)
	}
}
