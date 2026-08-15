package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"path"
	"strings"
	"time"

	"nodevas/internal/document"
)

// Word geometry. A4 minus 1" margins, in twips (1/20 pt); EMU is 1/914400".
const (
	docxContentTwips  = 9026
	docxEMUPerPixel   = 9525
	docxMaxImageWidth = docxContentTwips * 635 // twips → EMU
	docxListIndent    = 720
)

// RenderDOCX builds an OOXML WordprocessingML package (.docx). It is written
// by hand rather than through a dependency: the subset Markdown needs
// (styles, lists, tables, hyperlinks, inline images) is small and stable.
func RenderDOCX(doc *document.Doc, opts document.Options) ([]byte, error) {
	w := &docxWriter{opts: opts, nextRel: 3, nextDrawing: 1}
	w.blocks(doc.Blocks, docxContext{})
	if w.body.Len() == 0 {
		w.body.WriteString("<w:p/>")
	}
	return w.pack()
}

type docxContext struct {
	indent    int // extra left indent in twips
	listLevel int
	quote     bool
}

type docxRel struct {
	id     string
	kind   string
	target string
	mode   string
}

type docxMedia struct {
	name string
	data []byte
}

type docxNum struct {
	numID int
	level int
	start int
}

type docxWriter struct {
	opts        document.Options
	body        strings.Builder
	rels        []docxRel
	media       []docxMedia
	numbers     []docxNum
	mediaExt    map[string]string // extension → content type
	nextRel     int
	nextDrawing int
}

func (w *docxWriter) relID() string {
	id := fmt.Sprintf("rId%d", w.nextRel)
	w.nextRel++
	return id
}

func (w *docxWriter) addHyperlink(target string) string {
	target = document.SafeURL(target)
	if target == "" {
		return ""
	}
	id := w.relID()
	w.rels = append(w.rels, docxRel{
		id:     id,
		kind:   "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink",
		target: target,
		mode:   "External",
	})
	return id
}

func (w *docxWriter) addImage(asset document.Asset) string {
	extension := strings.ToLower(path.Ext(asset.Name))
	switch asset.MIME {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpeg"
	case "image/gif":
		extension = ".gif"
	}
	if extension == "" {
		extension = ".png"
	}
	name := fmt.Sprintf("image%d%s", len(w.media)+1, extension)
	w.media = append(w.media, docxMedia{name: name, data: asset.Data})
	if w.mediaExt == nil {
		w.mediaExt = map[string]string{}
	}
	w.mediaExt[strings.TrimPrefix(extension, ".")] = asset.MIME
	id := w.relID()
	w.rels = append(w.rels, docxRel{
		id:     id,
		kind:   "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image",
		target: "media/" + name,
	})
	return id
}

// allocOrderedNum gives each ordered list its own numbering instance so a
// second list starts over instead of continuing the first.
func (w *docxWriter) allocOrderedNum(level, start int) int {
	if start < 1 {
		start = 1
	}
	numID := len(w.numbers) + 2 // numId 1 is the shared bullet list
	w.numbers = append(w.numbers, docxNum{numID: numID, level: level, start: start})
	return numID
}

// ---------- blocks ----------

type paraProps struct {
	style   string
	numID   int
	level   int
	indent  int
	hanging int
	align   string
	spacing string
}

func (p paraProps) xml() string {
	var b strings.Builder
	if p.style != "" {
		fmt.Fprintf(&b, `<w:pStyle w:val="%s"/>`, p.style)
	}
	if p.numID > 0 {
		fmt.Fprintf(&b,
			`<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`,
			p.level, p.numID)
	}
	b.WriteString(p.spacing)
	if p.indent > 0 || p.hanging > 0 {
		b.WriteString(`<w:ind`)
		if p.indent > 0 {
			fmt.Fprintf(&b, ` w:left="%d"`, p.indent)
		}
		if p.hanging > 0 {
			fmt.Fprintf(&b, ` w:hanging="%d"`, p.hanging)
		}
		b.WriteString(`/>`)
	}
	if p.align != "" {
		fmt.Fprintf(&b, `<w:jc w:val="%s"/>`, p.align)
	}
	return b.String()
}

func (w *docxWriter) para(props paraProps, runs string) {
	w.body.WriteString("<w:p>")
	if xml := props.xml(); xml != "" {
		w.body.WriteString("<w:pPr>" + xml + "</w:pPr>")
	}
	w.body.WriteString(runs)
	w.body.WriteString("</w:p>")
}

func (w *docxWriter) blocks(blocks []document.Block, ctx docxContext) {
	for _, block := range blocks {
		switch node := block.(type) {
		case *document.Heading:
			level := node.Level
			if level < 1 {
				level = 1
			}
			if level > 6 {
				level = 6
			}
			w.para(paraProps{
				style:  fmt.Sprintf("Heading%d", level),
				indent: ctx.indent,
			}, w.runs(node.Inlines, runStyle{}))
		case *document.Paragraph:
			w.para(paraProps{
				style:  w.bodyStyle(ctx),
				indent: ctx.indent,
			}, w.runs(node.Inlines, runStyle{}))
		case *document.CodeBlock:
			lines := strings.Split(node.Code, "\n")
			for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
				lines = lines[:len(lines)-1]
			}
			if len(lines) == 0 {
				lines = []string{""}
			}
			for _, line := range lines {
				w.para(paraProps{
					style:  "SourceCode",
					indent: ctx.indent,
				}, w.textRun(line, runStyle{}))
			}
			w.para(paraProps{indent: ctx.indent}, "")
		case *document.BlockQuote:
			child := ctx
			child.quote = true
			child.indent = ctx.indent + 360
			w.blocks(node.Blocks, child)
		case *document.List:
			w.list(node, ctx)
		case *document.Table:
			w.table(node, ctx)
		case *document.Rule:
			w.body.WriteString(
				`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="D8DCE2"/></w:pBdr></w:pPr></w:p>`)
		}
	}
}

func (w *docxWriter) bodyStyle(ctx docxContext) string {
	if ctx.quote {
		return "Quote"
	}
	return ""
}

func (w *docxWriter) list(list *document.List, ctx docxContext) {
	numID := 1
	if list.Ordered {
		numID = w.allocOrderedNum(ctx.listLevel, list.Start)
	}
	for _, item := range list.Items {
		props := paraProps{
			style:  "ListParagraph",
			numID:  numID,
			level:  ctx.listLevel,
			indent: ctx.indent,
		}
		if item.Task {
			// Checkboxes are literal glyphs: a numbered bullet next to a box
			// reads as two markers for one item.
			props.numID = 0
			props.indent = ctx.indent + docxListIndent*(ctx.listLevel+1)
			props.hanging = 360
		}
		child := ctx
		child.listLevel = ctx.listLevel + 1
		child.indent = ctx.indent
		first := true
		for _, block := range item.Blocks {
			switch node := block.(type) {
			case *document.Paragraph:
				runs := w.runs(node.Inlines, runStyle{})
				if first && item.Task {
					mark := "☐ "
					if item.Checked {
						mark = "☑ "
					}
					runs = w.textRun(mark, runStyle{}) + runs
				}
				if first {
					w.para(props, runs)
					first = false
					continue
				}
				w.para(paraProps{
					style:  "ListParagraph",
					indent: ctx.indent + docxListIndent*(ctx.listLevel+1),
				}, runs)
			case *document.List:
				w.list(node, child)
			default:
				trailing := ctx
				trailing.indent = ctx.indent + docxListIndent*(ctx.listLevel+1)
				w.blocks([]document.Block{block}, trailing)
			}
		}
		if first {
			runs := ""
			if item.Task {
				mark := "☐ "
				if item.Checked {
					mark = "☑ "
				}
				runs = w.textRun(mark, runStyle{})
			}
			w.para(props, runs)
		}
	}
}

func (w *docxWriter) table(table *document.Table, ctx docxContext) {
	columns := len(table.Align)
	if columns == 0 {
		columns = len(table.Head)
	}
	if columns == 0 {
		for _, row := range table.Rows {
			if len(row) > columns {
				columns = len(row)
			}
		}
	}
	if columns == 0 {
		return
	}
	width := docxContentTwips / columns
	w.body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/>` +
		`<w:tblW w:w="0" w:type="auto"/>` +
		`<w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid>`)
	for i := 0; i < columns; i++ {
		fmt.Fprintf(&w.body, `<w:gridCol w:w="%d"/>`, width)
	}
	w.body.WriteString(`</w:tblGrid>`)
	if len(table.Head) > 0 {
		w.tableRow(table, table.Head, columns, width, true)
	}
	for _, row := range table.Rows {
		w.tableRow(table, row, columns, width, false)
	}
	w.body.WriteString(`</w:tbl>`)
	// Word needs a paragraph between/after tables.
	w.para(paraProps{indent: ctx.indent}, "")
}

func (w *docxWriter) tableRow(table *document.Table, cells []document.TableCell, columns, width int, header bool) {
	w.body.WriteString("<w:tr>")
	if header {
		w.body.WriteString(`<w:trPr><w:tblHeader/><w:cantSplit/></w:trPr>`)
	}
	for i := 0; i < columns; i++ {
		fmt.Fprintf(&w.body,
			`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>`, width)
		if header {
			w.body.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F2F4F7"/>`)
		}
		w.body.WriteString(`<w:vAlign w:val="top"/></w:tcPr>`)
		var inlines []document.Inline
		if i < len(cells) {
			inlines = cells[i].Inlines
		}
		props := paraProps{
			style:   "TableText",
			align:   docxAlign(document.AlignAt(table.Align, i)),
			spacing: `<w:spacing w:before="40" w:after="40"/>`,
		}
		w.body.WriteString("<w:p>")
		w.body.WriteString("<w:pPr>" + props.xml() + "</w:pPr>")
		w.body.WriteString(w.runs(inlines, runStyle{bold: header}))
		w.body.WriteString("</w:p></w:tc>")
	}
	w.body.WriteString("</w:tr>")
}

func docxAlign(align document.Align) string {
	switch align {
	case document.AlignCenter:
		return "center"
	case document.AlignRight:
		return "right"
	case document.AlignLeft:
		return "left"
	}
	return ""
}

// ---------- runs ----------

type runStyle struct {
	bold   bool
	italic bool
	strike bool
	code   bool
	link   bool
}

func (s runStyle) xml() string {
	var b strings.Builder
	switch {
	case s.code:
		b.WriteString(`<w:rStyle w:val="CodeChar"/>`)
	case s.link:
		b.WriteString(`<w:rStyle w:val="Hyperlink"/>`)
	}
	if s.bold {
		b.WriteString("<w:b/>")
	}
	if s.italic {
		b.WriteString("<w:i/>")
	}
	if s.strike {
		b.WriteString("<w:strike/>")
	}
	return b.String()
}

func (w *docxWriter) textRun(text string, style runStyle) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<w:r>")
	if rPr := style.xml(); rPr != "" {
		b.WriteString("<w:rPr>" + rPr + "</w:rPr>")
	}
	b.WriteString(`<w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r>`)
	return b.String()
}

func (w *docxWriter) runs(inlines []document.Inline, style runStyle) string {
	var b strings.Builder
	for _, inline := range inlines {
		switch node := inline.(type) {
		case *document.Text:
			b.WriteString(w.textRun(node.Value, style))
		case *document.CodeSpan:
			child := style
			child.code = true
			b.WriteString(w.textRun(node.Value, child))
		case *document.Strong:
			child := style
			child.bold = true
			b.WriteString(w.runs(node.Kids, child))
		case *document.Emphasis:
			child := style
			child.italic = true
			b.WriteString(w.runs(node.Kids, child))
		case *document.Strike:
			child := style
			child.strike = true
			b.WriteString(w.runs(node.Kids, child))
		case *document.Link:
			child := style
			child.link = true
			inner := w.runs(node.Kids, child)
			if inner == "" {
				inner = w.textRun(node.URL, child)
			}
			if id := w.addHyperlink(node.URL); id != "" {
				fmt.Fprintf(&b, `<w:hyperlink r:id="%s" w:history="1">%s</w:hyperlink>`, id, inner)
			} else {
				b.WriteString(inner)
			}
		case *document.Image:
			b.WriteString(w.image(node, style))
		case *document.LineBreak:
			if node.Hard {
				b.WriteString("<w:r><w:br/></w:r>")
			} else {
				b.WriteString(w.textRun(" ", style))
			}
		}
	}
	return b.String()
}

func (w *docxWriter) image(node *document.Image, style runStyle) string {
	asset, ok := w.opts.Resolve(node.URL)
	if !ok || asset.Width <= 0 || asset.Height <= 0 || len(asset.Data) == 0 {
		label := node.Alt
		if label == "" {
			label = node.URL
		}
		child := style
		child.italic = true
		return w.runs([]document.Inline{&document.Link{
			URL:  node.URL,
			Kids: []document.Inline{&document.Text{Value: label}},
		}}, child)
	}
	id := w.addImage(asset)
	cx := asset.Width * docxEMUPerPixel
	cy := asset.Height * docxEMUPerPixel
	if cx > docxMaxImageWidth {
		cy = int(float64(cy) * float64(docxMaxImageWidth) / float64(cx))
		cx = docxMaxImageWidth
	}
	drawing := w.nextDrawing
	w.nextDrawing++
	name := xmlEscape(asset.Name)
	description := xmlEscape(node.Alt)
	return fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
		`<wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/>`+
		`<wp:docPr id="%d" name="Picture %d" descr="%s"/>`+
		`<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>`+
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
		`<pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="%s" descr="%s"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
		`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic>`+
		`</a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
		cx, cy, drawing, drawing, description, drawing, name, description, id, cx, cy)
}

// xmlEscape escapes XML metacharacters and drops the control characters that
// XML 1.0 forbids, which would otherwise make Word refuse the file.
func xmlEscape(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\t', '\n':
			b.WriteRune(' ')
		default:
			if r < 0x20 || r == 0xFFFE || r == 0xFFFF {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------- package ----------

func (w *docxWriter) pack() ([]byte, error) {
	buffer := new(bytes.Buffer)
	archive := zip.NewWriter(buffer)
	modified := w.opts.ModifiedOrNow()
	add := func(name string, data []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = modified
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	parts := []struct {
		name string
		data []byte
	}{
		{"[Content_Types].xml", []byte(w.contentTypes())},
		{"_rels/.rels", []byte(docxRootRels)},
		{"docProps/core.xml", []byte(w.coreProps(modified))},
		{"docProps/app.xml", []byte(docxAppProps)},
		{"word/document.xml", []byte(w.document())},
		{"word/styles.xml", []byte(docxStyles)},
		{"word/numbering.xml", []byte(w.numbering())},
		{"word/_rels/document.xml.rels", []byte(w.documentRels())},
	}
	for _, part := range parts {
		if err := add(part.name, part.data); err != nil {
			return nil, err
		}
	}
	for _, media := range w.media {
		if err := add("word/media/"+media.name, media.data); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (w *docxWriter) contentTypes() string {
	var b strings.Builder
	b.WriteString(xmlHeader +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>`)
	for extension, mime := range w.mediaExt {
		fmt.Fprintf(&b, `<Default Extension="%s" ContentType="%s"/>`, extension, mime)
	}
	b.WriteString(
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
			`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
			`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
			`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
			`</Types>`)
	return b.String()
}

func (w *docxWriter) documentRels() string {
	var b strings.Builder
	b.WriteString(xmlHeader +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>`)
	for _, rel := range w.rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"`,
			rel.id, rel.kind, xmlEscape(rel.target))
		if rel.mode != "" {
			fmt.Fprintf(&b, ` TargetMode="%s"`, rel.mode)
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func (w *docxWriter) coreProps(modified time.Time) string {
	stamp := modified.UTC().Format("2006-01-02T15:04:05Z")
	title := xmlEscape(w.opts.Title)
	return xmlHeader +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"` +
		` xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/"` +
		` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + title + `</dc:title>` +
		`<dc:creator>Nodevas</dc:creator>` +
		`<cp:lastModifiedBy>Nodevas</cp:lastModifiedBy>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:modified>` +
		`</cp:coreProperties>`
}

func (w *docxWriter) document() string {
	return xmlHeader +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"` +
		` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"` +
		` xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"` +
		` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"` +
		` xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		`<w:body>` + w.body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"` +
		` w:header="708" w:footer="708" w:gutter="0"/></w:sectPr>` +
		`</w:body></w:document>`
}

func (w *docxWriter) numbering() string {
	var b strings.Builder
	b.WriteString(xmlHeader +
		`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	bullets := []string{"•", "◦", "▪"}
	b.WriteString(`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>`)
	for level := 0; level < 9; level++ {
		fmt.Fprintf(&b,
			`<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="bullet"/>`+
				`<w:lvlText w:val="%s"/><w:lvlJc w:val="left"/>`+
				`<w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr>`+
				`<w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:hint="default"/></w:rPr></w:lvl>`,
			level, bullets[level%len(bullets)], docxListIndent*(level+1))
	}
	b.WriteString(`</w:abstractNum>`)
	formats := []string{"decimal", "lowerLetter", "lowerRoman"}
	b.WriteString(`<w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="hybridMultilevel"/>`)
	for level := 0; level < 9; level++ {
		fmt.Fprintf(&b,
			`<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="%s"/>`+
				`<w:lvlText w:val="%%%d."/><w:lvlJc w:val="left"/>`+
				`<w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`,
			level, formats[level%len(formats)], level+1, docxListIndent*(level+1))
	}
	b.WriteString(`</w:abstractNum>`)
	b.WriteString(`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	for _, number := range w.numbers {
		fmt.Fprintf(&b, `<w:num w:numId="%d"><w:abstractNumId w:val="1"/>`, number.numID)
		for level := 0; level <= number.level; level++ {
			start := 1
			if level == number.level {
				start = number.start
			}
			fmt.Fprintf(&b,
				`<w:lvlOverride w:ilvl="%d"><w:startOverride w:val="%d"/></w:lvlOverride>`,
				level, start)
		}
		b.WriteString(`</w:num>`)
	}
	b.WriteString(`</w:numbering>`)
	return b.String()
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

const docxRootRels = xmlHeader +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
	`</Relationships>`

const docxAppProps = xmlHeader +
	`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"` +
	` xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
	`<Application>Nodevas</Application><DocSecurity>0</DocSecurity>` +
	`</Properties>`

const docxStyles = xmlHeader +
	`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr>` +
	`<w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="Microsoft JhengHei" w:cs="Calibri"/>` +
	`<w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:rPrDefault>` +
	`<w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault>` +
	`</w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="360" w:after="180"/><w:outlineLvl w:val="0"/></w:pPr>` +
	`<w:rPr><w:b/><w:sz w:val="40"/><w:szCs w:val="40"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="320" w:after="160"/><w:outlineLvl w:val="1"/></w:pPr>` +
	`<w:rPr><w:b/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="280" w:after="140"/><w:outlineLvl w:val="2"/></w:pPr>` +
	`<w:rPr><w:b/><w:sz w:val="27"/><w:szCs w:val="27"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="3"/></w:pPr>` +
	`<w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading5"><w:name w:val="heading 5"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="220" w:after="120"/><w:outlineLvl w:val="4"/></w:pPr>` +
	`<w:rPr><w:b/><w:i/><w:sz w:val="23"/><w:szCs w:val="23"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading6"><w:name w:val="heading 6"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>` +
	`<w:pPr><w:keepNext/><w:spacing w:before="200" w:after="120"/><w:outlineLvl w:val="5"/></w:pPr>` +
	`<w:rPr><w:b/><w:color w:val="4A4F57"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/>` +
	`<w:next w:val="Normal"/><w:qFormat/>` +
	`<w:pPr><w:pBdr><w:left w:val="single" w:sz="12" w:space="8" w:color="C9CED6"/></w:pBdr>` +
	`<w:ind w:left="360"/></w:pPr>` +
	`<w:rPr><w:i/><w:color w:val="4A4F57"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="SourceCode"><w:name w:val="Source Code"/><w:basedOn w:val="Normal"/>` +
	`<w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F4F5F7"/>` +
	`<w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr>` +
	`<w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/><w:sz w:val="19"/><w:szCs w:val="19"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/>` +
	`<w:uiPriority w:val="34"/><w:qFormat/>` +
	`<w:pPr><w:spacing w:after="80"/><w:contextualSpacing/></w:pPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="TableText"><w:name w:val="Table Text"/><w:basedOn w:val="Normal"/>` +
	`<w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr></w:style>` +
	`<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:uiPriority w:val="99"/>` +
	`<w:rPr><w:color w:val="1A56C4"/><w:u w:val="single"/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="CodeChar"><w:name w:val="Code Char"/>` +
	`<w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/><w:sz w:val="19"/>` +
	`<w:shd w:val="clear" w:color="auto" w:fill="F1F3F6"/></w:rPr></w:style>` +
	`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:uiPriority w:val="39"/>` +
	`<w:tblPr><w:tblBorders>` +
	`<w:top w:val="single" w:sz="4" w:space="0" w:color="CCD1D8"/>` +
	`<w:left w:val="single" w:sz="4" w:space="0" w:color="CCD1D8"/>` +
	`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="CCD1D8"/>` +
	`<w:right w:val="single" w:sz="4" w:space="0" w:color="CCD1D8"/>` +
	`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="CCD1D8"/>` +
	`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="CCD1D8"/>` +
	`</w:tblBorders><w:tblCellMar>` +
	`<w:top w:w="60" w:type="dxa"/><w:left w:w="108" w:type="dxa"/>` +
	`<w:bottom w:w="60" w:type="dxa"/><w:right w:w="108" w:type="dxa"/>` +
	`</w:tblCellMar></w:tblPr></w:style>` +
	`</w:styles>`
