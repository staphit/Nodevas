// The workspace and the projects in it: opening one, tracking which is active, and handing out the Store each request works against.

package project

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"nodevas/internal/engine"
	"nodevas/internal/lockfile"
	"nodevas/internal/realtime"
	"nodevas/internal/store"
)

// WorkspaceLockName is the file inside <workspace>/.vised that carries the
// cross-process workspace lock.
const WorkspaceLockName = "workspace.lock"

// WorkspaceLockPath is the lock file guarding a workspace root.
func WorkspaceLockPath(root string) string {
	return filepath.Join(root, store.DataDir, WorkspaceLockName)
}

// WorkspaceBusyError says another Nodevas process already owns a workspace.
// It is a named type rather than a formatted string so the CLI can turn it
// into an instruction ("stop the server") instead of a stack trace.
type WorkspaceBusyError struct {
	Workspace string
	Holder    string
}

func (e *WorkspaceBusyError) Error() string {
	if e.Holder != "" {
		return fmt.Sprintf(
			"workspace %q is already open in another Nodevas instance (%s)",
			e.Workspace, e.Holder,
		)
	}
	return fmt.Sprintf(
		"workspace %q is already open in another Nodevas instance",
		e.Workspace,
	)
}

func (e *WorkspaceBusyError) Is(target error) bool { return target == lockfile.ErrLocked }

// AcquireWorkspaceLock takes the cross-process lock on a workspace root. It is
// the entry point for callers that touch a workspace without opening a
// ProjectManager -- command-line account changes update the shared database
// and would otherwise clobber a running server's copy.
func AcquireWorkspaceLock(root string) (*lockfile.Lock, error) {
	path := WorkspaceLockPath(root)
	lock, err := lockfile.Acquire(path)
	if err == nil {
		return lock, nil
	}
	if errors.Is(err, lockfile.ErrLocked) {
		return nil, &WorkspaceBusyError{Workspace: root, Holder: lockfile.Describe(path)}
	}
	return nil, err
}

// ProjectManager serves one workspace directory that contains multiple
// sub-projects (each its own folder with graph.yaml + nodes/), so
// unrelated graphs never mix. One sub-project is active at a time.
type ProjectManager struct {
	workspace string
	// workspacePath mirrors workspace for readers that cannot take mu: a
	// Store callback may run while this manager already holds it.
	workspacePath atomic.Pointer[string]

	catalogRoot string
	hub         *realtime.Hub

	mu          sync.RWMutex
	activateMu  sync.Mutex
	importMu    sync.Mutex
	catalogMu   sync.Mutex
	store       *store.Store
	stopWatch   func() error
	searchIndex *searchIndex

	// stores caches one Store per project directory; see cachedStore for why
	// the cache is the only legal way to get one.
	storesMu sync.Mutex
	stores   map[string]*store.Store

	// lock is the OS-level advisory lock on the *active* workspace, held for
	// as long as this manager serves it. It is an outer layer over the
	// mutexes above, not a replacement: those serialise goroutines, this one
	// serialises processes. Held on an open descriptor so the kernel returns
	// it if we are killed.
	lockMu     sync.Mutex
	lock       *lockfile.Lock
	lockedRoot string

	// remote is set when the server listens beyond loopback. Endpoints that
	// take an arbitrary host path then stop accepting one: an account is
	// permission to edit projects, not to reach the whole disk.
	remote bool
}

func NewProjectManager(workspace string, hub *realtime.Hub) (*ProjectManager, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config directory: %w", err)
	}
	catalogRoot := filepath.Join(configRoot, "nodevas")
	return NewManagerAt(
		workspace,
		hub,
		catalogRoot,
	)
}

// NewManagerAt opens a workspace with an explicit catalog root, instead of
// discovering one from the user config directory.
func NewManagerAt(
	workspace string,
	hub *realtime.Hub,
	catalogRoot string,
) (*ProjectManager, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory")
	}
	catalogRoot, err = filepath.Abs(catalogRoot)
	if err != nil {
		return nil, fmt.Errorf("workspace catalog: %w", err)
	}
	pm := &ProjectManager{workspace: real, catalogRoot: catalogRoot, hub: hub}
	roots, err := pm.LoadWorkspaceRoots()
	catalogNeedsSave := os.IsNotExist(err)
	if catalogNeedsSave {
		roots = nil
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		roots = []string{real}
		catalogNeedsSave = true
	}
	if catalogNeedsSave {
		if err := pm.SaveWorkspaceRoots(roots); err != nil {
			return nil, err
		}
	}
	pm.setWorkspace(roots[0])
	if data, readErr := os.ReadFile(pm.activeWorkspaceFile()); readErr == nil {
		lastRoot := strings.TrimSpace(string(data))
		for _, root := range roots {
			if WorkspacePathEqual(root, lastRoot) {
				pm.setWorkspace(root)
				break
			}
		}
	}
	if err := pm.OpenWorkspace(); err != nil {
		return nil, err
	}
	// From here on the workspace lock and the file watcher are held, so every
	// failure has to hand them back rather than return a half-open manager.
	if err := pm.SaveActiveWorkspace(); err != nil {
		pm.Close()
		return nil, err
	}
	return pm, nil
}

// Workspace returns the directory that holds the sub-projects.
func (pm *ProjectManager) Workspace() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.workspace
}

// workspaceRoot is the lock-free reader used by callbacks a Store may invoke
// while this manager holds its own lock. Taking pm.mu there would deadlock.
func (pm *ProjectManager) workspaceRoot() string {
	if root := pm.workspacePath.Load(); root != nil {
		return *root
	}
	return ""
}

// setWorkspace keeps the locked field and its lock-free mirror in step.
func (pm *ProjectManager) setWorkspace(root string) {
	pm.workspace = root
	pm.workspacePath.Store(&root)
}

// SetRemote marks the workspace as served beyond loopback.
func (pm *ProjectManager) SetRemote(remote bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.remote = remote
}

func (pm *ProjectManager) lastFile() string {
	return filepath.Join(pm.workspace, ".vised-last")
}

func (pm *ProjectManager) Store() *store.Store {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.store
}

func (pm *ProjectManager) Close() {
	pm.mu.Lock()
	if pm.stopWatch != nil {
		_ = pm.stopWatch()
		pm.stopWatch = nil
	}
	pm.mu.Unlock()
	// Persist the search index before dropping the workspace: it is a pure
	// cache, but losing it on every clean shutdown means paying for a full
	// rebuild on the next start for no reason.
	pm.FlushSearchIndex()
	pm.releaseWorkspaceLock()
}

// lockWorkspace takes the cross-process lock for root, replacing whatever this
// manager held before.
//
// The new lock is taken before the old one is dropped, so a failed switch
// leaves us still owning the workspace we are actually serving rather than
// owning nothing. Re-locking the root we already hold is a no-op: reopening
// the same path would contend with our own descriptor and look like someone
// else's lock.
func (pm *ProjectManager) lockWorkspace(root string) error {
	pm.lockMu.Lock()
	defer pm.lockMu.Unlock()
	if pm.lock != nil && WorkspacePathEqual(pm.lockedRoot, root) {
		return nil
	}
	next, err := AcquireWorkspaceLock(root)
	if err != nil {
		return err
	}
	if pm.lock != nil {
		if releaseErr := pm.lock.Release(); releaseErr != nil {
			log.Printf("release workspace lock %s: %v", pm.lockedRoot, releaseErr)
		}
	}
	pm.lock = next
	pm.lockedRoot = root
	return nil
}

// releaseWorkspaceLock drops the workspace lock on clean shutdown. Skipping it
// is survivable -- the OS reclaims the lock when the process exits -- but a
// prompt release lets a desktop restart reopen the workspace immediately.
func (pm *ProjectManager) releaseWorkspaceLock() {
	pm.lockMu.Lock()
	defer pm.lockMu.Unlock()
	if pm.lock == nil {
		return
	}
	if err := pm.lock.Release(); err != nil {
		log.Printf("release workspace lock %s: %v", pm.lockedRoot, err)
	}
	pm.lock = nil
	pm.lockedRoot = ""
}

func templateGraph(template string) *engine.Graph {
	if template == "eisenhower" {
		quadrant := func(id, title, color string, x, y float64) engine.CanvasGroup {
			return engine.CanvasGroup{
				ID: id, Title: title, Color: color,
				X: x, Y: y, Width: 620, Height: 380,
			}
		}
		return &engine.Graph{
			Version: 1,
			Type:    "workflow",
			UI: &engine.UIState{
				Groups: []engine.CanvasGroup{
					quadrant("quad-do", "重要且緊急：立刻做", "#9f2f2d", 0, 0),
					quadrant("quad-schedule", "重要不緊急：排程做", "#1f6c9f", 640, 0),
					quadrant("quad-delegate", "緊急不重要：委派", "#956400", 0, 400),
					quadrant("quad-eliminate", "不緊急不重要：捨棄", "#6f6c66", 640, 400),
				},
			},
		}
	}
	return &engine.Graph{Version: 1, Type: "story"}
}

func (pm *ProjectManager) Activate(name string, create bool, template string) error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	return pm.activateLocked(name, create, template)
}

func (pm *ProjectManager) activateLocked(name string, create bool, template string) error {
	dir, err := pm.Resolve(name)
	if err != nil {
		return err
	}
	if create {
		graphPath := filepath.Join(dir, "graph.yaml")
		if _, statErr := store.StatProjectPath(pm.workspace, graphPath); statErr == nil {
			return fmt.Errorf("project %q already exists", name)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if parent := pathParent(name); parent != "" && parent != "." {
			parentDir, parentErr := pm.Resolve(parent)
			if parentErr != nil {
				return parentErr
			}
			// A plain grouping folder is an acceptable parent too.
			if info, parentErr := store.StatProjectPath(pm.workspace, parentDir); parentErr != nil || !info.IsDir() {
				return fmt.Errorf("parent %q does not exist", parent)
			}
		}
		if err := store.MkdirAllProjectPath(pm.workspace, filepath.Join(dir, "nodes"), 0o755); err != nil {
			return err
		}
		gp := filepath.Join(dir, "graph.yaml")
		if _, err := store.StatProjectPath(pm.workspace, gp); os.IsNotExist(err) {
			g := templateGraph(template)
			data, merr := engine.MarshalGraph(g)
			if merr != nil {
				return merr
			}
			if werr := pm.cachedStore(dir).WriteAtomic(gp, data); werr != nil {
				return werr
			}
		} else if err != nil {
			return err
		}
	}
	if _, err := store.StatProjectPath(pm.workspace, filepath.Join(dir, "graph.yaml")); err != nil {
		return fmt.Errorf("project %q has no graph.yaml", name)
	}

	st := pm.cachedStore(dir)
	if _, _, err := st.LoadGraph(); err != nil {
		return fmt.Errorf("load project %q: %w", name, err)
	}
	stop, err := realtime.WatchWithCallback(st, pm.hub, func(kind, id string) {
		switch kind {
		case "node-changed", "graph-changed":
			pm.invalidateSearch(st.Root(), id)
		}
	})
	if err != nil {
		return err
	}
	pm.mu.Lock()
	old := pm.stopWatch
	pm.store = st
	pm.stopWatch = stop
	pm.mu.Unlock()
	if old != nil {
		_ = old()
	}
	if err := store.WriteProjectFileAtomic(pm.workspace, pm.lastFile(), []byte(name)); err != nil {
		log.Printf("save last project: %v", err)
	}
	return nil
}

func (pm *ProjectManager) OpenWorkspace() error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	return pm.openWorkspaceLocked()
}

func (pm *ProjectManager) openWorkspaceLocked() error {
	// Take the cross-process lock before anything reads or writes inside the
	// workspace, so a second instance is turned away instead of racing us.
	if err := pm.lockWorkspace(pm.workspace); err != nil {
		return err
	}
	projects, err := pm.List()
	if err != nil {
		return err
	}
	openable := projects[:0:0]
	graphPaths := make([]string, 0, len(projects))
	for _, project := range projects {
		if !project.IsFolder {
			openable = append(openable, project)
			graphPaths = append(graphPaths, filepath.Join(project.Path, "graph.yaml"))
		}
	}
	// States defined before the vocabulary became workspace-wide still live in
	// one project each. Lifting them here is what makes them show up in the
	// others, instead of only in whichever project first defined them.
	if err := store.HoistProjectStatuses(pm.workspace, graphPaths); err != nil {
		log.Printf("share workspace statuses: %v", err)
	}
	if len(openable) == 0 {
		return pm.activateLocked("main", true, "")
	}
	name := openable[0].Name
	if data, readErr := store.ReadProjectFile(pm.workspace, pm.lastFile()); readErr == nil {
		last := strings.TrimSpace(string(data))
		for _, project := range openable {
			if project.Name == last {
				name = last
				break
			}
		}
	}
	return pm.activateLocked(name, false, "")
}

func (pm *ProjectManager) SwitchWorkspace(root string) error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	previous := pm.workspace
	pm.setWorkspace(root)
	if err := pm.openWorkspaceLocked(); err != nil {
		pm.setWorkspace(previous)
		// The switch may already have moved the lock to the new root; put it
		// back on the workspace we are still serving.
		if relockErr := pm.lockWorkspace(previous); relockErr != nil {
			log.Printf("re-lock workspace %s after failed switch: %v", previous, relockErr)
		}
		return err
	}
	if err := pm.SaveActiveWorkspace(); err != nil {
		log.Printf("save active workspace: %v", err)
	}
	pm.hub.Broadcast("project-changed", root)
	return nil
}

// storeCacheKey normalizes a project directory into a cache key. Windows
// compares paths case-insensitively, so "A/x" and "a/x" must land on the same
// Store there; elsewhere they are different directories.
func storeCacheKey(dir string) string {
	clean := filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// cachedStore returns the one Store for dir, creating it on first use.
//
// Identity is the invariant here: a Store serializes writes with its own
// mutex, so two Store values over the same directory serialize nothing. Every
// path that needs a Store for a project directory must come through here.
func (pm *ProjectManager) cachedStore(dir string) *store.Store {
	key := storeCacheKey(dir)
	pm.storesMu.Lock()
	defer pm.storesMu.Unlock()
	if st, ok := pm.stores[key]; ok {
		return st
	}
	if pm.stores == nil {
		pm.stores = make(map[string]*store.Store)
	}
	st := store.NewStore(dir)
	// Every project in a workspace shares one lifecycle vocabulary. The store
	// asks for it on each load, so switching workspaces needs no invalidation.
	st.SetSharedStatuses(func() []engine.StatusDefinition {
		return store.LoadWorkspaceStatuses(pm.workspaceRoot())
	})
	pm.stores[key] = st
	return st
}

// StoreFor returns the store for a named project without changing the active
// one. The project must already exist: this is a read/write path for existing
// content, not a creation path.
func (pm *ProjectManager) StoreFor(name string) (*store.Store, error) {
	dir, err := pm.Resolve(name)
	if err != nil {
		return nil, err
	}
	graphInfo, err := store.StatProjectPath(dir, filepath.Join(dir, "graph.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("project %q has no graph.yaml", name)
		}
		return nil, err
	}
	if !graphInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("project %q has no regular graph.yaml", name)
	}
	return pm.cachedStore(dir), nil
}

// EvictStores drops the cached stores for dir and everything under it. Call it
// after the directory is renamed or deleted, so a later project of the same
// name never inherits a Store pointing at the old path.
func (pm *ProjectManager) EvictStores(dir string) {
	prefix := storeCacheKey(dir)
	pm.storesMu.Lock()
	defer pm.storesMu.Unlock()
	for key := range pm.stores {
		if key == prefix || strings.HasPrefix(key, prefix+string(filepath.Separator)) {
			delete(pm.stores, key)
		}
	}
}

// CatalogRoot is the directory the workspace catalog is stored in.
func (pm *ProjectManager) CatalogRoot() string { return pm.catalogRoot }

// LockCatalog guards a read-modify-write of the workspace catalog. The caller
// is editing a file this manager also reads, so the lock has to be the same
// one, not a second lock over the same bytes.
func (pm *ProjectManager) LockCatalog() func() {
	pm.catalogMu.Lock()
	return pm.catalogMu.Unlock
}

// Hub is the broadcaster this manager notifies when the active project moves.
func (pm *ProjectManager) Hub() *realtime.Hub { return pm.hub }

// LockImport serialises operations that create a project directory, so two
// imports cannot pick the same free name.
func (pm *ProjectManager) LockImport() func() {
	pm.importMu.Lock()
	return pm.importMu.Unlock
}

// StopWatcher releases the active project's file watcher. Windows blocks a
// rename while the watcher still holds directory handles, so anything that
// moves the active project has to call this first.
func (pm *ProjectManager) StopWatcher() {
	pm.mu.Lock()
	stop := pm.stopWatch
	pm.stopWatch = nil
	pm.mu.Unlock()
	if stop != nil {
		_ = stop()
	}
}

// SetWorkspace points the manager at another workspace directory. The caller
// is expected to activate a project in it straight after.
func (pm *ProjectManager) SetWorkspace(root string) {
	pm.mu.Lock()
	pm.setWorkspace(root)
	pm.mu.Unlock()
}

// IsRemote reports whether the server is reachable from other machines, which
// is what makes host paths off limits.
func (pm *ProjectManager) IsRemote() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.remote
}
