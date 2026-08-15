// Creating, duplicating, and deleting graph nodes.

package store

import (
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
	"nodevas/internal/identity"
)

func (s *Store) nextNodeIDLocked(g *engine.Graph) (string, error) {
	used := make(map[string]bool, len(g.Nodes))
	for _, node := range g.Nodes {
		if node != nil {
			used[node.ID] = true
		}
	}
	trash, err := s.ListTrash()
	if err != nil {
		return "", err
	}
	for _, item := range trash {
		if item.NodeID != "" {
			used[item.NodeID] = true
		}
	}
	for number := 1; number <= 9_999_999; number++ {
		id := fmt.Sprintf("node-%04d", number)
		if used[id] {
			continue
		}
		if _, err := s.statPath(s.NodePath(id)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return id, nil
	}
	return "", errors.New("automatic node id space exhausted")
}

func (s *Store) CreateNode(n *engine.Node, body string) (string, error) {
	if n == nil {
		return "", errors.New("node is required")
	}
	nf, err := engine.ParseNodeFile([]byte(body))
	if err != nil {
		return "", fmt.Errorf("node file: %w", err)
	}
	hydrateNodeFromMeta(n, nf.Meta)
	if n.Kind == "" {
		n.Kind = "task"
	}
	// Normalized before SyncFrontmatter below, or "all" would land in the
	// document's frontmatter while the graph stores "". The enum itself is
	// checked by ValidateGraphForStorage once the node has joined the graph.
	n.WriteAccess = normalizeWriteAccess(n.WriteAccess)
	s.mu.Lock()
	defer s.mu.Unlock()
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	if n.ID == "" {
		n.ID, err = s.nextNodeIDLocked(g)
		if err != nil {
			return "", err
		}
	}
	if !engine.ValidNodeID(n.ID) {
		return "", fmt.Errorf("invalid node id %q", n.ID)
	}
	engine.SyncFrontmatter(nf, n)
	out, err := engine.ComposeNodeFile(nf)
	if err != nil {
		return "", err
	}
	if g.NodeByID(n.ID) != nil {
		return "", fmt.Errorf("node %q already exists", n.ID)
	}
	if _, err := s.statPath(s.NodePath(n.ID)); err == nil {
		return "", fmt.Errorf("node file %q already exists", n.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	g.Nodes = append(g.Nodes, n)
	if err := ValidateGraphForStorage(g); err != nil {
		return "", err
	}
	data, err := s.marshalGraph(g)
	if err != nil {
		return "", err
	}
	if err := s.applyUpdatesLocked([]fileUpdate{
		{
			path:          s.NodePath(n.ID),
			data:          out,
			checkRevision: true,
			expectedRev:   "",
		},
		{
			path:          s.GraphPath(),
			data:          data,
			checkRevision: true,
			expectedRev:   graphRev,
		},
	}); err != nil {
		return "", err
	}
	return n.ID, nil
}

// DuplicateNode creates an independent copy of a node, its document, subpages,
// attachments, expected plans, and visual style. Outgoing dependencies are
// intentionally not copied.
func (s *Store) DuplicateNode(sourceID string) (string, error) {
	if !engine.ValidNodeID(sourceID) {
		return "", fmt.Errorf("invalid node id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Page decoding already acquires these locks in this order when it commits
	// embedded media. Keep that order here and hold mediaMu through the final
	// multi-file commit so an upload cannot split the attachment snapshot.
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return "", err
	}
	source := g.NodeByID(sourceID)
	if source == nil {
		return "", fmt.Errorf("node %q not found", sourceID)
	}
	duplicate := *source
	duplicate.ID, err = s.nextNodeIDLocked(g)
	if err != nil {
		return "", err
	}
	duplicate.Title = strings.TrimSpace(source.Title)
	if duplicate.Title == "" {
		duplicate.Title = sourceID
	}
	duplicate.Title += "（副本）"
	duplicate.Tags = append([]string(nil), source.Tags...)
	duplicate.Effects = append([]engine.Effect(nil), source.Effects...)

	content, err := s.ReadFile(s.NodePath(sourceID))
	if errors.Is(err, os.ErrNotExist) {
		content = nil
	} else if err != nil {
		return "", err
	}
	content = rewriteAttachmentLinks(content, sourceID, duplicate.ID)
	nf, err := engine.ParseNodeFile(content)
	if err != nil {
		return "", err
	}
	engine.SyncFrontmatter(nf, &duplicate)
	nodeData, err := engine.ComposeNodeFile(nf)
	if err != nil {
		return "", err
	}
	g.Nodes = append(g.Nodes, &duplicate)
	for _, edge := range append([]*engine.Edge(nil), g.Edges...) {
		if edge != nil && edge.To == sourceID {
			copied := *edge
			copied.To = duplicate.ID
			g.Edges = append(g.Edges, &copied)
		}
	}
	if g.UI != nil {
		if position, ok := g.UI.Positions[sourceID]; ok {
			sourceStyle := g.UI.NodeStyles[sourceID]
			sourceWidth, sourceHeight := sourceStyle.Width, sourceStyle.Height
			if sourceWidth == 0 {
				sourceWidth = 152
			}
			if sourceHeight == 0 {
				sourceHeight = 68
			}
			candidate := engine.Position{X: position.X + 1, Y: position.Y}
			for attempt := 0; attempt < 10_000; attempt++ {
				overlaps := false
				for nodeID, other := range g.UI.Positions {
					if nodeID == duplicate.ID {
						continue
					}
					otherStyle := g.UI.NodeStyles[nodeID]
					otherWidth, otherHeight := otherStyle.Width, otherStyle.Height
					if otherWidth == 0 {
						otherWidth = 152
					}
					if otherHeight == 0 {
						otherHeight = 68
					}
					if math.Abs((candidate.X-other.X)*164) < (sourceWidth+otherWidth)/2+8 &&
						math.Abs((candidate.Y-other.Y)*80) < (sourceHeight+otherHeight)/2+8 {
						overlaps = true
						break
					}
				}
				if !overlaps {
					break
				}
				candidate.X++
				if candidate.X > 499 {
					candidate.X = 0
					candidate.Y++
				}
			}
			g.UI.Positions[duplicate.ID] = candidate
		}
		if plans, ok := g.UI.Plans[sourceID]; ok {
			if g.UI.Plans == nil {
				g.UI.Plans = map[string][]engine.PlanMilestone{}
			}
			g.UI.Plans[duplicate.ID] = append([]engine.PlanMilestone(nil), plans...)
		}
		if style, ok := g.UI.NodeStyles[sourceID]; ok {
			if g.UI.NodeStyles == nil {
				g.UI.NodeStyles = map[string]engine.NodeStyle{}
			}
			g.UI.NodeStyles[duplicate.ID] = style
		}
		if override, ok := g.UI.EntryOverrides[sourceID]; ok {
			g.UI.EntryOverrides[duplicate.ID] = override
		}
		g.UI.TimelineOrder = append(g.UI.TimelineOrder, duplicate.ID)
	}
	if err := ValidateGraphForStorage(g); err != nil {
		return "", err
	}
	graphData, err := s.marshalGraph(g)
	if err != nil {
		return "", err
	}
	updates := []fileUpdate{{
		path:          s.NodePath(duplicate.ID),
		data:          nodeData,
		checkRevision: true,
		expectedRev:   "",
	}}
	pageManifest, err := s.LoadNodePagesManifest(sourceID)
	if err != nil {
		return "", err
	}
	if len(pageManifest.Pages) > 0 {
		manifestData, marshalErr := marshalNodePagesManifest(pageManifest)
		if marshalErr != nil {
			return "", marshalErr
		}
		updates = append(updates, fileUpdate{
			path: s.NodePagesManifestPath(duplicate.ID),
			data: manifestData,
		})
		for _, page := range pageManifest.Pages {
			pagePath := s.NodePagePath(sourceID, page.ID, page.Format)
			pageData, readErr := s.ReadFile(pagePath)
			if readErr != nil {
				return "", readErr
			}
			updates = append(updates, fileUpdate{
				path: s.NodePagePath(duplicate.ID, page.ID, page.Format),
				data: rewritePageAttachmentLinks(pagePath, pageData, sourceID, duplicate.ID),
			})
		}
	}
	sourceAttachments := s.NodeFilesDir(sourceID)
	if err := ValidateProjectPath(s.root, sourceAttachments, true); err != nil {
		return "", fmt.Errorf("duplicate attachment source: %w", err)
	}
	attachments, err := readDuplicateAttachments(sourceAttachments)
	if err != nil {
		return "", err
	}
	if len(attachments) > 0 {
		destination := s.NodeFilesDir(duplicate.ID)
		if err := ValidateProjectPath(s.root, destination, true); err != nil {
			return "", fmt.Errorf("duplicate attachment destination is not a regular directory: %w", err)
		}
		if err := requireSafeDuplicateAttachmentDir(destination); err != nil {
			return "", err
		}
		for _, attachment := range attachments {
			updates = append(updates, fileUpdate{
				path:          filepath.Join(destination, attachment.name),
				data:          attachment.data,
				checkRevision: true,
				expectedRev:   "",
			})
		}
	}
	// Commit the graph last: until every node-local payload exists, readers
	// must not be able to discover the duplicate in graph.yaml.
	updates = append(updates, fileUpdate{
		path:          s.GraphPath(),
		data:          graphData,
		checkRevision: true,
		expectedRev:   graphRev,
	})
	if err := s.applyUpdatesLocked(updates); err != nil {
		return "", err
	}
	return duplicate.ID, nil
}

func hydrateNodeFromMeta(n *engine.Node, meta map[string]any) {
	if n.Title == "" {
		n.Title, _ = meta["title"].(string)
	}
	if n.Kind == "" {
		n.Kind, _ = meta["kind"].(string)
	}
	if n.Priority == "" {
		n.Priority, _ = meta["priority"].(string)
	}
	if n.Assignee == "" {
		n.Assignee, _ = meta["assignee"].(string)
	}
	if n.Requires == "" {
		n.Requires, _ = meta["requires"].(string)
	}
	if len(n.Effects) == 0 {
		if effects, ok := meta["effects"].([]any); ok {
			for _, effect := range effects {
				if values, ok := effect.(map[string]any); ok {
					if set, ok := values["set"].(string); ok {
						n.Effects = append(n.Effects, engine.Effect{Set: set})
					}
				}
			}
		}
	}
	if len(n.Tags) == 0 {
		if tags, ok := meta["tags"].([]any); ok {
			for _, tag := range tags {
				if value, ok := tag.(string); ok {
					n.Tags = append(n.Tags, value)
				}
			}
		}
	}
}

// checkNodesRemovable reports why a selection cannot leave the graph.
//
// A dependency that is itself going away is fine; one that survives is not,
// because it would be left pointing at nothing. kept is the node slice as it
// would look after the removal.
func checkNodesRemovable(g *engine.Graph, kept []*engine.Node, removing map[string]bool) error {
	for _, node := range kept {
		if node == nil {
			continue
		}
		expr, parseErr := node.RequiresExpr()
		if parseErr != nil {
			continue
		}
		for _, ref := range dsl.NodeRefs(expr) {
			if removing[ref] {
				return fmt.Errorf(
					"node %q is required by %q; remove that dependency first", ref, node.ID)
			}
		}
	}
	if g.UI == nil {
		return nil
	}
	for _, gate := range g.UI.LogicGates {
		for _, output := range gate.OutputNodes() {
			if removing[output] {
				return fmt.Errorf(
					"node %q is the output of logic gate %q; delete that gate first",
					output, gate.ID)
			}
		}
		for _, input := range gate.Inputs {
			if removing[input] {
				return fmt.Errorf(
					"node %q is an input of logic gate %q; delete that gate first",
					input, gate.ID)
			}
		}
	}
	return nil
}

// detachRemovedNodes repairs what a deletion would leave dangling: the
// `requires:` expressions of the surviving nodes, and the logic gates wired to
// the departing ones.
//
// Deleting a node is an editing gesture, not a schema violation, so a surviving
// dependency loses that operand rather than blocking the deletion. An
// expression that consisted only of removed nodes clears entirely; a gate whose
// output or last input is going away is dropped with it. kept is the node slice
// as it looks after the removal.
func detachRemovedNodes(g *engine.Graph, kept []*engine.Node, removing map[string]bool) {
	for _, node := range kept {
		if node == nil || node.Requires == "" {
			continue
		}
		expr, parseErr := node.RequiresExpr()
		if parseErr != nil || expr == nil {
			continue
		}
		referenced := false
		for _, ref := range dsl.NodeRefs(expr) {
			if removing[ref] {
				referenced = true
				break
			}
		}
		if !referenced {
			continue
		}
		if pruned := dsl.PruneNodeRefs(expr, func(id string) bool { return removing[id] }); pruned != nil {
			node.Requires = pruned.String()
		} else {
			node.Requires = ""
		}
	}

	if g.UI == nil {
		return
	}
	gates := g.UI.LogicGates[:0]
	for _, gate := range g.UI.LogicGates {
		// A relation gate drives several nodes, so it survives losing one. A
		// gate that had outputs and lost them all goes, but a draft that never
		// had one stays: incomplete wiring is persisted on purpose.
		wired := len(gate.OutputNodes()) > 0
		if len(gate.Outputs) > 0 {
			kept := make([]string, 0, len(gate.Outputs))
			for _, output := range gate.Outputs {
				if !removing[output] {
					kept = append(kept, output)
				}
			}
			gate.Outputs = kept
		}
		if removing[gate.Output] || (wired && len(gate.OutputNodes()) == 0) {
			continue
		}
		inputs := gate.Inputs[:0]
		for _, input := range gate.Inputs {
			if !removing[input] {
				inputs = append(inputs, input)
			}
		}
		gate.Inputs = inputs
		if len(gate.Inputs) == 0 {
			continue
		}
		gates = append(gates, gate)
	}
	g.UI.LogicGates = gates
}

// DeleteNode soft-deletes one node. It is the single-node case of
// DeleteNodes, which is where the actual work lives.
func (s *Store) DeleteNode(actor identity.Actor, id string) (DeleteNodeOutcome, error) {
	trashed, err := s.DeleteNodes(actor, []string{id})
	if err != nil {
		return DeleteNodeOutcome{}, err
	}
	outcome := DeleteNodeOutcome{CleanupPending: trashed.CleanupPending}
	if len(trashed.TrashFiles) > 0 {
		outcome.TrashFile = trashed.TrashFiles[0]
	}
	return outcome, nil
}

// DeleteNodes removes several nodes from graph.yaml in a single write and
// moves their markdown to .vised/trash/ (soft delete). Doing the whole batch
// as one graph write is what keeps a multi-selection from ending up half
// deleted when one of its nodes turns out to be referenced.
//
// The returned trash file names line up with ids; a node with no document on
// disk yields an empty name.
func (s *Store) DeleteNodes(actor identity.Actor, ids []string) (DeleteNodesOutcome, error) {
	var zero DeleteNodesOutcome
	if len(ids) == 0 {
		return zero, fmt.Errorf("no node ids given")
	}
	removing := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !engine.ValidNodeID(id) {
			return zero, fmt.Errorf("invalid node id")
		}
		if removing[id] {
			continue
		}
		removing[id] = true
		unique = append(unique, id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return zero, err
	}

	kept := g.Nodes[:0]
	found := make(map[string]bool, len(removing))
	for _, node := range g.Nodes {
		if node != nil && removing[node.ID] {
			if err := checkNodeWrite(actor, node); err != nil {
				return zero, err
			}
			found[node.ID] = true
			continue
		}
		kept = append(kept, node)
	}
	for _, id := range unique {
		if !found[id] {
			return zero, fmt.Errorf("node %q not found", id)
		}
	}

	detachRemovedNodes(g, kept, removing)

	g.Nodes = kept
	edges := g.Edges[:0]
	for _, edge := range g.Edges {
		if edge == nil || removing[edge.From] || removing[edge.To] {
			continue
		}
		edges = append(edges, edge)
	}
	g.Edges = edges
	removeNodeUIState(g, removing)

	data, err := s.marshalGraph(g)
	if err != nil {
		return zero, err
	}

	// Soft delete: copy to trash before committing the graph removal. If the
	// graph write fails, every copy is removed and the originals stay intact.
	trashDir := filepath.Join(s.root, DataDir, "trash")
	stamp := time.Now().UTC().Format("20060102-150405.000000000")
	trashPaths := make([]string, len(unique))
	cleanup := func() {
		for _, path := range trashPaths {
			if path != "" {
				_ = s.removePath(path)
			}
		}
	}
	for index, id := range unique {
		nodeData, readErr := s.ReadFile(s.NodePath(id))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			cleanup()
			return zero, readErr
		}
		if err := MkdirAllProjectPath(s.root, trashDir, 0o755); err != nil {
			cleanup()
			return zero, err
		}
		// The index keeps names unique when a whole batch shares one stamp.
		path := filepath.Join(trashDir, fmt.Sprintf("%s-%s.md", stamp, id))
		if _, err := s.statPath(path); err == nil {
			path = filepath.Join(trashDir, fmt.Sprintf("%s%03d-%s.md", stamp, index, id))
		}
		if err := s.WriteAtomic(path, nodeData); err != nil {
			cleanup()
			return zero, err
		}
		trashPaths[index] = path
	}
	records, err := nodeDeleteCleanupRecords(unique)
	if err != nil {
		cleanup()
		return zero, deleteCleanupUnavailable(err)
	}
	queued, err := s.queueDeleteCleanupsLocked(records)
	if err != nil {
		cleanup()
		return zero, deleteCleanupUnavailable(err)
	}

	if err := s.applyUpdatesLocked([]fileUpdate{{
		path:          s.GraphPath(),
		data:          data,
		checkRevision: true,
		expectedRev:   graphRev,
	}}); err != nil {
		cleanup()
		for _, item := range queued {
			if markerErr := s.removePath(item.path); markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove uncommitted delete cleanup marker: %w", markerErr))
			}
		}
		return zero, err
	}

	names := make([]string, len(unique))
	for index := range unique {
		if trashPaths[index] == "" {
			continue
		}
		names[index] = filepath.Base(trashPaths[index])
	}
	outcome := DeleteNodesOutcome{TrashFiles: names}
	var cleanupErrs []error
	for _, item := range queued {
		if err := s.finishDeleteCleanupLocked(item); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		outcome.CleanupPending = true
		log.Printf("node delete committed; cleanup queued for retry: %v", err)
	}
	return outcome, nil
}
