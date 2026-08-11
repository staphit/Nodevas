package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"nodevas/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/lockfile"
)

// Workspace catalog and project discovery.

var ProjectNamePattern = regexp.MustCompile(`^[\p{L}\p{N}_][\p{L}\p{N}_ .-]*$`)

// graphNodeCount answers "how many nodes does this graph.yaml hold" without
// re-parsing the YAML every time. List() runs on every search query and every
// project listing, and a full ParseGraph of a large graph purely to read
// len(g.Nodes) dominated that cost.
//
// The cache is keyed by size+modtime, so an edit through any path — the API,
// an external editor, a restored backup — invalidates it. A stat is all a warm
// lookup costs. Entries are never evicted by age: the key set is bounded by
// the number of projects in the workspace, and a deleted project's entry is
// only a few words.
var graphCountCache sync.Map // absolute graph.yaml path -> graphCountEntry

type graphCountEntry struct {
	size    int64
	modTime time.Time
	nodes   int
}

// graphNodeCount reports the node count of the graph.yaml at path. ok is false
// when the file does not exist or cannot be parsed, which callers read as
// "this directory is a grouping folder, not a project".
func graphNodeCount(root, path string) (nodes int, ok bool) {
	info, err := store.StatProjectPath(root, path)
	if err != nil || info.IsDir() {
		return 0, false
	}
	if cached, hit := graphCountCache.Load(path); hit {
		entry := cached.(graphCountEntry)
		if entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
			return entry.nodes, true
		}
	}
	data, err := store.ReadProjectFile(root, path)
	if err != nil {
		return 0, false
	}
	graph, err := engine.ParseGraph(data)
	if err != nil {
		// Unparseable graph.yaml still marks a project, just an unreadable one.
		// Do not cache: the next call should retry once the file is repaired.
		return 0, true
	}
	nodes = len(graph.Nodes)
	graphCountCache.Store(path, graphCountEntry{
		size:    info.Size(),
		modTime: info.ModTime(),
		nodes:   nodes,
	})
	return nodes, true
}

var ignoredProjectDirectories = map[string]bool{
	store.DataDir: true,
	"nodes":       true,
	"run":         true,
}

// ProjectManager serves one workspace directory that contains multiple
// sub-projects (each its own folder with graph.yaml + nodes/), so
// unrelated graphs never mix. One sub-project is active at a time.
type WorkspaceInfo struct {
	Path     string `json:"path"`
	Label    string `json:"label"`
	Active   bool   `json:"active"`
	Projects int    `json:"projects"`
}

func WorkspacePathEqual(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func (pm *ProjectManager) workspaceCatalogFile() string {
	return filepath.Join(pm.catalogRoot, "workspaces.json")
}

func (pm *ProjectManager) activeWorkspaceFile() string {
	return filepath.Join(pm.catalogRoot, "active-workspace")
}

func (pm *ProjectManager) catalogLockFile() string {
	return filepath.Join(pm.catalogRoot, "catalog.lock")
}

// catalogFileLockTimeout is short on purpose: the catalog critical section is
// a read, a comparison and one atomic write, so a wait longer than this means
// something is wrong rather than merely busy.
const catalogFileLockTimeout = 2 * time.Second

// lockCatalogFile guards the workspaces.json read-modify-write against other
// *processes*. catalogMu only covers this one; the catalog lives in the shared
// app config directory, so every Nodevas instance on the machine writes the
// same file regardless of which workspace it serves.
func (pm *ProjectManager) lockCatalogFile() (func(), error) {
	if err := os.MkdirAll(pm.catalogRoot, 0o755); err != nil {
		return nil, err
	}
	lock, err := lockfile.AcquireWait(pm.catalogLockFile(), catalogFileLockTimeout)
	if err != nil {
		if errors.Is(err, lockfile.ErrLocked) {
			return nil, fmt.Errorf("工作區清單正由其他 Nodevas 程序修改，請稍後再試")
		}
		return nil, err
	}
	return func() { _ = lock.Release() }, nil
}

func (pm *ProjectManager) NormalizeWorkspaceRoot(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("請提供工作區資料夾路徑")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("找不到工作區 %q", abs)
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%q 不是資料夾", real)
	}
	return real, nil
}

func (pm *ProjectManager) LoadWorkspaceRoots() ([]string, error) {
	return pm.loadWorkspaceRootsFile(pm.workspaceCatalogFile())
}

func (pm *ProjectManager) loadWorkspaceRootsFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var saved []string
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("讀取工作區清單失敗：%w", err)
	}
	roots := make([]string, 0, len(saved))
	for _, savedPath := range saved {
		root, normalizeErr := pm.NormalizeWorkspaceRoot(savedPath)
		if normalizeErr != nil {
			continue
		}
		found := false
		for _, existing := range roots {
			if WorkspacePathEqual(existing, root) {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, root)
		}
	}
	return roots, nil
}

func (pm *ProjectManager) SaveWorkspaceRoots(roots []string) error {
	data, err := json.MarshalIndent(roots, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(pm.workspaceCatalogFile()), 0o755); err != nil {
		return err
	}
	return store.NewStore(pm.catalogRoot).WriteAtomic(pm.workspaceCatalogFile(), data)
}

func (pm *ProjectManager) SaveActiveWorkspace() error {
	if err := os.MkdirAll(filepath.Dir(pm.activeWorkspaceFile()), 0o755); err != nil {
		return err
	}
	return store.NewStore(pm.catalogRoot).WriteAtomic(
		pm.activeWorkspaceFile(),
		[]byte(pm.workspace),
	)
}

func workspaceProjectCount(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root &&
				(ignoredProjectDirectories[entry.Name()] ||
					strings.HasPrefix(entry.Name(), ".") ||
					strings.HasSuffix(entry.Name(), ".pages") ||
					strings.HasSuffix(entry.Name(), ".files") ||
					entry.Type()&os.ModeSymlink != 0) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "graph.yaml" {
			count++
		}
		return nil
	})
	return count
}

func (pm *ProjectManager) WorkspaceInfos() ([]WorkspaceInfo, error) {
	pm.catalogMu.Lock()
	defer pm.catalogMu.Unlock()
	roots, err := pm.LoadWorkspaceRoots()
	if err != nil {
		return nil, err
	}
	infos := make([]WorkspaceInfo, 0, len(roots))
	for _, root := range roots {
		infos = append(infos, WorkspaceInfo{
			Path:     root,
			Label:    filepath.Base(filepath.Clean(root)),
			Active:   WorkspacePathEqual(root, pm.workspace),
			Projects: workspaceProjectCount(root),
		})
	}
	return infos, nil
}

type ProjectInfo struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Parent string `json:"parent,omitempty"`
	Depth  int    `json:"depth"`
	Path   string `json:"path"`
	Nodes  int    `json:"nodes"`
	// IsFolder marks a plain workspace directory (no graph.yaml): it groups
	// projects in the tree but cannot be opened itself.
	IsFolder bool `json:"isFolder,omitempty"`
}

func (pm *ProjectManager) detachedProjectsFile() string {
	return filepath.Join(pm.workspace, store.DataDir, "detached-projects.json")
}

func (pm *ProjectManager) loadDetachedProjects() (map[string]bool, error) {
	out := map[string]bool{}
	data, err := store.ReadProjectFile(pm.workspace, pm.detachedProjectsFile())
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("read detached project list: %w", err)
	}
	for _, name := range names {
		if name != "." && ValidProjectPath(name) {
			out[name] = true
		}
	}
	return out, nil
}

func (pm *ProjectManager) saveDetachedProjects(projects map[string]bool) error {
	names := make([]string, 0, len(projects))
	for name := range projects {
		if name != "." && ValidProjectPath(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return store.NewStore(pm.workspace).WriteAtomic(pm.detachedProjectsFile(), data)
}

func projectIsDetached(name string, detached map[string]bool) bool {
	for detachedName := range detached {
		if name == detachedName || strings.HasPrefix(name, detachedName+"/") {
			return true
		}
	}
	return false
}

// List recursively scans the workspace for projects (folders holding graph.yaml).
// The workspace root itself is represented as project "." when it has one.
func (pm *ProjectManager) List() ([]ProjectInfo, error) {
	detached, err := pm.loadDetachedProjects()
	if err != nil {
		return nil, err
	}
	var out []ProjectInfo
	add := func(name, dir string) {
		label := filepath.Base(dir)
		depth := strings.Count(filepath.ToSlash(name), "/")
		parent := ""
		if name == "." {
			label = filepath.Base(pm.workspace)
			depth = 0
		} else if slash := strings.LastIndex(name, "/"); slash >= 0 {
			parent = name[:slash]
		}
		info := ProjectInfo{
			Name:   name,
			Label:  label,
			Parent: parent,
			Depth:  depth,
			Path:   dir,
		}
		if nodes, isProject := graphNodeCount(pm.workspace, filepath.Join(dir, "graph.yaml")); isProject {
			info.Nodes = nodes
		} else {
			info.IsFolder = true
		}
		out = append(out, info)
	}
	if _, err := store.StatProjectPath(pm.workspace, filepath.Join(pm.workspace, "graph.yaml")); err == nil {
		add(".", pm.workspace)
	}
	err = filepath.WalkDir(pm.workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == pm.workspace {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if ignoredProjectDirectories[entry.Name()] || entry.Type()&os.ModeSymlink != 0 ||
			strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".pages") ||
			strings.HasSuffix(entry.Name(), ".files") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(pm.workspace, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if projectIsDetached(name, detached) {
			return filepath.SkipDir
		}
		// Plain directories (no graph.yaml) show up as grouping folders.
		add(name, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		left := strings.Split(out[i].Name, "/")
		right := strings.Split(out[j].Name, "/")
		for index := 0; index < len(left) && index < len(right); index++ {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return len(left) < len(right)
	})
	return out, nil
}

func ValidProjectPath(name string) bool {
	if name == "." {
		return true
	}
	if name == "" || len(name) > 256 || strings.Contains(name, `\`) {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." || ignoredProjectDirectories[segment] ||
			!ProjectNamePattern.MatchString(segment) {
			return false
		}
	}
	return true
}

// Resolve maps a project path to its directory, confined to the workspace.
func (pm *ProjectManager) Resolve(name string) (string, error) {
	if name == "." {
		return pm.workspace, nil
	}
	if !ValidProjectPath(name) {
		return "", fmt.Errorf("invalid project name %q", name)
	}
	candidate := filepath.Join(pm.workspace, filepath.FromSlash(name))
	rel, err := filepath.Rel(pm.workspace, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project %q resolves outside the workspace", name)
	}
	if _, err := store.StatProjectPath(pm.workspace, candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("project %q: %w", name, err)
	}
	return candidate, nil
}

func pathParent(name string) string {
	if name == "." {
		return ""
	}
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		return name[:slash]
	}
	return ""
}

// ---- HTTP handlers ----

// PostProjectOpen switches (or creates, with create=true) a sub-project.

// AddWorkspaceRoot records a workspace root in the catalog if it is not there
// already. The read, the check and the write are one critical section: two
// concurrent adds must not each write a list missing the other's entry.
func (pm *ProjectManager) AddWorkspaceRoot(root string) error {
	pm.catalogMu.Lock()
	defer pm.catalogMu.Unlock()
	unlock, err := pm.lockCatalogFile()
	if err != nil {
		return err
	}
	defer unlock()
	roots, err := pm.LoadWorkspaceRoots()
	if err != nil {
		return err
	}
	for _, existing := range roots {
		if WorkspacePathEqual(existing, root) {
			return nil
		}
	}
	return pm.SaveWorkspaceRoots(append(roots, root))
}

// WorkspaceRoots returns the catalog's workspace roots.
func (pm *ProjectManager) WorkspaceRoots() ([]string, error) {
	pm.catalogMu.Lock()
	defer pm.catalogMu.Unlock()
	return pm.LoadWorkspaceRoots()
}
