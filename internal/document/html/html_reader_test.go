package html

import (
	"strings"
	"testing"

	"nodevas/internal/document"
)

func TestReadHTMLStructure(t *testing.T) {
	source := `<!doctype html><html><head><title>x</title>
<style>p{color:red}</style></head><body>
  <h1>Title</h1>
  <p>document.Text with <strong>bold</strong>, <em>italic</em>, <del>struck</del> and <code>code</code>.</p>
  <ul>
    <li>first</li>
    <li>second <a href="https://example.com">link</a>
      <ul><li>nested</li></ul>
    </li>
    <li class="task"><input type="checkbox" disabled checked> done</li>
  </ul>
  <ol start="3"><li>three</li><li>four</li></ol>
  <blockquote><p>quoted</p></blockquote>
  <table>
    <thead><tr><th>Name</th><th style="text-align:right">Count</th></tr></thead>
    <tbody><tr><td>a</td><td style="text-align:right">1</td></tr></tbody>
  </table>
  <pre><code class="language-go">fmt.Println("hi")
</code></pre>
  <hr>
  <p><img src="/api/nodes/n1/files/shot.png" alt="封面"></p>
</body></html>`

	doc, err := ReadHTML(source)
	if err != nil {
		t.Fatalf("ReadHTML: %v", err)
	}
	markdown := document.RenderMarkdown(doc)
	for _, want := range []string{
		"# Title",
		"**bold**",
		"*italic*",
		"~~struck~~",
		"`code`",
		"- first",
		"[link](https://example.com)",
		"- [x] done",
		"3. three",
		"4. four",
		"> quoted",
		"| Name | Count |",
		"```go",
		`fmt.Println("hi")`,
		"---",
		"![封面](/api/nodes/n1/files/shot.png)",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("HTML → Markdown lost %q\n---\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "color:red") {
		t.Errorf("<style> leaked into the text:\n%s", markdown)
	}
	reparsed := document.Parse(markdown)
	var lists []*document.List
	for _, block := range reparsed.Blocks {
		if list, ok := block.(*document.List); ok {
			lists = append(lists, list)
		}
	}
	if len(lists) != 2 {
		t.Fatalf("lists = %d, want bullet + ordered\n%s", len(lists), markdown)
	}
	if len(lists[0].Items[1].Blocks) < 2 {
		t.Errorf("nested list was flattened:\n%s", markdown)
	}
	if !lists[1].Ordered || lists[1].Start != 3 {
		t.Errorf("ordered list = %+v", lists[1])
	}
}

func TestReadHTMLRoundTripsOurOwnOutput(t *testing.T) {
	source := strings.Join([]string{
		"# Title",
		"",
		"Body **bold** text.",
		"",
		"- one",
		"- two",
		"",
		"| a | b |",
		"| --- | ---: |",
		"| 1 | 2 |",
		"",
	}, "\n")
	rendered := RenderHTML(document.Parse(source), document.Options{Title: "t"})
	doc, err := ReadHTML(rendered)
	if err != nil {
		t.Fatalf("ReadHTML: %v", err)
	}
	back := document.RenderMarkdown(doc)
	for _, want := range []string{"# Title", "**bold**", "- one", "- two", "| a", "--:"} {
		if !strings.Contains(back, want) {
			t.Errorf("round trip lost %q\n---\n%s", want, back)
		}
	}
}

func TestReadHTMLCollapsesWhitespace(t *testing.T) {
	doc, err := ReadHTML("<p>  a\n   b\t c  </p>")
	if err != nil {
		t.Fatal(err)
	}
	if got := document.PlainText(doc.Blocks[0].(*document.Paragraph).Inlines); got != "a b c" {
		t.Errorf("text = %q, want %q", got, "a b c")
	}
}

func TestReadHTMLFragmentWithoutBodyTag(t *testing.T) {
	doc, err := ReadHTML("<h2>Heading</h2><p>text</p>")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(doc.Blocks))
	}
	if heading, ok := doc.Blocks[0].(*document.Heading); !ok || heading.Level != 2 {
		t.Errorf("first block = %#v, want H2", doc.Blocks[0])
	}
}
