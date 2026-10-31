// Dependencies named only in a node's `requires`.
//
// A hand-written graph.yaml may state a dependency in `requires` without the
// edge that draws it. The board draws the wire from the expression anyway, so
// everything a person can do to a wire has to work on that one too: styling it
// or bending it writes the explicit edge it stands for (a required edge, which
// is exactly what the expression already says), and the cycle check that guards
// adding a dependency follows these links as well as the wires.

package store

import (
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
	"nodevas/internal/locale"
)

// requiresWireGate names the logic gate that owns the from->to wire, if any.
// A gate-controlled condition is drawn through the gate, never as a direct
// wire, so no direct edge is written for it.
func requiresWireGate(graph *engine.Graph, from, to string) string {
	if gateID := logicGateOwningEdge(graph, from, to); gateID != "" {
		return gateID
	}
	return booleanGateForOutput(graph, to)
}

// materializeRequiresWires writes the explicit edge for every requires-only
// dependency that carries a bend or a label placement, so those decorations
// are kept with an edge instead of being swept as decorations of a missing one.
// The edge is a required one, which the target's `requires` already says, so
// what the graph means does not change.
func materializeRequiresWires(g *engine.Graph) {
	if g == nil || g.UI == nil || (len(g.UI.WireVertices) == 0 && len(g.UI.EdgeLabels) == 0) {
		return
	}
	live := make(map[string]bool, len(g.Edges))
	for _, edge := range g.Edges {
		if edge != nil {
			live[edge.From+"->"+edge.To] = true
		}
	}
	consider := func(key string) {
		if live[key] || strings.HasPrefix(key, "gate:") || strings.HasPrefix(key, "logic:") {
			return
		}
		from, to, ok := strings.Cut(key, "->")
		if !ok || from == "" || to == "" || from == to || g.NodeByID(from) == nil {
			return
		}
		target := g.NodeByID(to)
		if target == nil || !nodeRequiresRef(target, from) || requiresWireGate(g, from, to) != "" {
			return
		}
		g.Edges = append(g.Edges, &engine.Edge{From: from, To: to, Relation: engine.RelationRequired})
		live[key] = true
	}
	for key := range g.UI.WireVertices {
		consider(key)
	}
	for key := range g.UI.EdgeLabels {
		consider(key)
	}
}

// dependsOn reports whether node already waits for from, through its
// `requires` or a required wire.
func dependsOn(graph *engine.Graph, node *engine.Node, from string) bool {
	if nodeRequiresRef(node, from) {
		return true
	}
	for _, edge := range graph.Edges {
		if edge != nil && edge.Blocks() && edge.From == from && edge.To == node.ID {
			return true
		}
	}
	return false
}

// refuseDependencyCycle refuses making to wait for from when from already
// waits, directly or not, for to: the loop could never become ready. A
// dependency counts whether it is a required wire or only named in `requires`.
// The error names the loop the way Validate does, each node waiting for the
// next.
func refuseDependencyCycle(graph *engine.Graph, from, to string) error {
	deps := make(map[string][]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		if expr, err := dsl.Parse(node.Requires); err == nil && expr != nil {
			deps[node.ID] = append(deps[node.ID], dsl.NodeRefs(expr)...)
		}
	}
	for _, edge := range graph.Edges {
		if edge != nil && edge.Blocks() {
			deps[edge.To] = append(deps[edge.To], edge.From)
		}
	}
	// Breadth first from `from`, so the loop named is a shortest one.
	previous := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == to {
			path := []string{}
			for id := to; id != ""; id = previous[id] {
				path = append(path, id)
			}
			// path runs to … from; the loop reads to -> from -> … -> to.
			hops := []string{to}
			for i := len(path) - 1; i >= 0; i-- {
				hops = append(hops, path[i])
			}
			cycle := strings.Join(hops, " -> ")
			return locale.Errorf(
				"cannot make %s wait for %s: it would create a dependency cycle (%s)",
				"無法讓「%s」等待「%s」：這會形成相依循環（%s）",
				to, from, cycle)
		}
		for _, next := range deps[current] {
			if _, seen := previous[next]; !seen {
				previous[next] = current
				queue = append(queue, next)
			}
		}
	}
	return nil
}
