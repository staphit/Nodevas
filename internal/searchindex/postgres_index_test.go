package searchindex

// The search index on PostgreSQL: tsvectors of this package's own tokens,
// ranked by the FTS5 BM25 formula computed in SQL. Runs on internal/testpg.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

// newPGManager is a manager over a fresh PostgreSQL database.
func newPGManager(t *testing.T) (*Manager, *db.DB) {
	t.Helper()
	database := testpg.New(t)
	return NewManagerWithDB(database), database
}

func pgCount(t *testing.T, database *db.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// 39. The PostgreSQL ranker reproduces what FTS5 shipped: same top five, same
// scores, and recall on the eval corpus no worse.
func TestRankMatchesFTS5Golden(t *testing.T) {
	want := loadGolden(t)
	m, _ := newPGManager(t)
	answers, summary := runEval(t, m, evalProject(t))
	if len(answers) != len(want.Answers) {
		t.Fatalf("%d answers, golden has %d", len(answers), len(want.Answers))
	}
	var diff strings.Builder
	for i, got := range answers {
		golden := want.Answers[i]
		fmt.Fprintf(&diff, "%s firstGold fts5=%d pg=%d\n", got.ID, golden.FirstGold, got.FirstGold)
		top := min(5, len(golden.Ranked))
		if len(got.Ranked) < top {
			t.Errorf("%s: %d results, golden top has %d", got.ID, len(got.Ranked), top)
			continue
		}
		for k := range top {
			g, p := golden.Ranked[k], got.Ranked[k]
			if g.ID != p.ID || g.Match != p.Match {
				t.Errorf("%s #%d = %s (%s), FTS5 had %s (%s)", got.ID, k+1, p.ID, p.Match, g.ID, g.Match)
				continue
			}
			if relDiff(g.Score, p.Score) > 1e-6 {
				t.Errorf("%s #%d %s score %.9f, FTS5 %.9f", got.ID, k+1, p.ID, p.Score, g.Score)
			}
		}
	}
	t.Logf("recall FTS5: R@1 %.2f R@3 %.2f R@5 %.2f MRR %.3f", want.Summary.R1, want.Summary.R3, want.Summary.R5, want.Summary.MRR)
	t.Logf("recall PG:   R@1 %.2f R@3 %.2f R@5 %.2f MRR %.3f", summary.R1, summary.R3, summary.R5, summary.MRR)
	if summary.R5 < want.Summary.R5 || summary.MRR < want.Summary.MRR-1e-9 || summary.R5 < 0.80 || summary.MRR < 0.57 {
		t.Errorf("recall got worse:\n%s", diff.String())
	}
}

func relDiff(a, b float64) float64 {
	if a == b {
		return 0
	}
	return math.Abs(a-b) / math.Max(math.Abs(a), math.Abs(b))
}

// fts5Score is SQLite FTS5's bm25() with weights 1,1, k1 1.2, b 0.75, negated:
// for each query token, idf * f*(k1+1) / (f + k1*(1-b+b*D/avgdl)), where f is
// the number of the two columns holding the token (tokens are distinct per
// column), D is the document's token count over both columns, and
// idf = max(ln((N-df+0.5)/(df+0.5)), 1e-6).
func fts5Score(query []string, docs map[string][2]map[string]struct{}, id string) float64 {
	n := float64(len(docs))
	var total float64
	for _, doc := range docs {
		total += float64(len(doc[0]) + len(doc[1]))
	}
	avgdl := total / n
	doc := docs[id]
	length := float64(len(doc[0]) + len(doc[1]))
	var score float64
	for _, token := range query {
		df := 0.0
		for _, other := range docs {
			_, inMeta := other[0][token]
			_, inBody := other[1][token]
			if inMeta || inBody {
				df++
			}
		}
		idf := math.Log((n - df + 0.5) / (df + 0.5))
		if idf <= 0 {
			idf = 1e-6
		}
		f := 0.0
		if _, ok := doc[0][token]; ok {
			f++
		}
		if _, ok := doc[1][token]; ok {
			f++
		}
		score += idf * (f * 2.2) / (f + 1.2*(0.25+0.75*length/avgdl))
	}
	return score
}

// columns tokenises a project's documents the way the index does.
func columns(t *testing.T, root string) map[string][2]map[string]struct{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := engine.ParseGraph(data)
	if err != nil {
		t.Fatal(err)
	}
	names := userNames(graph)
	out := map[string][2]map[string]struct{}{}
	for _, node := range graph.Nodes {
		body, _, _ := readBody(root, node.ID)
		meta, metaTokens, bodyTokens := metadataText(node, names), map[string]struct{}{}, map[string]struct{}{}
		collectTokens(meta, metaTokens)
		collectTokens(body, bodyTokens)
		out[node.ID] = [2]map[string]struct{}{metaTokens, bodyTokens}
	}
	return out
}

// 40.
func TestBM25MatchesHandComputedFTS5Formula(t *testing.T) {
	m, _ := newPGManager(t)
	root := writeProject(t,
		fixtureNode{id: "d1", title: "港口", body: "林雨站在港口，看著漁船回來。海風很冷，港口的燈亮了。"},
		fixtureNode{id: "d2", title: "家", body: "林雨的妹妹叫林曉，她從小就怕水。"},
		fixtureNode{id: "d3", title: "市場", body: "市場裡人很多，有人在叫賣魚，海風吹進來。"},
	)
	query := "林雨在港口看海風"
	ranked := rank(t, m, root, query)
	if len(ranked) != 3 {
		t.Fatalf("ranked = %v", rankedIDs(ranked))
	}
	docs := columns(t, root)
	tokens := rankTokens(strings.ToLower(query))
	for _, hit := range ranked {
		want := fts5Score(tokens, docs, hit.NodeID)
		if relDiff(hit.Score, want) > 1e-9 {
			t.Errorf("%s score %.12f, FTS5 formula %.12f", hit.NodeID, hit.Score, want)
		}
	}
}

// 41. IDF and avgdl are the project's own: a second project full of the
// query's words changes nothing about the first one's scores.
func TestIDFIsPerProject(t *testing.T) {
	m, _ := newPGManager(t)
	nodes := []fixtureNode{
		{id: "a", title: "A", body: "海風很冷"},
		{id: "b", title: "B", body: "港口很大"},
	}
	first := writeProject(t, nodes...)
	before := rank(t, m, first, "海風")

	var noise []fixtureNode
	for i := range 40 {
		noise = append(noise, fixtureNode{id: fmt.Sprintf("n%d", i), title: "N", body: strings.Repeat("海風 ", i+1)})
	}
	rank(t, m, writeProject(t, noise...), "海風")

	after := rank(t, m, first, "海風")
	if len(before) == 0 || len(after) != len(before) {
		t.Fatalf("before %v after %v", rankedIDs(before), rankedIDs(after))
	}
	for i := range before {
		if before[i].NodeID != after[i].NodeID || relDiff(before[i].Score, after[i].Score) > 1e-12 {
			t.Fatalf("another project moved %s: %.9f -> %.9f", before[i].NodeID, before[i].Score, after[i].Score)
		}
	}
	want := fts5Score(rankTokens("海風"), columns(t, first), "a")
	if relDiff(before[0].Score, want) > 1e-9 {
		t.Fatalf("score %.9f, per-project FTS5 %.9f", before[0].Score, want)
	}
}

// 42. Every project shares one table now; a query never sees another's rows.
func TestRankedSearchNeverLeaksOtherProjects(t *testing.T) {
	m, _ := newPGManager(t)
	secret := writeProject(t, fixtureNode{id: "s", title: "機密", body: "only-in-secret 機密計畫 鑰匙"})
	public := writeProject(t, fixtureNode{id: "p", title: "公開", body: "公開的筆記"})
	rank(t, m, secret, "機密")
	for _, query := range []string{"機密計畫", "only-in-secret", "鑰匙", "機密"} {
		if got := rank(t, m, public, query); len(got) != 0 {
			t.Fatalf("query %q in the public project returned %v", query, rankedIDs(got))
		}
		if got := search(t, m, public, query); len(got) != 0 {
			t.Fatalf("search %q in the public project returned %v", query, ids(got))
		}
	}
}

// 43. tsquery syntax typed into the box is text.
func TestTsquerySyntaxIsPlainText(t *testing.T) {
	m, _ := newPGManager(t)
	root := storyCorpus(t)
	for _, query := range []string{
		`'a' & !b | c:*`, `NEAR(`, `\`, `''`, `--`, `)(`, `林雨 <-> 妹妹`, `!林雨`, `林雨:*`, `林雨 & | 妹妹`,
		`'; DROP TABLE search_documents; --`, `\'`, `''''`, "\x00", `林雨:A`, `(林雨`, `<2>`,
	} {
		if _, err := m.RankContext(context.Background(), root, query, 10); err != nil {
			t.Fatalf("rank %q: %v", query, err)
		}
		if _, err := m.SearchContext(context.Background(), root, strings.ToLower(query)); err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
	}
	if ranked := rank(t, m, root, "林雨的妹妹叫什麼名字？"); len(ranked) == 0 || ranked[0].NodeID != "s2" {
		t.Fatalf("index damaged: %v", rankedIDs(ranked))
	}
}

// 44.
func FuzzQueryNeverErrors(f *testing.F) {
	for _, seed := range []string{"林雨", `'a' & !b`, `\`, "x:*", "<->", "'''", "\x00", "é", "🙂"} {
		f.Add(seed)
	}
	database, err := db.OpenPostgres(fuzzURL(f), 0)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { database.Close() })
	m := NewManagerWithDB(database)
	root := f.TempDir()
	writeFuzzProject(f, root)
	f.Fuzz(func(t *testing.T, query string) {
		if !utf8.ValidString(query) {
			return
		}
		if _, err := m.RankContext(context.Background(), root, query, 10); err != nil {
			t.Fatalf("rank %q: %v", query, err)
		}
		if _, err := m.SearchContext(context.Background(), root, strings.ToLower(query)); err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
	})
}

func fuzzURL(f *testing.F) string { return testpg.NewURL(f) }

func writeFuzzProject(f *testing.F, root string) {
	graph := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "a", Title: "A"}}}
	data, _ := engine.MarshalGraph(graph)
	_ = os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644)
	_ = os.MkdirAll(filepath.Join(root, "nodes"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "nodes", "a.md"), []byte("林雨 café 'quoted' a&b"), 0o644)
}

// 45. A document whose tokens overflow a tsvector is still indexed, and text
// near its end is still found.
func TestOversizeDocumentIsIndexedAndFoundNearItsEnd(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	var body strings.Builder
	for body.Len() < docTextLimit-4096 {
		body.WriteRune(rune(0x4E00 + random.IntN(0x9FA5-0x4E00)))
	}
	body.WriteString(" 終點標記詞 ")
	root := writeProject(t,
		fixtureNode{id: "huge", title: "Huge", body: body.String()},
		fixtureNode{id: "small", title: "Small", body: "小文件"},
	)
	m, database := newPGManager(t)
	started := time.Now()
	if got := ids(search(t, m, root, "終點標記")); len(got) != 1 || got[0] != "huge" {
		t.Fatalf("search near the end = %v", got)
	}
	t.Logf("indexed a %d-byte document in %s", body.Len(), time.Since(started))
	if got := rankedIDs(rank(t, m, root, "終點標記詞")); len(got) == 0 || got[0] != "huge" {
		t.Fatalf("ranked near the end = %v", got)
	}
	if got := ids(search(t, m, root, "小文件")); len(got) != 1 || got[0] != "small" {
		t.Fatalf("the sibling = %v", got)
	}
	if pgCount(t, database, `SELECT count(*) FROM search_documents WHERE node_id = 'huge' AND oversize`) != 1 {
		t.Fatal("the huge document is not marked oversize")
	}
}

// 46. FTS5's unicode61 folded Latin diacritics; so does the index now.
func TestDiacriticsFoldLikeUnicode61(t *testing.T) {
	m, _ := newPGManager(t)
	root := writeProject(t,
		fixtureNode{id: "accent", title: "Accent", body: "un café à Zürich"},
		fixtureNode{id: "plain", title: "Plain", body: "a cafe in Zurich"},
	)
	for _, query := range []string{"cafe", "café", "zurich", "Zürich"} {
		got := rankedIDs(rank(t, m, root, query))
		slices.Sort(got)
		if !slices.Equal(got, []string{"accent", "plain"}) {
			t.Errorf("ranked %q = %v, want both spellings", query, got)
		}
	}
	// The substring check still decides the plain search exactly.
	if got := ids(search(t, m, root, "café")); len(got) != 1 || got[0] != "accent" {
		t.Fatalf("search café = %v", got)
	}
}

// 47. The table is derived data an operator may drop; it comes back.
func TestDroppedTableIsRecreated(t *testing.T) {
	database := testpg.New(t)
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "buried needle 埋著的針"})
	search(t, NewManagerWithDB(database), root, "needle")
	if _, err := database.Exec(`DROP TABLE search_documents`); err != nil {
		t.Fatal(err)
	}
	second := NewManagerWithDB(database)
	for _, query := range []string{"needle", "著的"} {
		if got := ids(search(t, second, root, query)); len(got) != 1 || got[0] != "alpha" {
			t.Fatalf("after DROP TABLE, %q = %v", query, got)
		}
	}
	if got := rankedIDs(rank(t, second, root, "埋著的針")); len(got) != 1 {
		t.Fatalf("ranked after DROP TABLE = %v", got)
	}
}

// 48. No database, no index: the caller's direct scan answers instead.
func TestManagerWithoutDatabaseReturnsErrNoIndex(t *testing.T) {
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "私有記憶體 index"})
	manager := NewManager()
	t.Cleanup(func() { manager.Close() })
	if _, err := manager.Search(root, "記憶"); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("Search = %v, want ErrNoIndex", err)
	}
	if _, err := manager.RankContext(context.Background(), root, "記憶", 10); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("RankContext = %v, want ErrNoIndex", err)
	}
}

// 49. Two writers indexing the same node at once leave one row.
func TestConcurrentPutsOfSameNodeDoNotDuplicate(t *testing.T) {
	database := testpg.New(t)
	root := writeProject(t, fixtureNode{id: "alpha", title: "Alpha", body: "同一個節點 same node"})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := NewManagerWithDB(database)
			if _, err := m.Search(root, "same"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent indexing: %v", err)
	}
	if n := pgCount(t, database, `SELECT count(*) FROM search_documents WHERE node_id = 'alpha'`); n != 1 {
		t.Fatalf("%d rows for one node", n)
	}
}

// 50. Budget: p95 under 150 ms for a ranked whole-sentence query over 5k docs.
func BenchmarkRank5kDocs(b *testing.B) {
	database, err := db.OpenPostgres(testpg.NewURL(b), 0)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { database.Close() })
	random := rand.New(rand.NewPCG(3, 4))
	root := b.TempDir()
	graph := &engine.Graph{Version: 1}
	_ = os.MkdirAll(filepath.Join(root, "nodes"), 0o755)
	for i := range 5000 {
		id := fmt.Sprintf("n%04d", i)
		graph.Nodes = append(graph.Nodes, &engine.Node{ID: id, Title: id})
		var body strings.Builder
		for range 400 {
			body.WriteRune(rune(0x4E00 + random.IntN(3000)))
		}
		_ = os.WriteFile(filepath.Join(root, "nodes", id+".md"), []byte(body.String()), 0o644)
	}
	data, _ := engine.MarshalGraph(graph)
	_ = os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644)
	m := NewManagerWithDB(database)
	ctx := context.Background()
	if _, err := m.RankContext(ctx, root, "warm", 10); err != nil {
		b.Fatal(err)
	}
	query := "林雨的妹妹叫什麼名字她從小就怕水嗎"
	var durations []time.Duration
	b.ResetTimer()
	for b.Loop() {
		started := time.Now()
		if _, err := m.RankContext(ctx, root, query, 10); err != nil {
			b.Fatal(err)
		}
		durations = append(durations, time.Since(started))
	}
	b.StopTimer()
	slices.Sort(durations)
	p95 := durations[len(durations)*95/100]
	b.ReportMetric(float64(p95.Milliseconds()), "p95-ms")
	if p95 > 150*time.Millisecond {
		b.Errorf("p95 %s over the 150 ms budget", p95)
	}
}
