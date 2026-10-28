package searchindex

// P5: after a bulk load the ranked query still runs on the GIN indexes. The
// indexes have no pending list (fastupdate=off, migration 0029 and
// SchemaTuning for a recreated table), and a bulk reindex leaves the table
// analysed, so the planner prices the index probes off real statistics.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/testpg"
)

// rankPlanQuery is the whole-sentence query the 5k benchmark measures.
const rankPlanQuery = "林雨的妹妹叫什麼名字她從小就怕水嗎"

// writeBulkProject lays out n nodes of random CJK text, chars runes each, and
// returns the project root.
func writeBulkProject(tb testing.TB, n, chars int) string {
	tb.Helper()
	random := rand.New(rand.NewPCG(3, 4))
	root := tb.TempDir()
	graph := &engine.Graph{Version: 1}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		tb.Fatal(err)
	}
	for i := range n {
		id := fmt.Sprintf("n%04d", i)
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: id, Title: id})
		var body strings.Builder
		for range chars {
			body.WriteRune(rune(0x4E00 + random.IntN(3000)))
		}
		if err := os.WriteFile(filepath.Join(root, "nodes", id+".md"), []byte(body.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		tb.Fatal(err)
	}
	return root
}

// explainRank is the plan of the ranked query rank sends for query.
// With analyze it runs it too and reports buffers.
func explainRank(tb testing.TB, database *db.DB, root, query string, analyze bool) string {
	tb.Helper()
	tokens := rankTokens(strings.ToLower(query))
	if len(tokens) > maxRankTokens {
		tokens = tokens[:maxRankTokens]
	}
	literals := make([]string, len(tokens))
	for i, token := range tokens {
		literals[i] = tsqueryLiteral(token)
	}
	project := cleanRoot(root)
	prefix := "EXPLAIN "
	if analyze {
		prefix = "EXPLAIN (ANALYZE, BUFFERS) "
	}
	rows, err := database.Reader().Query(prefix+rankedQuery, literals, project, project, project, 50)
	if err != nil {
		tb.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			tb.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		tb.Fatal(err)
	}
	return plan.String()
}

// checkRankPlanUsesIndexes fails unless both GIN indexes answer the token
// probes and no probe scans the table.
func checkRankPlanUsesIndexes(tb testing.TB, plan string) {
	tb.Helper()
	if strings.Contains(plan, "Seq Scan on search_documents") {
		tb.Errorf("ranked query scans search_documents sequentially:\n%s", plan)
	}
	for _, index := range []string{"search_documents_meta_tokens", "search_documents_body_tokens"} {
		if !strings.Contains(plan, "Index Scan on "+index) {
			tb.Errorf("ranked query does not use %s:\n%s", index, plan)
		}
	}
}

var topBuffers = regexp.MustCompile(`Buffers: shared hit=(\d+)(?: read=(\d+))?`)

// planBuffers is the shared buffers (hit + read) the top plan node touched.
func planBuffers(plan string) int {
	match := topBuffers.FindStringSubmatch(plan)
	if match == nil {
		return -1
	}
	hit, _ := strconv.Atoi(match[1])
	read, _ := strconv.Atoi(match[2])
	return hit + read
}

func TestBulkLoadLeavesRankOnTheGINIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("bulk load")
	}
	database := testpg.New(t)
	ctx := context.Background()
	// Only this test's own maintenance may analyse the table.
	if _, err := database.Exec(`ALTER TABLE search_documents SET (autovacuum_enabled = false)`); err != nil {
		t.Fatal(err)
	}
	const docs = 2000
	root := writeBulkProject(t, docs, 400)
	m := NewManagerWithDB(database)
	if _, err := m.RankContext(ctx, root, "warm", 10); err != nil {
		t.Fatal(err)
	}
	if n := count(t, database, `SELECT count(*) FROM search_documents`); n != docs {
		t.Fatalf("%d rows after the bulk load, want %d", n, docs)
	}
	var reltuples float64
	if err := database.QueryRow(`SELECT reltuples FROM pg_class WHERE oid = 'search_documents'::regclass`).Scan(&reltuples); err != nil {
		t.Fatal(err)
	}
	if reltuples < docs*0.9 {
		t.Errorf("search_documents reltuples = %v after a bulk load of %d: the table was not analysed", reltuples, docs)
	}
	checkRankPlanUsesIndexes(t, explainRank(t, database, root, rankPlanQuery, false))
}

// A table an operator dropped comes back with the same index options a
// migrated one has.
func TestRecreatedTableHasFastupdateOff(t *testing.T) {
	database := testpg.New(t)
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "needle"})
	if _, err := database.Exec(`DROP TABLE search_documents`); err != nil {
		t.Fatal(err)
	}
	search(t, NewManagerWithDB(database), root, "needle")
	for _, index := range []string{"search_documents_meta_tokens", "search_documents_body_tokens"} {
		var options string
		if err := database.QueryRow(
			`SELECT coalesce(array_to_string(reloptions, ','), '') FROM pg_class WHERE oid = to_regclass(?)`, index).Scan(&options); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(options, "fastupdate=off") {
			t.Errorf("recreated %s reloptions = %q, want fastupdate=off", index, options)
		}
	}
}
