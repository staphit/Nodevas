package html

import (
	"encoding/base64"
	"html"
	"strconv"
	"strings"

	"nodevas/internal/document"
)

const htmlStyle = `
:root { color-scheme: light; }
* { box-sizing: border-box; }
body {
  margin: 0 auto; padding: 48px 32px; max-width: 46em;
  font-family: -apple-system, "Segoe UI", "Microsoft JhengHei", "PingFang TC",
    "Noto Sans CJK TC", sans-serif;
  font-size: 16px; line-height: 1.75; color: #1b1d21; background: #fff;
}
h1, h2, h3, h4, h5, h6 { line-height: 1.3; margin: 1.6em 0 0.6em; font-weight: 700; }
h1 { font-size: 1.9em; } h2 { font-size: 1.5em; } h3 { font-size: 1.25em; }
h4 { font-size: 1.1em; } h5, h6 { font-size: 1em; }
h1:first-child, h2:first-child { margin-top: 0; }
p { margin: 0.85em 0; }
ul, ol { margin: 0.85em 0; padding-left: 1.6em; }
li { margin: 0.3em 0; }
li.task { list-style: none; margin-left: -1.3em; }
li.task input { margin-right: 0.5em; }
blockquote {
  margin: 1em 0; padding: 0.2em 1em; border-left: 3px solid #c9ced6;
  color: #4a4f57;
}
hr { border: 0; border-top: 1px solid #d8dce2; margin: 2em 0; }
code, pre { font-family: "Cascadia Mono", Consolas, "Courier New", monospace; }
code { background: #f1f3f6; padding: 0.12em 0.35em; border-radius: 4px; font-size: 0.92em; }
pre {
  background: #f6f7f9; border: 1px solid #e3e6ea; border-radius: 6px;
  padding: 12px 14px; overflow-x: auto; line-height: 1.55;
}
pre code { background: none; padding: 0; font-size: 0.9em; }
table { border-collapse: collapse; margin: 1.1em 0; width: 100%; }
th, td { border: 1px solid #ccd1d8; padding: 6px 10px; vertical-align: top; }
th { background: #f2f4f7; font-weight: 700; }
img { max-width: 100%; height: auto; }
a { color: #1a56c4; }
del { color: #6b7078; }
@media print {
  body { padding: 0; max-width: none; font-size: 12pt; }
  pre, blockquote, table, img { break-inside: avoid; }
  h1, h2, h3 { break-after: avoid; }
  a { color: inherit; text-decoration: none; }
}
`

// RenderHTML renders a standalone HTML file: styles inlined, images embedded
// as data URIs when the resolver can reach them, so the file works on its own
// and prints cleanly.
func RenderHTML(doc *document.Doc, opts document.Options) string {
	var b strings.Builder
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "document"
	}
	b.WriteString("<!doctype html>\n<html lang=\"zh-Hant\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	b.WriteString("<style>" + htmlStyle + "</style>\n")
	b.WriteString("</head>\n<body>\n")
	writeHTMLBlocks(&b, doc.Blocks, opts)
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

func writeHTMLBlocks(b *strings.Builder, blocks []document.Block, opts document.Options) {
	for _, block := range blocks {
		switch node := block.(type) {
		case *document.Heading:
			level := strconv.Itoa(node.Level)
			b.WriteString("<h" + level + ">")
			writeHTMLInlines(b, node.Inlines, opts)
			b.WriteString("</h" + level + ">\n")
		case *document.Paragraph:
			b.WriteString("<p>")
			writeHTMLInlines(b, node.Inlines, opts)
			b.WriteString("</p>\n")
		case *document.CodeBlock:
			b.WriteString("<pre><code")
			if node.Lang != "" {
				b.WriteString(" class=\"language-" + html.EscapeString(node.Lang) + "\"")
			}
			b.WriteString(">" + html.EscapeString(node.Code) + "</code></pre>\n")
		case *document.BlockQuote:
			b.WriteString("<blockquote>\n")
			writeHTMLBlocks(b, node.Blocks, opts)
			b.WriteString("</blockquote>\n")
		case *document.List:
			writeHTMLList(b, node, opts)
		case *document.Table:
			writeHTMLTable(b, node, opts)
		case *document.Rule:
			b.WriteString("<hr>\n")
		}
	}
}

func writeHTMLList(b *strings.Builder, list *document.List, opts document.Options) {
	tag := "ul"
	if list.Ordered {
		tag = "ol"
	}
	b.WriteString("<" + tag)
	if list.Ordered && list.Start != 1 && list.Start != 0 {
		b.WriteString(" start=\"" + strconv.Itoa(list.Start) + "\"")
	}
	b.WriteString(">\n")
	for _, item := range list.Items {
		if item.Task {
			b.WriteString("<li class=\"task\"><input type=\"checkbox\" disabled")
			if item.Checked {
				b.WriteString(" checked")
			}
			b.WriteString(">")
		} else {
			b.WriteString("<li>")
		}
		writeHTMLItemBlocks(b, item.Blocks, opts)
		b.WriteString("</li>\n")
	}
	b.WriteString("</" + tag + ">\n")
}

// writeHTMLItemBlocks unwraps the item's leading paragraph, so a tight list
// item is text rather than a nested <p>.
func writeHTMLItemBlocks(b *strings.Builder, blocks []document.Block, opts document.Options) {
	if len(blocks) > 0 {
		if paragraph, ok := blocks[0].(*document.Paragraph); ok {
			writeHTMLInlines(b, paragraph.Inlines, opts)
			blocks = blocks[1:]
			if len(blocks) > 0 {
				b.WriteString("\n")
			}
		}
	}
	writeHTMLBlocks(b, blocks, opts)
}

func writeHTMLTable(b *strings.Builder, table *document.Table, opts document.Options) {
	b.WriteString("<table>\n")
	if len(table.Head) > 0 {
		b.WriteString("<thead><tr>")
		for index, cell := range table.Head {
			b.WriteString("<th" + htmlAlign(document.AlignAt(table.Align, index)) + ">")
			writeHTMLInlines(b, cell.Inlines, opts)
			b.WriteString("</th>")
		}
		b.WriteString("</tr></thead>\n")
	}
	if len(table.Rows) > 0 {
		b.WriteString("<tbody>\n")
		for _, row := range table.Rows {
			b.WriteString("<tr>")
			for index, cell := range row {
				b.WriteString("<td" + htmlAlign(document.AlignAt(table.Align, index)) + ">")
				writeHTMLInlines(b, cell.Inlines, opts)
				b.WriteString("</td>")
			}
			b.WriteString("</tr>\n")
		}
		b.WriteString("</tbody>\n")
	}
	b.WriteString("</table>\n")
}

func htmlAlign(align document.Align) string {
	switch align {
	case document.AlignLeft:
		return " style=\"text-align:left\""
	case document.AlignCenter:
		return " style=\"text-align:center\""
	case document.AlignRight:
		return " style=\"text-align:right\""
	}
	return ""
}

func writeHTMLInlines(b *strings.Builder, inlines []document.Inline, opts document.Options) {
	for _, inline := range inlines {
		switch node := inline.(type) {
		case *document.Text:
			b.WriteString(html.EscapeString(node.Value))
		case *document.CodeSpan:
			b.WriteString("<code>" + html.EscapeString(node.Value) + "</code>")
		case *document.Strong:
			b.WriteString("<strong>")
			writeHTMLInlines(b, node.Kids, opts)
			b.WriteString("</strong>")
		case *document.Emphasis:
			b.WriteString("<em>")
			writeHTMLInlines(b, node.Kids, opts)
			b.WriteString("</em>")
		case *document.Strike:
			b.WriteString("<del>")
			writeHTMLInlines(b, node.Kids, opts)
			b.WriteString("</del>")
		case *document.Link:
			b.WriteString("<a href=\"" + html.EscapeString(document.SafeURL(node.URL)) + "\"")
			if node.Title != "" {
				b.WriteString(" title=\"" + html.EscapeString(node.Title) + "\"")
			}
			b.WriteString(" rel=\"noopener noreferrer\">")
			writeHTMLInlines(b, node.Kids, opts)
			b.WriteString("</a>")
		case *document.Image:
			source := document.SafeURL(node.URL)
			if asset, ok := opts.Resolve(node.URL); ok {
				source = "data:" + asset.MIME + ";base64," +
					base64.StdEncoding.EncodeToString(asset.Data)
			}
			b.WriteString("<img src=\"" + html.EscapeString(source) + "\" alt=\"" +
				html.EscapeString(node.Alt) + "\"")
			if node.Title != "" {
				b.WriteString(" title=\"" + html.EscapeString(node.Title) + "\"")
			}
			b.WriteString(">")
		case *document.LineBreak:
			if node.Hard {
				b.WriteString("<br>\n")
			} else {
				b.WriteString("\n")
			}
		}
	}
}
