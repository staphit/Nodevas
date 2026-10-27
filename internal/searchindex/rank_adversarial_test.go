package searchindex

import (
	"context"
	"testing"
)

// FTS5 query syntax typed into the search box is text, never syntax: no
// error, no column filter, no NEAR group, no prefix query.
func TestRankedSearchTreatsFTSSyntaxAsText(t *testing.T) {
	m := newManager(t)
	root := storyCorpus(t)
	for _, query := range []string{
		`"`, `""`, `"林雨`, `林雨"`, `title:林雨`, `body:*`, `NEAR(林雨 妹妹, 2)`, `林*`, `*`,
		`林雨 OR 1=1`, `林雨 AND NOT 妹妹`, `^林雨`, `-林雨`, `{title}: 林雨`, `'; DROP TABLE search_fts; --`,
		`)(`, `林雨 + 妹妹`, "\x00林雨", `\"`,
	} {
		if _, err := m.RankContext(context.Background(), root, query, 10); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
	}
	// The table is still there and still answers.
	ranked := rank(t, m, root, "林雨的妹妹叫什麼名字？")
	if len(ranked) == 0 || ranked[0].NodeID != "s2" {
		t.Fatalf("index damaged: %v", rankedIDs(ranked))
	}
	// A column filter is not honoured: "title:林曉" is the words title and 林曉.
	ranked = rank(t, m, root, "title:林曉")
	if len(ranked) == 0 {
		t.Fatal("no results for a query holding 林曉")
	}
}

// Whole Chinese sentences find something, and an exact title still wins
// over a sentence-shaped body match.
func TestRankedSearchSentenceAndTitleOrder(t *testing.T) {
	m := newManager(t)
	root := storyCorpus(t)
	for _, query := range []string{
		"林雨站在港口的時候在看什麼？",
		"誰從小就怕水，她住在哪裡",
		"老船長還記得什麼東西",
	} {
		if ranked := rank(t, m, root, query); len(ranked) == 0 {
			t.Fatalf("no hit for %q", query)
		}
	}
	ranked := rank(t, m, root, "林曉")
	if ranked[0].NodeID != "s5" || ranked[0].Match != MatchTitle {
		t.Fatalf("exact title not first: %+v", ranked[0])
	}
}
