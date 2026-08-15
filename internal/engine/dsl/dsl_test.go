package dsl

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) Expr {
	t.Helper()
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return e
}

func evalStr(t *testing.T, src string, done []string, flags map[string]any) bool {
	t.Helper()
	e := mustParse(t, src)
	doneSet := map[string]bool{}
	for _, d := range done {
		doneSet[d] = true
	}
	return Eval(e, Context{Done: func(id string) bool { return doneSet[id] }, Flags: flags})
}

func TestOperators(t *testing.T) {
	cases := []struct {
		src  string
		done []string
		want bool
	}{
		{"a", []string{"a"}, true},
		{"a", nil, false},
		{"a and b", []string{"a", "b"}, true},
		{"a and b", []string{"a"}, false},
		{"a or b", []string{"b"}, true},
		{"a or b", nil, false},
		{"not a", nil, true},
		{"not a", []string{"a"}, false},
		{"a nor b", nil, true},
		{"a nor b", []string{"a"}, false},
		{"a nor b", []string{"a", "b"}, false},
		{"a xor b", []string{"a"}, true},
		{"a xor b", []string{"a", "b"}, false},
		{"a xor b", nil, false},
		{"a nand b", []string{"a", "b"}, false},
		{"a nand b", []string{"a"}, true},
		{"a nand b", nil, true},
		// Case-insensitive keywords.
		{"a AND b", []string{"a", "b"}, true},
		{"a NOR b", nil, true},
		// Empty expression is vacuously true.
		{"", nil, true},
		{"   ", nil, true},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src, c.done, nil); got != c.want {
			t.Errorf("eval(%q, done=%v) = %v, want %v", c.src, c.done, got, c.want)
		}
	}
}

func TestPrecedence(t *testing.T) {
	// not > and > xor > or
	cases := []struct {
		src  string
		done []string
		want bool
	}{
		// a or b and c == a or (b and c)
		{"a or b and c", []string{"a"}, true},
		{"a or b and c", []string{"b"}, false},
		{"a or b and c", []string{"b", "c"}, true},
		// not a and b == (not a) and b
		{"not a and b", []string{"b"}, true},
		{"not a and b", []string{"a", "b"}, false},
		// a xor b or c == (a xor b) or c ... xor binds tighter than or
		{"a xor b or c", []string{"c"}, true},
		{"a xor b or c", []string{"a", "b"}, false},
		// Parens override.
		{"(a or b) and c", []string{"a"}, false},
		{"(a or b) and c", []string{"a", "c"}, true},
		// not with parens.
		{"not (a and b)", []string{"a"}, true},
		{"not (a and b)", []string{"a", "b"}, false},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src, c.done, nil); got != c.want {
			t.Errorf("eval(%q, done=%v) = %v, want %v", c.src, c.done, got, c.want)
		}
	}
}

func TestAllAnySugar(t *testing.T) {
	cases := []struct {
		src  string
		done []string
		want bool
	}{
		{"all(a, b, c)", []string{"a", "b", "c"}, true},
		{"all(a, b, c)", []string{"a", "b"}, false},
		{"any(a, b, c)", []string{"c"}, true},
		{"any(a, b, c)", nil, false},
		{"all(a, any(b, c))", []string{"a", "c"}, true},
		{"all(a, any(b, c))", []string{"a"}, false},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src, c.done, nil); got != c.want {
			t.Errorf("eval(%q, done=%v) = %v, want %v", c.src, c.done, got, c.want)
		}
	}
}

func TestFlags(t *testing.T) {
	cases := []struct {
		src   string
		flags map[string]any
		want  bool
	}{
		{"flag(found_cat)", map[string]any{"found_cat": true}, true},
		{"flag(found_cat)", map[string]any{"found_cat": false}, false},
		{"flag(found_cat)", map[string]any{}, false},
		{"flag(karma >= 3)", map[string]any{"karma": 3}, true},
		{"flag(karma >= 3)", map[string]any{"karma": 2.5}, false},
		{"flag(karma < 0)", map[string]any{"karma": -1}, true},
		{"flag(karma == 3)", map[string]any{"karma": 3.0}, true},
		{"flag(karma != 3)", map[string]any{"karma": 4}, true},
		{"flag(mood == happy)", map[string]any{"mood": "happy"}, true},
		{"flag(mood == \"happy\")", map[string]any{"mood": "happy"}, true},
		{"flag(mood != happy)", map[string]any{"mood": "sad"}, true},
		{"flag(alive == true)", map[string]any{"alive": true}, true},
		{"flag(alive == false)", map[string]any{"alive": true}, false},
		// Single '=' forgiven as '=='.
		{"flag(karma = 3)", map[string]any{"karma": 3}, true},
		// Type mismatch → false, not error.
		{"flag(karma >= 3)", map[string]any{"karma": "high"}, false},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src, nil, c.flags); got != c.want {
			t.Errorf("eval(%q, flags=%v) = %v, want %v", c.src, c.flags, got, c.want)
		}
	}
}

func TestMixed(t *testing.T) {
	got := evalStr(t, "chapter-2 and flag(karma >= 3)",
		[]string{"chapter-2"}, map[string]any{"karma": 5})
	if !got {
		t.Error("mixed node+flag should be true")
	}
	got = evalStr(t, "(quest-a nor quest-b) and ending-normal",
		[]string{"ending-normal"}, nil)
	if !got {
		t.Error("hidden ending gate should be true when neither quest done")
	}
}

func TestNodeRefsCollection(t *testing.T) {
	e := mustParse(t, "a and (b nor c) and not d and flag(karma >= 3)")
	refs := NodeRefs(e)
	want := []string{"a", "b", "c", "d"}
	if len(refs) != len(want) {
		t.Fatalf("NodeRefs = %v, want %v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("NodeRefs = %v, want %v", refs, want)
		}
	}
	flags := FlagRefs(e)
	if len(flags) != 1 || flags[0] != "karma" {
		t.Fatalf("FlagRefs = %v, want [karma]", flags)
	}
}

func TestHyphenatedIDs(t *testing.T) {
	if !evalStr(t, "setup-db and design-api", []string{"setup-db", "design-api"}, nil) {
		t.Error("hyphenated ids should parse as single identifiers")
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		src       string
		wantAtMin int // error offset must be >= this
	}{
		{"a and", 5},
		{"and a", 0},
		{"a and (b or", 11},
		{"(a", 2},
		{"a ~ b", 2},
		{"flag()", 5},
		{"flag(and)", 5},
		{"a b", 2},
		{"flag(karma >=)", 13},
	}
	for _, c := range cases {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("Parse(%q): expected error", c.src)
			continue
		}
		if err.Offset < c.wantAtMin {
			t.Errorf("Parse(%q): error offset %d, want >= %d (%s)", c.src, err.Offset, c.wantAtMin, err.Msg)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	cases := []string{
		"a and b",
		"a or b and c",
		"(a or b) and c",
		"not (a and b)",
		"a nor b",
		"a xor b nand c",
		"chapter-2 and flag(karma >= 3)",
		"flag(mood == \"happy\")",
	}
	for _, src := range cases {
		e := mustParse(t, src)
		rendered := e.String()
		e2, err := Parse(rendered)
		if err != nil {
			t.Errorf("re-parse of %q (from %q) failed: %v", rendered, src, err)
			continue
		}
		// Semantic check: same truth table over referenced nodes.
		refs := NodeRefs(e)
		if len(refs) > 6 {
			t.Fatalf("test case too large: %q", src)
		}
		flags := map[string]any{"karma": 5, "mood": "happy"}
		for mask := 0; mask < 1<<len(refs); mask++ {
			done := map[string]bool{}
			for i, r := range refs {
				done[r] = mask&(1<<i) != 0
			}
			ctx := Context{Done: func(id string) bool { return done[id] }, Flags: flags}
			if Eval(e, ctx) != Eval(e2, ctx) {
				t.Errorf("round-trip semantics differ for %q -> %q (mask %b)", src, rendered, mask)
				break
			}
		}
	}
}

func TestErrorMessageHasOffset(t *testing.T) {
	_, err := Parse("a and (b nor")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("error should mention offset: %v", err)
	}
}

func TestPruneNodeRefs(t *testing.T) {
	cases := []struct {
		src  string
		drop string
		want string // "" means the whole expression is gone
	}{
		{"a and b", "b", "a"},
		{"a and b", "a", "b"},
		{"a", "a", ""},
		{"a and (b or c)", "b", "a and c"},
		{"a and (b or c)", "a", "b or c"},
		{"not a and b", "a", "b"},
		{"a and flag(karma >= 3)", "a", "flag(karma >= 3)"},
		{"a and b", "c", "a and b"},
	}
	for _, tc := range cases {
		expr, err := Parse(tc.src)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		pruned := PruneNodeRefs(expr, func(id string) bool { return id == tc.drop })
		got := ""
		if pruned != nil {
			got = pruned.String()
		}
		if got != tc.want {
			t.Errorf("PruneNodeRefs(%q, drop %q) = %q, want %q", tc.src, tc.drop, got, tc.want)
		}
	}
}
