package export

import (
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/locale"
)

func relationFixture() *engine.Graph {
	return &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "a", Title: "開場"},
			{ID: "b", Title: "調查"},
			{ID: "c", Title: "舊線"},
		},
		Edges: []*engine.Edge{
			{From: "a", To: "b"},
			{From: "a", To: "c", Relation: engine.RelationDeprecated},
		},
	}
}

func TestRelationSectionDrawsAndLists(t *testing.T) {
	section := RelationSection(relationFixture(), 2, locale.Chinese)
	for _, want := range []string{
		"## 關係圖", "```mermaid", "flowchart TD", "-->", "-.->|棄用|",
		"**起點**：開場", "- 前置：開場", "- 後續：調查、舊線（棄用）",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section is missing %q:\n%s", want, section)
		}
	}
}

// An English reader's export labels the wiring in English: the section
// headings and list words were hard-coded Chinese whatever the language.
func TestRelationSectionSpeaksTheReadersLanguage(t *testing.T) {
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "a", Title: "Open"},
			{ID: "b", Title: "Investigate"},
			{ID: "c", Title: "Old line"},
			{ID: "d", Title: "Side"},
		},
		Edges: []*engine.Edge{
			{From: "a", To: "b"},
			{From: "a", To: "c", Relation: engine.RelationDeprecated},
			{From: "d", To: "b", Relation: engine.RelationOptional},
		},
	}
	section := RelationSection(graph, 2, locale.English)
	for _, want := range []string{
		"## Graph", "-.->|Deprecated|", "-.->|Optional|",
		"**Start nodes**: Open, Side",
		"- Requires: Open, Side (Optional)",
		"- Next: Investigate, Old line (Deprecated)",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section is missing %q:\n%s", want, section)
		}
	}
	if hasCJK(section) {
		t.Errorf("English section has Chinese words or punctuation:\n%s", section)
	}

	lines := NodeRelationLines(graph, "b", 2, locale.English)
	for _, want := range []string{"## Relations", "- Requires: Open, Side (Optional)"} {
		if !strings.Contains(lines, want) {
			t.Errorf("node lines are missing %q:\n%s", want, lines)
		}
	}
	if hasCJK(lines) {
		t.Errorf("English node lines have Chinese words or punctuation:\n%s", lines)
	}
}

func TestRelationSectionEmptyWithoutEdges(t *testing.T) {
	graph := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Title: "A"}}}
	if got := RelationSection(graph, 2, locale.English); got != "" {
		t.Errorf("want no section for an unwired project, got:\n%s", got)
	}
}

// An untitled document is headed in the reader's language.
func TestExportSectionNamesAnUntitledDocumentInTheReadersLanguage(t *testing.T) {
	if got := exportSection("", "body", 1, locale.English); !strings.HasPrefix(got, "# Untitled") {
		t.Errorf("English untitled section = %q", got)
	}
	if got := exportSection("", "body", 1, locale.Chinese); !strings.HasPrefix(got, "# 未命名") {
		t.Errorf("Chinese untitled section = %q", got)
	}
}

func hasCJK(text string) bool {
	for _, r := range text {
		if (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

// A dependency written only in `requires`, with no edge, is still a
// dependency: the board draws it and the node waits on it, so the exported
// graph draws and lists it too, and the waiting node is no start node.
func TestRelationSectionCountsRequiresOnlyDependencies(t *testing.T) {
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "node-0006", Title: "Docs"},
			{ID: "node-0007", Title: "Build"},
			{ID: "node-0008", Title: "Deploy", Requires: "node-0007 and node-0006"},
			{ID: "node-0009", Title: "Monitor", Requires: "node-0008"},
			{ID: "node-0010", Title: "Release", Requires: "node-0008 and node-0009"},
		},
		Edges: []*engine.Edge{
			{From: "node-0007", To: "node-0008"},
			{From: "node-0006", To: "node-0008"},
			{From: "node-0008", To: "node-0010"},
		},
	}
	section := RelationSection(graph, 2, locale.English)
	for _, want := range []string{
		"n_node-0008 --> n_node-0009",
		"n_node-0009 --> n_node-0010",
		"**Start nodes**: Build, Docs\n",
		"- **Monitor**\n  - Requires: Deploy\n  - Next: Release",
		"- **Release**\n  - Requires: Deploy, Monitor",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section is missing %q:\n%s", want, section)
		}
	}
	if strings.Count(section, "n_node-0008 --> n_node-0010") != 1 {
		t.Errorf("an edge also named in requires is drawn once:\n%s", section)
	}

	lines := NodeRelationLines(graph, "node-0009", 2, locale.English)
	for _, want := range []string{"- Requires: Deploy", "- Next: Release"} {
		if !strings.Contains(lines, want) {
			t.Errorf("node lines are missing %q:\n%s", want, lines)
		}
	}

	// A project wired only through requires still gets its graph section.
	bare := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "a", Title: "A"},
			{ID: "b", Title: "B", Requires: "a"},
		},
	}
	if got := RelationSection(bare, 2, locale.English); !strings.Contains(got, "n_a --> n_b") {
		t.Errorf("requires-only project lost its graph:\n%s", got)
	}
}
