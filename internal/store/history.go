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

// WriteVersioned = snapshot previous version + atomic write. Its only caller is the
// restore path, where the version being replaced is the one the user was just
// looking at, so the snapshot is unconditional.
func (s *Store) WriteVersioned(path string, data []byte) error {
	if err := s.snapshotHistoryAlways(path); err != nil {
		return fmt.Errorf("snapshot %s: %w", filepath.Base(path), err)
	}
	if err := s.WriteAtomic(path, data); err != nil {
		return err
	}
	s.noteHistoryWrite(path, Rev(data))
	return nil
}

// HistoryVersion describes one snapshot of a file.
type HistoryVersion struct {
	Name string `json:"name"`
	At   string `json:"at"`
	Size int64  `json:"size"`
}

// ListHistory returns snapshots for a project-relative file, newest first.
func (s *Store) ListHistory(rel string) ([]HistoryVersion, error) {
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

// RestoreHistory rolls a file back to a snapshot. The current version is
// snapshotted first, so a restore can itself be undone.
func (s *Store) RestoreHistory(rel, version string) error {
	dir, err := s.historyDir(rel)
	if err != nil {
		return err
	}
	if !historyVersionPattern.MatchString(version) ||
		filepath.Ext(version) != filepath.Ext(filepath.FromSlash(rel)) {
		return fmt.Errorf("invalid version name")
	}
	data, err := s.ReadFile(filepath.Join(dir, version))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.WriteVersioned(filepath.Join(s.root, filepath.FromSlash(rel)), data)
}

// ReadHistory returns the bytes of one snapshot, for previewing a version
// before deciding to restore it.
func (s *Store) ReadHistory(rel, version string) ([]byte, error) {
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
func (s *Store) RestoreTrash(file string) (string, error) {
	if strings.HasPrefix(file, "pages/") {
		return s.restoreTrashedNodePage(file)
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
	if err := s.applyUpdatesLocked([]fileUpdate{
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

func (s *Store) restoreTrashedNodePage(file string) (string, error) {
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
	if err := s.applyUpdatesLocked([]fileUpdate{
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
