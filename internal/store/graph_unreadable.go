package store

import (
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
	// Line is the line the parser blames, or 0 when it names none.
	Line    int
	message *locale.Message
	cause   error
}

// yamlLine finds the line yaml.v3 blames: "yaml: line 2: ..." for a syntax
// error, "yaml: unmarshal errors:\n  line 16: ..." for a type error.
var yamlLine = regexp.MustCompile(`\bline (\d+)\b`)

func graphUnreadable(cause error) *GraphUnreadableError {
	// The parser's own words stay, for whoever fixes the file; the
	// "graph.yaml: " they start with is already said by the sentence.
	detail := strings.TrimPrefix(cause.Error(), "graph.yaml: ")
	e := &GraphUnreadableError{cause: cause}
	if match := yamlLine.FindStringSubmatch(detail); match != nil {
		e.Line, _ = strconv.Atoi(match[1])
	}
	// "graph.yaml: " and the parser's "line N" stay in both languages: the
	// client recognizes an unreadable graph by them when a switch fails.
	if e.Line > 0 {
		e.message = locale.Say(
			"graph.yaml: line %d has an error. Fix the file, then reload. (%s)",
			"graph.yaml: 第 %d 行有錯誤。修正檔案後重新載入。（%s）",
			e.Line, detail)
	} else {
		e.message = locale.Say(
			"graph.yaml: the file has an error. Fix it, then reload. (%s)",
			"graph.yaml: 檔案有錯誤。修正檔案後重新載入。（%s）",
			detail)
	}
	return e
}

func (e *GraphUnreadableError) Error() string { return e.message.Error() }

// Unwrap reaches both the worded sentence, which locale.Render translates,
// and the parser's error.
func (e *GraphUnreadableError) Unwrap() []error { return []error{e.message, e.cause} }

func (e *GraphUnreadableError) Status() int { return http.StatusUnprocessableEntity }
