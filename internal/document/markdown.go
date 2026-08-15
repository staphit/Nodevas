package document

import (
	"regexp"
	"strconv"
	"strings"
)

// Parse converts Markdown into the block model. It covers the CommonMark +
// GFM subset the editor can produce: headings, lists (including tasks),
// quotes, fenced code, pipe tables, thematic breaks, and the usual inline
// emphasis, code, links and images. Anything it does not recognise survives
// as literal text rather than being dropped.
func Parse(source string) *Doc {
	return &Doc{Blocks: parseBlocks(splitLines(source))}
}

func splitLines(source string) []string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")
	source = strings.ReplaceAll(source, "\t", "    ")
	return strings.Split(source, "\n")
}

var (
	atxPattern       = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ ]+(.*))?$`)
	rulePattern      = regexp.MustCompile(`^ {0,3}(?:(?:\*[ ]*){3,}|(?:-[ ]*){3,}|(?:_[ ]*){3,})$`)
	fencePattern     = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})[ ]*(.*)$")
	bulletPattern    = regexp.MustCompile(`^( *)([-*+])(?:([ ]+)(.*))?$`)
	orderedPattern   = regexp.MustCompile(`^( *)(\d{1,9})([.)])(?:([ ]+)(.*))?$`)
	taskPattern      = regexp.MustCompile(`^\[([ xX])\](?:[ ]+(.*))?$`)
	setextPattern    = regexp.MustCompile(`^ {0,3}(=+|-{2,})[ ]*$`)
	tableRulePattern = regexp.MustCompile(`^ {0,3}\|?[ ]*:?-+:?[ ]*(?:\|[ ]*:?-+:?[ ]*)*\|?[ ]*$`)
)

func indentOf(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}

func dropIndent(line string, n int) string {
	i := 0
	for i < n && i < len(line) && line[i] == ' ' {
		i++
	}
	return line[i:]
}

// startsBlock reports whether a line interrupts a paragraph or a quote.
func startsBlock(line string) bool {
	switch {
	case strings.TrimSpace(line) == "":
		return true
	case fencePattern.MatchString(line):
		return true
	case atxPattern.MatchString(line):
		return true
	case rulePattern.MatchString(line):
		return true
	case isQuote(line):
		return true
	case itemAt(line) != nil:
		return true
	}
	return false
}

func isQuote(line string) bool {
	trimmed := dropIndent(line, 3)
	return strings.HasPrefix(trimmed, ">")
}

func stripQuote(line string) string {
	trimmed := dropIndent(line, 3)
	trimmed = strings.TrimPrefix(trimmed, ">")
	return strings.TrimPrefix(trimmed, " ")
}

func parseBlocks(lines []string) []Block {
	var blocks []Block
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil {
			block, next := parseFence(lines, i, m)
			blocks = append(blocks, block)
			i = next
			continue
		}
		if m := atxPattern.FindStringSubmatch(line); m != nil {
			text := trimClosingHashes(m[2])
			blocks = append(blocks, &Heading{Level: len(m[1]), Inlines: parseInline(text)})
			i++
			continue
		}
		if rulePattern.MatchString(line) {
			blocks = append(blocks, &Rule{})
			i++
			continue
		}
		if isQuote(line) {
			block, next := parseQuote(lines, i)
			blocks = append(blocks, block)
			i = next
			continue
		}
		if itemAt(line) != nil {
			block, next := parseList(lines, i)
			blocks = append(blocks, block)
			i = next
			continue
		}
		if isTableStart(lines, i) {
			block, next := parseTable(lines, i)
			blocks = append(blocks, block)
			i = next
			continue
		}
		block, next := parseParagraph(lines, i)
		if block != nil {
			blocks = append(blocks, block)
		}
		i = next
	}
	return blocks
}

func trimClosingHashes(text string) string {
	text = strings.TrimRight(text, " ")
	trimmed := strings.TrimRight(text, "#")
	if trimmed == text {
		return strings.TrimSpace(text)
	}
	if trimmed == "" || strings.HasSuffix(trimmed, " ") {
		return strings.TrimSpace(trimmed)
	}
	return strings.TrimSpace(text)
}

func runLength(source string, start int, ch byte) int {
	n := 0
	for start+n < len(source) && source[start+n] == ch {
		n++
	}
	return n
}

func parseFence(lines []string, start int, m []string) (Block, int) {
	indent := len(m[1])
	fence := m[2]
	lang := m[3]
	if cut := strings.IndexAny(lang, " \t"); cut >= 0 {
		lang = lang[:cut]
	}
	lang = strings.TrimSpace(lang)
	var body []string
	i := start + 1
	for i < len(lines) {
		trimmed := dropIndent(lines[i], 3)
		if run := runLength(trimmed, 0, fence[0]); run >= len(fence) &&
			strings.TrimSpace(trimmed[run:]) == "" {
			i++
			break
		}
		body = append(body, dropIndent(lines[i], indent))
		i++
	}
	return &CodeBlock{Lang: lang, Code: strings.Join(body, "\n")}, i
}

func parseQuote(lines []string, start int) (Block, int) {
	var inner []string
	i := start
	for i < len(lines) {
		line := lines[i]
		if isQuote(line) {
			inner = append(inner, stripQuote(line))
			i++
			continue
		}
		// Lazy continuation: a plain text line keeps the quote going.
		if startsBlock(line) || isTableStart(lines, i) {
			break
		}
		inner = append(inner, strings.TrimLeft(line, " "))
		i++
	}
	return &BlockQuote{Blocks: parseBlocks(inner)}, i
}

func parseParagraph(lines []string, start int) (Block, int) {
	text := []string{strings.TrimLeft(lines[start], " ")}
	i := start + 1
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			break
		}
		if m := setextPattern.FindStringSubmatch(line); m != nil {
			level := 2
			if m[1][0] == '=' {
				level = 1
			}
			return &Heading{
				Level:   level,
				Inlines: parseInline(strings.Join(text, "\n")),
			}, i + 1
		}
		if startsBlock(line) || isTableStart(lines, i) {
			break
		}
		text = append(text, strings.TrimLeft(line, " "))
		i++
	}
	joined := strings.Join(text, "\n")
	if strings.TrimSpace(joined) == "" {
		return nil, i
	}
	return &Paragraph{Inlines: parseInline(joined)}, i
}

// ---------- lists ----------

type itemMark struct {
	indent        int
	contentIndent int
	ordered       bool
	number        int
	delim         byte
	content       string
}

func itemAt(line string) *itemMark {
	if rulePattern.MatchString(line) {
		return nil
	}
	if m := bulletPattern.FindStringSubmatch(line); m != nil {
		gap := len(m[3])
		if gap == 0 || gap > 4 {
			gap = 1
		}
		return &itemMark{
			indent:        len(m[1]),
			contentIndent: len(m[1]) + 1 + gap,
			delim:         m[2][0],
			content:       m[4],
		}
	}
	if m := orderedPattern.FindStringSubmatch(line); m != nil {
		gap := len(m[4])
		if gap == 0 || gap > 4 {
			gap = 1
		}
		number, _ := strconv.Atoi(m[2])
		return &itemMark{
			indent:        len(m[1]),
			contentIndent: len(m[1]) + len(m[2]) + 1 + gap,
			ordered:       true,
			number:        number,
			delim:         m[3][0],
			content:       m[5],
		}
	}
	return nil
}

func parseList(lines []string, start int) (Block, int) {
	first := itemAt(lines[start])
	list := &List{Ordered: first.ordered, Start: first.number}
	if list.Ordered && list.Start == 0 {
		list.Start = 1
	}
	i := start
	for i < len(lines) {
		// A blank line between items keeps the list together (a "loose"
		// list); anything else after it ends the list.
		scan := i
		for scan < len(lines) && strings.TrimSpace(lines[scan]) == "" {
			scan++
		}
		if scan >= len(lines) {
			break
		}
		mark := itemAt(lines[scan])
		if mark == nil || mark.indent != first.indent ||
			mark.ordered != first.ordered || mark.delim != first.delim {
			break
		}
		i = scan
		itemLines := []string{mark.content}
		i++
		for i < len(lines) {
			line := lines[i]
			if strings.TrimSpace(line) == "" {
				// A blank line only stays inside the item when indented
				// content follows it.
				j := i
				for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
					j++
				}
				if j >= len(lines) || indentOf(lines[j]) < mark.contentIndent {
					break
				}
				for ; i < j; i++ {
					itemLines = append(itemLines, "")
				}
				continue
			}
			if indentOf(line) >= mark.contentIndent {
				itemLines = append(itemLines, dropIndent(line, mark.contentIndent))
				i++
				continue
			}
			if startsBlock(line) {
				break
			}
			itemLines = append(itemLines, strings.TrimLeft(line, " "))
			i++
		}
		item := &ListItem{}
		if m := taskPattern.FindStringSubmatch(itemLines[0]); m != nil {
			item.Task = true
			item.Checked = strings.EqualFold(m[1], "x")
			itemLines[0] = m[2]
		}
		item.Blocks = parseBlocks(itemLines)
		list.Items = append(list.Items, item)
	}
	return list, i
}

// ---------- tables ----------

func isTableStart(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	if !strings.Contains(lines[i], "|") || strings.TrimSpace(lines[i]) == "" {
		return false
	}
	if itemAt(lines[i]) != nil || isQuote(lines[i]) {
		return false
	}
	next := lines[i+1]
	return strings.Contains(next, "-") && tableRulePattern.MatchString(next)
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var buf strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			buf.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(buf.String()))
			buf.Reset()
		default:
			buf.WriteByte(line[i])
		}
	}
	cells = append(cells, strings.TrimSpace(buf.String()))
	return cells
}

func parseAligns(line string) []Align {
	cells := splitRow(line)
	aligns := make([]Align, 0, len(cells))
	for _, cell := range cells {
		left := strings.HasPrefix(cell, ":")
		right := strings.HasSuffix(cell, ":")
		switch {
		case left && right:
			aligns = append(aligns, AlignCenter)
		case right:
			aligns = append(aligns, AlignRight)
		case left:
			aligns = append(aligns, AlignLeft)
		default:
			aligns = append(aligns, AlignDefault)
		}
	}
	return aligns
}

func parseTable(lines []string, start int) (Block, int) {
	aligns := parseAligns(lines[start+1])
	table := &Table{Align: aligns}
	for _, cell := range fitRow(splitRow(lines[start]), len(aligns)) {
		table.Head = append(table.Head, TableCell{Inlines: parseInline(cell)})
	}
	i := start + 2
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "|") {
			break
		}
		row := make([]TableCell, 0, len(aligns))
		for _, cell := range fitRow(splitRow(line), len(aligns)) {
			row = append(row, TableCell{Inlines: parseInline(cell)})
		}
		table.Rows = append(table.Rows, row)
		i++
	}
	return table, i
}

func fitRow(cells []string, width int) []string {
	for len(cells) < width {
		cells = append(cells, "")
	}
	if len(cells) > width {
		cells = cells[:width]
	}
	return cells
}

// ---------- inline ----------

func isASCIIPunct(c byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) >= 0
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// skipCodeSpan returns the index just past a code span starting at i, or -1.
func skipCodeSpan(source string, i int) int {
	run := runLength(source, i, '`')
	for j := i + run; j < len(source); j++ {
		if source[j] != '`' {
			continue
		}
		closing := runLength(source, j, '`')
		if closing == run {
			return j + closing
		}
		j += closing - 1
	}
	return -1
}

// findClosing scans for delim, skipping escapes and code spans.
func findClosing(source string, from int, delim string) int {
	for i := from; i < len(source); i++ {
		switch source[i] {
		case '\\':
			i++
		case '`':
			if end := skipCodeSpan(source, i); end > 0 {
				i = end - 1
			}
		default:
			if strings.HasPrefix(source[i:], delim) {
				return i
			}
		}
	}
	return -1
}

func parseInline(source string) []Inline {
	var out []Inline
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, &Text{Value: buf.String()})
			buf.Reset()
		}
	}
	for i := 0; i < len(source); {
		c := source[i]
		switch {
		case c == '\\' && i+1 < len(source) && source[i+1] == '\n':
			flush()
			out = append(out, &LineBreak{Hard: true})
			i += 2
		case c == '\\' && i+1 < len(source) && isASCIIPunct(source[i+1]):
			buf.WriteByte(source[i+1])
			i += 2
		case c == '\n':
			pending := buf.String()
			hard := strings.HasSuffix(pending, "  ")
			buf.Reset()
			buf.WriteString(strings.TrimRight(pending, " "))
			flush()
			out = append(out, &LineBreak{Hard: hard})
			i++
		case c == '`':
			end := skipCodeSpan(source, i)
			run := runLength(source, i, '`')
			if end < 0 {
				buf.WriteString(source[i : i+run])
				i += run
				continue
			}
			flush()
			code := source[i+run : end-run]
			if len(code) > 1 && strings.HasPrefix(code, " ") && strings.HasSuffix(code, " ") {
				code = code[1 : len(code)-1]
			}
			out = append(out, &CodeSpan{Value: code})
			i = end
		case c == '!' && i+1 < len(source) && source[i+1] == '[':
			label, dest, title, end, ok := parseLinkish(source, i+1)
			if !ok {
				buf.WriteByte(c)
				i++
				continue
			}
			flush()
			out = append(out, &Image{URL: dest, Alt: PlainText(parseInline(label)), Title: title})
			i = end
		case c == '[':
			label, dest, title, end, ok := parseLinkish(source, i)
			if !ok {
				buf.WriteByte(c)
				i++
				continue
			}
			flush()
			out = append(out, &Link{URL: dest, Title: title, Kids: parseInline(label)})
			i = end
		case c == '<':
			inline, end := parseAngle(source, i)
			if inline == nil {
				buf.WriteByte(c)
				i++
				continue
			}
			flush()
			if _, blank := inline.(*Text); !blank {
				out = append(out, inline)
			}
			i = end
		case c == '~' && strings.HasPrefix(source[i:], "~~"):
			end := findClosing(source, i+2, "~~")
			if end < 0 || end == i+2 {
				buf.WriteString("~~")
				i += 2
				continue
			}
			flush()
			out = append(out, &Strike{Kids: parseInline(source[i+2 : end])})
			i = end + 2
		case c == '*' || c == '_':
			node, end := parseEmphasis(source, i)
			if node == nil {
				buf.WriteByte(c)
				i++
				continue
			}
			flush()
			out = append(out, node)
			i = end
		default:
			buf.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

func parseEmphasis(source string, i int) (Inline, int) {
	c := source[i]
	// `_` inside a word (snake_case) is literal.
	if c == '_' && i > 0 && isWordByte(source[i-1]) {
		return nil, i
	}
	run := runLength(source, i, c)
	if run >= 2 {
		delim := string(c) + string(c)
		if end := findClosing(source, i+2, delim); end > i+2 {
			if c == '_' && end+2 < len(source) && isWordByte(source[end+2]) {
				return nil, i
			}
			return &Strong{Kids: parseInline(source[i+2 : end])}, end + 2
		}
	}
	if i+1 < len(source) && source[i+1] == ' ' {
		return nil, i
	}
	if end := findClosing(source, i+1, string(c)); end > i+1 {
		if c == '_' && end+1 < len(source) && isWordByte(source[end+1]) {
			return nil, i
		}
		return &Emphasis{Kids: parseInline(source[i+1 : end])}, end + 1
	}
	return nil, i
}

// parseAngle handles <http://…> autolinks, <a@b.c> mail links and <br>.
func parseAngle(source string, i int) (Inline, int) {
	end := strings.IndexByte(source[i:], '>')
	if end < 0 {
		return nil, i
	}
	inner := source[i+1 : i+end]
	if strings.ContainsAny(inner, " \t\n") {
		switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(inner, "/"))) {
		case "br":
			return &LineBreak{Hard: true}, i + end + 1
		}
		return nil, i
	}
	switch strings.ToLower(strings.TrimSuffix(inner, "/")) {
	case "br":
		return &LineBreak{Hard: true}, i + end + 1
	}
	if schemePattern.MatchString(inner) {
		return &Link{URL: inner, Kids: []Inline{&Text{Value: inner}}}, i + end + 1
	}
	if mailPattern.MatchString(inner) {
		return &Link{URL: "mailto:" + inner, Kids: []Inline{&Text{Value: inner}}}, i + end + 1
	}
	return nil, i
}

var (
	schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]{1,31}:[^<>]*$`)
	mailPattern   = regexp.MustCompile(`^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$`)
)

// parseLinkish parses `[label](dest "title")` starting at the `[`.
func parseLinkish(source string, start int) (label, dest, title string, end int, ok bool) {
	depth := 0
	closing := -1
	for i := start; i < len(source) && closing < 0; i++ {
		switch source[i] {
		case '\\':
			i++
		case '`':
			if next := skipCodeSpan(source, i); next > 0 {
				i = next - 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				closing = i
			}
		}
	}
	if closing < 0 || closing+1 >= len(source) || source[closing+1] != '(' {
		return "", "", "", start, false
	}
	label = source[start+1 : closing]
	i := closing + 2
	depth = 1
	var raw strings.Builder
	for ; i < len(source); i++ {
		c := source[i]
		if c == '\\' && i+1 < len(source) {
			raw.WriteByte(source[i+1])
			i++
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
			if depth == 0 {
				break
			}
		}
		raw.WriteByte(c)
	}
	if depth != 0 {
		return "", "", "", start, false
	}
	dest, title = splitDestination(raw.String())
	return label, dest, title, i + 1, true
}

func splitDestination(raw string) (dest, title string) {
	raw = strings.TrimSpace(raw)
	for _, quote := range []byte{'"', '\''} {
		if cut := strings.LastIndexByte(raw, quote); cut > 0 && cut == len(raw)-1 {
			if open := strings.IndexByte(raw, quote); open >= 0 && open < cut {
				title = raw[open+1 : cut]
				raw = strings.TrimSpace(raw[:open])
			}
		}
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "<")
	raw = strings.TrimSuffix(raw, ">")
	return raw, title
}

// PlainText flattens inline nodes to their text, dropping every marker.
func PlainText(inlines []Inline) string {
	var b strings.Builder
	writeInlineText(&b, inlines)
	return b.String()
}

func writeInlineText(b *strings.Builder, inlines []Inline) {
	for _, inline := range inlines {
		switch node := inline.(type) {
		case *Text:
			b.WriteString(node.Value)
		case *CodeSpan:
			b.WriteString(node.Value)
		case *Strong:
			writeInlineText(b, node.Kids)
		case *Emphasis:
			writeInlineText(b, node.Kids)
		case *Strike:
			writeInlineText(b, node.Kids)
		case *Link:
			writeInlineText(b, node.Kids)
		case *Image:
			b.WriteString(node.Alt)
		case *LineBreak:
			b.WriteString(" ")
		}
	}
}
