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

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegments(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
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
