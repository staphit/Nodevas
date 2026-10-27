// The excerpt a ranked result shows: the part of the text that answers the
// query best.

package searchindex

import (
	"sort"
	"strings"
)

// ExcerptWidth is the excerpt length in runes.
const ExcerptWidth = 120

// Excerpt cuts the width runes of text that hold the most distinct query
// tokens (rankTokens of the query, compared with the text folded the way
// tokens are), the earliest such window on a tie, with "…" where it cuts. text is a search document (the metadata line, then the
// markdown); the metadata line and the frontmatter are left out, and
// whitespace is collapsed.
func Excerpt(text string, tokens []string, width int) string {
	if width <= 0 {
		width = ExcerptWidth
	}
	runes := []rune(strings.Join(strings.Fields(excerptBody(text)), " "))
	if len(runes) <= width {
		return string(runes)
	}
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = fold(r)
	}
	type hit struct{ at, token, end int }
	var hits []hit
	for t, token := range tokens {
		want := []rune(token)
		if len(want) == 0 {
			continue
		}
		for i := 0; i+len(want) <= len(lower); i++ {
			if runesEqual(lower[i:i+len(want)], want) {
				hits = append(hits, hit{at: i, token: t, end: i + len(want)})
			}
		}
	}
	start := 0
	if len(hits) > 0 {
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].at < hits[b].at })
		// A window opening at each hit; a second pointer drops hits that no
		// longer fit, counting distinct tokens as it goes.
		counts := make(map[int]int, len(tokens))
		best, bestStart, bestEnd := 0, 0, 0
		right := 0
		for left := range hits {
			for right < len(hits) && hits[right].end-hits[left].at <= width {
				counts[hits[right].token]++
				right++
			}
			if len(counts) > best {
				best, bestStart, bestEnd = len(counts), hits[left].at, hits[right-1].end
			}
			counts[hits[left].token]--
			if counts[hits[left].token] == 0 {
				delete(counts, hits[left].token)
			}
		}
		// Centre what matched in the window rather than opening on it.
		start = max(bestStart-(width-(bestEnd-bestStart))/2, 0)
	}
	end := min(start+width, len(runes))
	start = max(end-width, 0)
	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

// excerptBody drops the metadata line a search document opens with and the
// markdown's frontmatter.
func excerptBody(text string) string {
	if _, body, ok := strings.Cut(text, "\n"); ok {
		text = body
	}
	trimmed := strings.TrimLeft(text, "\r\n")
	if strings.HasPrefix(trimmed, "---\n") || strings.HasPrefix(trimmed, "---\r\n") {
		rest := trimmed[3:]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			after := rest[end+len("\n---"):]
			if _, body, ok := strings.Cut(after, "\n"); ok {
				return body
			}
			return ""
		}
	}
	return text
}

func runesEqual(a, b []rune) bool {
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RankTokens exposes the ranked search's query terms, for Excerpt.
func RankTokens(query string) []string { return rankTokens(strings.ToLower(query)) }
