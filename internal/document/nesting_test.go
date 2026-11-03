package document

import (
	"strings"
	"testing"
)

// blockDepth is how deep quotes and lists nest inside blocks.
func blockDepth(blocks []Block) int {
	deepest := 0
	for _, block := range blocks {
		depth := 0
		switch node := block.(type) {
		case *BlockQuote:
			depth = 1 + blockDepth(node.Blocks)
		case *List:
			for _, item := range node.Items {
				depth = max(depth, 1+blockDepth(item.Blocks))
			}
		}
		deepest = max(deepest, depth)
	}
	return deepest
}

// inlineDepth is how deep links and emphasis nest inside the blocks' text.
func inlineDepth(blocks []Block) int {
	var walk func([]Inline) int
	walk = func(inlines []Inline) int {
		deepest := 0
		for _, inline := range inlines {
			var kids []Inline
			switch node := inline.(type) {
			case *Strong:
				kids = node.Kids
			case *Emphasis:
				kids = node.Kids
			case *Strike:
				kids = node.Kids
			case *Link:
				kids = node.Kids
			default:
				continue
			}
			deepest = max(deepest, 1+walk(kids))
		}
		return deepest
	}
	deepest := 0
	for _, block := range blocks {
		switch node := block.(type) {
		case *Paragraph:
			deepest = max(deepest, walk(node.Inlines))
		case *BlockQuote:
			deepest = max(deepest, inlineDepth(node.Blocks))
		case *List:
			for _, item := range node.Items {
				deepest = max(deepest, inlineDepth(item.Blocks))
			}
		}
	}
	return deepest
}

// Markdown arrives from anyone who can write a page or ask for an export, and
// every level of nesting used to cost a parser frame: two million '>' ran the
// hub out of stack, a fatal error no recover catches.
func TestParseBoundsQuoteNesting(t *testing.T) {
	doc := Parse(strings.Repeat(">", 2_000_000) + " tail")
	if depth := blockDepth(doc.Blocks); depth > maxNesting {
		t.Fatalf("quotes nest %d deep, want at most %d", depth, maxNesting)
	}
	if text := RenderText(doc); !strings.Contains(text, "tail") {
		t.Fatalf("the text past the nesting limit was lost: %q", text[max(0, len(text)-40):])
	}
}

func TestParseBoundsListNesting(t *testing.T) {
	doc := Parse(strings.Repeat("- ", 3_000) + "tail")
	if depth := blockDepth(doc.Blocks); depth > maxNesting {
		t.Fatalf("lists nest %d deep, want at most %d", depth, maxNesting)
	}
	if text := RenderText(doc); !strings.Contains(text, "tail") {
		t.Fatalf("the text past the nesting limit was lost")
	}
}

func TestParseBoundsLinkNesting(t *testing.T) {
	const levels = 5_000
	doc := Parse(strings.Repeat("[", levels) + "tail" + strings.Repeat("](u)", levels))
	if depth := inlineDepth(doc.Blocks); depth > maxNesting {
		t.Fatalf("links nest %d deep, want at most %d", depth, maxNesting)
	}
	if text := RenderText(doc); !strings.Contains(text, "tail") {
		t.Fatalf("the text past the nesting limit was lost")
	}
}

// Ordinary nesting is untouched by the limit.
func TestParseKeepsOrdinaryNesting(t *testing.T) {
	doc := Parse("> quote\n> > inner\n\n- a\n  - b\n    - c\n\n**bold [link *em*](u)**")
	if depth := blockDepth(doc.Blocks[:1]); depth != 2 {
		t.Fatalf("quote depth = %d, want 2", depth)
	}
	if depth := blockDepth(doc.Blocks[1:2]); depth != 3 {
		t.Fatalf("list depth = %d, want 3", depth)
	}
	if depth := inlineDepth(doc.Blocks[2:]); depth != 3 {
		t.Fatalf("inline depth = %d, want 3", depth)
	}
}
