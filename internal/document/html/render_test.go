package html

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/document"
)

func TestRenderHTML(t *testing.T) {
	html := RenderHTML(parseSample(t), document.Options{Title: "文本-2"})
	for _, want := range []string{
		"<title>文本-2</title>",
		"<h1>Title</h1>",
		"<strong>bold</strong>",
		"<del>struck</del>",
		`<a href="https://example.com"`,
		"<th style=\"text-align:right\">Count</th>",
		"<td style=\"text-align:center\">yes</td>",
		`<input type="checkbox" disabled>`,
		`<input type="checkbox" disabled checked>`,
		"<ol start=\"3\">",
		"<blockquote>",
		"<hr>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
}

func TestRenderHTMLEscapesAndBlocksScriptURLs(t *testing.T) {
	html := RenderHTML(document.Parse("[x](javascript:alert(1)) <b>&amp;</b>"), document.Options{})
	if strings.Contains(html, "javascript:") {
		t.Errorf("script URL survived:\n%s", html)
	}
	if !strings.Contains(html, "&lt;b&gt;") {
		t.Errorf("raw HTML was not escaped:\n%s", html)
	}
}

func TestRenderHTMLEmbedsAssets(t *testing.T) {
	html := RenderHTML(document.Parse("![a](/api/nodes/n1/files/x.png)"), document.Options{
		Assets: func(url string) (document.Asset, bool) {
			return document.Asset{Data: []byte{1, 2, 3}, MIME: "image/png", Name: "x.png", Width: 10, Height: 5}, true
		},
	})
	if !strings.Contains(html, "src=\"data:image/png;base64,AQID\"") {
		t.Errorf("image was not embedded:\n%s", html)
	}
}

// parseSample parses the fixture shared with the core document tests.
func parseSample(t *testing.T) *document.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "sample.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc := document.Parse(string(raw))
	if len(doc.Blocks) == 0 {
		t.Fatal("sample parsed to nothing")
	}
	return doc
}
