// Pruning UI-state entries (edge labels, wire vertices, positions, plans)
// that referenced a node or edge that no longer exists.

package store

import (
	"strings"

	"nodevas/internal/engine"
)

// pruneEdgeDecorations drops label and bend-point placements whose wire is
// gone: a direct edge that no longer exists, a dependency gate on a removed node,
// or a logic gate that was deleted.
func pruneEdgeDecorations(g *engine.Graph) {
	if g == nil || g.UI == nil {
		return
	}
	liveEdges := make(map[string]bool, len(g.Edges))
	for _, edge := range g.Edges {
		if edge != nil {
			liveEdges[edge.From+"->"+edge.To] = true
		}
	}
	liveNodes := make(map[string]bool, len(g.Nodes))
	for _, node := range g.Nodes {
		if node != nil {
			liveNodes[node.ID] = true
		}
	}
	liveGates := make(map[string]bool, len(g.UI.LogicGates))
	for _, gate := range g.UI.LogicGates {
		liveGates[gate.ID] = true
	}
	for key := range g.UI.EdgeLabels {
		if !liveEdges[key] {
			delete(g.UI.EdgeLabels, key)
		}
	}
	for key := range g.UI.WireVertices {
		alive := liveEdges[key]
		if target, ok := strings.CutPrefix(key, "gate:"); ok {
			alive = liveNodes[target]
		} else if rest, ok := strings.CutPrefix(key, "logic:"); ok {
			gateID, _, _ := strings.Cut(rest, ":")
			alive = liveGates[gateID]
		}
		if !alive {
			delete(g.UI.WireVertices, key)
		}
	}
}

// removeNodeUIState drops every editor-only entry that referenced a node that
// is going away, so no stale position or plan is left behind.
func removeNodeUIState(g *engine.Graph, removing map[string]bool) {
	if g.UI == nil {
		return
	}
	for id := range removing {
		delete(g.UI.Positions, id)
		delete(g.UI.Gates, id)
		delete(g.UI.Plans, id)
		delete(g.UI.NodeStyles, id)
		delete(g.UI.EntryOverrides, id)
	}
	timelineOrder := g.UI.TimelineOrder[:0]
	for _, nodeID := range g.UI.TimelineOrder {
		if !removing[nodeID] {
			timelineOrder = append(timelineOrder, nodeID)
		}
	}
	g.UI.TimelineOrder = timelineOrder

	liveEdges := make(map[string]bool, len(g.Edges))
	for _, edge := range g.Edges {
		if edge != nil {
			liveEdges[edge.From+"->"+edge.To] = true
		}
	}
	for edgeKey := range g.UI.EdgeLabels {
		if !liveEdges[edgeKey] {
			delete(g.UI.EdgeLabels, edgeKey)
		}
	}
	for wireKey := range g.UI.WireVertices {
		if target, ok := strings.CutPrefix(wireKey, "gate:"); ok {
			if removing[target] {
				delete(g.UI.WireVertices, wireKey)
			}
			continue
		}
		if !liveEdges[wireKey] {
			delete(g.UI.WireVertices, wireKey)
		}
	}
}
