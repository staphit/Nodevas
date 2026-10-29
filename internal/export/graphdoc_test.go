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
