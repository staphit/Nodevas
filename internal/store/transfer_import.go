// Recreating an exported node selection inside a target project.

package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"nodevas/internal/engine"
)

// ImportNodes recreates a payload in this project under fresh node ids.
//
// Everything that can be preserved is; everything that cannot travel — a
// dependency on a node left behind, an assignee whose id is taken by someone
// else — is dropped and reported as a warning rather than failing the whole
// transfer.
func (s *Store) ImportNodes(payload *nodeTransferPayload) (*nodeTransferResult, error) {
	if payload == nil || len(payload.nodes) == 0 {
		return nil, errors.New("nothing to import")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, graphRev, err := s.loadGraphLocked()
	if err != nil {
		return nil, err
	}
	if g.UI == nil {
		g.UI = &engine.UIState{}
	}

	result := &nodeTransferResult{IDs: make(map[string]string, len(payload.nodes))}
	warn := func(format string, args ...any) {
		result.Warnings = append(result.Warnings, fmt.Sprintf(format, args...))
	}

	// Ids first: everything else is expressed in terms of the mapping.
	// Each new node is appended as it is allocated so the next id sees it.
	imported := make([]*engine.Node, 0, len(payload.nodes))
	for _, node := range payload.nodes {
		newID, err := s.nextNodeIDLocked(g)
		if err != nil {
			return nil, err
		}
		copied := *node
		copied.ID = newID
		copied.Tags = append([]string(nil), node.Tags...)
		copied.Effects = append([]engine.Effect(nil), node.Effects...)
		result.IDs[node.ID] = newID
		result.Order = append(result.Order, newID)
		imported = append(imported, &copied)
		g.Nodes = append(g.Nodes, &copied)
	}
	rename := func(id string) string { return result.IDs[id] }

	assignees := mergeUsers(g, payload.users, warn)
	planStatuses := mergePlanStatuses(g, payload.planStatuses, warn)
	mergeCustomStatuses(g, payload.customStatuses)
	for name, value := range payload.flags {
		if g.Flags == nil {
			g.Flags = map[string]any{}
		}
		if _, exists := g.Flags[name]; !exists {
			g.Flags[name] = value
		}
	}

	for index, node := range imported {
		source := payload.nodes[index]
		if node.Assignee != "" {
			node.Assignee = assignees[node.Assignee]
		}
		requires, ok := rewriteRequires(source.Requires, payload.selection, rename)
		if !ok {
			warn("節點「%s」的前置條件指向未一併帶走的節點，已清除", transferNodeLabel(source))
		}
		node.Requires = requires
	}

	for _, edge := range payload.edges {
		copied := edge
		copied.From = rename(edge.From)
		copied.To = rename(edge.To)
		g.Edges = append(g.Edges, &copied)
	}

	offsetX, offsetY := transferPlacementOffset(g.UI.Positions, payload.positions)
	for oldID, position := range payload.positions {
		if g.UI.Positions == nil {
			g.UI.Positions = map[string]engine.Position{}
		}
		g.UI.Positions[rename(oldID)] = engine.Position{
			X: position.X + offsetX,
			Y: position.Y + offsetY,
		}
	}
	for oldID, style := range payload.styles {
		if g.UI.NodeStyles == nil {
			g.UI.NodeStyles = map[string]engine.NodeStyle{}
		}
		g.UI.NodeStyles[rename(oldID)] = style
	}
	for oldID, override := range payload.entryOverride {
		if g.UI.EntryOverrides == nil {
			g.UI.EntryOverrides = map[string]bool{}
		}
		g.UI.EntryOverrides[rename(oldID)] = override
	}
	for oldID, plans := range payload.plans {
		if g.UI.Plans == nil {
			g.UI.Plans = map[string][]engine.PlanMilestone{}
		}
		remapped := make([]engine.PlanMilestone, 0, len(plans))
		for _, plan := range plans {
			if mapped, ok := planStatuses[plan.Status]; ok {
				plan.Status = mapped
			}
			remapped = append(remapped, plan)
		}
		g.UI.Plans[rename(oldID)] = remapped
	}
	for oldID, gate := range payload.gates {
		if g.UI.Gates == nil {
			g.UI.Gates = map[string]engine.GatePlacement{}
		}
		g.UI.Gates[rename(oldID)] = gate
	}
	for key, vertices := range payload.wireVertices {
		if g.UI.WireVertices == nil {
			g.UI.WireVertices = map[string][]engine.Position{}
		}
		g.UI.WireVertices[renameWireKey(key, rename)] = vertices
	}
	for key, label := range payload.edgeLabels {
		if g.UI.EdgeLabels == nil {
			g.UI.EdgeLabels = map[string]engine.EdgeLabelPlacement{}
		}
		from, to, _ := strings.Cut(key, "->")
		g.UI.EdgeLabels[rename(from)+"->"+rename(to)] = label
	}
	for _, oldID := range payload.timelineOrder {
		g.UI.TimelineOrder = append(g.UI.TimelineOrder, rename(oldID))
	}
	importLogicGates(g, payload.logicGates, payload.selection, rename, warn)

	if err := ValidateGraphForStorage(g); err != nil {
		return nil, err
	}
	graphData, err := s.marshalGraph(g)
	if err != nil {
		return nil, err
	}

	sourceProject := payload.sourceProject
	if sourceProject == "" {
		sourceProject = transferSourceProjectName(payload.sourceRoot, s.root)
	}
	updates := make([]fileUpdate, 0, len(imported)*2+1)
	for index, node := range imported {
		source := payload.nodes[index]
		document := rewriteNodeLinks(payload.documents[source.ID], sourceProject, result.IDs)
		nodeFile, err := engine.ParseNodeFile(rewriteAttachmentLinks(document, source.ID, node.ID))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", source.ID, err)
		}
		engine.SyncFrontmatter(nodeFile, node)
		data, err := engine.ComposeNodeFile(nodeFile)
		if err != nil {
			return nil, err
		}
		updates = append(updates, fileUpdate{
			path:          s.NodePath(node.ID),
			data:          data,
			checkRevision: true,
			expectedRev:   "",
		})
		if manifest, ok := payload.pages[source.ID]; ok {
			manifestData, err := marshalNodePagesManifest(manifest)
			if err != nil {
				return nil, err
			}
			updates = append(updates, fileUpdate{
				path: s.NodePagesManifestPath(node.ID),
				data: manifestData,
			})
			for name, data := range payload.pageFiles[source.ID] {
				updates = append(updates, fileUpdate{
					path: filepath.Join(s.NodePagesDir(node.ID), name),
					data: rewritePageAttachmentLinks(name, data, source.ID, node.ID),
				})
			}
		}
		for name, data := range payload.attachments[source.ID] {
			updates = append(updates, fileUpdate{
				path: filepath.Join(s.NodeFilesDir(node.ID), name),
				data: data,
			})
		}
	}
	// The graph goes last: no reader may see it point at unwritten content.
	updates = append(updates, fileUpdate{
		path:          s.GraphPath(),
		data:          graphData,
		checkRevision: true,
		expectedRev:   graphRev,
	})
	if err := s.applyUpdatesLocked(updates); err != nil {
		return nil, err
	}

	// The lifecycle history is appended after the graph is committed. It is
	// an audit trail, not structure: failing the transfer over it would
	// discard work that is already correctly on disk.
	if err := s.appendTransferJournal(payload.journal, result.IDs); err != nil {
		warn("節點已複製，但生命週期紀錄未能一併寫入：%v", err)
	}
	return result, nil
}

// transferPlacementOffset finds where to drop the incoming cluster: to the
// right of everything already on the board, with the selection's own relative
// layout intact.
func transferPlacementOffset(
	existing map[string]engine.Position,
	incoming map[string]engine.Position,
) (float64, float64) {
	if len(incoming) == 0 {
		return 0, 0
	}
	minX, minY := 0.0, 0.0
	first := true
	for _, position := range incoming {
		if first || position.X < minX {
			minX = position.X
		}
		if first || position.Y < minY {
			minY = position.Y
		}
		first = false
	}
	if len(existing) == 0 {
		return -minX, -minY
	}
	maxX, targetMinY := 0.0, 0.0
	first = true
	for _, position := range existing {
		if first || position.X > maxX {
			maxX = position.X
		}
		if first || position.Y < targetMinY {
			targetMinY = position.Y
		}
		first = false
	}
	return maxX + 2 - minX, targetMinY - minY
}
