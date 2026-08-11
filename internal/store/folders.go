package store

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"nodevas/internal/engine"
)

// Node folders [B-06].
//
// A node's identity is its id, and the graph only ever refers to that id, so
// where the document sits on disk is presentation, not meaning: moving
// nodes/a.md into nodes/章節一/a.md changes nothing about edges, requires or
// logic gates. That is the whole point of folders — they organise the file
// tree without touching the dependency graph.
//
// The trade-off is that nodes/<id>.md is no longer a formula. Every reader
// goes through NodeDocPath, which consults a per-project index built by
// walking nodes/. The index is a cache with two safety nets: mutations
// invalidate it explicitly, and a lookup whose file has vanished rebuilds it,
// so a move made in Explorer or by git heals on the next read.
//
// nodes/<id>.pages and nodes/<id>.files stay siblings of the document and
// travel with it.

const (
	// MaxFolderDepth bounds nesting so a pathological tree cannot make path
	// handling or the sidebar unbounded.
	MaxFolderDepth = 8
	maxFolderName  = 64
)

var (
	ErrFolderName     = errors.New("invalid folder name")
	ErrFolderExists   = errors.New("folder already exists")
	ErrFolderMissing  = errors.New("folder not found")
	ErrFolderIntoSelf = errors.New("cannot move a folder into itself")
)

// windowsReserved are names Windows refuses to create regardless of extension.
// Rejecting them everywhere keeps a project portable between platforms.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidFolderName reports whether one path segment is usable as a node folder.
// It rejects separators, the payload-directory suffixes, and everything the
// slowest of the supported filesystems would choke on.
func ValidFolderName(name string) bool {
	if name == "" || len(name) > maxFolderName {
		return false
	}
	if name != strings.TrimSpace(name) || strings.HasSuffix(name, ".") {
		return false
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return false
	}
	if windowsReserved[strings.ToLower(name)] {
		return false
	}
	lower := strings.ToLower(name)
	// A folder must never be mistaken for a node's payload directory or
	// document, because those are addressed by the id alone.
	if strings.HasSuffix(lower, ".pages") ||
		strings.HasSuffix(lower, ".files") ||
		strings.HasSuffix(lower, ".md") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return false
		}
	}
	return true
}

// CleanFolderPath normalises a slash-separated folder path and reports whether
// every segment is valid. The empty path means the root of nodes/.
func CleanFolderPath(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, "\\", "/")
	segments := make([]string, 0, 4)
	for segment := range strings.SplitSeq(raw, "/") {
		if segment == "" {
			continue
		}
		if !ValidFolderName(segment) {
			return "", false
		}
		segments = append(segments, segment)
	}
	if len(segments) > MaxFolderDepth {
		return "", false
	}
	return path.Join(segments...), true
}

// ---------- the per-project index ----------

type nodeTree struct {
	mu sync.Mutex
	// locations maps node id to its folder path under nodes/ ("" is the root).
	locations map[string]string
	// folders is every folder path under nodes/, sorted, root excluded.
	folders []string
	loaded  bool
}

var nodeTrees sync.Map // project root -> *nodeTree

func treeFor(root string) *nodeTree {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	value, _ := nodeTrees.LoadOrStore(abs, &nodeTree{})
	return value.(*nodeTree)
}

// InvalidateNodeTree drops the cached layout for a project. Call it after any
// change to where node files live, including changes made through the watcher.
func InvalidateNodeTree(root string) {
	tree := treeFor(root)
	tree.mu.Lock()
	tree.loaded = false
	tree.locations = nil
	tree.folders = nil
	tree.mu.Unlock()
}

// isPayloadDir reports whether a directory under nodes/ belongs to a node
// rather than being a user-made folder.
func isPayloadDir(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".pages") || strings.HasSuffix(lower, ".files")
}

// scanNodeTree walks nodes/ and records where each document lives.
func scanNodeTree(root string) (map[string]string, []string) {
	locations := map[string]string{}
	folders := []string{}
	nodesDir := filepath.Join(root, "nodes")
	_ = filepath.WalkDir(nodesDir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			if current == nodesDir {
				return nil
			}
			return fs.SkipDir
		}
		rel, relErr := filepath.Rel(nodesDir, current)
		if relErr != nil {
			return fs.SkipDir
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == "." {
				return nil
			}
			// Payload directories and anything unusable as a folder are not
			// part of the tree the user organises.
			if isPayloadDir(entry.Name()) || !ValidFolderName(entry.Name()) {
				return fs.SkipDir
			}
			if strings.Count(rel, "/")+1 > MaxFolderDepth {
				return fs.SkipDir
			}
			folders = append(folders, rel)
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !engine.ValidNodeID(id) {
			return nil
		}
		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		// A duplicated id across folders would make the location ambiguous;
		// the shallowest copy wins so behaviour stays deterministic.
		if existing, ok := locations[id]; ok && len(existing) <= len(dir) {
			return nil
		}
		locations[id] = dir
		return nil
	})
	sort.Strings(folders)
	return locations, folders
}

func (t *nodeTree) load(root string) {
	if t.loaded {
		return
	}
	t.locations, t.folders = scanNodeTree(root)
	t.loaded = true
}

// nodeFolder returns the folder a node's document sits in ("" for the root of
// nodes/). Unknown nodes resolve to the root, which is where a new document is
// created.
func nodeFolder(root, id string) string {
	tree := treeFor(root)
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.load(root)
	folder, ok := tree.locations[id]
	if ok {
		if _, err := StatProjectPath(root, filepath.Join(root, "nodes", filepath.FromSlash(folder), id+".md")); err == nil {
			return folder
		}
		// The file moved behind our back: rescan rather than serve a path
		// that no longer exists.
		tree.loaded = false
		tree.load(root)
		folder, ok = tree.locations[id]
	}
	if !ok {
		return ""
	}
	return folder
}

// NodeDocPath is the absolute path of a node's Markdown document.
func NodeDocPath(root, id string) string {
	return filepath.Join(root, "nodes", filepath.FromSlash(nodeFolder(root, id)), id+".md")
}

// NodePagesPath is the absolute path of a node's subpage directory.
func NodePagesPath(root, id string) string {
	return filepath.Join(root, "nodes", filepath.FromSlash(nodeFolder(root, id)), id+".pages")
}

// NodeAttachmentsPath is the absolute path of a node's attachment directory.
func NodeAttachmentsPath(root, id string) string {
	return filepath.Join(root, "nodes", filepath.FromSlash(nodeFolder(root, id)), id+".files")
}

// WalkNodeDocuments visits every node document under nodes/, in any folder,
// passing the node id, its folder ("" for the root) and the absolute path.
func WalkNodeDocuments(root string, visit func(id, folder, absolute string) error) error {
	tree := treeFor(root)
	tree.mu.Lock()
	tree.loaded = false
	tree.load(root)
	locations := make(map[string]string, len(tree.locations))
	maps.Copy(locations, tree.locations)
	tree.mu.Unlock()

	ids := make([]string, 0, len(locations))
	for id := range locations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		folder := locations[id]
		absolute := filepath.Join(root, "nodes", filepath.FromSlash(folder), id+".md")
		if err := visit(id, folder, absolute); err != nil {
			return err
		}
	}
	return nil
}

// ListNodePayloadDirs finds every "<id><suffix>" directory under nodes/, at any
// folder depth, and maps the node id to its absolute path. Used by callers that
// have to audit what is on disk rather than resolve one known node.
func ListNodePayloadDirs(root, suffix string) map[string]string {
	found := map[string]string{}
	nodesDir := filepath.Join(root, "nodes")
	_ = filepath.WalkDir(nodesDir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			if current == nodesDir {
				return nil
			}
			return fs.SkipDir
		}
		if !entry.IsDir() || current == nodesDir {
			return nil
		}
		name := entry.Name()
		if isPayloadDir(name) {
			if id, ok := strings.CutSuffix(name, suffix); ok {
				if engine.ValidNodeID(id) {
					found[id] = current
				}
			}
			return fs.SkipDir
		}
		if !ValidFolderName(name) {
			return fs.SkipDir
		}
		return nil
	})
	return found
}

// ListNodeFolders returns every folder under nodes/, sorted parent-first.
func ListNodeFolders(root string) []string {
	tree := treeFor(root)
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.load(root)
	out := make([]string, len(tree.folders))
	copy(out, tree.folders)
	return out
}

// NodeFolderAssignments returns node id -> folder path for every document
// found on disk. Nodes at the root are omitted.
func NodeFolderAssignments(root string) map[string]string {
	tree := treeFor(root)
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.load(root)
	out := make(map[string]string, len(tree.locations))
	for id, folder := range tree.locations {
		if folder == "" {
			continue
		}
		out[id] = folder
	}
	return out
}

// ---------- Store operations ----------

// NodeFolders reports the folder tree and which node sits in which folder.
func (s *Store) NodeFolders() ([]string, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ListNodeFolders(s.root), NodeFolderAssignments(s.root)
}

func (s *Store) folderAbs(rel string) string {
	return filepath.Join(s.root, "nodes", filepath.FromSlash(rel))
}

// CreateNodeFolder creates one folder, including any missing parents.
func (s *Store) CreateNodeFolder(rel string) (string, error) {
	clean, ok := CleanFolderPath(rel)
	if !ok || clean == "" {
		return "", ErrFolderName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.folderAbs(clean)
	if _, err := s.statPath(target); err == nil {
		return "", ErrFolderExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := MkdirAllProjectPath(s.root, target, 0o755); err != nil {
		return "", err
	}
	InvalidateNodeTree(s.root)
	return clean, nil
}

// RenameNodeFolder renames a folder in place, keeping its parent.
func (s *Store) RenameNodeFolder(rel, name string) (string, error) {
	clean, ok := CleanFolderPath(rel)
	if !ok || clean == "" {
		return "", ErrFolderMissing
	}
	if !ValidFolderName(name) {
		return "", ErrFolderName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.folderAbs(clean)
	if info, err := s.statPath(source); err != nil || !info.IsDir() {
		return "", ErrFolderMissing
	}
	target := path.Join(path.Dir(clean), name)
	if target == "." {
		target = name
	}
	if strings.EqualFold(target, clean) && target != clean {
		// A case-only rename cannot go through the exists check on a
		// case-insensitive filesystem; rename straight onto itself.
		if err := RenameProjectPath(s.root, source, s.root, s.folderAbs(target)); err != nil {
			return "", err
		}
		InvalidateNodeTree(s.root)
		return target, nil
	}
	if _, err := s.statPath(s.folderAbs(target)); err == nil {
		return "", ErrFolderExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := RenameProjectPath(s.root, source, s.root, s.folderAbs(target)); err != nil {
		return "", err
	}
	InvalidateNodeTree(s.root)
	return target, nil
}

// MoveNodeFolder moves a folder under a new parent ("" is the root).
func (s *Store) MoveNodeFolder(rel, parent string) (string, error) {
	clean, ok := CleanFolderPath(rel)
	if !ok || clean == "" {
		return "", ErrFolderMissing
	}
	cleanParent, ok := CleanFolderPath(parent)
	if !ok {
		return "", ErrFolderName
	}
	name := path.Base(clean)
	target := path.Join(cleanParent, name)
	if target == clean {
		return clean, nil
	}
	if cleanParent == clean || strings.HasPrefix(cleanParent+"/", clean+"/") {
		return "", ErrFolderIntoSelf
	}
	if strings.Count(target, "/")+1 > MaxFolderDepth {
		return "", ErrFolderName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.folderAbs(clean)
	if info, err := s.statPath(source); err != nil || !info.IsDir() {
		return "", ErrFolderMissing
	}
	if cleanParent != "" {
		if info, err := s.statPath(s.folderAbs(cleanParent)); err != nil || !info.IsDir() {
			return "", ErrFolderMissing
		}
	}
	if _, err := s.statPath(s.folderAbs(target)); err == nil {
		return "", ErrFolderExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := RenameProjectPath(s.root, source, s.root, s.folderAbs(target)); err != nil {
		return "", err
	}
	InvalidateNodeTree(s.root)
	return target, nil
}

// DeleteNodeFolder removes a folder and lifts everything it held into the
// parent folder. Deleting a folder must never delete a node — the trash is the
// only place nodes go.
func (s *Store) DeleteNodeFolder(rel string) error {
	clean, ok := CleanFolderPath(rel)
	if !ok || clean == "" {
		return ErrFolderMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.folderAbs(clean)
	info, err := s.statPath(source)
	if err != nil || !info.IsDir() {
		return ErrFolderMissing
	}
	parent := path.Dir(clean)
	if parent == "." {
		parent = ""
	}
	if err := ValidateProjectTree(s.root, source); err != nil {
		return err
	}
	entries, err := s.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from := filepath.Join(source, entry.Name())
		to := filepath.Join(s.folderAbs(parent), entry.Name())
		if _, statErr := s.statPath(to); statErr == nil {
			return fmt.Errorf("上層已有同名項目 %q，請先改名", entry.Name())
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err := RenameProjectPath(s.root, from, s.root, to); err != nil {
			return err
		}
	}
	if err := s.removePath(source); err != nil {
		return err
	}
	InvalidateNodeTree(s.root)
	return nil
}

// MoveNodesToFolder moves whole nodes — document, subpages and attachments —
// into a folder. The graph is not read or written: only the files move.
func (s *Store) MoveNodesToFolder(ids []string, folder string) error {
	clean, ok := CleanFolderPath(folder)
	if !ok {
		return ErrFolderName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if clean != "" {
		if info, err := s.statPath(s.folderAbs(clean)); err != nil || !info.IsDir() {
			return ErrFolderMissing
		}
	}
	moved := false
	for _, id := range ids {
		if !engine.ValidNodeID(id) {
			return fmt.Errorf("invalid node id %q", id)
		}
		from := nodeFolder(s.root, id)
		if from == clean {
			continue
		}
		sourceDir := s.folderAbs(from)
		targetDir := s.folderAbs(clean)
		// The document must exist; the payload directories are optional.
		if _, err := s.statPath(filepath.Join(sourceDir, id+".md")); err != nil {
			return fmt.Errorf("node %q not found", id)
		}
		for _, name := range []string{id + ".md", id + ".pages", id + ".files"} {
			source := filepath.Join(sourceDir, name)
			if _, err := s.statPath(source); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			target := filepath.Join(targetDir, name)
			if _, err := s.statPath(target); err == nil {
				return fmt.Errorf("目標資料夾已有 %q", name)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			s.markSelfWrite(source)
			s.markSelfWrite(target)
			if err := RenameProjectPath(s.root, source, s.root, target); err != nil {
				return err
			}
		}
		moved = true
	}
	if moved {
		InvalidateNodeTree(s.root)
	}
	return nil
}
