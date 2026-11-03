package searchindex

import (
	"context"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func rank(t *testing.T, m *Manager, root, query string) []Ranked {
	t.Helper()
	ranked, err := m.RankContext(context.Background(), root, query, 10)
	if err != nil {
		t.Fatalf("rank %q: %v", query, err)
	}
	return ranked
}

func rankedIDs(ranked []Ranked) []string {
	out := make([]string, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.NodeID)
	}
	return out
}

func TestRankTokensSplitAQuestion(t *testing.T) {
	tokens := rankTokens(strings.ToLower("林雨的妹妹叫什麼名字？"))
	want := []string{"林", "林雨", "雨", "雨的", "的", "的妹", "妹", "妹妹", "妹叫", "叫", "叫什",
		"什", "什麼", "麼", "麼名", "名", "名字", "字"}
	if !slices.Equal(tokens, want) {
		t.Fatalf("tokens = %q", tokens)
	}
	// Latin letters contribute bigrams only; a lone rune is still a token.
	if got := rankTokens("cat x"); !slices.Equal(got, []string{"ca", "at", "x"}) {
		t.Fatalf("latin tokens = %q", got)
	}
	if long := rankTokens(strings.Repeat("天地玄黃宇宙洪荒", 10)); len(long) > maxRankTokens {
		t.Fatalf("%d tokens, cap is %d", len(long), maxRankTokens)
	}
	// A long question keeps its last words: the cap must not cut a
	// 30-character sentence's tail.
	long := rankTokens("在第三章裡，老船長在港口唱的那首歌，後來被誰在市場上聽見了？")
	if !slices.Contains(long, "市場") || !slices.Contains(long, "聽見") {
		t.Fatalf("tail dropped: %q", long)
	}
	if many := rankTokens(strings.Repeat("一二三四五六七八九十", 20) + "甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉戌亥"); len(many) != maxRankTokens {
		t.Fatalf("%d tokens, cap is %d", len(many), maxRankTokens)
	}
}

// storyCorpus is five scenes; only one says who Lin Yu's sister is.
func storyCorpus(t *testing.T) string {
	return writeProject(t,
		fixtureNode{id: "s1", title: "第一場：港口", body: "林雨站在港口，看著漁船回來。海風很冷。"},
		fixtureNode{id: "s2", title: "第二場：家", body: "林雨的妹妹叫林曉，她從小就怕水。姐妹兩人住在老街。"},
		fixtureNode{id: "s3", title: "第三場：市場", body: "市場裡人很多，有人在叫賣魚。"},
		fixtureNode{id: "s4", title: "第四場：名字", body: "老船長忘了自己的名字，只記得一首歌。"},
		fixtureNode{id: "s5", title: "林曉", body: "角色卡。"},
	)
}

func TestAWholeQuestionFindsTheAnswer(t *testing.T) {
	m := newManager(t)
	root := storyCorpus(t)
	// The AND search finds nothing for a sentence...
	if got := search(t, m, root, "林雨的妹妹叫什麼名字？"); len(got) != 0 {
		t.Fatalf("default search changed: %v", ids(got))
	}
	// ...the ranked one puts the answer first.
	ranked := rank(t, m, root, "林雨的妹妹叫什麼名字？")
	if len(ranked) == 0 || ranked[0].NodeID != "s2" || ranked[0].Match != MatchTerms || ranked[0].Score <= 0 {
		t.Fatalf("ranked = %+v", rankedIDs(ranked))
	}
	for i := 1; i < len(ranked); i++ {
		if ranked[i].Score > ranked[i-1].Score {
			t.Fatalf("scores out of order: %+v", ranked)
		}
	}
}

func TestAnExactTitleRanksFirst(t *testing.T) {
	m := newManager(t)
	root := storyCorpus(t)
	ranked := rank(t, m, root, "林曉")
	if len(ranked) == 0 || ranked[0].NodeID != "s5" || ranked[0].Match != MatchTitle {
		t.Fatalf("ranked = %+v", ranked)
	}
	// s2 holds 林曉 as a phrase and comes next.
	if len(ranked) < 2 || ranked[1].NodeID != "s2" || ranked[1].Match != MatchPhrase {
		t.Fatalf("ranked = %v", rankedIDs(ranked))
	}
}

func TestAPhraseBeatsSharedTerms(t *testing.T) {
	m := newManager(t)
	root := writeProject(t,
		fixtureNode{id: "many", title: "many", body: "海 風 冷 海 風 冷 海風冷 很 很"},
		fixtureNode{id: "phrase", title: "phrase", body: "那天海風很冷。"},
	)
	ranked := rank(t, m, root, "海風很冷")
	if len(ranked) != 2 || ranked[0].NodeID != "phrase" || ranked[0].Match != MatchPhrase || ranked[1].Match != MatchTerms {
		t.Fatalf("ranked = %+v", ranked)
	}
}

func TestExcerptShowsTheMatchingPassage(t *testing.T) {
	text := "s2 第二場\n---\nid: s2\n---\n\n" + strings.Repeat("無關的文字。", 40) +
		"林雨的妹妹叫林曉，她從小就怕水。" + strings.Repeat("後面的段落。", 40)
	excerpt := Excerpt(text, RankTokens("林雨的妹妹叫什麼名字"), ExcerptWidth)
	if !strings.Contains(excerpt, "妹妹") || !strings.HasPrefix(excerpt, "…") || !strings.HasSuffix(excerpt, "…") {
		t.Fatalf("excerpt = %q", excerpt)
	}
	if n := utf8.RuneCountInString(excerpt); n > ExcerptWidth+2 {
		t.Fatalf("excerpt is %d runes", n)
	}
	if strings.Contains(Excerpt(text, nil, ExcerptWidth), "id: s2") {
		t.Fatal("frontmatter leaked into the excerpt")
	}
	if got := Excerpt("meta\nshort body", []string{"bo"}, ExcerptWidth); got != "short body" {
		t.Fatalf("short excerpt = %q", got)
	}
}
