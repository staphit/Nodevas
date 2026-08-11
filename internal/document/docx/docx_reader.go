package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"nodevas/internal/document"
)

// MediaSink stores an image pulled out of a document and returns the URL the
// Markdown should point at. Returning an error drops the image to its alt
// text rather than failing the whole conversion.
type MediaSink func(name string, data []byte) (string, error)

const (
	maxDOCXEntries                = 2048
	maxDOCXTotalUncompressedBytes = 128 << 20
	maxDOCXPartBytes              = 64 << 20
	maxDOCXImageBytes             = 16 << 20
	maxDOCXImages                 = 256
)

// ReadDOCX converts a Word document into the block model. It reads the subset
// this package can also write — headings, lists, tables, quotes, code,
// emphasis, links and inline images — and skips everything else instead of
// failing, so an unexpected part never costs the user their text.
func ReadDOCX(data []byte, sink MediaSink) (*document.Doc, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a readable .docx: %w", err)
	}
	if err := validateDOCXArchive(archive.File); err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	for _, file := range archive.File {
		files[path.Clean(file.Name)] = file
	}
	main, ok := files["word/document.xml"]
	if !ok {
		return nil, errors.New("not a Word document: word/document.xml is missing")
	}
	body, err := readZipEntry(main)
	if err != nil {
		return nil, err
	}
	reader := &docxReader{
		relations: map[string]docxRelation{},
		numbering: map[int]docxNumbering{},
		files:     files,
		sink:      sink,
		imageData: map[string][]byte{},
	}
	if rels, ok := files["word/_rels/document.xml.rels"]; ok {
		if raw, err := readZipEntry(rels); err == nil {
			reader.relations = parseDOCXRelations(raw)
		}
	}
	if numbering, ok := files["word/numbering.xml"]; ok {
		if raw, err := readZipEntry(numbering); err == nil {
			reader.numbering = parseDOCXNumbering(raw)
		}
	}
	return reader.parse(body)
}

func validateDOCXArchive(files []*zip.File) error {
	if len(files) > maxDOCXEntries {
		return fmt.Errorf("DOCX has too many entries: %d (limit %d)", len(files), maxDOCXEntries)
	}
	var total uint64
	images := 0
	for _, file := range files {
		size := file.UncompressedSize64
		if size > maxDOCXPartBytes {
			return fmt.Errorf("%s is too large", file.Name)
		}
		if size > uint64(maxDOCXTotalUncompressedBytes)-total {
			return fmt.Errorf("DOCX expands beyond %d bytes", maxDOCXTotalUncompressedBytes)
		}
		total += size

		clean := path.Clean(file.Name)
		if strings.HasPrefix(clean, "word/media/") && clean != "word/media" {
			images++
			if images > maxDOCXImages {
				return fmt.Errorf("DOCX has too many images: %d (limit %d)", images, maxDOCXImages)
			}
			if size > maxDOCXImageBytes {
				return fmt.Errorf("%s image is too large", file.Name)
			}
		}
	}
	return nil
}

func readZipEntry(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > maxDOCXPartBytes {
		return nil, fmt.Errorf("%s is too large", file.Name)
	}
	entry, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer entry.Close()
	data, err := io.ReadAll(io.LimitReader(entry, maxDOCXPartBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDOCXPartBytes {
		return nil, fmt.Errorf("%s is too large", file.Name)
	}
	return data, nil
}

type docxRelation struct {
	target   string
	external bool
}

type docxNumbering struct {
	formats map[int]string
	starts  map[int]int
}

type docxReader struct {
	relations map[string]docxRelation
	numbering map[int]docxNumbering
	files     map[string]*zip.File
	sink      MediaSink
	imageSeq  int
	imageData map[string][]byte
}

// ---------- side parts ----------

func parseDOCXRelations(raw []byte) map[string]docxRelation {
	out := map[string]docxRelation{}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if err != nil {
			return out
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		var relation docxRelation
		id := ""
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "Id":
				id = attr.Value
			case "Target":
				relation.target = attr.Value
			case "TargetMode":
				relation.external = strings.EqualFold(attr.Value, "External")
			}
		}
		if id != "" {
			out[id] = relation
		}
	}
}

// parseDOCXNumbering maps each numbering instance to the list format of every
// level, so the reader knows bullet from numbered.
func parseDOCXNumbering(raw []byte) map[int]docxNumbering {
	abstract := map[int]docxNumbering{}
	instances := map[int]int{} // numId → abstractNumId
	overrides := map[int]map[int]int{}

	decoder := xml.NewDecoder(bytes.NewReader(raw))
	currentAbstract, currentNum, currentLevel := -1, -1, -1
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "abstractNum":
				currentAbstract = attrInt(element, "abstractNumId", -1)
				if currentAbstract >= 0 {
					abstract[currentAbstract] = docxNumbering{
						formats: map[int]string{},
						starts:  map[int]int{},
					}
				}
			case "num":
				currentNum = attrInt(element, "numId", -1)
			case "abstractNumId":
				if currentNum >= 0 {
					instances[currentNum] = attrInt(element, "val", -1)
				}
			case "lvl":
				currentLevel = attrInt(element, "ilvl", -1)
			case "lvlOverride":
				currentLevel = attrInt(element, "ilvl", -1)
			case "numFmt":
				if currentAbstract >= 0 && currentLevel >= 0 {
					abstract[currentAbstract].formats[currentLevel] = attrString(element, "val")
				}
			case "start":
				if currentAbstract >= 0 && currentLevel >= 0 {
					abstract[currentAbstract].starts[currentLevel] = attrInt(element, "val", 1)
				}
			case "startOverride":
				if currentNum >= 0 && currentLevel >= 0 {
					if overrides[currentNum] == nil {
						overrides[currentNum] = map[int]int{}
					}
					overrides[currentNum][currentLevel] = attrInt(element, "val", 1)
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "abstractNum":
				currentAbstract = -1
			case "num":
				currentNum = -1
			case "lvl", "lvlOverride":
				currentLevel = -1
			}
		}
	}

	out := map[int]docxNumbering{}
	for numID, abstractID := range instances {
		source, ok := abstract[abstractID]
		if !ok {
			source = docxNumbering{formats: map[int]string{}, starts: map[int]int{}}
		}
		entry := docxNumbering{formats: map[int]string{}, starts: map[int]int{}}
		for level, format := range source.formats {
			entry.formats[level] = format
		}
		for level, start := range source.starts {
			entry.starts[level] = start
		}
		for level, start := range overrides[numID] {
			entry.starts[level] = start
		}
		out[numID] = entry
	}
	return out
}

func (r *docxReader) ordered(numID, level int) bool {
	entry, ok := r.numbering[numID]
	if !ok {
		return false
	}
	format, ok := entry.formats[level]
	if !ok {
		return false
	}
	return format != "bullet" && format != "none"
}

func (r *docxReader) listStart(numID, level int) int {
	if entry, ok := r.numbering[numID]; ok {
		if start, ok := entry.starts[level]; ok && start > 0 {
			return start
		}
	}
	return 1
}

// ---------- document body ----------

// docxPara is one w:p reduced to what the block model cares about.
type docxPara struct {
	style   string
	numID   int // -1 when the paragraph is not part of a list
	level   int
	indent  int
	align   document.Align
	inlines []document.Inline
}

func (r *docxReader) parse(raw []byte) (*document.Doc, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var body []bodyItem
	inBody := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read document.xml: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "body":
			inBody = true
		case "p":
			if !inBody {
				continue
			}
			paragraph, err := r.readParagraph(decoder)
			if err != nil {
				return nil, err
			}
			body = append(body, paragraphBlock(paragraph))
		case "tbl":
			if !inBody {
				continue
			}
			table, err := r.readTable(decoder)
			if err != nil {
				return nil, err
			}
			if table != nil {
				body = append(body, table)
			}
		}
	}
	return &document.Doc{Blocks: r.assemble(body)}, nil
}

// bodyItem is what the body stream holds while it is being read: either a
// finished document.Block or a paragraph still carrying its docx list and
// style metadata. assemble() folds the paragraphs into lists and code blocks
// and returns real blocks only, which is why this stays a docx-local type —
// document.Block is sealed on purpose.
type bodyItem any

// paragraphBlock keeps the paragraph's list/style metadata alongside its
// content until assemble() folds runs of them into lists and code blocks.
type docxParaBlock struct {
	para docxPara
}

func paragraphBlock(para docxPara) bodyItem { return &docxParaBlock{para: para} }

func (r *docxReader) readParagraph(decoder *xml.Decoder) (docxPara, error) {
	para := docxPara{numID: -1}
	builder := &inlineBuilder{}
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return para, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "p", "tbl":
				depth++
			case "pStyle":
				para.style = attrString(element, "val")
			case "numId":
				para.numID = attrInt(element, "val", -1)
			case "ilvl":
				para.level = attrInt(element, "val", 0)
			case "ind":
				para.indent = attrInt(element, "left", 0)
			case "jc":
				para.align = docxAlignOf(attrString(element, "val"))
			case "r":
				if err := r.readRun(decoder, builder, runStyle{}); err != nil {
					return para, err
				}
			case "hyperlink":
				if err := r.readHyperlink(decoder, builder, element); err != nil {
					return para, err
				}
			case "del":
				// Tracked deletion: the text is gone, skip the subtree.
				if err := skipElement(decoder); err != nil {
					return para, err
				}
			}
		case xml.EndElement:
			if element.Name.Local == "p" || element.Name.Local == "tbl" {
				depth--
			}
		}
	}
	builder.flush()
	para.inlines = builder.out
	return para, nil
}

func (r *docxReader) readHyperlink(
	decoder *xml.Decoder,
	builder *inlineBuilder,
	element xml.StartElement,
) error {
	target := ""
	for _, attr := range element.Attr {
		if attr.Name.Local == "id" {
			if relation, ok := r.relations[attr.Value]; ok {
				target = relation.target
			}
		}
		if attr.Name.Local == "anchor" && target == "" {
			target = "#" + attr.Value
		}
	}
	inner := &inlineBuilder{}
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch node := token.(type) {
		case xml.StartElement:
			switch node.Name.Local {
			case "hyperlink":
				depth++
			case "r":
				if err := r.readRun(decoder, inner, runStyle{}); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if node.Name.Local == "hyperlink" {
				depth--
			}
		}
	}
	inner.flush()
	if len(inner.out) == 0 {
		return nil
	}
	if target == "" {
		for _, node := range inner.out {
			builder.node(node)
		}
		return nil
	}
	builder.node(&document.Link{URL: target, Kids: inner.out})
	return nil
}

func (r *docxReader) readRun(decoder *xml.Decoder, builder *inlineBuilder, style runStyle) error {
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "r":
				depth++
			case "b":
				style.bold = attrFlag(element)
			case "i":
				style.italic = attrFlag(element)
			case "strike", "dstrike":
				style.strike = attrFlag(element)
			case "rStyle":
				switch attrString(element, "val") {
				case "CodeChar", "Code", "HTMLCode", "VerbatimChar":
					style.code = true
				}
			case "rFonts":
				if isMonospaceFont(attrString(element, "ascii")) {
					style.code = true
				}
			case "t":
				text, err := elementText(decoder)
				if err != nil {
					return err
				}
				builder.text(style, text)
			case "tab":
				builder.text(style, "    ")
			case "br":
				builder.node(&document.LineBreak{Hard: true})
			case "drawing", "pict":
				image, err := r.readDrawing(decoder, element.Name.Local)
				if err != nil {
					return err
				}
				if image != nil {
					builder.node(image)
				}
			}
		case xml.EndElement:
			if element.Name.Local == "r" {
				depth--
			}
		}
	}
	return nil
}

func isMonospaceFont(name string) bool {
	switch strings.ToLower(name) {
	case "consolas", "courier new", "cascadia mono", "cascadia code", "menlo", "monaco":
		return true
	}
	return false
}

// readDrawing pulls the embedded image out of the package and hands it to the
// sink; without a sink (or on failure) the image degrades to its alt text.
func (r *docxReader) readDrawing(decoder *xml.Decoder, closing string) (*document.Image, error) {
	relationID, alt := "", ""
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local == closing {
				depth++
			}
			switch element.Name.Local {
			case "blip":
				for _, attr := range element.Attr {
					if attr.Name.Local == "embed" || attr.Name.Local == "link" {
						relationID = attr.Value
					}
				}
			case "docPr", "cNvPr":
				if description := attrString(element, "descr"); description != "" && alt == "" {
					alt = description
				}
			case "imagedata":
				for _, attr := range element.Attr {
					if attr.Name.Local == "id" {
						relationID = attr.Value
					}
				}
			}
		case xml.EndElement:
			if element.Name.Local == closing {
				depth--
			}
		}
	}
	if relationID == "" {
		return nil, nil
	}
	relation, ok := r.relations[relationID]
	if !ok {
		return nil, nil
	}
	if relation.external {
		return &document.Image{URL: relation.target, Alt: alt}, nil
	}
	if r.sink == nil {
		return nil, nil
	}
	if r.imageSeq >= maxDOCXImages {
		return nil, fmt.Errorf("DOCX references too many images")
	}
	name := path.Base(relation.target)
	fileName := path.Clean("word/" + relation.target)
	file, ok := r.files[fileName]
	if !ok {
		return nil, nil
	}
	if file.UncompressedSize64 > maxDOCXImageBytes {
		return nil, fmt.Errorf("%s image is too large", file.Name)
	}
	data, ok := r.imageData[fileName]
	if !ok {
		var err error
		data, err = readZipEntry(file)
		if err != nil {
			return nil, err
		}
		if len(data) > maxDOCXImageBytes {
			return nil, fmt.Errorf("%s image is too large", file.Name)
		}
		r.imageData[fileName] = data
	}
	r.imageSeq++
	url, err := r.sink(name, data)
	if err != nil || url == "" {
		return nil, nil
	}
	if alt == "" {
		alt = strings.TrimSuffix(name, path.Ext(name))
	}
	return &document.Image{URL: url, Alt: alt}, nil
}

func (r *docxReader) readTable(decoder *xml.Decoder) (*document.Table, error) {
	table := &document.Table{}
	var rows [][]document.TableCell
	headerRow := -1
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "tbl":
				depth++
			case "tr":
				cells, header, aligns, err := r.readTableRow(decoder)
				if err != nil {
					return nil, err
				}
				if header && headerRow < 0 {
					headerRow = len(rows)
				}
				if len(aligns) > len(table.Align) {
					table.Align = aligns
				}
				rows = append(rows, cells)
			}
		case xml.EndElement:
			if element.Name.Local == "tbl" {
				depth--
			}
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if headerRow < 0 {
		headerRow = 0
	}
	table.Head = unboldRow(rows[headerRow])
	table.Rows = append(rows[:headerRow:headerRow], rows[headerRow+1:]...)
	if len(table.Align) < len(table.Head) {
		table.Align = fitAlignments(table.Align, len(table.Head))
	}
	return table, nil
}

func (r *docxReader) readTableRow(decoder *xml.Decoder) ([]document.TableCell, bool, []document.Align, error) {
	var cells []document.TableCell
	var aligns []document.Align
	header := false
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return nil, false, nil, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "tr":
				depth++
			case "tblHeader":
				header = true
			case "tc":
				cell, align, err := r.readTableCell(decoder)
				if err != nil {
					return nil, false, nil, err
				}
				cells = append(cells, cell)
				aligns = append(aligns, align)
			}
		case xml.EndElement:
			if element.Name.Local == "tr" {
				depth--
			}
		}
	}
	return cells, header, aligns, nil
}

func (r *docxReader) readTableCell(decoder *xml.Decoder) (document.TableCell, document.Align, error) {
	cell := document.TableCell{}
	align := document.AlignDefault
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return cell, align, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "tc":
				depth++
			case "p":
				paragraph, err := r.readParagraph(decoder)
				if err != nil {
					return cell, align, err
				}
				if paragraph.align != document.AlignDefault && align == document.AlignDefault {
					align = paragraph.align
				}
				if len(paragraph.inlines) == 0 {
					continue
				}
				if len(cell.Inlines) > 0 {
					cell.Inlines = append(cell.Inlines, &document.LineBreak{Hard: true})
				}
				cell.Inlines = append(cell.Inlines, paragraph.inlines...)
			case "tbl":
				// A nested table flattens into the cell's text.
				nested, err := r.readTable(decoder)
				if err != nil {
					return cell, align, err
				}
				if nested != nil {
					for _, row := range append([][]document.TableCell{nested.Head}, nested.Rows...) {
						for _, nestedCell := range row {
							cell.Inlines = append(cell.Inlines, nestedCell.Inlines...)
						}
					}
				}
			}
		case xml.EndElement:
			if element.Name.Local == "tc" {
				depth--
			}
		}
	}
	return cell, align, nil
}

// unboldRow drops the bold that a header row carries by convention — a
// Markdown table already renders its header strong.
func unboldRow(cells []document.TableCell) []document.TableCell {
	out := make([]document.TableCell, len(cells))
	for index, cell := range cells {
		allStrong := len(cell.Inlines) > 0
		for _, inline := range cell.Inlines {
			switch inline.(type) {
			case *document.Strong, *document.LineBreak:
			default:
				allStrong = false
			}
		}
		if !allStrong {
			out[index] = cell
			continue
		}
		var inlines []document.Inline
		for _, inline := range cell.Inlines {
			if strong, ok := inline.(*document.Strong); ok {
				inlines = append(inlines, strong.Kids...)
				continue
			}
			inlines = append(inlines, inline)
		}
		out[index] = document.TableCell{Inlines: inlines}
	}
	return out
}

func fitAlignments(aligns []document.Align, columns int) []document.Align {
	out := make([]document.Align, columns)
	copy(out, aligns)
	return out
}

// ---------- assembly ----------

// assemble folds the flat paragraph stream into headings, code blocks,
// quotes and (nested) lists.
func (r *docxReader) assemble(blocks []bodyItem) []document.Block {
	var out []document.Block
	var code []string
	var quote []document.Block
	stack := newListStack()

	flushCode := func() {
		if len(code) == 0 {
			return
		}
		out = append(out, &document.CodeBlock{Code: strings.Join(code, "\n")})
		code = nil
	}
	flushQuote := func() {
		if len(quote) == 0 {
			return
		}
		out = append(out, &document.BlockQuote{Blocks: quote})
		quote = nil
	}
	flushLists := func() {
		out = append(out, stack.close()...)
	}

	for _, block := range blocks {
		wrapped, ok := block.(*docxParaBlock)
		if !ok {
			flushCode()
			flushQuote()
			flushLists()
			out = append(out, block.(document.Block))
			continue
		}
		para := wrapped.para
		style := para.style
		switch {
		case isCodeStyle(style):
			flushQuote()
			flushLists()
			code = append(code, document.PlainText(para.inlines))
			continue
		case isQuoteStyle(style):
			flushCode()
			flushLists()
			if len(para.inlines) > 0 {
				quote = append(quote, &document.Paragraph{Inlines: para.inlines})
			}
			continue
		}
		flushCode()
		flushQuote()

		if level := headingLevel(style); level > 0 {
			flushLists()
			if len(para.inlines) == 0 {
				continue
			}
			out = append(out, &document.Heading{Level: level, Inlines: para.inlines})
			continue
		}
		if task, checked, inlines, ok := taskItem(para); ok {
			stack.push(r, -1, task, &document.ListItem{Task: true, Checked: checked,
				Blocks: []document.Block{&document.Paragraph{Inlines: inlines}}})
			out = append(out, stack.drain()...)
			continue
		}
		if para.numID >= 0 {
			stack.push(r, para.numID, para.level, &document.ListItem{
				Blocks: []document.Block{&document.Paragraph{Inlines: para.inlines}},
			})
			out = append(out, stack.drain()...)
			continue
		}
		flushLists()
		if len(para.inlines) == 0 {
			continue
		}
		out = append(out, &document.Paragraph{Inlines: para.inlines})
	}
	flushCode()
	flushQuote()
	flushLists()
	return out
}

func headingLevel(style string) int {
	lowered := strings.ToLower(style)
	for _, prefix := range []string{"heading", "berschrift"} { // de: Überschrift
		if rest, ok := strings.CutPrefix(lowered, prefix); ok {
			if level, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && level >= 1 && level <= 6 {
				return level
			}
		}
	}
	if lowered == "title" {
		return 1
	}
	return 0
}

func isCodeStyle(style string) bool {
	switch style {
	case "SourceCode", "Code", "HTMLPreformatted", "PlainText", "Preformatted":
		return true
	}
	return false
}

func isQuoteStyle(style string) bool {
	switch style {
	case "Quote", "IntenseQuote", "BlockQuote", "BlockText":
		return true
	}
	return false
}

// taskItem recognises the "☐ " / "☑ " prefix this package writes for task
// lists, plus the "[ ]" spelling a user may have typed.
func taskItem(para docxPara) (level int, checked bool, inlines []document.Inline, ok bool) {
	if para.numID >= 0 || len(para.inlines) == 0 {
		return 0, false, nil, false
	}
	text := document.PlainText(para.inlines)
	var prefix string
	switch {
	case strings.HasPrefix(text, "☐ "), strings.HasPrefix(text, "[ ] "):
		prefix, checked = strings.SplitAfterN(text, " ", 2)[0], false
	case strings.HasPrefix(text, "☑ "), strings.HasPrefix(text, "☒ "),
		strings.HasPrefix(text, "[x] "), strings.HasPrefix(text, "[X] "):
		prefix, checked = strings.SplitAfterN(text, " ", 2)[0], true
	default:
		return 0, false, nil, false
	}
	level = max(para.indent/docxListIndent-1, 0)
	return level, checked, trimInlinePrefix(para.inlines, len(prefix)), true
}

// trimInlinePrefix drops the first n bytes of text from the inline sequence.
func trimInlinePrefix(inlines []document.Inline, n int) []document.Inline {
	out := make([]document.Inline, 0, len(inlines))
	for _, inline := range inlines {
		if n <= 0 {
			out = append(out, inline)
			continue
		}
		text, ok := inline.(*document.Text)
		if !ok {
			out = append(out, inline)
			continue
		}
		if len(text.Value) <= n {
			n -= len(text.Value)
			continue
		}
		out = append(out, &document.Text{Value: text.Value[n:]})
		n = 0
	}
	return out
}

// listStack turns the flat (numId, level) stream into nested lists. Lists
// that finish while another one is still open land in done, in the order they
// closed.
type listStack struct {
	lists  []*document.List
	levels []int
	nums   []int
	done   []document.Block
}

func newListStack() *listStack { return &listStack{} }

// drain returns the lists closed since the last call.
func (s *listStack) drain() []document.Block {
	out := s.done
	s.done = nil
	return out
}

func (s *listStack) push(reader *docxReader, numID, level int, item *document.ListItem) {
	for len(s.lists) > 0 && s.levels[len(s.lists)-1] > level {
		s.pop()
	}
	if len(s.lists) > 0 &&
		s.levels[len(s.lists)-1] == level &&
		s.nums[len(s.lists)-1] != numID {
		s.pop()
	}
	if len(s.lists) == 0 || s.levels[len(s.lists)-1] < level {
		list := &document.List{
			Ordered: numID >= 0 && reader.ordered(numID, level),
			Start:   1,
		}
		if list.Ordered {
			list.Start = reader.listStart(numID, level)
		}
		s.lists = append(s.lists, list)
		s.levels = append(s.levels, level)
		s.nums = append(s.nums, numID)
	}
	current := s.lists[len(s.lists)-1]
	current.Items = append(current.Items, item)
}

// pop closes the innermost list: a nested one hangs under its parent's last
// item, a top-level one becomes a finished block.
func (s *listStack) pop() {
	if len(s.lists) == 0 {
		return
	}
	last := s.lists[len(s.lists)-1]
	s.lists = s.lists[:len(s.lists)-1]
	s.levels = s.levels[:len(s.levels)-1]
	s.nums = s.nums[:len(s.nums)-1]
	if len(s.lists) == 0 {
		s.done = append(s.done, last)
		return
	}
	parent := s.lists[len(s.lists)-1]
	if len(parent.Items) == 0 {
		parent.Items = append(parent.Items, &document.ListItem{})
	}
	item := parent.Items[len(parent.Items)-1]
	item.Blocks = append(item.Blocks, last)
}

// close unwinds every open list and returns everything finished so far.
func (s *listStack) close() []document.Block {
	for len(s.lists) > 0 {
		s.pop()
	}
	return s.drain()
}

// ---------- inline assembly ----------

// inlineBuilder merges neighbouring runs that share formatting, so "**a**"
// does not come back as "**a****b**".
type inlineBuilder struct {
	out     []document.Inline
	style   runStyle
	buf     strings.Builder
	started bool
}

func (b *inlineBuilder) text(style runStyle, value string) {
	if value == "" {
		return
	}
	if b.started && style != b.style {
		b.flush()
	}
	if !b.started {
		b.style = style
		b.started = true
	}
	b.buf.WriteString(value)
}

func (b *inlineBuilder) node(node document.Inline) {
	b.flush()
	b.out = append(b.out, node)
}

func (b *inlineBuilder) flush() {
	if b.buf.Len() > 0 {
		b.out = append(b.out, styledInline(b.style, b.buf.String()))
	}
	b.buf.Reset()
	b.started = false
}

func styledInline(style runStyle, text string) document.Inline {
	if style.code {
		return &document.CodeSpan{Value: text}
	}
	var node document.Inline = &document.Text{Value: text}
	if style.strike {
		node = &document.Strike{Kids: []document.Inline{node}}
	}
	if style.italic {
		node = &document.Emphasis{Kids: []document.Inline{node}}
	}
	if style.bold {
		node = &document.Strong{Kids: []document.Inline{node}}
	}
	return node
}

// ---------- xml helpers ----------

func attrString(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func attrInt(element xml.StartElement, name string, fallback int) int {
	value := attrString(element, name)
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return number
}

// attrFlag reads a toggle property: present means on unless w:val says off.
func attrFlag(element xml.StartElement) bool {
	switch attrString(element, "val") {
	case "", "1", "true", "on":
		return true
	}
	return false
}

func docxAlignOf(value string) document.Align {
	switch value {
	case "center":
		return document.AlignCenter
	case "right", "end":
		return document.AlignRight
	case "left", "start", "both":
		return document.AlignLeft
	}
	return document.AlignDefault
}

// elementText reads the character data of the element that just started.
func elementText(decoder *xml.Decoder) (string, error) {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return b.String(), err
		}
		switch node := token.(type) {
		case xml.CharData:
			b.Write(node)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return b.String(), nil
}

func skipElement(decoder *xml.Decoder) error {
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}
