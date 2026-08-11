// Tokenisation for the search index.
//
// The corpus is mostly Chinese, where whitespace carries no word boundaries,
// so whitespace tokenisation alone would index a whole paragraph as one term
// and find nothing. The scheme here is a single pass over runes that:
//
//   - splits the text into runs of letters and digits, dropping everything
//     else (whitespace, punctuation, symbols) as a separator — that is the
//     "normal" tokenisation, and it is what keeps a Latin query like
//     "foo bar" from having to match the space literally;
//   - emits, inside each run, every adjacent character bigram plus every
//     single character.
//
// Bigrams inside the run rather than whole words on purpose: the search this
// replaces was a plain substring match, so "oba" had to find "foobar" and a
// query typed mid-word had to keep working. Word tokens would silently stop
// finding those. Character bigrams give CJK the standard n-gram treatment and
// Latin its old substring behaviour from the same one code path.
//
// Bigrams over-match by construction ("ab"+"bc" is also produced by "bcab"),
// so postings are only ever a candidate set: index.query verifies the original
// query as a substring of the stored text before a document can be a result.
//
// Single characters are indexed as well because the HTTP layer admits a query
// of two *bytes*, and one CJK character is three — a one-character query has
// no bigram at all and would otherwise match everything.
//
// The postings live in SQLite FTS5 now (see persist.go), which changes nothing
// above: what is written into the FTS5 columns is the output of this file,
// space separated, and FTS5's own unicode61 tokeniser only has to split that
// back apart on the spaces. A token is letters and digits only, so unicode61
// cannot subdivide one — Han characters are letters to it, exactly as they are
// to unicode.IsLetter here.
package searchindex

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// forEachRun calls fn with each maximal run of letters/digits, lowercased.
// The slice handed to fn is reused, so fn must not retain it.
func forEachRun(text string, fn func(run []rune)) {
	run := make([]rune, 0, 64)
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			run = append(run, unicode.ToLower(r))
			continue
		}
		if len(run) > 0 {
			fn(run)
			run = run[:0]
		}
	}
	if len(run) > 0 {
		fn(run)
	}
}

// collectTokens adds the distinct index tokens of text to into.
//
// A token is one or two runes encoded as UTF-8, so a unigram and a bigram can
// never collide. The set is filled through an alloc-free map probe: only a
// token seen for the first time in this document allocates.
func collectTokens(text string, into map[string]struct{}) {
	var buf [8]byte
	add := func(runes []rune) {
		n := 0
		for _, r := range runes {
			n += utf8.EncodeRune(buf[n:], r)
		}
		if _, ok := into[string(buf[:n])]; ok {
			return
		}
		into[string(buf[:n])] = struct{}{}
	}
	forEachRun(text, func(run []rune) {
		for i := range run {
			add(run[i : i+1])
			if i+1 < len(run) {
				add(run[i : i+2])
			}
		}
	})
}

// indexTokens is what goes into an FTS5 column: every distinct token of text,
// sorted, separated by spaces.
//
// Sorted, not in map order, because retracting a document from a contentless
// FTS5 table means handing the same string back (see deleteIndexed) and Go's
// map iteration would hand back a different one every time.
func indexTokens(text string) string {
	set := make(map[string]struct{}, 256)
	collectTokens(text, set)
	tokens := make([]string, 0, len(set))
	for token := range set {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// queryTokens returns the tokens a query must be present under. Every token is
// required (postings are intersected), so a run contributes its bigrams —
// which are far more selective than its characters — and falls back to the
// single character only when the run is one rune long.
//
// A query made only of separators yields no tokens; the caller then has no
// candidate filter and must verify every document, which is correct if slow
// and is what a query like "--" deserves.
func queryTokens(query string) []string {
	tokens := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	add := func(token string) {
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	forEachRun(query, func(run []rune) {
		if len(run) == 1 {
			add(string(run))
			return
		}
		for i := 0; i+1 < len(run); i++ {
			add(string(run[i : i+2]))
		}
	})
	return tokens
}

// containsFold reports whether text contains lower, comparing case-folded.
//
// It is the exact equivalent of strings.Contains(strings.ToLower(text), lower)
// for a lower that is already lowercased valid UTF-8 — strings.ToLower maps
// rune by rune with unicode.ToLower, and a valid UTF-8 sequence can never
// start inside another rune's encoding, so a match always lands on a rune
// boundary. The point of doing it this way is that it allocates nothing: the
// old code built a lowercase copy of every document on every keystroke.
func containsFold(text, lower string) bool {
	if lower == "" {
		return true
	}
	first, _ := utf8.DecodeRuneInString(lower)
	for i, r := range text {
		if unicode.ToLower(r) != first {
			continue
		}
		if matchFoldAt(text[i:], lower) {
			return true
		}
	}
	return false
}

func matchFoldAt(text, lower string) bool {
	for _, want := range lower {
		if len(text) == 0 {
			return false
		}
		got, size := utf8.DecodeRuneInString(text)
		if unicode.ToLower(got) != want {
			return false
		}
		text = text[size:]
	}
	return true
}
