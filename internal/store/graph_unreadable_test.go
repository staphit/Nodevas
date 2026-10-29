package store

import (
	"errors"
	"os"
	"strconv"
	"testing"

	"nodevas/internal/locale"
)

// unreadable is the refusal the store gives for graph.yaml holding data.
func unreadable(t *testing.T, data string) *GraphUnreadableError {
	t.Helper()
	root := t.TempDir()
	st := NewStore(root)
	if err := os.WriteFile(st.GraphPath(), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := st.LoadGraph()
	var refusal *GraphUnreadableError
	if !errors.As(err, &refusal) {
		t.Fatalf("LoadGraph of %q = %v, not a GraphUnreadableError", data, err)
	}
	return refusal
}

// The line named is the one a text editor shows the mistake on. yaml.v3
// counts a parser error's line from zero and a scanner error's from one, so
// an unclosed bracket used to send the reader one line too high.
func TestUnreadableGraphNamesTheLineTheMistakeIsOn(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		line       int
	}{
		{"unclosed flow sequence", "version: 1\nnodes:\n  - id: a\n    title: [Build\n    kind: task\n", 4},
		{"unclosed flow sequence at the end", "version: 1\nnodes:\n  - id: [broken\n", 3},
		{"unclosed flow mapping", "version: 1\nnodes:\n  - id: a\n    title: {Build\n    kind: task\n", 4},
		{"misaligned key", "version: 1\nnodes:\n  - id: a\n   title: x\n", 3},
		{"bracket on the first line", "version: [1\n", 1},
		{"bracket left open at the end", "version: 1\nnodes: [\n", 2},
		// Scanner and decoding errors already count from one.
		{"mapping value in a scalar", "version: 1\nnodes:\n  - id: a\n    title: x: y\n", 4},
		{"wrong type", "version: 1\nnodes:\n  - id: a\n    priority: [1]\n", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusal := unreadable(t, tc.data)
			if refusal.Line != tc.line {
				t.Fatalf("Line = %d, want %d (%v)", refusal.Line, tc.line, refusal)
			}
			// The parser's words in brackets name the same line as the
			// sentence, or the reader is sent to two places.
			want := strconv.Itoa(tc.line)
			for _, language := range []locale.Language{locale.English, locale.Chinese} {
				text := locale.Render(refusal, language)
				for _, match := range yamlLine.FindAllStringSubmatch(text, -1) {
					if match[1] != want {
						t.Fatalf("%s: %q names line %s, want %s", language, text, match[1], want)
					}
				}
			}
		})
	}
}
