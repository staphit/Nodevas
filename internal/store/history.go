// What the store keeps so a change can be undone: version history and trash.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

var historyVersionPattern = regexp.MustCompile(
	`^\d{8}-\d{6}\.\d{9}\.(?:yaml|md|txt|html|docx)$`)

const historyPerFile = 50

// historyCoalesceWindow is how long one file's history stays closed to further
// snapshots while the only thing being overwritten is this store's own previous
// write.
//
// The editor saves on its own now (web/src/state/autosave.ts), roughly once
// every second and a half of typing. One snapshot per save would fill this
// file's fifty slots in about two minutes and evict every version a person
// might actually want back — the feature would still be there and would no
// longer be useful. Coalescing over five minutes turns "a version per save"
// into "a version per five minutes of work", which is the granularity someone
// restoring an earlier draft is looking for anyway.
//
// The window deliberately does not apply to content this store did not write:
// see snapshotHistory.
const historyCoalesceWindow = 5 * time.Minute

// historyMark is what the store remembers about one file so it can tell its own
// auto-save chain from somebody else's edit.
type historyMark struct {
	// rev of the content this store last wrote to the file.
	rev string
	// when the file last had a snapshot taken.
	snapshotAt time.Time
}

// noteHistoryWrite records what this store just put on disk. Caller holds s.mu.
func (s *Store) noteHistoryWrite(path, rev string) {
	if s.historyMarks == nil {
		s.historyMarks = map[string]historyMark{}
	}
	mark := s.historyMarks[path]
	mark.rev = rev
	s.historyMarks[path] = mark
}

// forgetHistoryMark drops what we believed about a file, forcing the next write
// to snapshot. Caller holds s.mu.
func (s *Store) forgetHistoryMark(path string) {
	delete(s.historyMarks, path)
}

// snapshotHistory copies the current on-disk version of path into
// .vised/history/<rel>/<timestamp><ext> before it gets overwritten, unless the
// content it would copy is this store's own recent write.
//
// The exception is the whole point. Overwriting our own last write is one more
// step in an editing session that history already has a starting version for,
// so within historyCoalesceWindow it adds nothing. Overwriting anything else —
// a collaborator's save, an external editor, a restored version — is the case
// history exists for, and is always snapshotted however recently we wrote. That
// is what keeps "以我的版本覆蓋" on a conflict from throwing the other version
// away: the content being clobbered is not ours, so it is kept.
//
// Contract: the caller holds s.mu, because a snapshot only means anything
// paired with the write that follows it.
func (s *Store) snapshotHistory(path string) error {
	return s.snapshotHistoryWithin(path, historyCoalesceWindow)
}

// snapshotHistoryAlways snapshots whatever is on disk, no coalescing. For the
// writes that destroy something on purpose — a restore, a lossy format
// conversion — where the version being replaced is exactly what the user will
// want back if they change their mind.
func (s *Store) snapshotHistoryAlways(path string) error {
	return s.snapshotHistoryWithin(path, 0)
}

func (s *Store) snapshotHistoryWithin(path string, window time.Duration) error {
	data, err := s.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if window > 0 {
		mark, known := s.historyMarks[path]
		if known && mark.rev == Rev(data) && !mark.snapshotAt.IsZero() &&
			time.Since(mark.snapshotAt) < window {
			return nil
		}
	}
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.root, DataDir, "history", filepath.ToSlash(rel))
	if err := MkdirAllProjectPath(s.root, dir, 0o755); err != nil {
		return err
	}
	name := time.Now().UTC().Format("20060102-150405.000000000") + filepath.Ext(path)
	if err := s.WriteAtomic(filepath.Join(dir, name), data); err != nil {
		return err
	}
	if s.historyMarks == nil {
		s.historyMarks = map[string]historyMark{}
	}
	mark := s.historyMarks[path]
	mark.snapshotAt = time.Now()
	s.historyMarks[path] = mark
	return s.pruneHistory(dir)
}

func (s *Store) pruneHistory(dir string) error {
	entries, err := s.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) <= historyPerFile {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && historyVersionPattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= historyPerFile {
		return nil
	}
	sort.Strings(names) // timestamp names sort chronologically
	for _, n := range names[:len(names)-historyPerFile] {
		if err := s.removePath(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// WriteVersioned = keep the previous version + atomic write. Its only caller is the
// restore path, where the version being replaced is the one the user was just
// looking at, so keeping it is unconditional.
func (s *Store) WriteVersioned(path string, data []byte) error {
	return s.writeVersionedLocked(identity.Actor{}, "", path, data)
}

// writeVersionedLocked is WriteVersioned on behalf of actor. Caller holds s.mu.
func (s *Store) writeVersionedLocked(actor identity.Actor, kind, path string, data []byte) error {
	backend := s.history()
	if err := backend.beforeOverwrite(path, true); err != nil {
		return fmt.Errorf("snapshot %s: %w", filepath.Base(path), err)
	}
	if err := s.WriteAtomic(path, data); err != nil {
		return err
	}
	s.noteHistoryWrite(path, Rev(data))
	backend.wrote(actor, kind, []string{path})
	return nil
}

// HistoryVersion describes one version of a file. Name is what read and
// restore take: a copy's file name for the file backend, a commit ID for git.
type HistoryVersion struct {
	Name string `json:"name"`
	At   string `json:"at"`
	Size int64  `json:"size"`
	// Author is who saved this version, when the backend knows (git).
	Author string `json:"author,omitempty"`
}

// ListHistory returns the versions of a project-relative file, newest first.
func (s *Store) ListHistory(rel string) ([]HistoryVersion, error) {
	return s.history().list(rel)
}

// RestoreHistory rolls a file back to a version for actor. The current
// version is kept first, so a restore can itself be undone, and the restore
// is recorded at once rather than at the end of the save window.
func (s *Store) RestoreHistory(actor identity.Actor, rel, version string) error {
	backend := s.history()
	data, err := backend.read(rel, version)
	if err != nil {
		return err
	}
	target, err := backend.restoreTarget(rel)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if actor.IsAgent() {
		data, err = s.checkAgentRestoreLocked(actor, filepath.ToSlash(rel), data)
	}
	if err == nil {
		err = s.writeVersionedLocked(actor, "restore", target, data)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	backend.commitSoon()
	return nil
}

// checkAgentRestoreLocked holds an agent's restore to the rules of a normal
// write. graph.yaml is refused outright: an old graph would bring back kinds,
// write access and types without any of the checks that guard them. A node
// file needs write access to the node as it is now, and a main document's
// frontmatter is rewritten from the graph, so a restored snapshot can never
// make its kind or write_access disagree with graph.yaml. Caller holds s.mu.
func (s *Store) checkAgentRestoreLocked(actor identity.Actor, rel string, data []byte) ([]byte, error) {
	if actor.HasRole() {
		return nil, &ErrAgentRestoreDenied{Path: rel, Msg: fmt.Sprintf("role %s may not restore history; ask a person", actor.AgentRole)}
	}
	if rel == "graph.yaml" {
		return nil, &ErrAgentRestoreDenied{Path: rel, Msg: "agents may not restore graph.yaml; ask a person"}
	}
	id, mainDoc, ok := historyNodeID(rel)
	if !ok {
		return nil, &ErrAgentRestoreDenied{Path: rel, Msg: fmt.Sprintf("agents may not restore %q", rel)}
	}
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	n := g.NodeByID(id)
	if n == nil {
		return nil, &ErrAgentRestoreDenied{Path: rel,
			Msg: fmt.Sprintf("node %q is not on the board; a person can restore it from the trash", id)}
	}
	if err := checkNodeWrite(actor, n); err != nil {
		return nil, err
	}
	if !mainDoc {
		return data, nil
	}
	nf, err := engine.ParseNodeFile(data)
	if err != nil {
		return nil, &ErrAgentRestoreDenied{Path: rel,
			Msg: fmt.Sprintf("this version of %q cannot be read (%v); ask a person to restore it", rel, err)}
	}
	engine.SyncFrontmatter(nf, n)
	return engine.ComposeNodeFile(nf)
}

// historyNodeID names the node a history path belongs to and whether it is
// the node's main document (nodes/<id>.md) rather than a subpage
// (nodes/<id>.pages/<page>). historyDir has already validated the shape.
func historyNodeID(rel string) (id string, mainDoc, ok bool) {
	tail, isNode := strings.CutPrefix(rel, "nodes/")
	if !isNode {
		return "", false, false
	}
	if directory, _, isPage := strings.Cut(tail, "/"); isPage {
		id, ok = strings.CutSuffix(directory, ".pages")
		return id, false, ok
	}
	id, ok = strings.CutSuffix(tail, ".md")
	return id, true, ok
}

// ReadHistory returns the bytes of one version, for previewing it before
// deciding to restore it.
func (s *Store) ReadHistory(rel, version string) ([]byte, error) {
	return s.history().read(rel, version)
}

// listHistoryFiles is ListHistory for the file backend.
func (s *Store) listHistoryFiles(rel string) ([]HistoryVersion, error) {
	dir, err := s.historyDir(rel)
	if err != nil {
		return nil, err
	}
	entries, err := s.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []HistoryVersion{}, nil
		}
		return nil, err
	}
	out := []HistoryVersion{}
	for _, e := range entries {
		if e.IsDir() || !historyVersionPattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, HistoryVersion{Name: e.Name(), At: info.ModTime().UTC().Format(time.RFC3339), Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// readHistoryFile is ReadHistory for the file backend: one snapshot's bytes.
func (s *Store) readHistoryFile(rel, version string) ([]byte, error) {
	dir, err := s.historyDir(rel)
	if err != nil {
		return nil, err
	}
	if !historyVersionPattern.MatchString(version) ||
		filepath.Ext(version) != filepath.Ext(filepath.FromSlash(rel)) {
		return nil, fmt.Errorf("invalid version name")
	}
	return s.ReadFile(filepath.Join(dir, version))
}

// historyDir validates a project-relative path and maps it into history/.
// Two shapes are tracked: a node's main document (nodes/<id>.md) and one of
// its subpages (nodes/<id>.pages/<page>.<ext>), matching where
// snapshotHistory writes.
func (s *Store) historyDir(rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	if rel == "graph.yaml" {
		return filepath.Join(s.root, DataDir, "history", rel), nil
	}
	const nodePrefix = "nodes/"
	if !strings.HasPrefix(rel, nodePrefix) {
		return "", fmt.Errorf("history not tracked for %q", rel)
	}
	tail := strings.TrimPrefix(rel, nodePrefix)
	switch strings.Count(tail, "/") {
	case 0:
		if !strings.HasSuffix(tail, ".md") ||
			!engine.ValidNodeID(strings.TrimSuffix(tail, ".md")) {
			return "", fmt.Errorf("history not tracked for %q", rel)
		}
	case 1:
		dir, filename, _ := strings.Cut(tail, "/")
		nodeID, ok := strings.CutSuffix(dir, ".pages")
		if !ok || !engine.ValidNodeID(nodeID) {
			return "", fmt.Errorf("history not tracked for %q", rel)
		}
		extension := path.Ext(filename)
		if !historyPageExtensions[extension] ||
			!engine.ValidNodeID(strings.TrimSuffix(filename, extension)) {
			return "", fmt.Errorf("history not tracked for %q", rel)
		}
	default:
		return "", fmt.Errorf("history not tracked for %q", rel)
	}
	return filepath.Join(s.root, DataDir, "history", filepath.FromSlash(rel)), nil
}

// historyPageExtensions are the subpage files history is kept for — the same
// set pageFormatExtension stores, so every subpage format is recoverable.
var historyPageExtensions = map[string]bool{
	".md":   true,
	".txt":  true,
	".html": true,
	".docx": true,
}

type trashedNodePage struct {
	NodeID string `json:"nodeId"`
	PageID string `json:"pageId"`
	Title  string `json:"title"`
	Format string `json:"format,omitempty"`
	// Content holds the text of records written before pages could be
	// binary; Data carries the exact bytes and wins when both are present.
	Content string `json:"content,omitempty"`
	Data    []byte `json:"data,omitempty"`
	At      string `json:"at"`
}

// bytes returns the page file to restore, whichever field carries it.
func (p trashedNodePage) bytes() []byte {
	if len(p.Data) > 0 {
		return p.Data
	}
	return []byte(p.Content)
}

// TrashItem is one soft-deleted node or subpage.
type TrashItem struct {
	File     string `json:"file"`
	Kind     string `json:"kind"`
	NodeID   string `json:"nodeId"`
	ParentID string `json:"parentId,omitempty"`
	Title    string `json:"title,omitempty"`
	At       string `json:"at"`
}

func (s *Store) ListTrash() ([]TrashItem, error) {
	dir := filepath.Join(s.root, DataDir, "trash")
	entries, err := s.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []TrashItem{}, nil
		}
		return nil, err
	}
	out := []TrashItem{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), TmpPrefix) || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, _ := e.Info()
		item := TrashItem{File: e.Name(), Kind: "node"}
		if info != nil {
			item.At = info.ModTime().UTC().Format(time.RFC3339)
		}
		// name: 20060102-150405-<id>.md
		base := strings.TrimSuffix(e.Name(), ".md")
		if parts := strings.SplitN(base, "-", 3); len(parts) == 3 {
			item.NodeID = parts[2]
		}
		out = append(out, item)
	}
	pageDir := filepath.Join(dir, "pages")
	pageEntries, pageErr := s.ReadDir(pageDir)
	if pageErr != nil && !os.IsNotExist(pageErr) {
		return nil, pageErr
	}
	for _, entry := range pageEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := s.ReadFile(filepath.Join(pageDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var page trashedNodePage
		if json.Unmarshal(data, &page) != nil ||
			!engine.ValidNodeID(page.NodeID) ||
			!engine.ValidNodeID(page.PageID) {
			continue
		}
		out = append(out, TrashItem{
			File:     filepath.ToSlash(filepath.Join("pages", entry.Name())),
			Kind:     "page",
			NodeID:   page.PageID,
			ParentID: page.NodeID,
			Title:    page.Title,
			At:       page.At,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File > out[j].File })
	return out, nil
}

// RestoreTrash moves a trashed node file back and re-registers the node in
// graph.yaml (definition rebuilt from the file's frontmatter snapshot).
func (s *Store) RestoreTrash(actor identity.Actor, file string) (string, error) {
	// A restore is recorded at once, like a delete; with nothing written this
	// is a no-op.
	defer s.history().commitSoon()
	if strings.HasPrefix(file, "pages/") {
		return s.restoreTrashedNodePage(actor, file)
	}
	if strings.ContainsAny(file, `/\`) || filepath.Base(file) != file || !strings.HasSuffix(file, ".md") {
		return "", fmt.Errorf("invalid trash file")
	}
	src := filepath.Join(s.root, DataDir, "trash", file)
	data, err := s.ReadFile(src)
	if err != nil {
		return "", err
	}
	nf, err := engine.ParseNodeFile(data)
	if err != nil {
		return "", err
	}
	id, _ := nf.Meta["id"].(string)
	if id == "" {
		base := strings.TrimSuffix(file, ".md")
		if parts := strings.SplitN(base, "-", 3); len(parts) == 3 {
			id = parts[2]
		}
	}
	if !engine.ValidNodeID(id) {
		return "", fmt.Errorf("cannot determine node id for %q", file)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	if g.NodeByID(id) != nil {
		return "", fmt.Errorf("node %q already exists", id)
	}
	if _, err := s.statPath(s.NodePath(id)); err == nil {
		return "", fmt.Errorf("node file %q already exists", id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	n := &engine.Node{ID: id}
	hydrateNodeFromMeta(n, nf.Meta)
	g.Nodes = append(g.Nodes, n)
	if err := ValidateGraphForStorage(g); err != nil {
		return "", err
	}
	gdata, err := s.marshalGraph(g)
	if err != nil {
		return "", err
	}
	if err := s.applyUpdatesLocked(actor, []fileUpdate{
		{
			path:          s.NodePath(id),
			data:          data,
			checkRevision: true,
			expectedRev:   "",
		},
		{
			path:          s.GraphPath(),
			data:          gdata,
			checkRevision: true,
			expectedRev:   graphRev,
		},
	}); err != nil {
		return "", err
	}
	if err := s.removePath(src); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("node restored but trash cleanup failed: %w", err)
	}
	return id, nil
}

func (s *Store) restoreTrashedNodePage(actor identity.Actor, file string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(file))
	if !strings.HasPrefix(clean, "pages/") ||
		strings.Count(clean, "/") != 1 ||
		!strings.HasSuffix(clean, ".json") {
		return "", fmt.Errorf("invalid page trash file")
	}
	src := filepath.Join(s.root, DataDir, "trash", filepath.FromSlash(clean))
	data, err := s.ReadFile(src)
	if err != nil {
		return "", err
	}
	var page trashedNodePage
	if err := json.Unmarshal(data, &page); err != nil {
		return "", fmt.Errorf("invalid page trash record: %w", err)
	}
	if !engine.ValidNodeID(page.NodeID) || !engine.ValidNodeID(page.PageID) {
		return "", fmt.Errorf("invalid page trash record")
	}
	title := strings.TrimSpace(page.Title)
	if title == "" || len(title) > 256 {
		return "", fmt.Errorf("invalid page title")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	if g.NodeByID(page.NodeID) == nil {
		return "", fmt.Errorf("parent node %q no longer exists", page.NodeID)
	}
	manifest, err := s.LoadNodePagesManifest(page.NodeID)
	if err != nil {
		return "", err
	}
	for _, current := range manifest.Pages {
		if current.ID == page.PageID {
			return "", fmt.Errorf("page %q already exists", page.PageID)
		}
	}
	if len(manifest.Pages) >= maxNodePages {
		return "", fmt.Errorf("node pages reached the maximum of %d", maxNodePages)
	}
	format, err := NormalizePageFormat(page.Format)
	if err != nil {
		return "", err
	}
	manifest.Pages = append(manifest.Pages, NodePageInfo{
		ID:     page.PageID,
		Title:  title,
		Format: format,
	})
	manifestData, err := marshalNodePagesManifest(manifest)
	if err != nil {
		return "", err
	}
	if err := s.applyUpdatesLocked(actor, []fileUpdate{
		{path: s.NodePagesManifestPath(page.NodeID), data: manifestData},
		{
			path:          s.NodePagePath(page.NodeID, page.PageID, format),
			data:          page.bytes(),
			checkRevision: true,
			expectedRev:   "",
		},
	}); err != nil {
		return "", err
	}
	if err := s.removePath(src); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("page restored but trash cleanup failed: %w", err)
	}
	return page.NodeID, nil
}
