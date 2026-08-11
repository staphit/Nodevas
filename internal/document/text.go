package document

import (
	"strconv"
	"strings"
)

// RenderText renders the document as readable plain text: markers are
// dropped, structure survives as indentation, underlines and padded table
// columns.
func RenderText(doc *Doc) string {
	w := &textWriter{}
	w.blocks(doc.Blocks, "")
	body := strings.Join(w.trimmed(), "\n")
	if body == "" {
		return ""
	}
	return body + "\n"
}

type textWriter struct{ lines []string }

func (w *textWriter) push(prefix, text string) {
	for _, part := range strings.Split(text, "\n") {
		w.lines = append(w.lines, strings.TrimRight(prefix+part, " "))
	}
}

func (w *textWriter) blank() {
	if len(w.lines) > 0 && w.lines[len(w.lines)-1] != "" {
		w.lines = append(w.lines, "")
	}
}

// trimmed drops the leading and trailing blank lines.
func (w *textWriter) trimmed() []string {
	start, end := 0, len(w.lines)
	for start < end && strings.TrimSpace(w.lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(w.lines[end-1]) == "" {
		end--
	}
	return w.lines[start:end]
}

func (w *textWriter) blocks(blocks []Block, prefix string) {
	for _, block := range blocks {
		switch node := block.(type) {
		case *Heading:
			w.blank()
			text := inlineText(node.Inlines)
			w.push(prefix, text)
			if node.Level <= 2 {
				fill := "="
				if node.Level == 2 {
					fill = "-"
				}
				width := displayWidth(text)
				if width < 3 {
					width = 3
				}
				if width > 72 {
					width = 72
				}
				w.push(prefix, strings.Repeat(fill, width))
			}
			w.blank()
		case *Paragraph:
			w.blank()
			w.push(prefix, inlineText(node.Inlines))
			w.blank()
		case *CodeBlock:
			w.blank()
			for _, line := range strings.Split(node.Code, "\n") {
				w.push(prefix+"    ", line)
			}
			w.blank()
		case *BlockQuote:
			w.blank()
			sub := &textWriter{}
			sub.blocks(node.Blocks, "")
			for _, line := range sub.trimmed() {
				w.lines = append(w.lines, strings.TrimRight(prefix+"> "+line, " "))
			}
			w.blank()
		case *List:
			w.list(node, prefix)
		case *Table:
			w.blank()
			for _, line := range renderTextTable(node) {
				w.push(prefix, line)
			}
			w.blank()
		case *Rule:
			w.blank()
			w.push(prefix, strings.Repeat("-", 40))
			w.blank()
		}
	}
}

func (w *textWriter) list(list *List, prefix string) {
	w.blank()
	for index, item := range list.Items {
		marker := "- "
		switch {
		case item.Task && item.Checked:
			marker = "[x] "
		case item.Task:
			marker = "[ ] "
		case list.Ordered:
			marker = strconv.Itoa(list.Start+index) + ". "
		}
		sub := &textWriter{}
		sub.blocks(item.Blocks, "")
		lines := sub.trimmed()
		if len(lines) == 0 {
			lines = []string{""}
		}
		pad := strings.Repeat(" ", len(marker))
		for i, line := range lines {
			lead := pad
			if i == 0 {
				lead = marker
			}
			w.lines = append(w.lines, strings.TrimRight(prefix+lead+line, " "))
		}
	}
	w.blank()
}

func renderTextTable(table *Table) []string {
	rows := make([][]string, 0, len(table.Rows)+1)
	if len(table.Head) > 0 {
		rows = append(rows, cellsText(table.Head))
	}
	for _, row := range table.Rows {
		rows = append(rows, cellsText(row))
	}
	if len(rows) == 0 {
		return nil
	}
	columns := len(table.Align)
	if columns == 0 {
		columns = len(rows[0])
	}
	widths := make([]int, columns)
	for _, row := range rows {
		for i := 0; i < columns && i < len(row); i++ {
			if width := displayWidth(row[i]); width > widths[i] {
				widths[i] = width
			}
		}
	}
	for i := range widths {
		if widths[i] < 3 {
			widths[i] = 3
		}
	}
	out := make([]string, 0, len(rows)+1)
	for index, row := range rows {
		cells := make([]string, columns)
		for i := 0; i < columns; i++ {
			text := ""
			if i < len(row) {
				text = row[i]
			}
			cells[i] = padCell(text, widths[i], AlignAt(table.Align, i))
		}
		out = append(out, strings.TrimRight(strings.Join(cells, "  "), " "))
		if index == 0 && len(table.Head) > 0 {
			rule := make([]string, columns)
			for i := 0; i < columns; i++ {
				rule[i] = strings.Repeat("-", widths[i])
			}
			out = append(out, strings.Join(rule, "  "))
		}
	}
	return out
}

func cellsText(cells []TableCell) []string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = strings.ReplaceAll(inlineText(cell.Inlines), "\n", " ")
	}
	return out
}

func padCell(text string, width int, align Align) string {
	gap := width - displayWidth(text)
	if gap <= 0 {
		return text
	}
	switch align {
	case AlignRight:
		return strings.Repeat(" ", gap) + text
	case AlignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", gap-left)
	default:
		return text + strings.Repeat(" ", gap)
	}
}

// inlineText flattens inline content for plain text: links keep their target
// in parentheses, images become "alt <url>".
func inlineText(inlines []Inline) string {
	var b strings.Builder
	for _, inline := range inlines {
		switch node := inline.(type) {
		case *Text:
			b.WriteString(node.Value)
		case *CodeSpan:
			b.WriteString(node.Value)
		case *Strong:
			b.WriteString(inlineText(node.Kids))
		case *Emphasis:
			b.WriteString(inlineText(node.Kids))
		case *Strike:
			b.WriteString(inlineText(node.Kids))
		case *Link:
			label := inlineText(node.Kids)
			b.WriteString(label)
			if node.URL != "" && node.URL != label && "mailto:"+label != node.URL {
				b.WriteString(" (" + node.URL + ")")
			}
		case *Image:
			alt := node.Alt
			if alt == "" {
				alt = "image"
			}
			b.WriteString(alt)
			if node.URL != "" {
				b.WriteString(" <" + node.URL + ">")
			}
		case *LineBreak:
			b.WriteString("\n")
		}
	}
	return b.String()
}

// displayWidth counts fullwidth characters as two columns so padded tables
// line up in a monospaced viewer.
func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeWidth(r)
	}
	return width
}

func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r == 0x2329, r == 0x232A,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}
