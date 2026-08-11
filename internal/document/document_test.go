package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseSample parses the fixture every renderer test shares. It lives in
// testdata so the docx and html packages can read the same file.
func parseSample(t *testing.T) *Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "sample.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc := Parse(string(raw))
	if len(doc.Blocks) == 0 {
		t.Fatal("sample parsed to nothing")
	}
	return doc
}

func TestParseBlockStructure(t *testing.T) {
	doc := parseSample(t)
	var (
		headings int
		lists    []*List
		tables   []*Table
		codes    []*CodeBlock
		quotes   []*BlockQuote
		rules    int
	)
	for _, block := range doc.Blocks {
		switch node := block.(type) {
		case *Heading:
			headings++
		case *List:
			lists = append(lists, node)
		case *Table:
			tables = append(tables, node)
		case *CodeBlock:
			codes = append(codes, node)
		case *BlockQuote:
			quotes = append(quotes, node)
		case *Rule:
			rules++
		}
	}
	if headings != 2 {
		t.Errorf("headings = %d, want 2", headings)
	}
	if len(lists) != 2 {
		t.Fatalf("lists = %d, want 2", len(lists))
	}
	if len(lists[0].Items) != 4 {
		t.Errorf("bullet list items = %d, want 4", len(lists[0].Items))
	}
	if !lists[1].Ordered || lists[1].Start != 3 {
		t.Errorf("ordered list = %+v, want ordered starting at 3", lists[1])
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(tables))
	}
	if got := len(tables[0].Rows); got != 2 {
		t.Errorf("table rows = %d, want 2", got)
	}
	if got := tables[0].Align; len(got) != 3 ||
		got[0] != AlignLeft || got[1] != AlignRight || got[2] != AlignCenter {
		t.Errorf("table alignment = %v", got)
	}
	if len(codes) != 1 || codes[0].Lang != "go" ||
		!strings.Contains(codes[0].Code, `fmt.Println("hi")`) {
		t.Errorf("code block = %+v", codes)
	}
	if len(quotes) != 1 {
		t.Errorf("quotes = %d, want 1", len(quotes))
	}
	if rules != 1 {
		t.Errorf("rules = %d, want 1", rules)
	}
}

func TestParseListDetails(t *testing.T) {
	doc := Parse("- alpha\n  - deep\n- [x] done\n")
	list, ok := doc.Blocks[0].(*List)
	if !ok {
		t.Fatalf("first block = %T, want *List", doc.Blocks[0])
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(list.Items))
	}
	if len(list.Items[0].Blocks) != 2 {
		t.Fatalf("first item blocks = %d, want paragraph + nested list", len(list.Items[0].Blocks))
	}
	if _, ok := list.Items[0].Blocks[1].(*List); !ok {
		t.Errorf("nested block = %T, want *List", list.Items[0].Blocks[1])
	}
	if !list.Items[1].Task || !list.Items[1].Checked {
		t.Errorf("second item = %+v, want a checked task", list.Items[1])
	}
}

func TestParseLooseListStaysOneList(t *testing.T) {
	doc := Parse("- alpha\n\n- beta\n\n- gamma\n\ntail paragraph\n")
	list, ok := doc.Blocks[0].(*List)
	if !ok {
		t.Fatalf("first block = %T, want *List", doc.Blocks[0])
	}
	if len(list.Items) != 3 {
		t.Fatalf("items = %d, want 3 — blank lines must not split the list", len(list.Items))
	}
	if len(doc.Blocks) != 2 {
		t.Fatalf("blocks = %d, want list + paragraph", len(doc.Blocks))
	}
	if _, ok := doc.Blocks[1].(*Paragraph); !ok {
		t.Errorf("block after the list = %T, want *Paragraph", doc.Blocks[1])
	}
	numbered := Parse("1. one\n\n2. two\n")
	ordered, ok := numbered.Blocks[0].(*List)
	if !ok || len(ordered.Items) != 2 || !ordered.Ordered {
		t.Fatalf("ordered loose list = %#v", numbered.Blocks[0])
	}
}

func TestParseInlineVariants(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"plain **bold** text", "plain bold text"},
		{`escaped \*not emphasis\*`, "escaped *not emphasis*"},
		{"snake_case_stays", "snake_case_stays"},
		{"a `*literal*` span", "a *literal* span"},
		{"[label](https://example.com)", "label"},
		{"<https://example.com>", "https://example.com"},
		{"![alt](x.png)", "alt"},
	}
	for _, testCase := range cases {
		got := PlainText(parseInline(testCase.source))
		if got != testCase.want {
			t.Errorf("PlainText(%q) = %q, want %q", testCase.source, got, testCase.want)
		}
	}
}

func TestParseSetextHeading(t *testing.T) {
	doc := Parse("Title\n=====\n\nSub\n---\n")
	first, ok := doc.Blocks[0].(*Heading)
	if !ok || first.Level != 1 || PlainText(first.Inlines) != "Title" {
		t.Fatalf("first block = %#v, want H1 Title", doc.Blocks[0])
	}
	second, ok := doc.Blocks[1].(*Heading)
	if !ok || second.Level != 2 {
		t.Fatalf("second block = %#v, want H2", doc.Blocks[1])
	}
}

func TestRenderText(t *testing.T) {
	text := RenderText(parseSample(t))
	for _, want := range []string{
		"Title\n=====",
		"- first item",
		"[ ] open task",
		"[x] done task",
		"3. three",
		"> quoted line",
		`fmt.Println("hi")`,
		"link (https://example.com)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plain text is missing %q\n---\n%s", want, text)
		}
	}
	if strings.Contains(text, "**") || strings.Contains(text, "~~") {
		t.Errorf("markdown markers survived into plain text:\n%s", text)
	}
}

func TestRenderTextTableAligns(t *testing.T) {
	doc := Parse("| a | bbbb |\n| --- | ---: |\n| ccc | d |\n")
	lines := strings.Split(strings.TrimSpace(RenderText(doc)), "\n")
	if len(lines) != 3 {
		t.Fatalf("table lines = %d, want 3:\n%v", len(lines), lines)
	}
	if lines[0] != "a    bbbb" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[2] != "ccc     d" {
		t.Errorf("row = %q, want right-aligned second column", lines[2])
	}
}

func TestRenderTextWideColumns(t *testing.T) {
	doc := Parse("| 名稱 | v |\n| --- | --- |\n| ab | c |\n")
	lines := strings.Split(strings.TrimSpace(RenderText(doc)), "\n")
	if lines[0] != "名稱  v" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[2] != "ab    c" {
		t.Errorf("row = %q, want padding that accounts for fullwidth text", lines[2])
	}
}
