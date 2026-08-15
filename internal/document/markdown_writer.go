package document

import (
	"strconv"
	"strings"
)

// RenderMarkdown turns the block model back into Markdown. It is the writer
// half of every conversion that ends in a Markdown document — the readers
// produce blocks, this produces the text the editor edits.
func RenderMarkdown(doc *Doc) string {
	var out []string
	for _, block := range doc.Blocks {
		if text := markdownBlock(block, ""); text != "" {
			out = append(out, text)
		}
	}
	body := strings.Join(out, "\n\n")
	if body == "" {
		return ""
	}
	return body + "\n"
}

func markdownBlocks(blocks []Block, indent string) string {
	var out []string
	for _, block := range blocks {
		if text := markdownBlock(block, indent); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n\n")
}

func markdownBlock(block Block, indent string) string {
	switch node := block.(type) {
	case *Heading:
		level := min(max(node.Level, 1), 6)
		return indent + strings.Repeat("#", level) + " " + markdownInlines(node.Inlines)
	case *Paragraph:
		return indentLines(markdownInlines(node.Inlines), indent)
	case *CodeBlock:
		fence := strings.Repeat("`", max(3, longestBacktickRun(node.Code)+1))
		lines := []string{indent + fence + node.Lang}
		for _, line := range strings.Split(node.Code, "\n") {
			lines = append(lines, indent+line)
		}
		return strings.Join(append(lines, indent+fence), "\n")
	case *BlockQuote:
		inner := markdownBlocks(node.Blocks, "")
		var quoted []string
		for _, line := range strings.Split(inner, "\n") {
			quoted = append(quoted, strings.TrimRight(indent+"> "+line, " "))
		}
		return strings.Join(quoted, "\n")
	case *List:
		return markdownList(node, indent)
	case *Table:
		return markdownTable(node, indent)
	case *Rule:
		return indent + "---"
	}
	return ""
}

func markdownList(list *List, indent string) string {
	var out []string
	for index, item := range list.Items {
		marker := "- "
		switch {
		case item.Task && item.Checked:
			marker = "- [x] "
		case item.Task:
			marker = "- [ ] "
		case list.Ordered:
			marker = strconv.Itoa(max(list.Start, 1)+index) + ". "
		}
		body := markdownBlocks(item.Blocks, "")
		lines := strings.Split(body, "\n")
		if len(lines) == 1 && lines[0] == "" {
			out = append(out, strings.TrimRight(indent+marker, " "))
			continue
		}
		pad := strings.Repeat(" ", len([]rune(marker)))
		for i, line := range lines {
			lead := indent + pad
			if i == 0 {
				lead = indent + marker
			}
			out = append(out, strings.TrimRight(lead+line, " "))
		}
	}
	return strings.Join(out, "\n")
}

func markdownTable(table *Table, indent string) string {
	columns := len(table.Align)
	for _, row := range append([][]TableCell{table.Head}, table.Rows...) {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return ""
	}
	cells := make([][]string, 0, len(table.Rows)+1)
	cells = append(cells, markdownRow(table.Head, columns))
	for _, row := range table.Rows {
		cells = append(cells, markdownRow(row, columns))
	}
	widths := make([]int, columns)
	for i := range widths {
		widths[i] = 3
	}
	for _, row := range cells {
		for i, cell := range row {
			if width := displayWidth(cell); width > widths[i] {
				widths[i] = width
			}
		}
	}
	lines := make([]string, 0, len(cells)+1)
	line := func(row []string) string {
		parts := make([]string, columns)
		for i, cell := range row {
			parts[i] = padCell(cell, widths[i], AlignAt(table.Align, i))
		}
		return indent + "| " + strings.Join(parts, " | ") + " |"
	}
	lines = append(lines, line(cells[0]))
	rule := make([]string, columns)
	for i := range rule {
		rule[i] = markdownAlignRule(widths[i], AlignAt(table.Align, i))
	}
	lines = append(lines, indent+"| "+strings.Join(rule, " | ")+" |")
	for _, row := range cells[1:] {
		lines = append(lines, line(row))
	}
	return strings.Join(lines, "\n")
}

func markdownRow(row []TableCell, columns int) []string {
	out := make([]string, columns)
	for i := range out {
		if i < len(row) {
			text := markdownInlines(row[i].Inlines)
			text = strings.ReplaceAll(text, "\n", " ")
			out[i] = strings.ReplaceAll(text, "|", `\|`)
		}
	}
	return out
}

func markdownAlignRule(width int, align Align) string {
	if width < 3 {
		width = 3
	}
	switch align {
	case AlignLeft:
		return ":" + strings.Repeat("-", width-1)
	case AlignRight:
		return strings.Repeat("-", width-1) + ":"
	case AlignCenter:
		return ":" + strings.Repeat("-", width-2) + ":"
	default:
		return strings.Repeat("-", width)
	}
}

func markdownInlines(inlines []Inline) string {
	var b strings.Builder
	for _, inline := range inlines {
		switch node := inline.(type) {
		case *Text:
			b.WriteString(escapeMarkdown(node.Value))
		case *CodeSpan:
			fence := strings.Repeat("`", longestBacktickRun(node.Value)+1)
			value := node.Value
			if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
				value = " " + value + " "
			}
			b.WriteString(fence + value + fence)
		case *Strong:
			b.WriteString("**" + markdownInlines(node.Kids) + "**")
		case *Emphasis:
			b.WriteString("*" + markdownInlines(node.Kids) + "*")
		case *Strike:
			b.WriteString("~~" + markdownInlines(node.Kids) + "~~")
		case *Link:
			label := markdownInlines(node.Kids)
			if label == "" {
				label = escapeMarkdown(node.URL)
			}
			b.WriteString("[" + label + "](" + markdownURL(node.URL) + markdownTitle(node.Title) + ")")
		case *Image:
			b.WriteString("![" + escapeMarkdown(node.Alt) + "](" +
				markdownURL(node.URL) + markdownTitle(node.Title) + ")")
		case *LineBreak:
			if node.Hard {
				b.WriteString("  \n")
			} else {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func markdownURL(url string) string {
	if strings.ContainsAny(url, " ()") {
		return "<" + url + ">"
	}
	return url
}

func markdownTitle(title string) string {
	if title == "" {
		return ""
	}
	return ` "` + strings.ReplaceAll(title, `"`, `\"`) + `"`
}

// escapeMarkdown protects the characters that would otherwise be read back as
// markup. It stays conservative: escaping everything punctuation-shaped makes
// the source unreadable for no gain.
func escapeMarkdown(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch c {
		case '\\', '`', '*', '_', '[', ']', '<', '|':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '~':
			if i+1 < len(text) && text[i+1] == '~' {
				b.WriteString(`\~`)
				continue
			}
			b.WriteByte(c)
		case '#', '>', '+':
			// Only meaningful at the start of a line.
			if b.Len() == 0 || strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		case '-':
			if b.Len() == 0 || strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		case '.', ')':
			// "1." and "1)" open a list; escape the delimiter after digits.
			if onlyDigitsSinceLineStart(b.String()) {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func onlyDigitsSinceLineStart(written string) bool {
	if cut := strings.LastIndexByte(written, '\n'); cut >= 0 {
		written = written[cut+1:]
	}
	if written == "" {
		return false
	}
	for i := 0; i < len(written); i++ {
		if written[i] < '0' || written[i] > '9' {
			return false
		}
	}
	return true
}

func longestBacktickRun(text string) int {
	longest, current := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return longest
}

func indentLines(text, indent string) string {
	if indent == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(indent+line, " ")
	}
	return strings.Join(lines, "\n")
}
