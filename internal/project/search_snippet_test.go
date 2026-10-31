package project

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The palette showed "…��受打擾的時段" for a Chinese node: the snippet was cut
// 50 bytes before the match, which lands inside a three-byte character, and
// it carried the raw frontmatter ("title: 團隊同步會議 ---") along with it.
func TestSearchSnippetCutsOnCharactersAndLeavesTheFrontmatterOut(t *testing.T) {
	haystack := "node-0003 團隊同步會議 task 中 小安 每日 會議 \n" +
		"---\nid: node-0003\nkind: task\ntags:\n  - 每日\n  - 會議\ntitle: 團隊同步會議\n---\n\n" +
		"每天早上十五分鐘，快速同步進度。分享阻礙、決策與負責人。會議結論記在節點內文裡。" +
		strings.Repeat("之後的內容還有很多很多。", 20)

	for _, query := range []string{"決策", "會議", "內容"} {
		snippet := searchSnippet(haystack, query)
		if !utf8.ValidString(snippet) || strings.ContainsRune(snippet, utf8.RuneError) {
			t.Fatalf("snippet for %q is not whole characters: %q", query, snippet)
		}
		if !strings.Contains(snippet, query) {
			t.Fatalf("snippet for %q does not show the match: %q", query, snippet)
		}
		if strings.Contains(snippet, "title:") || strings.Contains(snippet, "---") {
			t.Fatalf("snippet for %q shows the frontmatter: %q", query, snippet)
		}
	}

	// No match in the body (the query hit the metadata line): still whole
	// characters, however the text is cut.
	long := "x\n" + strings.Repeat("長", 200)
	if snippet := searchSnippet(long, "zzz"); !utf8.ValidString(snippet) || strings.ContainsRune(snippet, utf8.RuneError) {
		t.Fatalf("unmatched snippet is not whole characters: %q", snippet)
	}
}
