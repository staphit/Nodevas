package export

import (
	"fmt"
	"sort"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
	"nodevas/internal/locale"
)

// The relationship graph, written into the exported document.
//
// A project export is a pile of node documents; without the wiring the reader
// has to reconstruct the structure from prose. Every project export therefore
// opens with the graph in two forms: a Mermaid flowchart, which Markdown
// readers draw, and a plain outline, which survives into .txt and .docx where
// no diagram can be rendered.

const maxDiagramNodes = 300

// graphWords are the words the graph section is written in, one set per
// interface language: the section is read by whoever asked for the export,
// whatever language the project's own text is in.
type graphWords struct {
	graph, relations, entries, requires, next string
	optional, deprecated, untitled            string
	tooMany                                   string // takes the node limit
	colon, separator, open, close             string
}

var graphWordsIn = map[locale.Language]graphWords{
	locale.English: {
		graph: "Graph", relations: "Relations", entries: "Start nodes",
		requires: "Requires", next: "Next",
		optional: "Optional", deprecated: "Deprecated", untitled: "Untitled",
		tooMany: "(More than %d nodes: the diagram is left out and only the relations are listed.)",
		colon:   ": ", separator: ", ", open: " (", close: ")",
	},
	locale.Chinese: {
		graph: "關係圖", relations: "關係", entries: "起點",
		requires: "前置", next: "後續",
		optional: "選用", deprecated: "棄用", untitled: "未命名",
		tooMany: "（節點超過 %d 個，略過流程圖，僅列出關係。）",
		colon:   "：", separator: "、", open: "（", close: "）",
	},
}

func wordsFor(language locale.Language) graphWords {
	if words, ok := graphWordsIn[language]; ok {
		return words
	}
	return graphWordsIn[locale.English]
}

func relationLabel(edge *engine.Edge, words graphWords) string {
	switch edge.Relation {
	case engine.RelationOptional:
		return words.optional
	case engine.RelationDeprecated:
		return words.deprecated
	}
	return ""
}

// relationSuffix marks an optional or deprecated wire after a title.
func relationSuffix(edge *engine.Edge, words graphWords) string {
	if label := relationLabel(edge, words); label != "" {
		return words.open + label + words.close
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
func mermaidLabel(text string, words graphWords) string {
	replacer := strings.NewReplacer(`"`, "'", "[", "(", "]", ")", "\n", " ")
	label := strings.TrimSpace(replacer.Replace(text))
	if label == "" {
		return words.untitled
	}
	if runes := []rune(label); len(runes) > 40 {
		label = string(runes[:40]) + "…"
	}
	return label
}

// dependencyEdges is the project's wiring as the board draws it: every edge
// between two known nodes, plus a required edge for each dependency named only
// in a node's `requires`. A hand-written graph.yaml may state a dependency in
// the expression without the edge; the node still waits on it, so the export
// must not lose it. known reports whether an id is a node worth listing.
func dependencyEdges(graph *engine.Graph, known func(string) bool) []*engine.Edge {
	edges := make([]*engine.Edge, 0, len(graph.Edges))
	wired := make(map[string]bool, len(graph.Edges))
	for _, edge := range graph.Edges {
		if edge == nil || !known(edge.From) || !known(edge.To) {
			continue
		}
		edges = append(edges, edge)
		wired[edge.From+"->"+edge.To] = true
	}
	for _, node := range graph.Nodes {
		if node == nil || !known(node.ID) {
			continue
		}
		expr, err := node.RequiresExpr()
		if err != nil || expr == nil {
			continue
		}
		for _, from := range dsl.NodeRefs(expr) {
			key := from + "->" + node.ID
			if from == node.ID || wired[key] || !known(from) {
				continue
			}
			edges = append(edges, &engine.Edge{From: from, To: node.ID, Relation: engine.RelationRequired})
			wired[key] = true
		}
	}
	return edges
}

// RelationSection renders the project's wiring as a Markdown section in
// language, or "" when the project has no edges worth drawing.
func RelationSection(graph *engine.Graph, level int, language locale.Language) string {
	words := wordsFor(language)
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
	edges := dependencyEdges(graph, func(id string) bool { return titles[id] != "" })
	if len(edges) == 0 {
		return ""
	}

	var body strings.Builder
	body.WriteString(strings.Repeat("#", min(level, 6)) + " " + words.graph + "\n\n")

	if len(order) <= maxDiagramNodes {
		body.WriteString("```mermaid\nflowchart TD\n")
		for _, id := range order {
			fmt.Fprintf(&body, "  %s[\"%s\"]\n", mermaidID(id), mermaidLabel(titles[id], words))
		}
		for _, edge := range edges {
			arrow := "-->"
			if edge.Relation != engine.RelationRequired {
				arrow = "-.->"
			}
			if label := relationLabel(edge, words); label != "" {
				fmt.Fprintf(&body, "  %s %s|%s| %s\n",
					mermaidID(edge.From), arrow, label, mermaidID(edge.To))
				continue
			}
			fmt.Fprintf(&body, "  %s %s %s\n",
				mermaidID(edge.From), arrow, mermaidID(edge.To))
		}
		body.WriteString("```\n\n")
	} else {
		body.WriteString(fmt.Sprintf(words.tooMany+"\n\n", maxDiagramNodes))
	}

	// The outline is the fallback for formats that cannot draw: every node
	// with what it waits for and what waits on it.
	incoming := map[string][]string{}
	outgoing := map[string][]string{}
	for _, edge := range edges {
		suffix := relationSuffix(edge, words)
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
		fmt.Fprintf(&body, "**%s**%s%s\n\n", words.entries, words.colon,
			strings.Join(entries, words.separator))
	}
	for _, id := range order {
		if len(incoming[id]) == 0 && len(outgoing[id]) == 0 {
			continue
		}
		fmt.Fprintf(&body, "- **%s**", titles[id])
		if len(incoming[id]) > 0 {
			fmt.Fprintf(&body, "\n  - %s%s%s", words.requires, words.colon,
				strings.Join(incoming[id], words.separator))
		}
		if len(outgoing[id]) > 0 {
			fmt.Fprintf(&body, "\n  - %s%s%s", words.next, words.colon,
				strings.Join(outgoing[id], words.separator))
		}
		body.WriteString("\n")
	}
	return strings.TrimSpace(body.String())
}

// NodeRelationLines renders one node's own wiring in language, for a
// single-node export.
func NodeRelationLines(graph *engine.Graph, nodeID string, level int, language locale.Language) string {
	if graph == nil {
		return ""
	}
	words := wordsFor(language)
	titles := map[string]string{}
	for _, node := range graph.Nodes {
		if node != nil {
			titles[node.ID] = nodeTitle(node)
		}
	}
	var before, after []string
	for _, edge := range dependencyEdges(graph, func(id string) bool { return titles[id] != "" }) {
		suffix := relationSuffix(edge, words)
		if edge.To == nodeID {
			before = append(before, titles[edge.From]+suffix)
		}
		if edge.From == nodeID {
			after = append(after, titles[edge.To]+suffix)
		}
	}
	if len(before) == 0 && len(after) == 0 {
		return ""
	}
	var body strings.Builder
	body.WriteString(strings.Repeat("#", min(level, 6)) + " " + words.relations + "\n\n")
	if len(before) > 0 {
		fmt.Fprintf(&body, "- %s%s%s\n", words.requires, words.colon, strings.Join(before, words.separator))
	}
	if len(after) > 0 {
		fmt.Fprintf(&body, "- %s%s%s\n", words.next, words.colon, strings.Join(after, words.separator))
	}
	return strings.TrimSpace(body.String())
}
