package export

import (
	"strings"
	"testing"

	"nodevas/internal/engine"
)

func TestRelationSectionDrawsAndLists(t *testing.T) {
	graph := &engine.Graph{
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
	section := RelationSection(graph, 2)
	for _, want := range []string{
		"## 關係圖", "```mermaid", "flowchart TD", "-->", "-.->|棄用|",
		"**起點**：開場", "- 前置：開場", "- 後續：調查",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section is missing %q:\n%s", want, section)
		}
	}
}

func TestRelationSectionEmptyWithoutEdges(t *testing.T) {
	graph := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Title: "A"}}}
	if got := RelationSection(graph, 2); got != "" {
		t.Errorf("want no section for an unwired project, got:\n%s", got)
	}
}
