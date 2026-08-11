package dsl

import (
	"strings"
	"unicode"
)

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokNumber
	tokString
	tokLParen
	tokRParen
	tokComma
	tokCmp // == != >= <= > <
)

type token struct {
	kind tokKind
	text string
	pos  int // byte offset
}

type lexer struct {
	src  string
	i    int
	toks []token
}

func lex(src string) ([]token, *ParseError) {
	l := &lexer{src: src}
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			l.i++
		case c == '(':
			l.emit(tokLParen, "(", 1)
		case c == ')':
			l.emit(tokRParen, ")", 1)
		case c == ',':
			l.emit(tokComma, ",", 1)
		case c == '=' || c == '!' || c == '>' || c == '<':
			if err := l.lexCmp(); err != nil {
				return nil, err
			}
		case c == '"' || c == '\'':
			if err := l.lexString(c); err != nil {
				return nil, err
			}
		case c >= '0' && c <= '9':
			l.lexNumber()
		case c == '-' && l.i+1 < len(l.src) && l.src[l.i+1] >= '0' && l.src[l.i+1] <= '9':
			l.lexNumber()
		case isIdentStart(rune(c)):
			l.lexIdent()
		default:
			return nil, errf(l.i, "unexpected character %q", string(c))
		}
	}
	l.toks = append(l.toks, token{kind: tokEOF, pos: len(l.src)})
	return l.toks, nil
}

func (l *lexer) emit(k tokKind, text string, width int) {
	l.toks = append(l.toks, token{kind: k, text: text, pos: l.i})
	l.i += width
}

func (l *lexer) lexCmp() *ParseError {
	start := l.i
	c := l.src[l.i]
	two := ""
	if l.i+1 < len(l.src) && l.src[l.i+1] == '=' {
		two = l.src[l.i : l.i+2]
	}
	switch {
	case two == "==" || two == "!=" || two == ">=" || two == "<=":
		l.emit(tokCmp, two, 2)
	case c == '>' || c == '<':
		l.emit(tokCmp, string(c), 1)
	case c == '=':
		// Be forgiving: single '=' means '=='.
		l.emit(tokCmp, "==", 1)
	default:
		return errf(start, "unexpected character %q", string(c))
	}
	return nil
}

func (l *lexer) lexString(quote byte) *ParseError {
	start := l.i
	l.i++ // opening quote
	var sb strings.Builder
	for l.i < len(l.src) {
		c := l.src[l.i]
		if c == quote {
			l.i++
			l.toks = append(l.toks, token{kind: tokString, text: sb.String(), pos: start})
			return nil
		}
		sb.WriteByte(c)
		l.i++
	}
	return errf(start, "unterminated string")
}

func (l *lexer) lexNumber() {
	start := l.i
	if l.src[l.i] == '-' {
		l.i++
	}
	for l.i < len(l.src) && (l.src[l.i] >= '0' && l.src[l.i] <= '9' || l.src[l.i] == '.') {
		l.i++
	}
	l.toks = append(l.toks, token{kind: tokNumber, text: l.src[start:l.i], pos: start})
}

func (l *lexer) lexIdent() {
	start := l.i
	for l.i < len(l.src) && isIdentPart(rune(l.src[l.i])) {
		l.i++
	}
	l.toks = append(l.toks, token{kind: tokIdent, text: l.src[start:l.i], pos: start})
}

// Identifiers cover node ids and flag names: letters, digits, '_', '-', '.'.
// Hyphens are common in node ids (e.g. setup-db).
func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isIdentPart(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}
