package html

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"nodevas/internal/document"
)

// ReadHTML converts an HTML document into the block model, so an HTML page
// can be turned into any other format. Markup with no counterpart in the
// model (layout wrappers, styling) collapses to its text.
func ReadHTML(source string) (*document.Doc, error) {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return nil, err
	}
	reader := &htmlReader{}
	body := findElement(root, atom.Body)
	if body == nil {
		body = root
	}
	return &document.Doc{Blocks: reader.children(body)}, nil
}

type htmlReader struct{}

func findElement(node *html.Node, want atom.Atom) *html.Node {
	if node.Type == html.ElementNode && node.DataAtom == want {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, want); found != nil {
			return found
		}
	}
	return nil
}

// children walks a container, gathering loose inline content into paragraphs
// and recursing into the block-level elements between them.
func (r *htmlReader) children(node *html.Node) []document.Block {
	var blocks []document.Block
	var inlines []document.Inline
	flush := func() {
		trimmed := trimInlineEdges(inlines)
		if len(trimmed) > 0 {
			blocks = append(blocks, &document.Paragraph{Inlines: trimmed})
		}
		inlines = nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if text := collapseSpace(child.Data); text != "" {
				inlines = append(inlines, &document.Text{Value: text})
			}
		case html.ElementNode:
			if isSkippedElement(child.DataAtom) {
				continue
			}
			if isBlockElement(child.DataAtom) {
				flush()
				blocks = append(blocks, r.block(child)...)
				continue
			}
			inlines = append(inlines, r.inlines(child)...)
		}
	}
	flush()
	return blocks
}

func isSkippedElement(name atom.Atom) bool {
	switch name {
	case atom.Script, atom.Style, atom.Head, atom.Title, atom.Meta, atom.Link,
		atom.Noscript, atom.Template, atom.Iframe, atom.Object, atom.Embed:
		return true
	}
	return false
}

func isBlockElement(name atom.Atom) bool {
	switch name {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.P, atom.Pre, atom.Blockquote, atom.Ul, atom.Ol, atom.Table,
		atom.Hr, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header,
		atom.Footer, atom.Aside, atom.Nav, atom.Figure, atom.Figcaption,
		atom.Dl, atom.Dt, atom.Dd, atom.Form, atom.Fieldset:
		return true
	}
	return false
}

func (r *htmlReader) block(node *html.Node) []document.Block {
	switch node.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level, _ := strconv.Atoi(strings.TrimPrefix(node.Data, "h"))
		inlines := trimInlineEdges(r.inlineChildren(node))
		if len(inlines) == 0 {
			return nil
		}
		return []document.Block{&document.Heading{Level: level, Inlines: inlines}}
	case atom.P, atom.Figcaption, atom.Dt, atom.Dd:
		inlines := trimInlineEdges(r.inlineChildren(node))
		if len(inlines) == 0 {
			return nil
		}
		return []document.Block{&document.Paragraph{Inlines: inlines}}
	case atom.Pre:
		return []document.Block{r.codeBlock(node)}
	case atom.Blockquote:
		inner := r.children(node)
		if len(inner) == 0 {
			return nil
		}
		return []document.Block{&document.BlockQuote{Blocks: inner}}
	case atom.Ul, atom.Ol:
		if list := r.list(node); list != nil {
			return []document.Block{list}
		}
		return nil
	case atom.Table:
		if table := r.table(node); table != nil {
			return []document.Block{table}
		}
		return nil
	case atom.Hr:
		return []document.Block{&document.Rule{}}
	}
	// Layout containers contribute their children, not themselves.
	return r.children(node)
}

func (r *htmlReader) codeBlock(node *html.Node) document.Block {
	lang := ""
	source := node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.DataAtom == atom.Code {
			source = child
			for _, class := range strings.Fields(attribute(child, "class")) {
				if rest, ok := strings.CutPrefix(class, "language-"); ok {
					lang = rest
				}
			}
			break
		}
	}
	return &document.CodeBlock{Lang: lang, Code: strings.Trim(rawText(source), "\n")}
}

func (r *htmlReader) list(node *html.Node) *document.List {
	list := &document.List{Ordered: node.DataAtom == atom.Ol, Start: 1}
	if start, err := strconv.Atoi(attribute(node, "start")); err == nil && start > 0 {
		list.Start = start
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.DataAtom != atom.Li {
			continue
		}
		item := &document.ListItem{}
		if checkbox := findCheckbox(child); checkbox != nil {
			item.Task = true
			item.Checked = hasAttribute(checkbox, "checked")
			checkbox.Parent.RemoveChild(checkbox)
		}
		item.Blocks = r.children(child)
		list.Items = append(list.Items, item)
	}
	if len(list.Items) == 0 {
		return nil
	}
	return list
}

// findCheckbox locates the task-list checkbox of a list item, if any.
func findCheckbox(node *html.Node) *html.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.DataAtom == atom.Input &&
			strings.EqualFold(attribute(child, "type"), "checkbox") {
			return child
		}
		if child.Type == html.ElementNode && !isBlockElement(child.DataAtom) {
			if found := findCheckbox(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func (r *htmlReader) table(node *html.Node) *document.Table {
	var rows [][]document.TableCell
	var aligns []document.Align
	headerRow := -1
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			switch child.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(child)
			case atom.Tr:
				var cells []document.TableCell
				header := false
				for cell := child.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type != html.ElementNode ||
						(cell.DataAtom != atom.Td && cell.DataAtom != atom.Th) {
						continue
					}
					if cell.DataAtom == atom.Th {
						header = true
					}
					index := len(cells)
					if align := cellAlign(cell); align != document.AlignDefault {
						for len(aligns) <= index {
							aligns = append(aligns, document.AlignDefault)
						}
						if aligns[index] == document.AlignDefault {
							aligns[index] = align
						}
					}
					cells = append(cells, document.TableCell{
						Inlines: trimInlineEdges(r.inlineChildren(cell)),
					})
				}
				if len(cells) == 0 {
					continue
				}
				if header && headerRow < 0 {
					headerRow = len(rows)
				}
				rows = append(rows, cells)
			}
		}
	}
	walk(node)
	if len(rows) == 0 {
		return nil
	}
	if headerRow < 0 {
		headerRow = 0
	}
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	for len(aligns) < columns {
		aligns = append(aligns, document.AlignDefault)
	}
	return &document.Table{
		Align: aligns[:columns],
		Head:  rows[headerRow],
		Rows:  append(rows[:headerRow:headerRow], rows[headerRow+1:]...),
	}
}

func cellAlign(node *html.Node) document.Align {
	style := strings.ToLower(attribute(node, "style") + ";" + attribute(node, "align"))
	switch {
	case strings.Contains(style, "center"):
		return document.AlignCenter
	case strings.Contains(style, "right"):
		return document.AlignRight
	case strings.Contains(style, "left"):
		return document.AlignLeft
	}
	return document.AlignDefault
}

// ---------- inline ----------

func (r *htmlReader) inlineChildren(node *html.Node) []document.Inline {
	var out []document.Inline
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if text := collapseSpace(child.Data); text != "" {
				out = append(out, &document.Text{Value: text})
			}
		case html.ElementNode:
			if isSkippedElement(child.DataAtom) {
				continue
			}
			out = append(out, r.inlines(child)...)
		}
	}
	return out
}

func (r *htmlReader) inlines(node *html.Node) []document.Inline {
	switch node.DataAtom {
	case atom.Br:
		return []document.Inline{&document.LineBreak{Hard: true}}
	case atom.Img:
		source := attribute(node, "src")
		if source == "" {
			return nil
		}
		return []document.Inline{&document.Image{
			URL:   source,
			Alt:   attribute(node, "alt"),
			Title: attribute(node, "title"),
		}}
	case atom.A:
		kids := r.inlineChildren(node)
		href := attribute(node, "href")
		if href == "" {
			return kids
		}
		if len(kids) == 0 {
			kids = []document.Inline{&document.Text{Value: href}}
		}
		return []document.Inline{&document.Link{URL: href, Title: attribute(node, "title"), Kids: kids}}
	case atom.Strong, atom.B:
		if kids := r.inlineChildren(node); len(kids) > 0 {
			return []document.Inline{&document.Strong{Kids: kids}}
		}
	case atom.Em, atom.I:
		if kids := r.inlineChildren(node); len(kids) > 0 {
			return []document.Inline{&document.Emphasis{Kids: kids}}
		}
	case atom.Del, atom.S, atom.Strike:
		if kids := r.inlineChildren(node); len(kids) > 0 {
			return []document.Inline{&document.Strike{Kids: kids}}
		}
	case atom.Code, atom.Kbd, atom.Samp:
		if text := rawText(node); strings.TrimSpace(text) != "" {
			return []document.Inline{&document.CodeSpan{Value: collapseSpace(text)}}
		}
	case atom.Input:
		if strings.EqualFold(attribute(node, "type"), "checkbox") {
			if hasAttribute(node, "checked") {
				return []document.Inline{&document.Text{Value: "[x] "}}
			}
			return []document.Inline{&document.Text{Value: "[ ] "}}
		}
	case atom.Blockquote, atom.P, atom.Div, atom.Li:
		// A block element found in inline position: keep its text flowing.
		return r.inlineChildren(node)
	}
	return r.inlineChildren(node)
}

// ---------- text helpers ----------

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val
		}
	}
	return ""
}

func hasAttribute(node *html.Node, name string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return true
		}
	}
	return false
}

// rawText keeps whitespace exactly as written, for <pre> and <code>.
func rawText(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			b.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

// collapseSpace folds HTML's insignificant whitespace into single spaces.
func collapseSpace(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		} else if space {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

// trimInlineEdges removes the leading and trailing whitespace a paragraph
// inherits from indented markup.
func trimInlineEdges(inlines []document.Inline) []document.Inline {
	for len(inlines) > 0 {
		text, ok := inlines[0].(*document.Text)
		if !ok {
			break
		}
		trimmed := strings.TrimLeft(text.Value, " ")
		if trimmed == "" {
			inlines = inlines[1:]
			continue
		}
		inlines = append([]document.Inline{&document.Text{Value: trimmed}}, inlines[1:]...)
		break
	}
	for len(inlines) > 0 {
		last := len(inlines) - 1
		if _, ok := inlines[last].(*document.LineBreak); ok {
			inlines = inlines[:last]
			continue
		}
		text, ok := inlines[last].(*document.Text)
		if !ok {
			break
		}
		trimmed := strings.TrimRight(text.Value, " ")
		if trimmed == "" {
			inlines = inlines[:last]
			continue
		}
		inlines = append(inlines[:last:last], &document.Text{Value: trimmed})
		break
	}
	return inlines
}
