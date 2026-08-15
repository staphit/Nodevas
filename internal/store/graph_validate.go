// Validating a graph before it is written to disk.

package store

import (
	"fmt"
	"math"
	"strings"

	"nodevas/internal/engine"
)

// relationGateOperator reports whether a gate marks its edges with a relation
// instead of writing a condition. Those gates are many-to-many.
func relationGateOperator(operator string) bool {
	return operator == "optional" || operator == "deprecated"
}

func ValidateGraphForStorage(g *engine.Graph) error {
	if g == nil {
		return fmt.Errorf("graph is null")
	}
	// Rewriting `requires` rebuilds the edge list, so labels and bend points of
	// dropped edges are swept before validation.
	pruneEdgeDecorations(g)
	if len(g.Nodes) > MaxGraphNodes {
		return fmt.Errorf("graph has too many nodes (maximum %d)", MaxGraphNodes)
	}
	if len(g.Edges) > maxGraphEdges {
		return fmt.Errorf("graph has too many edges (maximum %d)", maxGraphEdges)
	}
	if len(g.Users) > maxGraphUsers {
		return fmt.Errorf("graph has too many users (maximum %d)", maxGraphUsers)
	}
	userIDs := make(map[string]struct{}, len(g.Users))
	userNames := make(map[string]struct{}, len(g.Users))
	for index, user := range g.Users {
		if !engine.ValidNodeID(user.ID) {
			return fmt.Errorf("invalid user id %q at index %d", user.ID, index)
		}
		name := strings.TrimSpace(user.Name)
		if name == "" || len(name) > 256 {
			return fmt.Errorf("user %q has an empty or oversized name", user.ID)
		}
		if _, exists := userIDs[user.ID]; exists {
			return fmt.Errorf("duplicate user id %q", user.ID)
		}
		normalizedName := strings.ToLower(name)
		if _, exists := userNames[normalizedName]; exists {
			return fmt.Errorf("duplicate user name %q", name)
		}
		userIDs[user.ID] = struct{}{}
		userNames[normalizedName] = struct{}{}
	}
	ids := make(map[string]struct{}, len(g.Nodes))
	for index, node := range g.Nodes {
		if node == nil {
			return fmt.Errorf("node at index %d is null", index)
		}
		if !engine.ValidNodeID(node.ID) {
			return fmt.Errorf("invalid node id %q", node.ID)
		}
		if _, exists := ids[node.ID]; exists {
			return fmt.Errorf("duplicate node id %q", node.ID)
		}
		if len(node.Requires) > 64<<10 || len(node.Title) > 64<<10 {
			return fmt.Errorf("node %q contains an oversized title or requirement", node.ID)
		}
		switch node.Priority {
		case "", "urgent", "high", "medium", "low":
		default:
			return fmt.Errorf("node %q has invalid priority %q", node.ID, node.Priority)
		}
		// Every write path funnels through here, so "all" arriving from any
		// client normalizes to the stored zero value before the enum check.
		node.WriteAccess = normalizeWriteAccess(node.WriteAccess)
		switch node.WriteAccess {
		case engine.WriteAccessAll, engine.WriteAccessWorker,
			engine.WriteAccessOrchestrator, engine.WriteAccessHumanOnly:
		default:
			return fmt.Errorf("node %q has invalid write_access %q", node.ID, node.WriteAccess)
		}
		if len(node.Tags) > 64 {
			return fmt.Errorf("node %q has too many tags", node.ID)
		}
		for _, tag := range node.Tags {
			if strings.TrimSpace(tag) == "" || len(tag) > 128 {
				return fmt.Errorf("node %q contains an empty or oversized tag", node.ID)
			}
		}
		if node.Assignee != "" {
			if _, exists := userIDs[node.Assignee]; !exists {
				return fmt.Errorf("node %q references unknown assignee %q", node.ID, node.Assignee)
			}
		}
		for _, effect := range node.Effects {
			if len(effect.Set) > 4096 {
				return fmt.Errorf("node %q contains an oversized effect", node.ID)
			}
		}
		if len(node.Links) > maxNodeLinks {
			return fmt.Errorf("node %q has too many links (maximum %d)", node.ID, maxNodeLinks)
		}
		for _, link := range node.Links {
			if !engine.ValidNodeID(link.Node) {
				return fmt.Errorf("node %q links to invalid node id %q", node.ID, link.Node)
			}
			label := strings.TrimSpace(link.Label)
			if label == "" || len(label) > 128 {
				return fmt.Errorf("node %q has a link with an empty or oversized label", node.ID)
			}
			if link.Project != "" && !engine.ValidProjectRef(link.Project) {
				return fmt.Errorf("node %q links to invalid project %q", node.ID, link.Project)
			}
		}
		ids[node.ID] = struct{}{}
	}
	for index, edge := range g.Edges {
		if edge == nil {
			return fmt.Errorf("edge at index %d is null", index)
		}
		switch edge.Relation {
		case engine.RelationRequired, engine.RelationOptional, engine.RelationDeprecated:
		default:
			return fmt.Errorf("edge %d has unknown relation %q", index, edge.Relation)
		}
		switch edge.Line {
		case "", "solid", "dashed", "dotted":
		default:
			return fmt.Errorf("edge %d has unknown line %q", index, edge.Line)
		}
	}
	if g.UI != nil {
		if len(g.UI.TimelineOrder) > MaxGraphNodes {
			return fmt.Errorf("timeline order has too many nodes (maximum %d)", MaxGraphNodes)
		}
		timelineIDs := make(map[string]struct{}, len(g.UI.TimelineOrder))
		for _, nodeID := range g.UI.TimelineOrder {
			if _, exists := ids[nodeID]; !exists {
				return fmt.Errorf("timeline order references unknown node %q", nodeID)
			}
			if _, exists := timelineIDs[nodeID]; exists {
				return fmt.Errorf("timeline order contains duplicate node %q", nodeID)
			}
			timelineIDs[nodeID] = struct{}{}
		}
		if len(g.UI.Gates) > MaxGraphNodes {
			return fmt.Errorf("graph has too many dependency-gate placements (maximum %d)", MaxGraphNodes)
		}
		for nodeID, placement := range g.UI.Gates {
			if _, exists := ids[nodeID]; !exists {
				return fmt.Errorf("dependency-gate placement references unknown node %q", nodeID)
			}
			if placement.X != nil || placement.Y != nil {
				if placement.X == nil || placement.Y == nil {
					return fmt.Errorf("dependency-gate placement %q must contain both x and y", nodeID)
				}
				if math.IsNaN(*placement.X) || math.IsInf(*placement.X, 0) ||
					math.IsNaN(*placement.Y) || math.IsInf(*placement.Y, 0) {
					return fmt.Errorf("dependency-gate placement %q has invalid coordinates", nodeID)
				}
			} else {
				ratio := 0.0
				if placement.Ratio != nil {
					ratio = *placement.Ratio
				}
				if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
					return fmt.Errorf("dependency-gate placement %q has invalid ratio", nodeID)
				}
			}
		}
		if len(g.UI.PlanStatuses) > 64 {
			return fmt.Errorf("graph has too many custom plan statuses (maximum 64)")
		}
		planStatuses := map[engine.Status]struct{}{
			engine.StatusStarted:    {},
			engine.StatusInProgress: {},
			engine.StatusDone:       {},
		}
		planStatusLabels := make(map[string]struct{}, len(g.UI.PlanStatuses))
		for _, definition := range g.UI.PlanStatuses {
			if !strings.HasPrefix(definition.ID, "custom-") || !engine.ValidNodeID(definition.ID) {
				return fmt.Errorf("invalid custom plan status id %q", definition.ID)
			}
			label := strings.TrimSpace(definition.Label)
			if label == "" || len(label) > 128 {
				return fmt.Errorf("custom plan status %q has an empty or oversized label", definition.ID)
			}
			status := engine.Status(definition.ID)
			if _, exists := planStatuses[status]; exists {
				return fmt.Errorf("duplicate plan status %q", definition.ID)
			}
			normalizedLabel := strings.ToLower(label)
			if _, exists := planStatusLabels[normalizedLabel]; exists {
				return fmt.Errorf("duplicate custom plan status label %q", label)
			}
			planStatuses[status] = struct{}{}
			planStatusLabels[normalizedLabel] = struct{}{}
		}
		if len(g.UI.LogicGates) > maxLogicGates {
			return fmt.Errorf("graph has too many logic gates (maximum %d)", maxLogicGates)
		}
		gateIDs := make(map[string]struct{}, len(g.UI.LogicGates))
		gateOutputs := make(map[string]string, len(g.UI.LogicGates))
		for index, gate := range g.UI.LogicGates {
			if !engine.ValidNodeID(gate.ID) {
				return fmt.Errorf("invalid logic gate id %q at index %d", gate.ID, index)
			}
			if _, exists := gateIDs[gate.ID]; exists {
				return fmt.Errorf("duplicate logic gate id %q", gate.ID)
			}
			gateIDs[gate.ID] = struct{}{}
			switch gate.Operator {
			// "optional" and "deprecated" are relation gates: they mark their
			// edges instead of writing a condition on the output node.
			case "must", "and", "or", "xor", "nand", "nor", "optional", "deprecated":
			default:
				return fmt.Errorf("logic gate %q has invalid operator %q", gate.ID, gate.Operator)
			}
			if math.IsNaN(gate.X) || math.IsInf(gate.X, 0) ||
				math.IsNaN(gate.Y) || math.IsInf(gate.Y, 0) {
				return fmt.Errorf("logic gate %q has invalid coordinates", gate.ID)
			}
			if len(gate.Inputs) > MaxGraphNodes {
				return fmt.Errorf("logic gate %q has too many inputs", gate.ID)
			}
			inputs := make(map[string]struct{}, len(gate.Inputs))
			for _, input := range gate.Inputs {
				if _, exists := ids[input]; !exists {
					return fmt.Errorf("logic gate %q references unknown input %q", gate.ID, input)
				}
				if _, exists := inputs[input]; exists {
					return fmt.Errorf("logic gate %q has duplicate input %q", gate.ID, input)
				}
				inputs[input] = struct{}{}
			}
			if gate.Operator == "must" && len(gate.Inputs) > 1 {
				return fmt.Errorf("MUST logic gate %q accepts only one input", gate.ID)
			}
			outputs := gate.Outputs
			if len(outputs) == 0 && gate.Output != "" {
				outputs = []string{gate.Output}
			}
			if len(outputs) > 1 && !relationGateOperator(gate.Operator) {
				return fmt.Errorf("logic gate %q accepts only one output", gate.ID)
			}
			seenOutputs := make(map[string]struct{}, len(outputs))
			for _, output := range outputs {
				if _, exists := ids[output]; !exists {
					return fmt.Errorf("logic gate %q references unknown output %q", gate.ID, output)
				}
				if _, exists := seenOutputs[output]; exists {
					return fmt.Errorf("logic gate %q has duplicate output %q", gate.ID, output)
				}
				seenOutputs[output] = struct{}{}
				if owner, exists := gateOutputs[output]; exists {
					return fmt.Errorf(
						"logic gates %q and %q share output %q",
						owner, gate.ID, output,
					)
				}
				gateOutputs[output] = gate.ID
			}
			if len(gate.Applied) > 64<<10 {
				return fmt.Errorf("logic gate %q contains an oversized applied expression", gate.ID)
			}
		}
		for nodeID, plans := range g.UI.Plans {
			if len(plans) > 32 {
				return fmt.Errorf("node %q has too many plan milestones", nodeID)
			}
			for _, plan := range plans {
				if _, exists := planStatuses[plan.Status]; !exists {
					return fmt.Errorf("node %q has invalid plan status %q", nodeID, plan.Status)
				}
				if len(plan.Note) > 4096 {
					return fmt.Errorf("node %q contains an oversized plan note", nodeID)
				}
			}
		}
		if len(g.UI.NodeStyles) > MaxGraphNodes {
			return fmt.Errorf("graph has too many node styles")
		}
		if len(g.UI.EntryOverrides) > MaxGraphNodes {
			return fmt.Errorf("graph has too many entry overrides")
		}
		for nodeID := range g.UI.EntryOverrides {
			if _, exists := ids[nodeID]; !exists {
				return fmt.Errorf("entry override references unknown node %q", nodeID)
			}
		}
		for nodeID, style := range g.UI.NodeStyles {
			if _, exists := ids[nodeID]; !exists {
				return fmt.Errorf("node style references unknown node %q", nodeID)
			}
			if math.IsNaN(style.Width) || math.IsInf(style.Width, 0) ||
				math.IsNaN(style.Height) || math.IsInf(style.Height, 0) ||
				(style.Width != 0 && (style.Width < 120 || style.Width > 360)) ||
				(style.Height != 0 && (style.Height < 52 || style.Height > 180)) {
				return fmt.Errorf("node style %q has invalid dimensions", nodeID)
			}
			if len(style.Color) > 32 {
				return fmt.Errorf("node style %q has an oversized color", nodeID)
			}
		}
		if len(g.UI.Groups) > 512 || len(g.UI.Annotations) > 512 {
			return fmt.Errorf("graph has too many canvas decorations")
		}
		decorationIDs := map[string]struct{}{}
		validateDecoration := func(id string, x, y, width, height float64, text, color string) error {
			if !engine.ValidNodeID(id) {
				return fmt.Errorf("canvas decoration has invalid id %q", id)
			}
			if _, exists := decorationIDs[id]; exists {
				return fmt.Errorf("duplicate canvas decoration id %q", id)
			}
			decorationIDs[id] = struct{}{}
			if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) ||
				math.IsNaN(width) || math.IsInf(width, 0) ||
				math.IsNaN(height) || math.IsInf(height, 0) ||
				x < 0 || y < 0 || width < 80 || width > 10000 || height < 60 || height > 10000 {
				return fmt.Errorf("canvas decoration %q has invalid geometry", id)
			}
			if len(text) > 64<<10 || len(color) > 32 {
				return fmt.Errorf("canvas decoration %q contains oversized content", id)
			}
			return nil
		}
		for _, group := range g.UI.Groups {
			if err := validateDecoration(group.ID, group.X, group.Y, group.Width, group.Height, group.Title, group.Color); err != nil {
				return err
			}
		}
		for _, annotation := range g.UI.Annotations {
			if err := validateDecoration(annotation.ID, annotation.X, annotation.Y, annotation.Width, annotation.Height, annotation.Text, annotation.Color); err != nil {
				return err
			}
		}
		if len(g.UI.SavedViews) > 128 {
			return fmt.Errorf("graph has too many saved views")
		}
		viewIDs := map[string]struct{}{}
		for _, view := range g.UI.SavedViews {
			if !engine.ValidNodeID(view.ID) || strings.TrimSpace(view.Name) == "" || len(view.Name) > 128 {
				return fmt.Errorf("saved view %q has an invalid id or name", view.ID)
			}
			if _, exists := viewIDs[view.ID]; exists {
				return fmt.Errorf("duplicate saved view id %q", view.ID)
			}
			viewIDs[view.ID] = struct{}{}
			switch view.Sort {
			case "", "manual", "title", "priority", "status", "assignee":
			default:
				return fmt.Errorf("saved view %q has invalid sort %q", view.ID, view.Sort)
			}
		}
	}
	return nil
}
