package store

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"nodevas/internal/locale"
)

// GraphUnreadableError is a graph.yaml on disk that does not parse or does not
// hold a valid graph. It is the reader's file to fix, not the server failing,
// so it answers 422 and says which line to look at in the reader's language.
// Nothing is written over the file while it stands.
type GraphUnreadableError struct {
	// Line is the line the mistake is on, counted from one as an editor
	// counts, or 0 when the parser names none.
	Line    int
	message *locale.Message
	cause   error
}

// yamlLine finds the line yaml.v3 blames: "yaml: line 2: ..." for a syntax
// error, "yaml: unmarshal errors:\n  line 16: ..." for a type error.
var yamlLine = regexp.MustCompile(`\bline (\d+)\b`)

// yamlParserLine is a syntax error yaml.v3's parser (not its scanner) found.
// Those name the line counting from zero: the parser keeps its marks
// zero-based and only a scanner error has one added before it is printed.
var yamlParserLine = regexp.MustCompile(`\byaml: line (\d+): (?:did not find expected (?:<stream-start>|<document start>|node content|key|'-' indicator|',' or '\]'|',' or '\}')|found (?:duplicate %YAML directive|duplicate %TAG directive|incompatible YAML document|undefined tag handle))`)

// GraphUnreadable is the refusal for graph.yaml holding data, which failed to
// parse or validate with cause: for a reader of the file other than the
// store's own (an export reading it straight from disk).
func GraphUnreadable(data []byte, cause error) error { return graphUnreadable(data, cause) }

func graphUnreadable(data []byte, cause error) *GraphUnreadableError {
	// The parser's own words stay, for whoever fixes the file; the
	// "graph.yaml: " they start with is already said by the sentence.
	detail := strings.TrimPrefix(cause.Error(), "graph.yaml: ")
	detail = editorLine(data, detail)
	e := &GraphUnreadableError{cause: cause}
	if match := yamlLine.FindStringSubmatch(detail); match != nil {
		e.Line, _ = strconv.Atoi(match[1])
	}
	// The sentence leads with the file it is about; the client shows what
	// follows on its own, so that starts a sentence: a capital in English,
	// and in Chinese the full-width colon every Chinese sentence uses. The
	// parser's "line N" stays as it wrote it.
	if e.Line > 0 {
		e.message = locale.Say(
			"graph.yaml: Line %d has an error. Fix the file, then reload. (%s)",
			"graph.yaml：第 %d 行有錯誤。修正檔案後重新載入。（%s）",
			e.Line, detail)
	} else {
		e.message = locale.Say(
			"graph.yaml: The file has an error. Fix it, then reload. (%s)",
			"graph.yaml：檔案有錯誤。修正檔案後重新載入。（%s）",
			detail)
	}
	return e
}

// yamlBlockBreak is a parser error inside a block list or mapping: an entry
// that does not line up with the ones before it, most often a key indented
// less than its siblings. yaml.v3 prints where that list or mapping starts,
// not the entry that broke it.
var yamlBlockBreak = regexp.MustCompile(`did not find expected (?:key|'-' indicator)$`)

// editorLine rewrites a parser error's zero-based line in detail as the line
// an editor shows. Where the parser blames the end of the file (an unclosed
// bracket it read to the end for), that is the file's last line; where it
// blames the start of a block list or mapping, it is the line of the entry
// that broke it.
func editorLine(data []byte, detail string) string {
	match := yamlParserLine.FindStringSubmatchIndex(detail)
	if match == nil {
		return detail
	}
	line, err := strconv.Atoi(detail[match[2]:match[3]])
	if err != nil {
		return detail
	}
	line++
	if last := bytes.Count(bytes.TrimRight(data, "\r\n"), []byte("\n")) + 1; line > last {
		line = last
	}
	if said := detail[match[0]:match[1]]; yamlBlockBreak.MatchString(said) {
		line = brokenEntryLine(data, said, line)
	}
	return detail[:match[2]] + strconv.Itoa(line) + detail[match[3]:]
}

// brokenEntryLine is the line, counted from one, of the entry that ended a
// block list or mapping starting on line start with the parser error said.
// yaml.v3 keeps that entry's position to itself, so it is the fewest lines
// of the file that already fail the same way: the parser stops at the first
// mistake it reads, so the lines before the entry parse and every prefix
// holding it fails. A binary search keeps that to a handful of parses
// however long the file. It is start when no prefix fails that way.
func brokenEntryLine(data []byte, said string, start int) int {
	lines := bytes.SplitAfter(data, []byte("\n"))
	failsAt := func(n int) bool {
		return yamlFails(bytes.Join(lines[:n], nil), said)
	}
	if start >= len(lines) || !failsAt(len(lines)) {
		return start
	}
	// failsAt(high) holds throughout; failsAt(low) does not.
	low, high := start, len(lines)
	for high-low > 1 {
		mid := low + (high-low)/2
		if failsAt(mid) {
			high = mid
		} else {
			low = mid
		}
	}
	return high
}

// yamlFails reports whether reading every document in data stops on the
// parser error said.
func yamlFails(data []byte, said string) bool {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if err == nil {
			continue
		}
		return err != io.EOF && strings.Contains(err.Error(), said)
	}
}

func (e *GraphUnreadableError) Error() string { return e.message.Error() }

// Unwrap reaches both the worded sentence, which locale.Render translates,
// and the parser's error.
func (e *GraphUnreadableError) Unwrap() []error { return []error{e.message, e.cause} }

func (e *GraphUnreadableError) Status() int { return http.StatusUnprocessableEntity }
