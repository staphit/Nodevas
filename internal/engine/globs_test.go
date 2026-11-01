package engine

import (
	"math/rand/v2"
	"path"
	"strings"
	"testing"
	"time"
)

// matchWithin runs MatchGlob in the background and fails the test when it has
// not answered within limit.
func matchWithin(t *testing.T, limit time.Duration, pattern, p string) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- MatchGlob(pattern, p) }()
	select {
	case got := <-done:
		return got
	case <-time.After(limit):
		t.Fatalf("MatchGlob(%d-char glob, %d-char path) did not finish within %s", len(pattern), len(p), limit)
		return false
	}
}

// A test glob is an editor's to write and a changed path is a runner's to
// report, and the hub matches every reported path against every glob while it
// holds the pipeline's lock. Trying every split of the path for every "**"
// is exponential in their number: twelve of them against a 41-segment path
// never finished.
func TestMatchGlobManyDoubleStarsIsFast(t *testing.T) {
	deep := strings.Repeat("a/", 40) + "b.go"
	if matchWithin(t, 2*time.Second, strings.Repeat("**/", 12)+"x", deep) {
		t.Fatal("a glob ending in x matched a path ending in b.go")
	}
	// The longest glob a workflow accepts (200 characters) against the longest
	// path a run may report (1024 bytes), both made of nothing but splits.
	longest := strings.Repeat("**/", 66) + "x"
	if err := ValidateWorkflow(&Workflow{Preset: PresetCustom, TestGlobs: []string{longest}}); err != nil {
		t.Fatalf("the workflow refuses the glob, so it is not the worst case: %v", err)
	}
	widest := strings.Repeat("a/", 511) + "b"
	if matchWithin(t, 2*time.Second, longest, widest) {
		t.Fatal("a glob ending in x matched a path ending in b")
	}
	if !matchWithin(t, 2*time.Second, strings.Repeat("**/*/", 33)+"b", widest) {
		t.Fatal("a glob of splits and single segments missed a path deep enough for it")
	}
}

func TestMatchGlobKeepsDoubleStarSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"a/**/b/**/c", "a/x/y/b/z/c", true},
		{"a/**/b/**/c", "a/b/c", true},
		{"a/**/b/**/c", "a/x/c", false},
		{"**/test/**", "x/tests/y.go", false},
		{"**/test/**", "x/test/y.go", true},
		{"**/test/**", "test", true},
		{"**", "a/b/c", true},
		{"**/**", "a", true},
		{"a/**", "a", true},
		{"a/**", "b", false},
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "a/b/c.ts", false},
		{"*/*.go", "a/b/c.go", false},
		{"[/x", "a", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// referenceMatch is the matcher as it was first written: try every split for
// every "**". Exponential, but exact, so on small inputs it is the spec.
func referenceMatch(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if referenceMatch(pat[1:], segs[i:]) {
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

func TestMatchGlobAgreesWithTryingEverySplit(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	patternParts := []string{"**", "**", "*", "a", "b", "?", "[ab]", "a*", "*b", "["}
	pathParts := []string{"a", "b", "ab", "ba", "bb"}
	pick := func(parts []string, most int) []string {
		out := make([]string, random.IntN(most+1))
		for i := range out {
			out[i] = parts[random.IntN(len(parts))]
		}
		return out
	}
	for range 20000 {
		pattern := strings.Join(pick(patternParts, 6), "/")
		p := strings.Join(pick(pathParts, 7), "/")
		want := pattern != "" && p != "" &&
			referenceMatch(strings.Split(pattern, "/"), strings.Split(p, "/"))
		if got := MatchGlob(pattern, p); got != want {
			t.Fatalf("MatchGlob(%q, %q) = %v, trying every split says %v", pattern, p, got, want)
		}
	}
}
