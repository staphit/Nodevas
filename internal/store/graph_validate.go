// Validating a graph before it is written to disk.

package store

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"nodevas/internal/engine"
	"nodevas/internal/locale"
)

// relationGateOperator reports whether a gate marks its edges with a relation
// instead of writing a condition. Those gates are many-to-many.
func relationGateOperator(operator string) bool {
	return operator == "optional" || operator == "deprecated"
}

func ValidateGraphForStorage(g *engine.Graph) error {
	if g == nil {
		return locale.Errorf("graph is null", "圖是空值")
	}
	// Rewriting `requires` rebuilds the edge list, so labels and bend points of
	// dropped edges are swept before validation.
	pruneEdgeDecorations(g)
	if len(g.Nodes) > MaxGraphNodes {
		return locale.Errorf("graph has too many nodes (maximum %d)", "圖的節點太多（最多 %d 個）", MaxGraphNodes)
	}
	if len(g.Edges) > maxGraphEdges {
		return locale.Errorf("graph has too many edges (maximum %d)", "圖的連線太多（最多 %d 條）", maxGraphEdges)
	}
	if len(g.Users) > maxGraphUsers {
		return locale.Errorf("graph has too many users (maximum %d)", "圖的使用者太多（最多 %d 位）", maxGraphUsers)
	}
	if err := engine.ValidateNodeTypeDefs(g.NodeTypes); err != nil {
		return err
	}
	if !engine.ValidOfficeStyle(g.OfficeStyle) {
		return locale.Errorf("office_style: unknown style %q", "office_style：不明的樣式「%s」", g.OfficeStyle)
	}
	if err := engine.ValidateAgentRoleDefs(g.AgentRoles); err != nil {
		return err
	}
	if err := engine.ValidateGraphWorkflows(g); err != nil {
		return err
	}
	for _, node := range g.Nodes {
		if node == nil || node.Acceptance == nil {
			continue
		}
		// A client may send new criteria without ids; they get the next free
		// one, existing ids are kept.
		node.Acceptance.Items = engine.AssignAcceptanceIDs(node.Acceptance.Items)
		if node.Acceptance.Items == nil {
			node.Acceptance.Items = []engine.AcceptanceItem{}
		}
		if err := engine.ValidateNodeAcceptance(g, node); err != nil {
			return err
		}
	}
	userIDs := make(map[string]struct{}, len(g.Users))
	userNames := make(map[string]struct{}, len(g.Users))
	for index, user := range g.Users {
		if !engine.ValidNodeID(user.ID) {
			return locale.Errorf("invalid user id %q at index %d", "第 %[2]d 筆的使用者代號「%[1]s」無效", user.ID, index)
		}
		name := strings.TrimSpace(user.Name)
		if name == "" || len(name) > 256 {
			return locale.Errorf("user %q has an empty or oversized name", "使用者「%s」的名稱空白或過長", user.ID)
		}
		if _, exists := userIDs[user.ID]; exists {
			return locale.Errorf("duplicate user id %q", "重複的使用者代號「%s」", user.ID)
		}
		normalizedName := strings.ToLower(name)
		if _, exists := userNames[normalizedName]; exists {
			return locale.Errorf("duplicate user name %q", "重複的使用者名稱「%s」", name)
		}
		userIDs[user.ID] = struct{}{}
		userNames[normalizedName] = struct{}{}
	}
	ids := make(map[string]struct{}, len(g.Nodes))
	for index, node := range g.Nodes {
		if node == nil {
			return locale.Errorf("node at index %d is null", "第 %d 個節點是空值", index)
		}
		if !engine.ValidNodeID(node.ID) {
			return locale.Errorf("invalid node id %q", "節點代號「%s」無效", node.ID)
		}
		if _, exists := ids[node.ID]; exists {
			return locale.Errorf("duplicate node id %q", "重複的節點代號「%s」", node.ID)
		}
		if err := validateNodeForStorage(node, userIDs); err != nil {
			return err
		}
		ids[node.ID] = struct{}{}
	}
	for index, edge := range g.Edges {
		if edge == nil {
			return locale.Errorf("edge at index %d is null", "第 %d 條連線是空值", index)
		}
		switch edge.Relation {
		case engine.RelationRequired, engine.RelationOptional, engine.RelationDeprecated:
		default:
			return locale.Errorf("edge %d has unknown relation %q", "第 %d 條連線的關係「%s」不明", index, edge.Relation)
		}
		switch edge.Line {
		case "", "solid", "dashed", "dotted":
		default:
			return locale.Errorf("edge %d has unknown line %q", "第 %d 條連線的線型「%s」不明", index, edge.Line)
		}
	}
	if g.UI != nil {
		if len(g.UI.TimelineOrder) > MaxGraphNodes {
			return locale.Errorf("timeline order has too many nodes (maximum %d)", "時間軸順序的節點太多（最多 %d 個）", MaxGraphNodes)
		}
		timelineIDs := make(map[string]struct{}, len(g.UI.TimelineOrder))
		for _, nodeID := range g.UI.TimelineOrder {
			if _, exists := ids[nodeID]; !exists {
				return locale.Errorf("timeline order references unknown node %q", "時間軸順序參照了不明的節點「%s」", nodeID)
			}
			if _, exists := timelineIDs[nodeID]; exists {
				return locale.Errorf("timeline order contains duplicate node %q", "時間軸順序含有重複的節點「%s」", nodeID)
			}
			timelineIDs[nodeID] = struct{}{}
		}
		if len(g.UI.Gates) > MaxGraphNodes {
			return locale.Errorf("graph has too many dependency-gate placements (maximum %d)", "圖的相依閘位置太多（最多 %d 個）", MaxGraphNodes)
		}
		for nodeID, placement := range g.UI.Gates {
			if _, exists := ids[nodeID]; !exists {
				return locale.Errorf("dependency-gate placement references unknown node %q", "相依閘位置參照了不明的節點「%s」", nodeID)
			}
			if placement.X != nil || placement.Y != nil {
				if placement.X == nil || placement.Y == nil {
					return locale.Errorf("dependency-gate placement %q must contain both x and y", "相依閘位置「%s」必須同時有 x 與 y", nodeID)
				}
				if math.IsNaN(*placement.X) || math.IsInf(*placement.X, 0) ||
					math.IsNaN(*placement.Y) || math.IsInf(*placement.Y, 0) {
					return locale.Errorf("dependency-gate placement %q has invalid coordinates", "相依閘位置「%s」的座標無效", nodeID)
				}
			} else {
				ratio := 0.0
				if placement.Ratio != nil {
					ratio = *placement.Ratio
				}
				if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
					return locale.Errorf("dependency-gate placement %q has invalid ratio", "相依閘位置「%s」的比例無效", nodeID)
				}
			}
		}
		if len(g.UI.PlanStatuses) > 64 {
			return locale.Errorf("graph has too many custom plan statuses (maximum 64)", "圖的自訂計畫狀態太多（最多 64 個）")
		}
		planStatuses := map[engine.Status]struct{}{
			engine.StatusStarted:    {},
			engine.StatusInProgress: {},
			engine.StatusDone:       {},
		}
		planStatusLabels := make(map[string]struct{}, len(g.UI.PlanStatuses))
		for _, definition := range g.UI.PlanStatuses {
			if !strings.HasPrefix(definition.ID, "custom-") || !engine.ValidNodeID(definition.ID) {
				return locale.Errorf("invalid custom plan status id %q", "自訂計畫狀態代號「%s」無效", definition.ID)
			}
			label := strings.TrimSpace(definition.Label)
			if label == "" || len(label) > 128 {
				return locale.Errorf("custom plan status %q has an empty or oversized label", "自訂計畫狀態「%s」的標籤空白或過長", definition.ID)
			}
			status := engine.Status(definition.ID)
			if _, exists := planStatuses[status]; exists {
				return locale.Errorf("duplicate plan status %q", "重複的計畫狀態「%s」", definition.ID)
			}
			normalizedLabel := strings.ToLower(label)
			if _, exists := planStatusLabels[normalizedLabel]; exists {
				return locale.Errorf("duplicate custom plan status label %q", "重複的自訂計畫狀態標籤「%s」", label)
			}
			planStatuses[status] = struct{}{}
			planStatusLabels[normalizedLabel] = struct{}{}
		}
		if len(g.UI.LogicGates) > maxLogicGates {
			return locale.Errorf("graph has too many logic gates (maximum %d)", "圖的邏輯閘太多（最多 %d 個）", maxLogicGates)
		}
		gateIDs := make(map[string]struct{}, len(g.UI.LogicGates))
		gateOutputs := make(map[string]string, len(g.UI.LogicGates))
		for index, gate := range g.UI.LogicGates {
			if !engine.ValidNodeID(gate.ID) {
				return locale.Errorf("invalid logic gate id %q at index %d", "第 %[2]d 個邏輯閘的代號「%[1]s」無效", gate.ID, index)
			}
			if _, exists := gateIDs[gate.ID]; exists {
				return locale.Errorf("duplicate logic gate id %q", "重複的邏輯閘代號「%s」", gate.ID)
			}
			gateIDs[gate.ID] = struct{}{}
			switch gate.Operator {
			// "optional" and "deprecated" are relation gates: they mark their
			// edges instead of writing a condition on the output node.
			case "must", "and", "or", "xor", "nand", "nor", "optional", "deprecated":
			default:
				return locale.Errorf("logic gate %q has invalid operator %q", "邏輯閘「%s」的運算子「%s」無效", gate.ID, gate.Operator)
			}
			if math.IsNaN(gate.X) || math.IsInf(gate.X, 0) ||
				math.IsNaN(gate.Y) || math.IsInf(gate.Y, 0) {
				return locale.Errorf("logic gate %q has invalid coordinates", "邏輯閘「%s」的座標無效", gate.ID)
			}
			if len(gate.Inputs) > MaxGraphNodes {
				return locale.Errorf("logic gate %q has too many inputs", "邏輯閘「%s」的輸入太多", gate.ID)
			}
			inputs := make(map[string]struct{}, len(gate.Inputs))
			for _, input := range gate.Inputs {
				if _, exists := ids[input]; !exists {
					return locale.Errorf("logic gate %q references unknown input %q", "邏輯閘「%s」參照了不明的輸入「%s」", gate.ID, input)
				}
				if _, exists := inputs[input]; exists {
					return locale.Errorf("logic gate %q has duplicate input %q", "邏輯閘「%s」有重複的輸入「%s」", gate.ID, input)
				}
				inputs[input] = struct{}{}
			}
			if gate.Operator == "must" && len(gate.Inputs) > 1 {
				return locale.Errorf("MUST logic gate %q accepts only one input", "MUST 邏輯閘「%s」只接受一個輸入", gate.ID)
			}
			outputs := gate.Outputs
			if len(outputs) == 0 && gate.Output != "" {
				outputs = []string{gate.Output}
			}
			if len(outputs) > 1 && !relationGateOperator(gate.Operator) {
				return locale.Errorf("logic gate %q accepts only one output", "邏輯閘「%s」只接受一個輸出", gate.ID)
			}
			seenOutputs := make(map[string]struct{}, len(outputs))
			for _, output := range outputs {
				if _, exists := ids[output]; !exists {
					return locale.Errorf("logic gate %q references unknown output %q", "邏輯閘「%s」參照了不明的輸出「%s」", gate.ID, output)
				}
				if _, exists := seenOutputs[output]; exists {
					return locale.Errorf("logic gate %q has duplicate output %q", "邏輯閘「%s」有重複的輸出「%s」", gate.ID, output)
				}
				seenOutputs[output] = struct{}{}
				if owner, exists := gateOutputs[output]; exists {
					return locale.Errorf("logic gates %q and %q share output %q", "邏輯閘「%s」與「%s」共用輸出「%s」",
						owner, gate.ID, output,
					)
				}
				gateOutputs[output] = gate.ID
			}
			if len(gate.Applied) > 64<<10 {
				return locale.Errorf("logic gate %q contains an oversized applied expression", "邏輯閘「%s」套用的運算式過長", gate.ID)
			}
		}
		for nodeID, plans := range g.UI.Plans {
			if len(plans) > 32 {
				return locale.Errorf("node %q has too many plan milestones", "節點「%s」的計畫里程碑太多", nodeID)
			}
			for _, plan := range plans {
				if _, exists := planStatuses[plan.Status]; !exists {
					return locale.Errorf("node %q has invalid plan status %q", "節點「%s」的計畫狀態「%s」無效", nodeID, plan.Status)
				}
				if len(plan.Note) > 4096 {
					return locale.Errorf("node %q contains an oversized plan note", "節點「%s」的計畫備註過長", nodeID)
				}
			}
		}
		if len(g.UI.NodeStyles) > MaxGraphNodes {
			return locale.Errorf("graph has too many node styles", "圖的節點樣式太多")
		}
		if len(g.UI.EntryOverrides) > MaxGraphNodes {
			return locale.Errorf("graph has too many entry overrides", "圖的入口覆寫太多")
		}
		for nodeID := range g.UI.EntryOverrides {
			if _, exists := ids[nodeID]; !exists {
				return locale.Errorf("entry override references unknown node %q", "入口覆寫參照了不明的節點「%s」", nodeID)
			}
		}
		for nodeID, style := range g.UI.NodeStyles {
			if _, exists := ids[nodeID]; !exists {
				return locale.Errorf("node style references unknown node %q", "節點樣式參照了不明的節點「%s」", nodeID)
			}
			if math.IsNaN(style.Width) || math.IsInf(style.Width, 0) ||
				math.IsNaN(style.Height) || math.IsInf(style.Height, 0) ||
				(style.Width != 0 && (style.Width < 120 || style.Width > 360)) ||
				(style.Height != 0 && (style.Height < 52 || style.Height > 180)) {
				return locale.Errorf("node style %q has invalid dimensions", "節點樣式「%s」的尺寸無效", nodeID)
			}
			if !safeCSSColor(style.Color) || !safeCSSColor(style.TextColor) || !safeCSSColor(style.BorderColor) {
				return locale.Errorf("node style %q has an invalid color", "節點樣式「%s」的顏色無效", nodeID)
			}
		}
		if len(g.UI.Groups) > 512 || len(g.UI.Annotations) > 512 {
			return locale.Errorf("graph has too many canvas decorations", "圖的畫布裝飾太多")
		}
		decorationIDs := map[string]struct{}{}
		validateDecoration := func(id string, x, y, width, height float64, text, color string) error {
			if !engine.ValidNodeID(id) {
				return locale.Errorf("canvas decoration has invalid id %q", "畫布裝飾的代號「%s」無效", id)
			}
			if _, exists := decorationIDs[id]; exists {
				return locale.Errorf("duplicate canvas decoration id %q", "重複的畫布裝飾代號「%s」", id)
			}
			decorationIDs[id] = struct{}{}
			if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) ||
				math.IsNaN(width) || math.IsInf(width, 0) ||
				math.IsNaN(height) || math.IsInf(height, 0) ||
				x < 0 || y < 0 || width < 80 || width > 10000 || height < 60 || height > 10000 {
				return locale.Errorf("canvas decoration %q has invalid geometry", "畫布裝飾「%s」的位置大小無效", id)
			}
			if len(text) > 64<<10 {
				return locale.Errorf("canvas decoration %q contains oversized content", "畫布裝飾「%s」的內容過長", id)
			}
			if !safeCSSColor(color) {
				return locale.Errorf("canvas decoration %q has an invalid color", "畫布裝飾「%s」的顏色無效", id)
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
			return locale.Errorf("graph has too many saved views", "圖的已存檢視太多")
		}
		viewIDs := map[string]struct{}{}
		for _, view := range g.UI.SavedViews {
			if !engine.ValidNodeID(view.ID) || strings.TrimSpace(view.Name) == "" || len(view.Name) > 128 {
				return locale.Errorf("saved view %q has an invalid id or name", "已存檢視「%s」的代號或名稱無效", view.ID)
			}
			if _, exists := viewIDs[view.ID]; exists {
				return locale.Errorf("duplicate saved view id %q", "重複的已存檢視代號「%s」", view.ID)
			}
			viewIDs[view.ID] = struct{}{}
			switch view.Sort {
			case "", "manual", "title", "priority", "status", "assignee":
			default:
				return locale.Errorf("saved view %q has invalid sort %q", "已存檢視「%s」的排序「%s」無效", view.ID, view.Sort)
			}
		}
	}
	return nil
}

// validateNodeForStorage checks one node's own fields (not its id or its
// uniqueness, which need the whole graph). create_subgraph calls it per new
// node so an error can name the offending entry.
func validateNodeForStorage(node *engine.Node, userIDs map[string]struct{}) error {
	if len(node.Requires) > 64<<10 || len(node.Title) > 64<<10 {
		return locale.Errorf("node %q contains an oversized title or requirement", "節點「%s」的標題或前置條件過長", node.ID)
	}
	// An unknown kind is kept (it reads as Task), including a legacy
	// free-text one ("bug", "伏筆"), so old graphs stay saveable; only its
	// size and control characters are checked here. New unknown kinds are
	// refused on the write paths.
	if len(node.Kind) > 64 || strings.IndexFunc(node.Kind, unicode.IsControl) >= 0 {
		return locale.Errorf("node %q has an invalid type %q", "節點「%s」的類型「%s」無效", node.ID, node.Kind)
	}
	switch node.Priority {
	case "", "urgent", "high", "medium", "low":
	default:
		return locale.Errorf("node %q has invalid priority %q", "節點「%s」的優先順序「%s」無效", node.ID, node.Priority)
	}
	// Every write path funnels through here, so "all" arriving from any
	// client normalizes to the stored zero value before the enum check.
	node.WriteAccess = normalizeWriteAccess(node.WriteAccess)
	switch node.WriteAccess {
	case engine.WriteAccessAll, engine.WriteAccessWorker,
		engine.WriteAccessOrchestrator, engine.WriteAccessHumanOnly:
	default:
		return locale.Errorf("node %q has invalid write_access %q", "節點「%s」的 write_access「%s」無效", node.ID, node.WriteAccess)
	}
	if len(node.Tags) > 64 {
		return locale.Errorf("node %q has too many tags", "節點「%s」的標籤太多", node.ID)
	}
	for _, tag := range node.Tags {
		if strings.TrimSpace(tag) == "" || len(tag) > 128 {
			return locale.Errorf("node %q contains an empty or oversized tag", "節點「%s」有空白或過長的標籤", node.ID)
		}
	}
	if node.Assignee != "" {
		if _, exists := userIDs[node.Assignee]; !exists {
			return locale.Errorf("node %q references unknown assignee %q", "節點「%s」指派給不明的人員「%s」", node.ID, node.Assignee)
		}
	}
	for _, effect := range node.Effects {
		if len(effect.Set) > 4096 {
			return locale.Errorf("node %q contains an oversized effect", "節點「%s」的效果過長", node.ID)
		}
	}
	if len(node.Links) > maxNodeLinks {
		return locale.Errorf("node %q has too many links (maximum %d)", "節點「%s」的連結太多（最多 %d 個）", node.ID, maxNodeLinks)
	}
	for _, link := range node.Links {
		if !engine.ValidNodeID(link.Node) {
			return locale.Errorf("node %q links to invalid node id %q", "節點「%s」連結到無效的節點代號「%s」", node.ID, link.Node)
		}
		label := strings.TrimSpace(link.Label)
		if label == "" || len(label) > 128 {
			return locale.Errorf("node %q has a link with an empty or oversized label", "節點「%s」有標籤空白或過長的連結", node.ID)
		}
		if link.Project != "" && !engine.ValidProjectRef(link.Project) {
			return locale.Errorf("node %q links to invalid project %q", "節點「%s」連結到無效的專案「%s」", node.ID, link.Project)
		}
	}
	if len(node.Refs) > maxNodeRefs {
		return locale.Errorf("node %q has too many references (maximum %d)", "節點「%s」的參照太多（最多 %d 個）", node.ID, maxNodeRefs)
	}
	// Only the syntax of a reference is checked: a dangling one (a hand edit,
	// a deleted note) is tolerated so a project never fails to load. The
	// write paths that add references refuse unknown targets.
	seenRefs := make(map[string]struct{}, len(node.Refs))
	for _, ref := range node.Refs {
		if !engine.ValidNodeID(ref) {
			return locale.Errorf("node %q references invalid node id %q", "節點「%s」參照了無效的節點代號「%s」", node.ID, ref)
		}
		if ref == node.ID {
			return locale.Errorf("node %q references itself", "節點「%s」參照了自己", node.ID)
		}
		if _, exists := seenRefs[ref]; exists {
			return locale.Errorf("node %q references %q twice", "節點「%s」重複參照「%s」", node.ID, ref)
		}
		seenRefs[ref] = struct{}{}
	}
	return nil
}

// cssColor is what a stored colour may contain: hex, a named colour or a
// colour function (rgb(), hsl()...). The client puts these into CSS custom
// properties, so a value must not be able to reach a url() or image-set():
// no ':', '/', quotes or backslash escapes means no remote fetch.
var cssColor = regexp.MustCompile(`^[#a-zA-Z0-9(),.% -]*$`)

func safeCSSColor(v string) bool {
	return len(v) <= 32 && cssColor.MatchString(v)
}
