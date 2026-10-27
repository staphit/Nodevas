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
// The postings live in PostgreSQL (see persist.go), which changes nothing
// above: what goes into the tsvector columns is the output of this file, as
// lexemes handed to array_to_tsvector, so PostgreSQL's own text-search parser
// never sees the text and cannot re-split it.
//
// Latin diacritics are folded ("café" is indexed as "cafe"), as SQLite FTS5's
// unicode61 tokeniser folded them when the index lived there. Only the tokens
// are folded: the substring check still compares the text as written.
package searchindex

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// foldTable maps a precomposed Latin letter to its base letter: NFD, then the
// combining marks dropped, which is what unicode61's remove_diacritics did.
// Only the Latin blocks are folded — NFD would take a Hangul syllable apart
// into jamo, which are letters and would change what a run is.
var foldTable = func() map[rune]rune {
	table := make(map[rune]rune, 512)
	add := func(from, to rune) {
		for r := from; r <= to; r++ {
			decomposed := []rune(norm.NFD.String(string(r)))
			if len(decomposed) < 2 || !unicode.IsLetter(decomposed[0]) {
				continue
			}
			marks := true
			for _, mark := range decomposed[1:] {
				if !unicode.Is(unicode.Mn, mark) {
					marks = false
					break
				}
			}
			if marks {
				table[r] = decomposed[0]
			}
		}
	}
	add(0x00C0, 0x024F) // Latin-1 Supplement, Latin Extended-A and -B
	add(0x1E00, 0x1EFF) // Latin Extended Additional (Vietnamese)
	return table
}()

// fold lowercases r and drops its diacritic.
func fold(r rune) rune {
	r = unicode.ToLower(r)
	if r < 0xC0 {
		return r
	}
	if base, ok := foldTable[r]; ok {
		return base
	}
	return r
}

// forEachRun calls fn with each maximal run of letters/digits, lowercased and
// with Latin diacritics folded. The slice handed to fn is reused, so fn must
// not retain it.
func forEachRun(text string, fn func(run []rune)) {
	run := make([]rune, 0, 64)
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			run = append(run, fold(r))
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

// documentTokens is what goes into a tsvector column: every distinct token of
// text, the number of them, and whether some had to be left out to fit.
//
// A tsvector holds under a megabyte of lexemes, and the biggest documents have
// more distinct bigrams than that (see maxLexemeBytes). Every single character
// is kept — there are only so many of them, and they are what a ranked search
// of a Chinese question scores most — and then bigrams in the order the text
// first uses them, until the budget is spent. The document is marked oversize
// so a plain search still verifies it whatever its tokens say. The count is of
// every token, kept or not: it is the document length BM25 normalises by.
func documentTokens(text string) (tokens []string, count int, cut bool) {
	seen := make(map[string]struct{}, 256)
	var unigrams, bigrams []string
	var buf [8]byte
	add := func(runes []rune) {
		n := 0
		for _, r := range runes {
			n += utf8.EncodeRune(buf[n:], r)
		}
		if _, ok := seen[string(buf[:n])]; ok {
			return
		}
		token := string(buf[:n])
		seen[token] = struct{}{}
		if len(runes) == 1 {
			unigrams = append(unigrams, token)
		} else {
			bigrams = append(bigrams, token)
		}
	}
	forEachRun(text, func(run []rune) {
		for i := range run {
			add(run[i : i+1])
			if i+1 < len(run) {
				add(run[i : i+2])
			}
		}
	})
	count = len(unigrams) + len(bigrams)
	tokens = make([]string, 0, count)
	spent := 0
	for _, token := range unigrams {
		if spent+len(token) > maxLexemeBytes {
			cut = true
			break
		}
		spent += len(token)
		tokens = append(tokens, token)
	}
	for _, token := range bigrams {
		if spent+len(token) > maxLexemeBytes {
			cut = true
			break
		}
		spent += len(token)
		tokens = append(tokens, token)
	}
	return tokens, count, cut
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

// maxRankTokens bounds the tokens a ranked search scores. A CJK
// character yields two tokens (itself and its bigram), so 32 would drop the
// tail of any question over 16 characters; 64 keeps a 32-character question
// whole and is still a small query.
const maxRankTokens = 64

// rankTokens is what a ranked search looks for: any of these, scored by BM25,
// rather than all of them. A whole question ("林雨的妹妹叫什麼名字？") then
// finds the passage that shares most of its words instead of nothing at all.
//
// Per run, in text order: a CJK character on its own (a Chinese word is often
// one character, and the index holds every one), then the bigram starting at
// it. A Latin letter alone is not a term worth scoring, so only its bigrams
// are. A one-rune run is its own token either way. The index tokens are
// unchanged: these are a different selection of the same vocabulary.
func rankTokens(query string) []string {
	tokens := make([]string, 0, maxRankTokens)
	seen := make(map[string]struct{}, maxRankTokens)
	add := func(token string) {
		if len(tokens) == maxRankTokens {
			return
		}
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
		for i := range run {
			if isCJK(run[i]) {
				add(string(run[i : i+1]))
			}
			if i+1 < len(run) {
				add(string(run[i : i+2]))
			}
		}
	})
	return tokens
}

// isCJK reports whether r is from the CJK blocks (radicals onwards), where a
// single character carries meaning.
func isCJK(r rune) bool { return r >= 0x2E80 }
