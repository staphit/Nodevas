// The project store: one directory on disk, one mutex, and the reads and writes every other layer goes through. State and the journal live here too because they are written under the same lock as the graph.

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/workspace"
)

const (
	DataDir       = workspace.DataDir
	TmpPrefix     = ".tmp-"
	MaxGraphNodes = 10_000
	maxGraphEdges = 50_000
	maxGraphUsers = 10_000
	maxLogicGates = 10_000
	maxNodePages  = 256
	// maxNodeLinks bounds a node's named references; they are a menu for the
	// `/` inserter, not a database.
	maxNodeLinks        = 64
	maxJournalLineBytes = 64 << 10
	// maxJournalBytes is only an absolute sanity bound on one journal
	// segment. Compaction (see journal.go) rotates the file at
	// journalCompactBytes, an eighth of this, so a normally operating
	// project never approaches it.
	maxJournalBytes = 64 << 20
)

// ErrConflict is returned when an optimistic-lock revision check fails.
type ErrConflict struct {
	DiskRev     string
	DiskContent string
}

func (e *ErrConflict) Error() string { return "revision conflict" }

// Rev is the optimistic-lock fingerprint of file content.
func Rev(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

// Store owns all file access for one project directory.
// Layout: graph.yaml, nodes/<id>.md, run/{state.json,journal.jsonl},
// .vised/{history,trash,drafts}.
type Store struct {
	mu      sync.Mutex
	mediaMu sync.Mutex
	root    string

	selfMu     sync.Mutex
	selfWrites map[string]time.Time

	// sharedStatuses supplies the workspace-wide lifecycle vocabulary; see
	// workspacedefs.go. Absent for a store nobody wired one into.
	sharedMu       sync.Mutex
	sharedStatuses func() []engine.StatusDefinition

	// compactFailedAt throttles retries of a failing journal compaction.
	// Guarded by mu.
	compactFailedAt time.Time

	// historyMarks is what lets version history survive auto-saving editors;
	// see snapshotHistory. Guarded by mu, like every other write path here.
	historyMarks map[string]historyMark

	// deleteCleanupFault is a deterministic test seam for a failure after an
	// authoritative delete commit. Production leaves it nil. Guarded by mu.
	deleteCleanupFault func(deleteCleanupRecord) error
}

func NewStore(root string) *Store {
	s := &Store{
		root:         root,
		selfWrites:   map[string]time.Time{},
		historyMarks: map[string]historyMark{},
	}
	// A directory can be replaced wholesale between stores (import, copy,
	// remove and recreate), so never inherit a previous scan of it.
	InvalidateNodeTree(root)
	s.cleanupTmp()
	s.retryDeleteCleanups()
	return s
}

func (s *Store) Root() string      { return s.root }
func (s *Store) GraphPath() string { return filepath.Join(s.root, "graph.yaml") }
func (s *Store) StatePath() string { return filepath.Join(s.root, "run", "state.json") }
func (s *Store) JournalPath() string {
	return filepath.Join(s.root, "run", "journal.jsonl")
}

// Node files live in nodes/, optionally inside user-made folders; see
// node_folders.go for how a path is resolved from an id.
func (s *Store) NodePath(id string) string {
	return NodeDocPath(s.root, id)
}
func (s *Store) NodePagesDir(id string) string {
	return NodePagesPath(s.root, id)
}
func (s *Store) NodePagesManifestPath(id string) string {
	return filepath.Join(s.NodePagesDir(id), "pages.json")
}
func (s *Store) NodePagePath(nodeID, pageID, format string) string {
	extension, ok := pageFormatExtension[format]
	if !ok {
		extension = ".md"
	}
	return filepath.Join(s.NodePagesDir(nodeID), pageID+extension)
}
func (s *Store) draftPath(id string) string {
	return filepath.Join(s.root, DataDir, "drafts", id+".md")
}

// cleanupTmp removes torn temp files left by a crash mid-save.
func (s *Store) cleanupTmp() {
	dirs := []string{
		s.root,
		filepath.Join(s.root, "run"),
		filepath.Join(s.root, DataDir, "trash"),
		filepath.Join(s.root, DataDir, "drafts"),
	}
	// nodes/ nests, so every folder in it can hold a torn temp file.
	nodesDir := filepath.Join(s.root, "nodes")
	dirs = append(dirs, nodesDir)
	for _, folder := range ListNodeFolders(s.root) {
		dirs = append(dirs, filepath.Join(nodesDir, filepath.FromSlash(folder)))
	}
	for _, dir := range dirs {
		entries, err := s.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), TmpPrefix) {
				_ = s.removePath(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// ---------- self-write suppression (file watcher echo) ----------

func (s *Store) WasSelfWrite(path string) bool {
	s.selfMu.Lock()
	defer s.selfMu.Unlock()
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	now := time.Now()
	for candidate, writtenAt := range s.selfWrites {
		if now.Sub(writtenAt) > 2*time.Second {
			delete(s.selfWrites, candidate)
		}
	}
	t, ok := s.selfWrites[abs]
	if !ok {
		return false
	}
	return now.Sub(t) <= 2*time.Second
}

func (s *Store) markSelfWrite(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	s.selfMu.Lock()
	now := time.Now()
	for candidate, writtenAt := range s.selfWrites {
		if now.Sub(writtenAt) > 2*time.Second {
			delete(s.selfWrites, candidate)
		}
	}
	s.selfWrites[abs] = time.Now()
	s.selfMu.Unlock()
}

// ---------- atomic write + local history ----------

// WriteAtomic writes via temp file + rename so a crash can never leave a
// half-written file (the VS Code / SQLite pattern).
func (s *Store) WriteAtomic(path string, data []byte) error {
	return writeProjectFileAtomic(s.root, path, data, 0o644, s.markSelfWrite)
}

type fileUpdate struct {
	path          string
	data          []byte
	before        []byte
	existed       bool
	checkRevision bool
	expectedRev   string
}

// applyUpdatesLocked writes a small group of files with rollback. Callers order the
// graph last, so readers never observe a graph that references unwritten node
// content.
//
// The caller must hold s.mu — the compiler cannot check that, hence the name.
func (s *Store) applyUpdatesLocked(updates []fileUpdate) error {
	for i := range updates {
		before, err := s.ReadFile(updates[i].path)
		switch {
		case err == nil:
			updates[i].before = before
			updates[i].existed = true
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
		if updates[i].checkRevision {
			actualRev := ""
			if updates[i].existed {
				actualRev = Rev(updates[i].before)
			}
			if actualRev != updates[i].expectedRev {
				return &ErrConflict{DiskRev: actualRev, DiskContent: string(updates[i].before)}
			}
		}
		if updates[i].existed && bytes.Equal(updates[i].before, updates[i].data) {
			continue
		}
		if err := s.snapshotHistory(updates[i].path); err != nil {
			return err
		}
	}

	applied := make([]fileUpdate, 0, len(updates))
	for _, update := range updates {
		if update.existed && bytes.Equal(update.before, update.data) {
			continue
		}
		if err := s.WriteAtomic(update.path, update.data); err != nil {
			combinedErr := err
			for i := len(applied) - 1; i >= 0; i-- {
				previous := applied[i]
				// A rolled-back file is no longer the content this store
				// believes it wrote, so its history mark has to go with it:
				// the next write must snapshot rather than coalesce.
				s.forgetHistoryMark(previous.path)
				if previous.existed {
					if rollbackErr := s.WriteAtomic(previous.path, previous.before); rollbackErr != nil {
						combinedErr = errors.Join(combinedErr, fmt.Errorf("rollback %s: %w", previous.path, rollbackErr))
					}
				} else {
					s.markSelfWrite(previous.path)
					if rollbackErr := s.removePath(previous.path); rollbackErr != nil &&
						!errors.Is(rollbackErr, os.ErrNotExist) {
						combinedErr = errors.Join(combinedErr, fmt.Errorf("rollback %s: %w", previous.path, rollbackErr))
					}
				}
			}
			return combinedErr
		}
		s.noteHistoryWrite(update.path, Rev(update.data))
		applied = append(applied, update)
	}
	return nil
}
func (s *Store) checkRevision(path, baseRev string) error {
	current, err := s.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if baseRev == "" {
				return nil
			}
			return &ErrConflict{}
		}
		return err
	}
	currentRev := Rev(current)
	if currentRev != baseRev {
		return &ErrConflict{DiskRev: currentRev, DiskContent: string(current)}
	}
	return nil
}

// ---------- drafts (hot exit) ----------

func (s *Store) SaveDraft(id, content string) error {
	if !engine.ValidNodeID(id) {
		return fmt.Errorf("invalid node id")
	}
	return s.WriteAtomic(s.draftPath(id), []byte(content))
}

func (s *Store) LoadDraft(id string) (string, error) {
	if !engine.ValidNodeID(id) {
		return "", fmt.Errorf("invalid node id")
	}
	data, err := s.ReadFile(s.draftPath(id))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Store) DeleteDraft(id string) error {
	if !engine.ValidNodeID(id) {
		return fmt.Errorf("invalid node id")
	}
	err := s.removePath(s.draftPath(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ---------- run state (journal-backed) ----------

// LoadState rebuilds run state from run/checkpoint.json plus the journal
// events recorded after it (the journal stays the source of truth; the
// checkpoint is only a replay shortcut). state.json is consulted for runtime
// flag overrides and current cache state.
func (s *Store) LoadState() (*engine.RunState, error) {
	rs, _, err := s.replayJournal()
	if err != nil {
		return nil, err
	}
	// Merge the current state cache for flags and the pre-journal fallback.
	if cdata, err := s.ReadFile(s.StatePath()); err == nil {
		if cached, parseErr := engine.ParseRunState(cdata); parseErr == nil {
			if rs == nil {
				rs = cached
			} else {
				if rs.Flags == nil {
					rs.Flags = cached.Flags
				}
				if rs.StartedAt == "" {
					rs.StartedAt = cached.StartedAt
				}
			}
		} else if rs == nil {
			return nil, parseErr
		}
	} else if !errors.Is(err, os.ErrNotExist) && rs == nil {
		return nil, err
	}
	if rs == nil {
		rs = engine.NewRunState(time.Now())
	}
	return rs, nil
}

func (s *Store) readJournalBounded(path string) ([]byte, error) {
	return ReadProjectFileLimit(s.root, path, maxJournalBytes)
}

// SetStatus appends to the journal (fsync'd), then refreshes the
// state.json cache atomically. Every event carries a note and a snapshot
// of the flags at that moment, so each stamp records the situation at
// that point in the node's lifecycle (開始/進行/完成…).
func (s *Store) SetStatus(nodeID string, status engine.Status, by, note string) (*engine.RunState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setStatusLocked(nodeID, status, by, note)
}

// setStatusLocked is SetStatus with the write lock already held. Claiming a
// node has to check who holds it and write the status in one critical section
// — a check that releases the lock before writing is not a check — so the two
// halves cannot each take the lock for themselves.
func (s *Store) setStatusLocked(nodeID string, status engine.Status, by, note string) (*engine.RunState, error) {
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if g.NodeByID(nodeID) == nil {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}
	if strings.HasPrefix(string(status), "custom-status-") {
		// loadGraphLocked already merged the workspace vocabulary in, so this
		// check covers both project-local and workspace-wide definitions.
		defined := false
		if g.UI != nil {
			for _, definition := range g.UI.CustomStatuses {
				if definition.ID == string(status) {
					defined = true
					break
				}
			}
		}
		if !defined {
			return nil, fmt.Errorf("custom status %q is not defined in this project or workspace", status)
		}
	}
	node := g.NodeByID(nodeID)
	rs, err := s.LoadState()
	if err != nil {
		return nil, err
	}
	before := engine.ComputeStatuses(g, rs)

	from := before[nodeID]
	if !engine.CanTransition(from, status) {
		return nil, fmt.Errorf("invalid status transition %s -> %s", from, status)
	}
	if err := rs.SetStatus(nodeID, status, by, time.Now()); err != nil {
		return nil, err
	}
	if status == engine.StatusDone {
		if rs.Flags == nil {
			rs.Flags = map[string]any{}
		}
		engine.ApplyEffects(node, rs.Flags)
	}
	ev := &rs.History[len(rs.History)-1]
	ev.From = from
	ev.Note = note
	ev.Flags = engine.EffectiveFlags(g, rs)
	ev.StateFlags = maps.Clone(rs.Flags)
	line, err := engine.AppendJournalLine(*ev)
	if err != nil {
		return nil, err
	}
	if err := s.appendJournal(line); err != nil {
		return nil, err
	}

	if data, err := engine.MarshalRunState(rs); err == nil {
		_ = s.WriteAtomic(s.StatePath(), data) // cache only — best effort
	}
	return rs, nil
}

// MoveEvent retargets a stamp's display time by appending a move event —
// the journal stays append-only; the move is its own audit record.
func (s *Store) MoveEvent(actor identity.Actor, id, newT, by, note string) (*engine.RunState, error) {
	movedAt, err := time.Parse(time.RFC3339, newT)
	if err != nil {
		return nil, fmt.Errorf("invalid event time: %w", err)
	}
	newT = movedAt.UTC().Format(time.RFC3339)
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.LoadState()
	if err != nil {
		return nil, err
	}
	var target *engine.HistoryEvent
	targetIndex := -1
	for i := range rs.History {
		if rs.History[i].ID == id && rs.History[i].Event == "status" {
			target = &rs.History[i]
			targetIndex = i
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("event %q not found", id)
	}
	g, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if err := checkNodeWrite(actor, g.NodeByID(target.Node)); err != nil {
		return nil, err
	}
	for i := targetIndex - 1; i >= 0; i-- {
		previous := rs.History[i]
		if previous.Event != "status" || previous.Node != target.Node {
			continue
		}
		previousAt, parseErr := time.Parse(time.RFC3339, previous.T)
		if parseErr == nil && movedAt.Before(previousAt) {
			return nil, fmt.Errorf(
				"實際紀錄不可早於上一個生命週期事件（%s）",
				previousAt.Local().Format("2006-01-02 15:04"),
			)
		}
		break
	}
	isLatestStatus := true
	for i := targetIndex + 1; i < len(rs.History); i++ {
		next := rs.History[i]
		if next.Event != "status" || next.Node != target.Node {
			continue
		}
		isLatestStatus = false
		nextAt, parseErr := time.Parse(time.RFC3339, next.T)
		if parseErr == nil && movedAt.After(nextAt) {
			return nil, fmt.Errorf(
				"實際紀錄不可晚於下一個生命週期事件（%s）",
				nextAt.Local().Format("2006-01-02 15:04"),
			)
		}
		break
	}
	mv := engine.HistoryEvent{
		T:          newT,
		Event:      "move",
		Ref:        id,
		Node:       target.Node,
		By:         by,
		Note:       note,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	line, err := engine.AppendJournalLine(mv)
	if err != nil {
		return nil, err
	}
	if err := s.appendJournal(line); err != nil {
		return nil, err
	}
	target.T = newT
	if state := rs.Nodes[target.Node]; isLatestStatus && state != nil {
		state.At = newT
	}
	rs.History = append(rs.History, mv)
	if data, err := engine.MarshalRunState(rs); err == nil {
		_ = s.WriteAtomic(s.StatePath(), data)
	}
	return rs, nil
}

// appendJournal appends one or more rendered events and fsyncs them. Crossing
// the soft size threshold first checkpoints and rotates the journal, so the
// hard limit below stays out of reach and a status change is never refused
// just because the project has a long history.
//
// The caller must hold s.mu (every caller does; the name predates the
// ...Locked convention and is spelled this way in transfer.go too).
func (s *Store) appendJournal(line []byte) error {
	if len(line) == 0 || len(line) > maxJournalLineBytes {
		return fmt.Errorf("journal event exceeds %d bytes", maxJournalLineBytes)
	}
	if err := MkdirAllProjectPath(s.root, filepath.Dir(s.JournalPath()), 0o755); err != nil {
		return err
	}
	size := int64(0)
	if info, err := s.statPath(s.JournalPath()); err == nil {
		size = info.Size()
	} else if !os.IsNotExist(err) {
		return err
	}
	s.maybeCompactLocked(size, len(line))
	if info, err := s.statPath(s.JournalPath()); err == nil {
		size = info.Size()
	} else if os.IsNotExist(err) {
		size = 0
	} else {
		return err
	}
	if size > int64(maxJournalBytes-len(line)) {
		return fmt.Errorf("run journal reached its %d-byte safety limit", maxJournalBytes)
	}
	s.markSelfWrite(s.JournalPath())
	f, err := openProjectFileAppend(s.root, s.JournalPath(), 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	return f.Sync()
}

// WriteMetaFile writes a small private file (credentials, tokens, settings)
// atomically and readable only by its owner. It is separate from a Store's
// WriteAtomic because these files live beside a workspace, not inside one.
func WriteMetaFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
