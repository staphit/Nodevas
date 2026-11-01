package document

import (
	"strings"
	"testing"
	"time"
)

// parseWithin parses source on another goroutine and fails the test when that
// takes longer than limit. A parse that runs away keeps its goroutine until
// the test binary exits.
func parseWithin(t *testing.T, limit time.Duration, name, source string) *Doc {
	t.Helper()
	done := make(chan *Doc, 1)
	go func() { done <- Parse(source) }()
	select {
	case doc := <-done:
		return doc
	case <-time.After(limit):
		t.Fatalf("%s: parsing %d bytes took longer than %v", name, len(source), limit)
		return nil
	}
}

// Every '[' or '<' that opens nothing, and every backtick of an unmatched run
// met while looking for a closing '*', used to search to the end of the
// paragraph again, so a megabyte of them took minutes and the 16 MiB an export
// or a saved page accepts took hours, with a docx slot or the store's lock
// held all along.
func TestParseInlineDelimitersStayLinear(t *testing.T) {
	const size = 1 << 20
	var runs strings.Builder
	runs.WriteString("*")
	for run := 1; runs.Len() < size/4; run++ {
		runs.WriteString(strings.Repeat("`", run) + "a")
	}
	cases := []struct {
		name, source, keep string
	}{
		{"open brackets", strings.Repeat("[", size), strings.Repeat("[", size)},
		{"open angles", strings.Repeat("<", size), strings.Repeat("<", size)},
		{"links without a closing parenthesis", strings.Repeat("[a](", size/4) + " tail", "tail"},
		{"brackets with no destination", strings.Repeat("[", size/2) + strings.Repeat("]", size/2) + " tail", "tail"},
		{"unmatched code span runs", runs.String() + " tail", "tail"},
	}
	for _, c := range cases {
		doc := parseWithin(t, 2*time.Second, c.name, c.source)
		if text := RenderText(doc); !strings.Contains(text, c.keep) {
			t.Fatalf("%s: the text was lost: %q", c.name, text[max(0, len(text)-40):])
		}
	}
}

// Ordinary inline markup parses as before.
func TestParseKeepsOrdinaryInlines(t *testing.T) {
	doc := Parse("a [link](u \"t\") <https://x.y> <br> `code` *em* **strong** ~~gone~~ [x] a < b > c ![alt](i.png)")
	para, ok := doc.Blocks[0].(*Paragraph)
	if !ok {
		t.Fatalf("block = %T, want a paragraph", doc.Blocks[0])
	}
	var kinds []string
	for _, inline := range para.Inlines {
		switch inline.(type) {
		case *Link:
			kinds = append(kinds, "link")
		case *LineBreak:
			kinds = append(kinds, "br")
		case *CodeSpan:
			kinds = append(kinds, "code")
		case *Emphasis:
			kinds = append(kinds, "em")
		case *Strong:
			kinds = append(kinds, "strong")
		case *Strike:
			kinds = append(kinds, "strike")
		case *Image:
			kinds = append(kinds, "image")
		}
	}
	want := "link link br code em strong strike image"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("inlines = %q, want %q", got, want)
	}
	if text := RenderText(doc); !strings.Contains(text, "[x] a < b > c") {
		t.Fatalf("literal brackets and angles were lost: %q", text)
	}
}
