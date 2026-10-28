// The workspace and the projects in it: opening one, tracking which is active, and handing out the Store each request works against.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	// searchLive mirrors searchIndex for the store write hook, which may run
	// while pm.mu is held; see noteSearchChange.
	searchLive atomic.Pointer[searchIndex]

	// watches are the watchers on projects other than the active one that
	// Watch was asked for, keyed like stores. They are dropped with their
	// project's stores and whenever the active project's watcher stops.
	watchMu sync.Mutex
	watches map[string]func() error
	// perAccount: see SetPerAccount.
	perAccount atomic.Bool

	// stores caches one Store per project directory; see cachedStore for why
	// the cache is the only legal way to get one.
	storesMu sync.Mutex
	stores   map[string]*store.Store

	// lookups is what StoreFor remembers about project names; see
	// lookup_cache.go.
	lookups projectLookups

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

	// linksWarm counts WarmNodeLinks passes still reading; closing tells them
	// to stop at the next project.
	linksWarm sync.WaitGroup
	closing   atomic.Bool
}

// DefaultCatalogRoot is where NewProjectManager keeps the workspace catalog,
// secrets and the serve log: nodevas under the user config directory.
func DefaultCatalogRoot() (string, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config directory: %w", err)
	}
	return filepath.Join(configRoot, "nodevas"), nil
}

func NewProjectManager(workspace string, hub *realtime.Hub) (*ProjectManager, error) {
	catalogRoot, err := DefaultCatalogRoot()
	if err != nil {
		return nil, err
	}
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
	// A link warm-up reads every project's documents; it stops at the next
	// project, and nothing reads them once Close returns.
	pm.closing.Store(true)
	pm.linksWarm.Wait()
	pm.mu.Lock()
	if pm.stopWatch != nil {
		_ = pm.stopWatch()
		pm.stopWatch = nil
	}
	pm.mu.Unlock()
	pm.stopExtraWatches(everyWatch)
	// Saves batch into one history commit per window; whatever is still
	// waiting is recorded now rather than lost with the process.
	closeStores(pm.cachedStores(), 30*time.Second)
	// Persist the search index before dropping the workspace: it is a pure
	// cache, but losing it on every clean shutdown means paying for a full
	// rebuild on the next start for no reason. Then wait for its background
	// passes: they read project files the caller may be about to remove.
	pm.FlushSearchIndex()
	pm.closeSearchIndex()
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

// eisenhowerTitles are the Eisenhower template's quadrant titles per
// interface language, in do / schedule / delegate / eliminate order. The
// titles are written into the new project's graph, so they are chosen once,
// in the language of whoever created it.
var eisenhowerTitles = map[string][4]string{
	"en":    {"Important and urgent: do now", "Important, not urgent: schedule", "Urgent, not important: delegate", "Neither urgent nor important: drop"},
	"zh-TW": {"重要且緊急：立刻做", "重要不緊急：排程做", "緊急不重要：委派", "不緊急不重要：捨棄"},
}

// TemplateLanguage maps a language tag ("zh-TW", "zh-Hant-TW", "en-US", an
// Accept-Language list...) to one the templates are written in: Chinese
// for any zh tag, English for everything else.
func TemplateLanguage(tag string) string {
	first, _, _ := strings.Cut(tag, ",")
	first, _, _ = strings.Cut(first, ";")
	first = strings.ToLower(strings.TrimSpace(first))
	if first == "zh" || strings.HasPrefix(first, "zh-") {
		return "zh-TW"
	}
	return "en"
}

func templateGraph(template, language string) *engine.Graph {
	if template == "eisenhower" {
		quadrant := func(id, title, color string, x, y float64) engine.CanvasGroup {
			return engine.CanvasGroup{
				ID: id, Title: title, Color: color,
				X: x, Y: y, Width: 620, Height: 380,
			}
		}
		titles := eisenhowerTitles[TemplateLanguage(language)]
		return &engine.Graph{
			Version: 1,
			Type:    "workflow",
			UI: &engine.UIState{
				Groups: []engine.CanvasGroup{
					quadrant("quad-do", titles[0], "#9f2f2d", 0, 0),
					quadrant("quad-schedule", titles[1], "#1f6c9f", 640, 0),
					quadrant("quad-delegate", titles[2], "#956400", 0, 400),
					quadrant("quad-eliminate", titles[3], "#6f6c66", 640, 400),
				},
			},
		}
	}
	return &engine.Graph{Version: 1, Type: "story"}
}

// Activate opens name, first creating it from template when create is set;
// language is the creator's interface language for the template's titles.
func (pm *ProjectManager) Activate(name string, create bool, template, language string) error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	return pm.activateLocked(name, create, template, language)
}

func (pm *ProjectManager) activateLocked(name string, create bool, template, language string) error {
	dir, err := pm.Resolve(name)
	if err != nil {
		return err
	}
	if create {
		if err := pm.createLocked(name, dir, template, language, false); err != nil {
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
	stop, err := pm.watchStore(st)
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
	// The active project has its own watcher now; a second one would report
	// every external edit twice.
	pm.stopExtraWatches(func(key string) bool { return key == storeCacheKey(dir) })
	pm.warmSearch(dir)
	if err := store.WriteProjectFileAtomic(pm.workspace, pm.lastFile(), []byte(name)); err != nil {
		log.Printf("save last project: %v", err)
	}
	return nil
}

// SetPerAccount marks a server where each account has its own current
// project (internal/server/current_project.go). The active project is then
// only the default an account starts on, so opening a project for one caller
// (OpenForCaller, an import) leaves it where it is.
func (pm *ProjectManager) SetPerAccount(perAccount bool) { pm.perAccount.Store(perAccount) }

// OpenForCaller opens a project a request just made or asked for: it becomes
// the active project on loopback; with per-account projects it is only
// checked to open, and making it the caller's current one is the caller's
// business.
func (pm *ProjectManager) OpenForCaller(name string) error {
	if !pm.perAccount.Load() {
		return pm.Activate(name, false, "", "")
	}
	st, err := pm.StoreFor(name)
	if err != nil {
		return err
	}
	if _, _, err := st.LoadGraph(); err != nil {
		return fmt.Errorf("load project %q: %w", name, err)
	}
	pm.warmSearch(st.Root())
	return nil
}

// ActivateNew creates name from template, with the workflow a project a
// person creates starts with, and opens it; language is the creator's
// interface language for the template's titles.
func (pm *ProjectManager) ActivateNew(name, template, language string) error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	dir, err := pm.Resolve(name)
	if err != nil {
		return err
	}
	if err := pm.createLocked(name, dir, template, language, true); err != nil {
		return err
	}
	return pm.activateLocked(name, false, "", "")
}

// Create makes a new project, with the workflow a project a person creates
// starts with, without opening it. On a server with accounts
// each person has their own current project (internal/server), so one person
// creating a project must not move the project everyone else starts on.
func (pm *ProjectManager) Create(name, template, language string) error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	dir, err := pm.Resolve(name)
	if err != nil {
		return err
	}
	if err := pm.createLocked(name, dir, template, language, true); err != nil {
		return err
	}
	if _, _, err := pm.cachedStore(dir).LoadGraph(); err != nil {
		return fmt.Errorf("load project %q: %w", name, err)
	}
	return nil
}

// createLocked writes a new project. workflow gives it the workflow a project
// a person creates starts with (engine.NewProjectWorkflow); without it the
// project has none (legacy) until someone picks a preset, which is what a
// workspace's own first project and programmatic callers get.
func (pm *ProjectManager) createLocked(name, dir, template, language string, workflow bool) error {
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
	if _, err := store.StatProjectPath(pm.workspace, graphPath); os.IsNotExist(err) {
		// Written once, workflow included: a second write right after this
		// one could beat the history's first open and leave the creation
		// recorded twice, once as an edit undoing the other.
		graph := templateGraph(template, language)
		if workflow {
			graph.Workflow = engine.NewProjectWorkflow(graph.Type)
		}
		data, merr := engine.MarshalGraph(graph)
		if merr != nil {
			return merr
		}
		if werr := pm.cachedStore(dir).CreateGraph(data); werr != nil {
			return werr
		}
	} else if err != nil {
		return err
	}
	return nil
}

// watchStore starts the file watcher that reports external edits to st's
// project to the clients in its room.
func (pm *ProjectManager) watchStore(st *store.Store) (func() error, error) {
	return realtime.WatchWithCallback(st, pm.hub, func(kind, id string) {
		switch kind {
		case "node-changed", "graph-changed":
			pm.invalidateSearch(st.Root(), id)
		}
		if kind == "graph-changed" {
			// Possibly gone: the next lookup looks again.
			pm.lookups.forget(st.Root())
		}
	})
}

// Watch keeps a file watcher on st's project although it is not the active
// one: on a server with accounts, each person's current project needs its
// external edits reported as the active project's are. Watching a project
// already watched is a no-op, so callers may ask on every request. A watcher
// that cannot start is logged, not fatal: the project still serves, it only
// misses edits made outside Nodevas.
func (pm *ProjectManager) Watch(st *store.Store) {
	if st == nil || st == pm.Store() {
		return
	}
	key := storeCacheKey(st.Root())
	pm.watchMu.Lock()
	defer pm.watchMu.Unlock()
	if _, ok := pm.watches[key]; ok {
		return
	}
	stop, err := pm.watchStore(st)
	if err != nil {
		log.Printf("watch project %s: %v", st.Root(), err)
		return
	}
	if pm.watches == nil {
		pm.watches = make(map[string]func() error)
	}
	pm.watches[key] = stop
	pm.warmSearch(st.Root())
}

// Watching reports whether external edits to the project in dir are being
// reported: it is the active project, or Watch was asked for it.
func (pm *ProjectManager) Watching(dir string) bool {
	if active := pm.Store(); active != nil && storeCacheKey(active.Root()) == storeCacheKey(dir) {
		pm.mu.RLock()
		defer pm.mu.RUnlock()
		return pm.stopWatch != nil
	}
	pm.watchMu.Lock()
	defer pm.watchMu.Unlock()
	_, ok := pm.watches[storeCacheKey(dir)]
	return ok
}

// stopExtraWatches stops the watchers Watch started whose key matches.
func (pm *ProjectManager) stopExtraWatches(match func(key string) bool) {
	pm.watchMu.Lock()
	var stops []func() error
	for key, stop := range pm.watches {
		if match(key) {
			stops = append(stops, stop)
			delete(pm.watches, key)
		}
	}
	pm.watchMu.Unlock()
	for _, stop := range stops {
		_ = stop()
	}
}

func everyWatch(string) bool { return true }

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
		return pm.activateLocked("main", true, "", "")
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
	return pm.activateLocked(name, false, "", "")
}

func (pm *ProjectManager) SwitchWorkspace(root string) error {
	pm.activateMu.Lock()
	defer pm.activateMu.Unlock()
	previous := pm.workspace
	pm.stopExtraWatches(everyWatch)
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
// Key is a project's identity across the server: its directory, cleaned, and
// case-folded on Windows. Membership (internal/members) and the store cache
// both use it, so two spellings of one directory are one project.
func Key(dir string) string { return storeCacheKey(dir) }

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
	// The store's own writes are invisible to the watcher by design; this is
	// how they reach the search index and the project lookup cache.
	st.SetOnChange(func(path string) {
		pm.noteSearchChange(dir, path)
		pm.noteStoreChange(dir, path)
	})
	pm.stores[key] = st
	return st
}

// StoreFor returns the store for a named project without changing the active
// one. The project must already exist: this is a read/write path for existing
// content, not a creation path.
//
// A project found once is remembered (lookup_cache.go), so the requests that
// follow do not stat the disk.
func (pm *ProjectManager) StoreFor(name string) (*store.Store, error) {
	workspace := pm.workspaceRoot()
	if dir, ok := pm.lookups.get(workspace, name); ok {
		return pm.cachedStore(dir), nil
	}
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
	pm.lookups.remember(workspace, name, dir)
	return pm.cachedStore(dir), nil
}

// ListedStore is StoreFor for a project the catalog also lists: one that has
// not been detached. It costs a stat and one small file read, where List
// walks the workspace, so it can run on every request.
func (pm *ProjectManager) ListedStore(name string) (*store.Store, error) {
	st, err := pm.StoreFor(name)
	if err != nil {
		return nil, err
	}
	detached, err := pm.loadDetachedProjects()
	if err != nil {
		return nil, err
	}
	if projectIsDetached(name, detached) {
		return nil, fmt.Errorf("project %q is not in the workspace", name)
	}
	return st, nil
}

// NameFor is the name ?project= takes for the project in dir: its path below
// the workspace, slash-separated, or "." for the workspace root. It reports
// false for a directory outside the workspace.
func (pm *ProjectManager) NameFor(dir string) (string, bool) {
	rel, err := filepath.Rel(pm.Workspace(), dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// EvictStores drops the cached stores for dir and everything under it. Call it
// after the directory is renamed or deleted, so a later project of the same
// name never inherits a Store pointing at the old path.
func (pm *ProjectManager) EvictStores(dir string) {
	prefix := storeCacheKey(dir)
	under := func(key string) bool {
		return key == prefix || strings.HasPrefix(key, prefix+string(filepath.Separator))
	}
	// A watcher holds directory handles (Windows refuses the rename while it
	// does) and would outlive the path it watches.
	pm.stopExtraWatches(under)
	pm.lookups.forget(dir)
	pm.storesMu.Lock()
	var evicted []*store.Store
	for key, st := range pm.stores {
		if under(key) {
			evicted = append(evicted, st)
			delete(pm.stores, key)
		}
	}
	pm.storesMu.Unlock()
	// Pending history is recorded while the folder is still where the store
	// thinks it is; a rename then carries the repository along with it.
	closeStores(evicted, 10*time.Second)
}

// closeStores records each store's pending history and stops its timers.
func closeStores(stores []*store.Store, timeout time.Duration) {
	if len(stores) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, st := range stores {
		if err := st.Close(ctx); err != nil {
			log.Printf("history for %s: %v", st.Root(), err)
		}
	}
}

// cachedStores is every store this manager has handed out.
func (pm *ProjectManager) cachedStores() []*store.Store {
	pm.storesMu.Lock()
	defer pm.storesMu.Unlock()
	stores := make([]*store.Store, 0, len(pm.stores))
	for _, st := range pm.stores {
		stores = append(stores, st)
	}
	return stores
}

// FlushHistory records every project's pending history now, so a backup taken
// next holds it.
func (pm *ProjectManager) FlushHistory(ctx context.Context) error {
	var errs []error
	for _, st := range pm.cachedStores() {
		if err := st.FlushHistory(ctx); err != nil {
			errs = append(errs, fmt.Errorf("history for %s: %w", st.Root(), err))
		}
	}
	return errors.Join(errs...)
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
	// The move may take another account's current project with it. Its
	// watcher is started again by the next request that resolves to it.
	pm.stopExtraWatches(everyWatch)
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
