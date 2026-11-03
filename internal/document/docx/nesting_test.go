package docx

import (
	"fmt"
	"strings"
	"testing"

	"nodevas/internal/document"
)

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func documentXML(body string) []byte {
	return []byte(`<w:document ` + wordNS + `><w:body>` + body + `</w:body></w:document>`)
}

// listDepth is how deep lists nest inside blocks.
func listDepth(blocks []document.Block) int {
	deepest := 0
	for _, block := range blocks {
		list, ok := block.(*document.List)
		if !ok {
			continue
		}
		for _, item := range list.Items {
			deepest = max(deepest, 1+listDepth(item.Blocks))
		}
	}
	return deepest
}

// A .docx page is read on upload and again on every view. Each nested table
// cost three reader frames, so a 150 KB file of a million and a half nested
// tables overflowed the stack, a fatal error that took the hub down on every
// open. The innermost text has to survive the flattening.
func TestReadDOCXBoundsNestedTables(t *testing.T) {
	const levels = 1_500_000
	var body strings.Builder
	body.WriteString(strings.Repeat("<w:tbl><w:tr><w:tc>", levels))
	body.WriteString("<w:p><w:r><w:t>deep</w:t></w:r></w:p>")
	body.WriteString(strings.Repeat("</w:tc></w:tr></w:tbl>", levels))
	data := zipPayload(t, map[string][]byte{"word/document.xml": documentXML(body.String())})
	doc, err := ReadDOCX(data, nil)
	if err != nil {
		t.Fatalf("ReadDOCX: %v", err)
	}
	if text := document.RenderText(doc); !strings.Contains(text, "deep") {
		t.Fatalf("the innermost table text was lost: %q", text)
	}
}

// List levels come from w:ilvl or the indent, any number a file cares to
// write; each one used to open another nested list for every renderer to
// recurse through.
func TestReadDOCXBoundsListNesting(t *testing.T) {
	const levels = 2_000
	var body strings.Builder
	for level := range levels {
		fmt.Fprintf(&body, `<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>item%d</w:t></w:r></w:p>`, level, level)
	}
	data := zipPayload(t, map[string][]byte{"word/document.xml": documentXML(body.String())})
	doc, err := ReadDOCX(data, nil)
	if err != nil {
		t.Fatalf("ReadDOCX: %v", err)
	}
	if depth := listDepth(doc.Blocks); depth > maxDOCXListDepth {
		t.Fatalf("lists nest %d deep, want at most %d", depth, maxDOCXListDepth)
	}
	if text := document.RenderText(doc); !strings.Contains(text, fmt.Sprintf("item%d", levels-1)) {
		t.Fatal("the items past the nesting limit were lost")
	}
}

// Ordinary nesting reads as it always did.
func TestReadDOCXKeepsOrdinaryNesting(t *testing.T) {
	var body strings.Builder
	for level := range 3 {
		fmt.Fprintf(&body, `<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>item%d</w:t></w:r></w:p>`, level, level)
	}
	body.WriteString(`<w:tbl><w:tr><w:tc><w:tbl><w:tr><w:tc><w:p><w:r><w:t>inner</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc></w:tr></w:tbl>`)
	data := zipPayload(t, map[string][]byte{"word/document.xml": documentXML(body.String())})
	doc, err := ReadDOCX(data, nil)
	if err != nil {
		t.Fatalf("ReadDOCX: %v", err)
	}
	if depth := listDepth(doc.Blocks); depth != 3 {
		t.Fatalf("list depth = %d, want 3", depth)
	}
	table, ok := doc.Blocks[len(doc.Blocks)-1].(*document.Table)
	if !ok || document.PlainText(table.Head[0].Inlines) != "inner" {
		t.Fatalf("nested table did not flatten into the cell: %#v", doc.Blocks[len(doc.Blocks)-1])
	}
}
