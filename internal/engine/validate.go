package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nodevas/internal/engine/dsl"
)

// Issue is a validation finding.
type Issue struct {
	Severity string `json:"severity"` // "error" | "warning"
	NodeID   string `json:"nodeId,omitempty"`
	Field    string `json:"field,omitempty"`  // e.g. "requires"
	Offset   int    `json:"offset,omitempty"` // byte offset within the field, -1 if n/a
	Msg      string `json:"msg"`
}

// Validate checks the whole graph: DSL syntax, dangling references,
// duplicate ids, dependency cycles, and disconnected nodes.
func Validate(g *Graph) []Issue {
	var issues []Issue
	add := func(sev, nodeID, field string, off int, format string, args ...any) {
		issues = append(issues, Issue{Severity: sev, NodeID: nodeID, Field: field, Offset: off, Msg: fmt.Sprintf(format, args...)})
	}
	if g == nil {
		add("error", "", "", -1, "graph is null")
		return issues
	}
	if g.Version < 1 {
		add("error", "", "version", -1, "version must be at least 1")
	}

	ids := map[string]bool{}
	for index, n := range g.Nodes {
		if n == nil {
			add("error", "", "nodes", -1, "node at index %d is null", index)
			continue
		}
		if n.ID == "" {
			add("error", "", "id", -1, "node with empty id")
			continue
		}
		if !ValidNodeID(n.ID) {
			add("error", n.ID, "id", -1, "invalid node id %q", n.ID)
		}
		if ids[n.ID] {
			add("error", n.ID, "id", -1, "duplicate node id %q", n.ID)
		}
		switch n.Priority {
		case "", "urgent", "high", "medium", "low":
		default:
			add("error", n.ID, "priority", -1, "invalid priority %q", n.Priority)
		}
		ids[n.ID] = true
		for i, effect := range n.Effects {
			if err := ValidateEffect(effect.Set); err != nil {
				add("error", n.ID, fmt.Sprintf("effects[%d]", i), -1, "%s", err)
			}
		}
	}

	// requires: syntax + references. deps collects node-ref edges for cycle
	// detection (not/nor references count as dependencies too).
	deps := map[string][]string{}
	for _, n := range g.Nodes {
		if n == nil || n.ID == "" {
			continue
		}
		expr, perr := n.RequiresExpr()
		if perr != nil {
			add("error", n.ID, "requires", perr.Offset, "syntax error: %s", perr.Msg)
			continue
		}
		if expr == nil {
			continue
		}
		refs := dsl.NodeRefs(expr)
		deps[n.ID] = refs
		for _, ref := range refs {
			if !ids[ref] {
				add("error", n.ID, "requires", -1, "unknown node %q", ref)
			}
			if ref == n.ID {
				add("error", n.ID, "requires", -1, "node depends on itself")
			}
		}
		for _, f := range dsl.FlagRefs(expr) {
			if _, ok := g.Flags[f]; !ok {
				add("warning", n.ID, "requires", -1, "flag %q is not declared in graph flags", f)
			}
		}
	}

	// Edge endpoint checks.
	edgeKeys := map[string]bool{}
	for index, e := range g.Edges {
		if e == nil {
			add("error", "", "edges", -1, "edge at index %d is null", index)
			continue
		}
		if !ids[e.From] {
			add("error", "", "edges", -1, "edge from unknown node %q", e.From)
		}
		if !ids[e.To] {
			add("error", "", "edges", -1, "edge to unknown node %q", e.To)
		}
		edgeKeys[e.From+"->"+e.To] = true
	}

	// Timeline plans are presentation-only expected milestones. Keep them
	// structurally valid and separate from actual run history.
	if g.UI != nil {
		for nodeID, position := range g.UI.Positions {
			if !ids[nodeID] {
				add("error", nodeID, "ui.positions", -1, "position references unknown node %q", nodeID)
			}
			if math.IsNaN(position.X) || math.IsInf(position.X, 0) ||
				math.IsNaN(position.Y) || math.IsInf(position.Y, 0) {
				add("error", nodeID, "ui.positions", -1, "position must contain finite coordinates")
			}
		}
		for nodeID, placement := range g.UI.Gates {
			if !ids[nodeID] {
				add("error", nodeID, "ui.gates", -1, "gate placement references unknown node %q", nodeID)
			}
			if placement.X != nil || placement.Y != nil {
				if placement.X == nil || placement.Y == nil {
					add("error", nodeID, "ui.gates", -1, "gate placement must contain both x and y coordinates")
				} else if math.IsNaN(*placement.X) || math.IsInf(*placement.X, 0) ||
					math.IsNaN(*placement.Y) || math.IsInf(*placement.Y, 0) {
					add("error", nodeID, "ui.gates", -1, "gate placement must contain finite coordinates")
				}
			} else {
				ratio := 0.0
				if placement.Ratio != nil {
					ratio = *placement.Ratio
				}
				if math.IsNaN(ratio) || math.IsInf(ratio, 0) ||
					ratio < 0 || ratio > 1 {
					add("error", nodeID, "ui.gates", -1, "gate ratio must be a finite number between 0 and 1")
				}
			}
		}
		for edgeKey, placement := range g.UI.EdgeLabels {
			if !edgeKeys[edgeKey] {
				add("error", "", "ui.edge_labels", -1, "label placement references unknown edge %q", edgeKey)
			}
			if math.IsNaN(placement.Ratio) || math.IsInf(placement.Ratio, 0) ||
				placement.Ratio < 0 || placement.Ratio > 1 {
				add("error", "", "ui.edge_labels", -1, "label ratio must be a finite number between 0 and 1")
			}
		}
		for wireKey, vertices := range g.UI.WireVertices {
			validKey := edgeKeys[wireKey]
			if strings.HasPrefix(wireKey, "gate:") {
				validKey = ids[strings.TrimPrefix(wireKey, "gate:")]
			}
			if !validKey {
				add("error", "", "ui.wire_vertices", -1, "wire vertices reference unknown edge or gate %q", wireKey)
			}
			if len(vertices) > 64 {
				add("error", "", "ui.wire_vertices", -1, "wire %q has too many vertices", wireKey)
			}
			for _, vertex := range vertices {
				if math.IsNaN(vertex.X) || math.IsInf(vertex.X, 0) ||
					math.IsNaN(vertex.Y) || math.IsInf(vertex.Y, 0) {
					add("error", "", "ui.wire_vertices", -1, "wire %q vertices must contain finite coordinates", wireKey)
					break
				}
			}
		}
		planStatuses := map[Status]bool{
			StatusStarted:    true,
			StatusInProgress: true,
			StatusDone:       true,
		}
		planStatusLabels := map[string]bool{}
		if len(g.UI.PlanStatuses) > 64 {
			add("error", "", "ui.plan_statuses", -1, "too many custom plan statuses (maximum 64)")
		}
		for _, definition := range g.UI.PlanStatuses {
			status := Status(definition.ID)
			if !strings.HasPrefix(definition.ID, "custom-") || !ValidNodeID(definition.ID) {
				add("error", "", "ui.plan_statuses", -1, "invalid custom plan status id %q", definition.ID)
			}
			label := strings.TrimSpace(definition.Label)
			if label == "" || len(label) > 128 {
				add("error", "", "ui.plan_statuses", -1, "custom plan status %q has an empty or oversized label", definition.ID)
			}
			if planStatuses[status] {
				add("error", "", "ui.plan_statuses", -1, "duplicate plan status %q", definition.ID)
			}
			normalizedLabel := strings.ToLower(label)
			if planStatusLabels[normalizedLabel] {
				add("error", "", "ui.plan_statuses", -1, "duplicate custom plan status label %q", label)
			}
			planStatuses[status] = true
			planStatusLabels[normalizedLabel] = true
		}
		for nodeID, plans := range g.UI.Plans {
			if !ids[nodeID] {
				add("error", nodeID, "ui.plans", -1, "plan references unknown node %q", nodeID)
			}
			seen := map[Status]bool{}
			dates := map[Status]string{}
			for _, plan := range plans {
				if _, err := time.Parse("2006-01-02", plan.Date); err != nil {
					add("error", nodeID, "ui.plans", -1, "invalid plan date %q; want YYYY-MM-DD", plan.Date)
				}
				if !planStatuses[plan.Status] {
					add("error", nodeID, "ui.plans", -1, "invalid plan status %q", plan.Status)
					continue
				}
				if seen[plan.Status] {
					add("error", nodeID, "ui.plans", -1, "duplicate %s plan milestone", plan.Status)
				}
				seen[plan.Status] = true
				dates[plan.Status] = plan.Date
			}
			if dates[StatusStarted] != "" && dates[StatusInProgress] != "" && dates[StatusStarted] > dates[StatusInProgress] {
				add("error", nodeID, "ui.plans", -1, "planned progress precedes planned start")
			}
			if dates[StatusInProgress] != "" && dates[StatusDone] != "" && dates[StatusInProgress] > dates[StatusDone] {
				add("error", nodeID, "ui.plans", -1, "planned completion precedes planned progress")
			}
			if dates[StatusStarted] != "" && dates[StatusDone] != "" && dates[StatusStarted] > dates[StatusDone] {
				add("error", nodeID, "ui.plans", -1, "planned completion precedes planned start")
			}
		}
		for nodeID, style := range g.UI.NodeStyles {
			if !ids[nodeID] {
				add("error", nodeID, "ui.node_styles", -1, "node style references unknown node %q", nodeID)
			}
			if math.IsNaN(style.Width) || math.IsInf(style.Width, 0) ||
				math.IsNaN(style.Height) || math.IsInf(style.Height, 0) ||
				(style.Width != 0 && (style.Width < 120 || style.Width > 360)) ||
				(style.Height != 0 && (style.Height < 52 || style.Height > 180)) {
				add("error", nodeID, "ui.node_styles", -1, "node style has invalid dimensions")
			}
		}
		decorationIDs := map[string]bool{}
		checkDecoration := func(id, field string, x, y, width, height float64) {
			if !ValidNodeID(id) {
				add("error", "", field, -1, "invalid canvas decoration id %q", id)
			}
			if decorationIDs[id] {
				add("error", "", field, -1, "duplicate canvas decoration id %q", id)
			}
			decorationIDs[id] = true
			if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) ||
				math.IsNaN(width) || math.IsInf(width, 0) ||
				math.IsNaN(height) || math.IsInf(height, 0) ||
				x < 0 || y < 0 || width < 80 || height < 60 {
				add("error", "", field, -1, "canvas decoration %q has invalid geometry", id)
			}
		}
		for _, group := range g.UI.Groups {
			checkDecoration(group.ID, "ui.groups", group.X, group.Y, group.Width, group.Height)
		}
		for _, annotation := range g.UI.Annotations {
			checkDecoration(annotation.ID, "ui.annotations", annotation.X, annotation.Y, annotation.Width, annotation.Height)
		}
	}

	// Cycle detection over requires references (must form a DAG).
	for _, cycle := range findCycles(deps) {
		add("error", cycle[0], "requires", -1, "dependency cycle: %s", joinCycle(cycle))
	}

	// And the same over the visual projection. Readiness comes from `requires`,
	// but a wire cycle still means the projection is not a faithful, usable
	// picture of that executable condition and must be repaired.
	edgeDeps := map[string][]string{}
	for _, e := range g.Edges {
		if e == nil || !e.Blocks() {
			continue
		}
		edgeDeps[e.To] = append(edgeDeps[e.To], e.From)
	}
	for _, cycle := range findCycles(edgeDeps) {
		add("error", cycle[0], "edges", -1,
			"dependency cycle through the visual wires: %s; repair the projection to match requires",
			joinCycle(cycle))
	}

	// Disconnected nodes: no requires, not referenced, no edges.
	if len(g.Nodes) > 1 {
		connected := map[string]bool{}
		for id, refs := range deps {
			if len(refs) > 0 {
				connected[id] = true
				for _, r := range refs {
					connected[r] = true
				}
			}
		}
		for _, e := range g.Edges {
			if e == nil {
				continue
			}
			connected[e.From] = true
			connected[e.To] = true
		}
		for _, n := range g.Nodes {
			if n == nil {
				continue
			}
			if n.ID != "" && !connected[n.ID] {
				add("warning", n.ID, "", -1, "node is not connected to anything")
			}
		}
	}

	return issues
}

// findCycles returns each dependency cycle found (as a node id sequence,
// first node repeated logically at the end). Deterministic order.
func findCycles(deps map[string][]string) [][]string {
	var order []string
	for id := range deps {
		order = append(order, id)
	}
	sort.Strings(order)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var cycles [][]string
	var stack []string

	var visit func(id string)
	visit = func(id string) {
		color[id] = gray
		stack = append(stack, id)
		for _, dep := range deps[id] {
			switch color[dep] {
			case white:
				visit(dep)
			case gray:
				// Found a cycle: slice the stack from dep's position.
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == dep {
						cyc := append([]string{}, stack[i:]...)
						cycles = append(cycles, cyc)
						break
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}

	for _, id := range order {
		if color[id] == white {
			visit(id)
		}
	}
	return cycles
}

func joinCycle(cycle []string) string {
	var sb strings.Builder
	for _, id := range cycle {
		sb.WriteString(id)
		sb.WriteString(" -> ")
	}
	sb.WriteString(cycle[0])
	return sb.String()
}
