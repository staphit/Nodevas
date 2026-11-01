package dsl

import (
	"strings"
	"testing"
)

// A requires expression is an editor's to write and the parser recursed once
// per parenthesis or `not`: nested deep enough it overflowed the stack, a
// fatal error that killed the hub. Past maxNesting it is a parse error.
func TestParseRefusesDeepNesting(t *testing.T) {
	deep := maxNesting + 1
	for _, src := range []string{
		strings.Repeat("(", deep) + "a" + strings.Repeat(")", deep),
		strings.Repeat("not ", deep) + "a",
		strings.Repeat("all(", deep) + "a" + strings.Repeat(")", deep),
	} {
		if _, err := Parse(src); err == nil || !strings.Contains(err.Msg, "nests too deeply") {
			t.Fatalf("Parse(%.12q…) error = %v, want a nesting error", src, err)
		}
	}
}

func TestParseRefusesHugeNestingWithoutCrashing(t *testing.T) {
	if _, err := Parse(strings.Repeat("(", 2_000_000) + "a"); err == nil {
		t.Fatal("two million open parentheses parsed")
	}
}

func TestParseKeepsOrdinaryNesting(t *testing.T) {
	// Each level is a parenthesis and a not.
	depth := maxNesting / 4
	src := strings.Repeat("(not ", depth) + "a" + strings.Repeat(")", depth)
	mustParse(t, src)
	mustParse(t, "a and (b or not (c xor all(d, any(e, flag(f >= 2)))))")
}
