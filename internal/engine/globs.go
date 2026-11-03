package engine

import (
	"path"
	"strings"
)

// Slash-separated glob matching with ** for the test-file globs. A segment
// matches with path.Match; "**" matches any number of whole segments,
// including none. Backslashes are refused in both the pattern and the path,
// never translated, so a Windows path cannot sneak past a pattern.

// MatchGlob reports whether a slash-separated path matches pattern.
func MatchGlob(pattern, p string) bool {
	if pattern == "" || p == "" || strings.Contains(pattern, `\`) || strings.Contains(p, `\`) {
		return false
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

// matchSegments fills the table "pat[i:] matches segs[j:]" one pattern
// segment at a time, from the last one back, so it costs at most
// len(pat)*len(segs) segment matches. Trying every split of the path for every
// "**" instead is exponential in their number, and the hub matches
// editor-written globs against runner-reported paths.
func matchSegments(pat, segs []string) bool {
	// next[j]: pat[i+1:] matches segs[j:]. The empty pattern matches only the
	// empty path.
	next := make([]bool, len(segs)+1)
	cur := make([]bool, len(segs)+1)
	next[len(segs)] = true
	for i := len(pat) - 1; i >= 0; i-- {
		if pat[i] == "**" {
			// "**" takes no segment (next[j]) or one more (cur[j+1]).
			cur[len(segs)] = next[len(segs)]
			for j := len(segs) - 1; j >= 0; j-- {
				cur[j] = next[j] || cur[j+1]
			}
		} else {
			cur[len(segs)] = false
			for j := len(segs) - 1; j >= 0; j-- {
				cur[j] = false
				if next[j+1] {
					ok, err := path.Match(pat[i], segs[j])
					cur[j] = err == nil && ok
				}
			}
		}
		next, cur = cur, next
	}
	return next[0]
}

// MatchAnyGlob reports whether path matches any pattern.
func MatchAnyGlob(patterns []string, p string) bool {
	for _, pattern := range patterns {
		if MatchGlob(pattern, p) {
			return true
		}
	}
	return false
}
