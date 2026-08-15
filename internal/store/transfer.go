package store

// Moving a node selection between projects.
//
// Two stores are involved and each serializes its own writes with its own
// mutex, so the transfer never holds both: it snapshots the source under the
// source lock (ExportNodes), writes under the target lock (ImportNodes), and
// only then removes the originals. That ordering is also the failure policy —
// if the removal fails, the content exists twice, which the user can fix;
// the reverse would lose it.
//
// A node is more than a row in graph.yaml: it carries a document, subpages,
// attachments, position, style, expected plan, lifecycle history, and the
// project-level definitions those refer to (assignee, custom statuses, flags).
// Everything here exists because one of those would otherwise be dropped
// silently or would make the target graph fail validation.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nodevas/internal/engine"
)

// maxTransferNodes bounds one transfer. The whole selection is held in memory
// with its attachments, so this is a memory bound, not a graph bound.
const maxTransferNodes = 2_000

// nodeTransferPayload is a self-contained snapshot of a node selection: the
// nodes plus every referenced thing the target project needs in order to
// accept them. Ids inside are still the source project's.
type nodeTransferPayload struct {
	nodes []*engine.Node
	// selection is the node id set, for deciding which references survive.
	selection map[string]bool
	// sourceRoot is the directory the selection came from. The node links in
	// the documents are resolved against the project they sit in, so the
	// target has to know which project that was; see
	// transferSourceProjectName.
	sourceRoot string
	// sourceProject is the project path the selection came from, as the
	// workspace names it. The caller knows it; a Store only ever sees a
	// directory, so it has to be told.
	sourceProject string

	documents   map[string][]byte
	pages       map[string]nodePagesManifest
	pageFiles   map[string]map[string][]byte // node id -> file base name -> bytes
	attachments map[string]map[string][]byte // node id -> file name -> bytes

	edges         []engine.Edge
	positions     map[string]engine.Position
	styles        map[string]engine.NodeStyle
	entryOverride map[string]bool
	plans         map[string][]engine.PlanMilestone
	gates         map[string]engine.GatePlacement
	logicGates    []engine.LogicGate
	wireVertices  map[string][]engine.Position
	edgeLabels    map[string]engine.EdgeLabelPlacement
	timelineOrder []string

	users          []engine.User
	planStatuses   []engine.PlanStatusDefinition
	customStatuses []engine.StatusDefinition
	flags          map[string]any
	journal        []engine.HistoryEvent
}

// nodeTransferResult reports what landed in the target project.
type nodeTransferResult struct {
	// IDs maps every source node id to the id it was given in the target.
	IDs map[string]string `json:"ids"`
	// Order lists the new ids in the source graph's order.
	Order []string `json:"order"`
	// Warnings name what could not come along. They are not failures: the
	// transfer happened, but something was dropped and the user should know.
	Warnings []string `json:"warnings,omitempty"`
}

// uniqueNodeIDs validates a selection and drops repeats, keeping order.
func uniqueNodeIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, errors.New("no node ids given")
	}
	if len(ids) > maxTransferNodes {
		return nil, fmt.Errorf("too many nodes in one transfer (maximum %d)", maxTransferNodes)
	}
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !engine.ValidNodeID(id) {
			return nil, fmt.Errorf("invalid node id %q", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique, nil
}

// effectFlagName returns the flag an effect statement writes to.
func effectFlagName(stmt string) string {
	stmt = strings.TrimSpace(stmt)
	for _, op := range []string{"+=", "-=", "="} {
		if i := strings.Index(stmt, op); i > 0 {
			return strings.TrimSpace(stmt[:i])
		}
	}
	return stmt
}

// readDirFiles reads every regular file in dir. A missing directory is not an
// error: most nodes have no attachments.
func (s *Store) readDirFiles(dir string) (map[string][]byte, error) {
	entries, err := s.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), TmpPrefix) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil, fmt.Errorf("unsafe attachment entry %q", entry.Name())
		}
		data, err := s.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		files[entry.Name()] = data
	}
	if len(files) == 0 {
		return nil, nil
	}
	return files, nil
}

// SetSourceProject records where the selection came from, so links in the
// moved documents can be rewritten against the right project. Callers that
// know the workspace name should always set it; without it the name is
// guessed from the directory layout, which is only exact for a flat
// workspace.
func (p *nodeTransferPayload) SetSourceProject(name string) {
	if p != nil {
		p.sourceProject = name
	}
}

// transferSourceProjectName is what a link has to say to keep pointing at the
// project a selection came from: that project's path relative to the workspace
// holding both projects, which is how link targets are resolved.
//
// A Store knows directories, not project names, so the workspace is located by
// the lock file it leaves behind (see project.WorkspaceLockPath); the file
// stays on disk after release, and only a workspace root has one, while every
// project directory has a .vised of its own. Failing that, the nearest shared
// ancestor is the best guess available and is exact for a flat workspace.
//
// This is the fallback. The HTTP layer knows the real name and passes it
// through SetSourceProject; only callers that do not (tests, future direct
// users of the store) land here.
func transferSourceProjectName(sourceRoot, targetRoot string) string {
	if sourceRoot == "" || targetRoot == "" {
		return ""
	}
	workspace := commonAncestorDir(sourceRoot, targetRoot)
	if workspace == "" {
		return ""
	}
	for dir := workspace; ; {
		if _, err := os.Stat(filepath.Join(dir, DataDir, workspaceLockFileName)); err == nil {
			workspace = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	rel, err := filepath.Rel(workspace, sourceRoot)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// workspaceLockFileName names <workspace>/.vised/workspace.lock. It repeats
// project.WorkspaceLockName because that package imports this one.
const workspaceLockFileName = "workspace.lock"

// commonAncestorDir returns the deepest directory containing both paths, or
// "" when they are on different volumes.
func commonAncestorDir(a, b string) string {
	dir := filepath.Clean(a)
	other := filepath.Clean(b)
	for {
		rel, err := filepath.Rel(dir, other)
		if err != nil {
			return ""
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// appendTransferJournal replays the moved lifecycle stamps into this
// project's journal under the new node ids. Event ids are reissued so a
// second transfer of the same nodes cannot collide with the first.
func (s *Store) appendTransferJournal(events []engine.HistoryEvent, ids map[string]string) error {
	if len(events) == 0 {
		return nil
	}
	stamp := time.Now().UnixNano()
	eventIDs := make(map[string]string, len(events))
	var buffer []byte
	for index, event := range events {
		copied := event
		if copied.Event == "status" {
			newNode, ok := ids[copied.Node]
			if !ok {
				continue
			}
			copied.Node = newNode
			newEventID := fmt.Sprintf("ev-%d-%d", stamp, index)
			if copied.ID != "" {
				eventIDs[copied.ID] = newEventID
			}
			copied.ID = newEventID
		} else {
			ref, ok := eventIDs[copied.Ref]
			if !ok {
				continue
			}
			copied.Ref = ref
			copied.ID = fmt.Sprintf("ev-%d-%d", stamp, index)
			if node, ok := ids[copied.Node]; ok {
				copied.Node = node
			}
		}
		line, err := engine.AppendJournalLine(copied)
		if err != nil {
			return err
		}
		buffer = append(buffer, line...)
	}
	if len(buffer) == 0 {
		return nil
	}
	return s.appendJournal(buffer)
}
