package searchindex

// Step 0 of the PostgreSQL move: record what the FTS5 ranker returns for the
// eval corpus while it still exists. Delete this file with the SQLite index;
// golden.json stays.
//
//	NODEVAS_WRITE_SEARCH_GOLDEN=1 go test ./internal/searchindex -run TestWriteFTS5Golden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFTS5Golden(t *testing.T) {
	if os.Getenv("NODEVAS_WRITE_SEARCH_GOLDEN") != "1" {
		t.Skip("set NODEVAS_WRITE_SEARCH_GOLDEN=1 to regenerate golden.json from the FTS5 ranker")
	}
	m := newManager(t) // the SQLite FTS5 index
	answers, summary := runEval(t, m, evalProject(t))
	out := golden{
		Generator: "FTS5 bm25(search_fts, 1.0, 1.0), SQLite index at d532b16",
		Limit:     goldenDepth,
		Summary:   summary,
		Answers:   answers,
	}
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evalDir, "golden.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("FTS5 baseline: R@1 %.2f R@3 %.2f R@5 %.2f MRR %.2f", summary.R1, summary.R3, summary.R5, summary.MRR)
}
