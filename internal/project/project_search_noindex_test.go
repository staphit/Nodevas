package project

import (
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/searchindex"
)

// Without a database there is no index any more (no private in-memory one):
// ranked search falls back to the direct scan, whose hits all hold the query
// as a phrase.
func TestRankFallsBackToDirectScanWithoutIndex(t *testing.T) {
	root := writeSearchProject(t,
		&engine.Node{ID: "phrase", Title: "Phrase"},
		&engine.Node{ID: "terms", Title: "Terms"},
	)
	writeNodeBody(t, root, "phrase", "那天海風很冷。")
	writeNodeBody(t, root, "terms", "海 風 冷 很")
	pm := &ProjectManager{}
	results, err := pm.RankProjectNodes(ProjectInfo{Name: "main", Path: root}, "海風很冷", 10)
	if err != nil {
		t.Fatalf("RankProjectNodes: %v", err)
	}
	if len(results) != 1 || results[0].NodeID != "phrase" || results[0].Match != searchindex.MatchPhrase {
		ids := make([]string, 0, len(results))
		for _, r := range results {
			ids = append(ids, r.NodeID+"/"+r.Match)
		}
		t.Fatalf("results = %v, want only the direct scan's phrase hit", ids)
	}
}
