package engine

import "testing"

func TestOpensFrontMatter(t *testing.T) {
	for text, want := range map[string]bool{
		"---\nx: 1\n---\nb": true,
		"\n  ---\r\n":       true,
		"---":               true,
		"--- x":             false,
		"text\n---\n":       false,
		"----\n":            false,
		"":                  false,
	} {
		if got := OpensFrontMatter(text); got != want {
			t.Errorf("OpensFrontMatter(%q) = %v, want %v", text, got, want)
		}
	}
}
