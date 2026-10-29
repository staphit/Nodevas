package store

import (
	"bytes"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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

// editorLine rewrites a parser error's zero-based line in detail as the line
// an editor shows. Where the parser blames the end of the file (an unclosed
// bracket it read to the end for), that is the file's last line.
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
	return detail[:match[2]] + strconv.Itoa(line) + detail[match[3]:]
}

func (e *GraphUnreadableError) Error() string { return e.message.Error() }

// Unwrap reaches both the worded sentence, which locale.Render translates,
// and the parser's error.
func (e *GraphUnreadableError) Unwrap() []error { return []error{e.message, e.cause} }

func (e *GraphUnreadableError) Status() int { return http.StatusUnprocessableEntity }
