package export

import (
	"fmt"
	"sort"
	"strings"

	"nodevas/internal/engine"
)

// The relationship graph, written into the exported document.
//
// A project export is a pile of node documents; without the wiring the reader
// has to reconstruct the structure from prose. Every project export therefore
// opens with the graph in two forms: a Mermaid flowchart, which Markdown
// readers draw, and a plain outline, which survives into .txt and .docx where
// no diagram can be rendered.

const maxDiagramNodes = 300

func relationLabel(edge *engine.Edge) string {
	switch edge.Relation {
	case engine.RelationOptional:
		return "選用"
	case engine.RelationDeprecated:
		return "棄用"
	}
	return ""
}

// mermaidID keeps node ids usable as Mermaid identifiers: the ids are
// validated elsewhere, but a diagram must never break on an unexpected one.
func mermaidID(id string) string {
	var out strings.Builder
	for _, r := range id {
		if r == '-' || r == '_' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			out.WriteRune(r)
			continue
		}
		out.WriteRune('_')
	}
	if out.Len() == 0 {
		return "n"
	}
	return "n_" + out.String()
}

// mermaidLabel quotes a title for a Mermaid node. Quotes and brackets would
// otherwise end the label early.
func mermaidLabel(text string) string {
	replacer := strings.NewReplacer(`"`, "'", "[", "(", "]", ")", "\n", " ")
	label := strings.TrimSpace(replacer.Replace(text))
	if label == "" {
		return "未命名"
	}
	if runes := []rune(label); len(runes) > 40 {
		label = string(runes[:40]) + "…"
	}
	return label
}

// RelationSection renders the project's wiring as a Markdown section, or ""
// when the project has no edges worth drawing.
func RelationSection(graph *engine.Graph, level int) string {
	if graph == nil || len(graph.Nodes) == 0 {
		return ""
	}
	titles := make(map[string]string, len(graph.Nodes))
	order := make([]string, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		titles[node.ID] = nodeTitle(node)
		order = append(order, node.ID)
	}
	edges := make([]*engine.Edge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		if edge == nil || titles[edge.From] == "" || titles[edge.To] == "" {
			continue
		}
		edges = append(edges, edge)
	}
	if len(edges) == 0 {
		return ""
	}

	var body strings.Builder
	body.WriteString(strings.Repeat("#", min(level, 6)) + " 關係圖\n\n")

	if len(order) <= maxDiagramNodes {
		body.WriteString("```mermaid\nflowchart TD\n")
		for _, id := range order {
			fmt.Fprintf(&body, "  %s[\"%s\"]\n", mermaidID(id), mermaidLabel(titles[id]))
		}
		for _, edge := range edges {
			arrow := "-->"
			if edge.Relation != engine.RelationRequired {
				arrow = "-.->"
			}
			if label := relationLabel(edge); label != "" {
				fmt.Fprintf(&body, "  %s %s|%s| %s\n",
					mermaidID(edge.From), arrow, label, mermaidID(edge.To))
				continue
			}
			fmt.Fprintf(&body, "  %s %s %s\n",
				mermaidID(edge.From), arrow, mermaidID(edge.To))
		}
		body.WriteString("```\n\n")
	} else {
		body.WriteString(fmt.Sprintf(
			"（節點超過 %d 個，略過流程圖，僅列出關係。）\n\n", maxDiagramNodes))
	}

	// The outline is the fallback for formats that cannot draw: every node
	// with what it waits for and what waits on it.
	incoming := map[string][]string{}
	outgoing := map[string][]string{}
	for _, edge := range edges {
		suffix := ""
		if label := relationLabel(edge); label != "" {
			suffix = "（" + label + "）"
		}
		incoming[edge.To] = append(incoming[edge.To], titles[edge.From]+suffix)
		outgoing[edge.From] = append(outgoing[edge.From], titles[edge.To]+suffix)
	}
	// A node nothing points at starts the graph, unless the author said
	// otherwise: `ui.entry_overrides` is what the editor's 起點 switch writes.
	var overrides map[string]bool
	if graph.UI != nil {
		overrides = graph.UI.EntryOverrides
	}
	var entries []string
	for _, id := range order {
		isEntry := len(incoming[id]) == 0
		if override, ok := overrides[id]; ok {
			isEntry = override
		}
		if isEntry {
			entries = append(entries, titles[id])
		}
	}
	sort.Strings(entries)
	if len(entries) > 0 {
		fmt.Fprintf(&body, "**起點**：%s\n\n", strings.Join(entries, "、"))
	}
	for _, id := range order {
		if len(incoming[id]) == 0 && len(outgoing[id]) == 0 {
			continue
		}
		fmt.Fprintf(&body, "- **%s**", titles[id])
		if len(incoming[id]) > 0 {
			fmt.Fprintf(&body, "\n  - 前置：%s", strings.Join(incoming[id], "、"))
		}
		if len(outgoing[id]) > 0 {
			fmt.Fprintf(&body, "\n  - 後續：%s", strings.Join(outgoing[id], "、"))
		}
		body.WriteString("\n")
	}
	return strings.TrimSpace(body.String())
}

// NodeRelationLines renders one node's own wiring, for a single-node export.
func NodeRelationLines(graph *engine.Graph, nodeID string, level int) string {
	if graph == nil {
		return ""
	}
	titles := map[string]string{}
	for _, node := range graph.Nodes {
		if node != nil {
			titles[node.ID] = nodeTitle(node)
		}
	}
	var before, after []string
	for _, edge := range graph.Edges {
		if edge == nil {
			continue
		}
		suffix := ""
		if label := relationLabel(edge); label != "" {
			suffix = "（" + label + "）"
		}
		if edge.To == nodeID && titles[edge.From] != "" {
			before = append(before, titles[edge.From]+suffix)
		}
		if edge.From == nodeID && titles[edge.To] != "" {
			after = append(after, titles[edge.To]+suffix)
		}
	}
	if len(before) == 0 && len(after) == 0 {
		return ""
	}
	var body strings.Builder
	body.WriteString(strings.Repeat("#", min(level, 6)) + " 關係\n\n")
	if len(before) > 0 {
		fmt.Fprintf(&body, "- 前置：%s\n", strings.Join(before, "、"))
	}
	if len(after) > 0 {
		fmt.Fprintf(&body, "- 後續：%s\n", strings.Join(after, "、"))
	}
	return strings.TrimSpace(body.String())
}
