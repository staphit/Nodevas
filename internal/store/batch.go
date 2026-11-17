// Batches: a create_subgraph call recorded so it can be taken back as one.
//
// A Copilot draft lands on the board with one Create, so it should leave with
// one Undo. The batch record is a project sidecar (.vised/batches.json), like
// created_by_run.json: who made the batch, through what, which nodes, and for
// each node a fingerprint of its own fields and its status at commit. Undo
// trashes the nodes nobody has touched since and refuses the rest; redo
// restores them and puts back the dependencies the batch had added to nodes
// that were already on the board, which a plain restore from the trash does
// not know about.
//
// There is no per-node revision to compare against: the node document's own
// revision moves when another node links to it and stays put when the node's
// status, claim or place on the canvas changes. Hence the fingerprint (the
// node's own editable fields and body, not its links) plus the journal status
// plus a live-claim check.

package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

const (
	// maxBatchRecords bounds the sidecar; the oldest batches go first and
	// can no longer be undone.
	maxBatchRecords = 200
	// maxBatchFileBytes bounds what a read of the sidecar takes in: two
	// hundred batches of a hundred nodes, with room.
	maxBatchFileBytes = 16 << 20
)

var (
	batchIDPattern  = regexp.MustCompile(`^b-[0-9a-f]{24}$`)
	batchViaPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

// BatchSpec asks create_subgraph to record the call as a batch. Via says what
// made it ("copilot-draft:<draft id>"); the server picks the batch id.
type BatchSpec struct {
	Via string `json:"via"`
}

// BatchLink is a dependency a batch put into a node that already existed:
// To waits for From.
type BatchLink struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Codes of BatchRefusal, for a client to word the refusal itself.
const (
	// BatchRefusedChanged: the node's own fields or document changed.
	BatchRefusedChanged = "changed"
	// BatchRefusedStatus: the node's status changed.
	BatchRefusedStatus = "status"
	// BatchRefusedClaimed: somebody holds a live claim on the node.
	BatchRefusedClaimed = "claimed"
	// BatchRefusedBusy: the caller's Busy check said so (a run on it).
	BatchRefusedBusy = "busy"
	// BatchRefusedDenied: the actor may not delete the node.
	BatchRefusedDenied = "denied"
	// BatchSkippedExists: redo found the node already back on the board.
	BatchSkippedExists = "exists"
	// BatchSkippedNotInTrash: redo found no copy of the node in the trash.
	BatchSkippedNotInTrash = "not-in-trash"
	// BatchSkippedLink: redo could not put a dependency back.
	BatchSkippedLink = "link"
)

// BatchRefusal is a node an undo kept, or one a redo could not bring back
// (for a link, ID is the existing node it went into).
type BatchRefusal struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// BatchUndoOptions carries the checks the store cannot make itself.
type BatchUndoOptions struct {
	// Busy reports a node something outside the store is working on (a run),
	// with a reason. It is called before the store's lock is taken, so it
	// may call back into the store.
	Busy func(nodeID string) (bool, string)
}

// BatchUndoResult is what an undo did: the nodes moved to the trash, and the
// ones kept because they changed. A node already gone is in neither.
type BatchUndoResult struct {
	Trashed []string       `json:"trashed"`
	Refused []BatchRefusal `json:"refused"`
}

// BatchRedoResult is what a redo did: the nodes restored, the ones (or
// links) it could not bring back, and how many links into existing nodes it
// put back.
type BatchRedoResult struct {
	Restored []string       `json:"restored"`
	Skipped  []BatchRefusal `json:"skipped"`
	Links    int            `json:"links"`
}

// ErrBatchNotFound is an unknown batch, or one the actor may not undo; the
// two are answered alike so a batch's existence does not leak.
type ErrBatchNotFound struct{ said error }

func (e *ErrBatchNotFound) Error() string { return e.said.Error() }

// Unwrap is the refusal in both languages.
func (e *ErrBatchNotFound) Unwrap() error { return e.said }

func batchNotFound() error {
	return &ErrBatchNotFound{said: locale.Errorf("batch not found", "找不到這批節點")}
}

type batchFile struct {
	Batches map[string]*batchRecord `json:"batches"`
}

type batchActor struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// batchNodeMark is one node as the batch left it.
type batchNodeMark struct {
	Fingerprint string        `json:"fingerprint"`
	Status      engine.Status `json:"status,omitempty"`
}

type batchRecord struct {
	ID        string                   `json:"id"`
	Project   string                   `json:"project"`
	Actor     batchActor               `json:"actor"`
	Via       string                   `json:"via"`
	CreatedAt string                   `json:"createdAt"`
	IDs       []string                 `json:"ids"`
	Nodes     map[string]batchNodeMark `json:"nodes"`
	Links     []BatchLink              `json:"links,omitempty"`
	Undone    *batchUndone             `json:"undone,omitempty"`
}

// batchUndone is what an undo took away, kept for the redo: the nodes'
// definitions (a node restored from its document alone loses its refs, write
// access, deadline and acceptance) and their places on the canvas.
type batchUndone struct {
	At        string                     `json:"at"`
	By        string                     `json:"by"`
	IDs       []string                   `json:"ids"`
	Nodes     map[string]engine.Node     `json:"nodes"`
	Positions map[string]engine.Position `json:"positions,omitempty"`
}

// errBatchesUnparsable is a sidecar that was read but is not batch records.
type errBatchesUnparsable struct{ err error }

func (e *errBatchesUnparsable) Error() string { return "batch records unreadable: " + e.err.Error() }
func (e *errBatchesUnparsable) Unwrap() error { return e.err }

func (s *Store) batchesPath() string {
	return filepath.Join(s.root, DataDir, "batches.json")
}

// loadBatchesLocked reads the sidecar; a missing one is empty. A sidecar that
// cannot be read is an error, so a write never replaces records it did not
// see; one that does not parse is reported too.
func (s *Store) loadBatchesLocked() (*batchFile, error) {
	file := &batchFile{Batches: map[string]*batchRecord{}}
	raw, err := ReadProjectFileLimit(s.root, s.batchesPath(), maxBatchFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, file); err != nil {
		return nil, &errBatchesUnparsable{err: err}
	}
	if file.Batches == nil {
		file.Batches = map[string]*batchRecord{}
	}
	return file, nil
}

func (s *Store) saveBatchesLocked(file *batchFile) error {
	for len(file.Batches) > maxBatchRecords {
		oldestID, oldest := "", ""
		for id, record := range file.Batches {
			if oldestID == "" || record.CreatedAt < oldest {
				oldestID, oldest = id, record.CreatedAt
			}
		}
		delete(file.Batches, oldestID)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := MkdirAllProjectPath(s.root, filepath.Dir(s.batchesPath()), 0o755); err != nil {
		return err
	}
	return s.WriteAtomic(s.batchesPath(), append(data, '\n'))
}

// batchHistoryNote is how the version history names a batch commit, or "" to
// let it name the change itself.
func batchHistoryNote(via string) string {
	if strings.HasPrefix(via, CopilotDraftVia) {
		return "created from a Copilot draft (" + via + ")"
	}
	if strings.HasPrefix(via, CopilotPlanVia) {
		return "created from a coding agent's plan (via Copilot) (" + via + ")"
	}
	return ""
}

func newBatchID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "b-" + hex.EncodeToString(raw[:]), nil
}

// recordBatchLocked records a committed batch and returns its id. The caller
// holds s.mu exclusively and has just written ids. The marks are taken from
// what is now on disk, through the same reads an undo makes, so an untouched
// node always matches.
func (s *Store) recordBatchLocked(actor identity.Actor, via string, ids []string, links []BatchLink) (string, error) {
	g, _, err := s.graphViewLocked()
	if err != nil {
		return "", err
	}
	state, err := s.LoadState()
	if err != nil {
		return "", err
	}
	marks := make(map[string]batchNodeMark, len(ids))
	for _, id := range ids {
		n := g.NodeByID(id)
		if n == nil {
			return "", locale.Errorf("node %q not found", "找不到節點「%s」", id)
		}
		fingerprint, err := s.nodeFingerprintLocked(n)
		if err != nil {
			return "", err
		}
		marks[id] = batchNodeMark{Fingerprint: fingerprint, Status: journalStatus(state, id)}
	}
	file, err := s.loadBatchesLocked()
	if err != nil {
		var unparsable *errBatchesUnparsable
		if !errors.As(err, &unparsable) {
			return "", err
		}
		// Records that do not parse cannot be undone anyway; start over
		// rather than leave every later batch undoable.
		log.Printf("batch records unreadable, starting over: %v", err)
		file = &batchFile{Batches: map[string]*batchRecord{}}
	}
	id, err := newBatchID()
	if err != nil {
		return "", err
	}
	file.Batches[id] = &batchRecord{
		ID:        id,
		Project:   filepath.Base(s.root),
		Actor:     batchActor{ID: actor.ID, Name: actor.Name},
		Via:       via,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		IDs:       append([]string(nil), ids...),
		Nodes:     marks,
		Links:     append([]BatchLink(nil), links...),
	}
	if err := s.saveBatchesLocked(file); err != nil {
		return "", err
	}
	return id, nil
}

// batchForLocked is the record of batchID if actor may undo it: the actor who
// made it, or an administrator. Anything else is ErrBatchNotFound.
func (s *Store) batchForLocked(actor identity.Actor, batchID string) (*batchFile, *batchRecord, error) {
	if !batchIDPattern.MatchString(batchID) {
		return nil, nil, batchNotFound()
	}
	file, err := s.loadBatchesLocked()
	if err != nil {
		return nil, nil, err
	}
	record := file.Batches[batchID]
	if record == nil || (record.Actor.ID != actor.ID && !actor.IsAdmin()) {
		return nil, nil, batchNotFound()
	}
	return file, record, nil
}

// journalStatus is the status recorded for a node, not the one derived from
// its dependencies: a dependency finishing is not a change to the node.
// "ready" counts as none, since that is where a released claim or a reset
// puts a node that was never worked on.
func journalStatus(state *engine.RunState, id string) engine.Status {
	if state == nil || state.Nodes[id] == nil || state.Nodes[id].Status == engine.StatusReady {
		return ""
	}
	return state.Nodes[id].Status
}

// syncedMetaKeys are the frontmatter keys SyncFrontmatter writes from the
// graph: either covered by the node's own fields or links, never the
// document's own.
var syncedMetaKeys = map[string]bool{
	"id": true, "title": true, "kind": true, "priority": true, "assignee": true,
	"write_access": true, "requires": true, "tags": true, "refs": true, "effects": true,
}

// nodeFingerprintLocked hashes what a person edits on a node: its own fields,
// its document's body and own frontmatter, and its pages. It leaves out what
// other nodes change (requires, refs, links, edges) and what is not the node
// (status, claims, position). The caller holds s.mu.
func (s *Store) nodeFingerprintLocked(n *engine.Node) (string, error) {
	body, extra := "", map[string]any{}
	data, err := s.ReadFile(s.NodePath(n.ID))
	switch {
	case err == nil:
		if nf, parseErr := engine.ParseNodeFile(data); parseErr == nil {
			body = nf.Body
			for key, value := range nf.Meta {
				if !syncedMetaKeys[key] {
					extra[key] = value
				}
			}
		} else {
			body = string(data)
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	pages, err := s.ReadFile(s.NodePagesManifestPath(n.ID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	own := struct {
		Title       string             `json:"title,omitempty"`
		Kind        string             `json:"kind,omitempty"`
		Priority    string             `json:"priority,omitempty"`
		Assignee    string             `json:"assignee,omitempty"`
		Deadline    string             `json:"deadline,omitempty"`
		WriteAccess string             `json:"writeAccess,omitempty"`
		Tags        []string           `json:"tags,omitempty"`
		Effects     []engine.Effect    `json:"effects,omitempty"`
		Acceptance  *engine.Acceptance `json:"acceptance,omitempty"`
		Workflow    *engine.Workflow   `json:"workflow,omitempty"`
		Body        string             `json:"body,omitempty"`
		Meta        string             `json:"meta,omitempty"`
		Pages       string             `json:"pages,omitempty"`
	}{
		Title: n.Title, Kind: n.Kind, Priority: n.Priority, Assignee: n.Assignee,
		Deadline: n.Deadline, WriteAccess: n.WriteAccess, Tags: n.Tags, Effects: n.Effects,
		Acceptance: n.Acceptance, Workflow: n.Workflow, Body: body, Pages: string(pages),
	}
	if len(extra) > 0 {
		// encoding/json and fmt both write map keys sorted.
		if meta, err := json.Marshal(extra); err == nil {
			own.Meta = string(meta)
		} else {
			own.Meta = fmt.Sprintf("%v", extra)
		}
	}
	raw, err := json.Marshal(own)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16]), nil
}

// UndoBatch moves a batch's nodes to the trash (restorable, edges and the
// dependencies of other nodes on them going too, as a delete does). A node
// whose own fields or status changed since the batch, that is claimed, that
// opts.Busy names, or that the actor may not delete is kept and listed in
// Refused; a node already gone is skipped. Only the batch's actor or an
// administrator may undo it; anyone else gets ErrBatchNotFound.
//
// Undoing a batch twice is a no-op. An undo that kept every node leaves the
// batch as it was, so it can be retried.
func (s *Store) UndoBatch(actor identity.Actor, batchID string, opts BatchUndoOptions) (BatchUndoResult, error) {
	result := BatchUndoResult{Trashed: []string{}, Refused: []BatchRefusal{}}
	if !batchIDPattern.MatchString(batchID) {
		return result, batchNotFound()
	}

	// Busy reaches outside the store, so it runs before the store's lock.
	busy := map[string]string{}
	if opts.Busy != nil {
		s.mu.RLock()
		_, record, err := s.batchForLocked(actor, batchID)
		s.mu.RUnlock()
		if err != nil {
			return result, err
		}
		if record.Undone == nil {
			for _, id := range record.IDs {
				if isBusy, why := opts.Busy(id); isBusy {
					if why == "" {
						why = "something is working on it"
					}
					busy[id] = why
				}
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	file, record, err := s.batchForLocked(actor, batchID)
	if err != nil {
		return result, err
	}
	if record.Undone != nil {
		return result, nil
	}
	g, _, err := s.graphViewLocked()
	if err != nil {
		return result, err
	}
	state, err := s.LoadState()
	if err != nil {
		return result, err
	}
	claims := s.loadClaimsLocked()
	now := claimClock()
	refuse := func(id, code, reason string) {
		result.Refused = append(result.Refused, BatchRefusal{ID: id, Code: code, Reason: reason})
	}
	var eligible []string
	for _, id := range record.IDs {
		n := g.NodeByID(id)
		if n == nil {
			continue
		}
		mark := record.Nodes[id]
		fingerprint, err := s.nodeFingerprintLocked(n)
		if err != nil {
			return BatchUndoResult{Trashed: []string{}, Refused: []BatchRefusal{}}, err
		}
		switch status := journalStatus(state, id); {
		case fingerprint != mark.Fingerprint:
			refuse(id, BatchRefusedChanged, "edited since it was created")
		// A claim also sets a status; the claim is the better reason.
		case claims.Claims[id].Live(now):
			refuse(id, BatchRefusedClaimed, "claimed by "+claims.Claims[id].Owner)
		case status != mark.Status:
			refuse(id, BatchRefusedStatus, fmt.Sprintf("status changed to %s", status))
		case busy[id] != "":
			refuse(id, BatchRefusedBusy, busy[id])
		default:
			if err := checkNodeWrite(actor, n); err != nil {
				refuse(id, BatchRefusedDenied, err.Error())
				continue
			}
			eligible = append(eligible, id)
		}
	}
	if len(eligible) == 0 {
		return result, nil
	}
	if err := checkRoleDelete(actor, eligible[0]); err != nil {
		return BatchUndoResult{Trashed: []string{}, Refused: []BatchRefusal{}}, err
	}

	// What the redo needs is recorded before anything is deleted, so a
	// delete is never left without the record that can take it back.
	undone := &batchUndone{
		At: time.Now().UTC().Format(time.RFC3339Nano), By: actor.ID,
		IDs: eligible, Nodes: make(map[string]engine.Node, len(eligible)),
	}
	removing := make(map[string]bool, len(eligible))
	for _, id := range eligible {
		removing[id] = true
		var copied engine.Node
		raw, err := json.Marshal(g.NodeByID(id))
		if err == nil {
			err = json.Unmarshal(raw, &copied)
		}
		if err != nil {
			return BatchUndoResult{Trashed: []string{}, Refused: []BatchRefusal{}}, err
		}
		undone.Nodes[id] = copied
		if g.UI != nil {
			if position, ok := g.UI.Positions[id]; ok {
				if undone.Positions == nil {
					undone.Positions = map[string]engine.Position{}
				}
				undone.Positions[id] = position
			}
		}
	}
	record.Undone = undone
	if err := s.saveBatchesLocked(file); err != nil {
		return BatchUndoResult{Trashed: []string{}, Refused: []BatchRefusal{}},
			locale.Errorf("cannot record the undo: %w", "無法記錄這次復原：%w", err)
	}
	if _, err := s.deleteNodesLocked(actor, eligible, removing); err != nil {
		record.Undone = nil
		if saveErr := s.saveBatchesLocked(file); saveErr != nil {
			log.Printf("batch %s: undo failed and its record could not be put back: %v", batchID, saveErr)
		}
		return BatchUndoResult{Trashed: []string{}, Refused: []BatchRefusal{}}, err
	}
	result.Trashed = eligible
	return result, nil
}

// RedoBatch takes an undo back: the nodes it trashed return from the trash
// with their full definitions and places, and the dependencies the batch had
// put into existing nodes are put back. A node already back, or no longer in
// the trash, is skipped, and so is a dependency whose endpoint is gone; any
// node or dependency a restored node names that is gone is dropped from it.
// The same actors as UndoBatch may redo. Redoing a batch that is not undone
// is a no-op.
func (s *Store) RedoBatch(actor identity.Actor, batchID string) (BatchRedoResult, error) {
	result := BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}
	if !batchIDPattern.MatchString(batchID) {
		return result, batchNotFound()
	}
	// A restore is recorded at once, like a delete; it runs after the unlock.
	defer s.history().commitSoon()
	s.mu.Lock()
	defer s.mu.Unlock()
	file, record, err := s.batchForLocked(actor, batchID)
	if err != nil {
		return result, err
	}
	undone := record.Undone
	if undone == nil {
		return result, nil
	}
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return result, err
	}
	before := map[engine.Issue]bool{}
	for _, issue := range engine.Validate(g) {
		before[issue] = true
	}
	trash, err := s.ListTrash()
	if err != nil {
		return result, err
	}
	// ListTrash is newest first: the copy the undo made is the first named.
	trashFile := map[string]string{}
	for _, item := range trash {
		if item.Kind == "node" && item.NodeID != "" && trashFile[item.NodeID] == "" {
			trashFile[item.NodeID] = item.File
		}
	}
	skip := func(id, code, reason string) {
		result.Skipped = append(result.Skipped, BatchRefusal{ID: id, Code: code, Reason: reason})
	}

	type restoring struct {
		node      *engine.Node
		trashPath string
		data      []byte
	}
	var restores []restoring
	back := map[string]bool{}
	for _, id := range undone.IDs {
		snapshot, ok := undone.Nodes[id]
		if !ok || !engine.ValidNodeID(id) {
			continue
		}
		if g.NodeByID(id) != nil {
			skip(id, BatchSkippedExists, "already on the board")
			continue
		}
		if _, err := s.statPath(s.NodePath(id)); err == nil {
			skip(id, BatchSkippedExists, "its document already exists")
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
		}
		name := trashFile[id]
		if name == "" {
			skip(id, BatchSkippedNotInTrash, "no longer in the trash")
			continue
		}
		path := filepath.Join(s.root, DataDir, "trash", name)
		data, err := s.ReadFile(path)
		if err != nil {
			skip(id, BatchSkippedNotInTrash, "no longer in the trash")
			continue
		}
		n := snapshot
		n.ID = id
		if err := checkRole(actor, g, engine.CapCreate, &n); err != nil {
			return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
		}
		back[id] = true
		restores = append(restores, restoring{node: &n, trashPath: path, data: data})
	}
	if len(restores) == 0 {
		return result, nil
	}

	// A restored node keeps only the dependencies and references that still
	// lead somewhere.
	present := func(id string) bool { return back[id] || g.NodeByID(id) != nil }
	for _, r := range restores {
		n := r.node
		if n.Requires != "" {
			expr, parseErr := n.RequiresExpr()
			if parseErr == nil && expr != nil {
				if pruned := dsl.PruneNodeRefs(expr, func(id string) bool { return !present(id) }); pruned != nil {
					n.Requires = pruned.String()
				} else {
					n.Requires = ""
				}
			}
		}
		if len(n.Refs) > 0 {
			refs := n.Refs[:0:0]
			for _, ref := range n.Refs {
				if present(ref) {
					refs = append(refs, ref)
				}
			}
			if len(refs) == 0 {
				refs = nil
			}
			n.Refs = refs
		}
	}
	for _, r := range restores {
		g.Nodes = append(g.Nodes, r.node)
		if position, ok := undone.Positions[r.node.ID]; ok {
			if g.UI == nil {
				g.UI = &engine.UIState{}
			}
			if g.UI.Positions == nil {
				g.UI.Positions = map[string]engine.Position{}
			}
			g.UI.Positions[r.node.ID] = position
		}
	}

	touched := map[string]bool{}
	for _, link := range record.Links {
		if !back[link.From] && !back[link.To] {
			continue // nothing of this link was taken away by the undo
		}
		if g.NodeByID(link.From) == nil || g.NodeByID(link.To) == nil {
			skip(link.To, BatchSkippedLink, fmt.Sprintf("%s -> %s: a node it joins is gone", link.From, link.To))
			continue
		}
		if err := applyGraphOp(actor, g, GraphOp{Kind: "add-edge", From: link.From, To: link.To}, touched, func(string) bool { return false }); err != nil {
			skip(link.To, BatchSkippedLink, fmt.Sprintf("%s -> %s: %v", link.From, link.To, err))
			continue
		}
		result.Links++
	}

	if err := ValidateGraphForStorage(g); err != nil {
		return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
	}
	for _, issue := range engine.Validate(g) {
		if issue.Severity == "error" && !before[issue] {
			return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}},
				invalidSubgraph("", "%s", "%s", subgraphIssueText(issue))
		}
	}
	data, err := s.marshalGraph(g)
	if err != nil {
		return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
	}
	updates := make([]fileUpdate, 0, len(restores)+len(touched)+1)
	nodePaths := make([]string, 0, len(restores))
	for _, r := range restores {
		nf, err := engine.ParseNodeFile(r.data)
		if err != nil {
			return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
		}
		engine.SyncFrontmatter(nf, r.node)
		out, err := engine.ComposeNodeFile(nf)
		if err != nil {
			return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
		}
		nodePaths = append(nodePaths, s.NodePath(r.node.ID))
		updates = append(updates, fileUpdate{path: s.NodePath(r.node.ID), data: out, checkRevision: true, expectedRev: ""})
	}
	for _, n := range g.Nodes {
		if n == nil || back[n.ID] || !touched[n.ID] {
			continue
		}
		update, err := s.prepareNodeFileUpdate(n)
		if err != nil {
			return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}},
				locale.Errorf("sync node %s: %w", "同步節點 %s：%w", n.ID, err)
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	updates = append(updates, fileUpdate{path: s.GraphPath(), data: data, checkRevision: true, expectedRev: graphRev})
	if err := s.applyUpdatesLocked(actor, updates); err != nil {
		return BatchRedoResult{Restored: []string{}, Skipped: []BatchRefusal{}}, err
	}
	s.history().wrote(actor, "restore", nodePaths)
	for _, r := range restores {
		if err := s.removePath(r.trashPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("batch %s: %s restored, trash copy left: %v", batchID, r.node.ID, err)
		}
		result.Restored = append(result.Restored, r.node.ID)
	}
	record.Undone = nil
	if err := s.saveBatchesLocked(file); err != nil {
		// The nodes are back; only a later undo of this batch is lost.
		log.Printf("batch %s: redone, record not updated: %v", batchID, err)
	}
	return result, nil
}
