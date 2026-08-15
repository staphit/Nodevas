package dsl

import (
	"strconv"
	"strings"
)

// Parse parses a gate expression. Empty/whitespace-only input returns
// (nil, nil): an absent requires means "always satisfied".
func Parse(src string) (Expr, *ParseError) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, perr := p.parseExpr(0)
	if perr != nil {
		return nil, perr
	}
	if p.peek().kind != tokEOF {
		return nil, errf(p.peek().pos, "unexpected %q", p.peek().text)
	}
	return e, nil
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) expect(k tokKind, what string) (token, *ParseError) {
	t := p.next()
	if t.kind != k {
		return t, errf(t.pos, "expected %s, got %q", what, t.text)
	}
	return t, nil
}

// Pratt loop: binds operators with precedence >= min.
func (p *parser) parseExpr(min int) (Expr, *ParseError) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tokIdent {
			return left, nil
		}
		op := strings.ToLower(t.text)
		prec := opPrec(op)
		if prec == 0 || prec < min {
			return left, nil
		}
		p.next()
		// Left-assoc: right side binds strictly tighter.
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op, L: left, R: right, pos: left.Pos()}
	}
}

func (p *parser) parseUnary() (Expr, *ParseError) {
	t := p.peek()
	if t.kind == tokIdent && strings.ToLower(t.text) == "not" {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: "not", X: x, pos: t.pos}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, *ParseError) {
	t := p.next()
	switch t.kind {
	case tokLParen:
		e, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil
	case tokIdent:
		low := strings.ToLower(t.text)
		switch low {
		case "flag":
			return p.parseFlag(t.pos)
		case "all", "any":
			return p.parseAllAny(low, t.pos)
		case "and", "or", "xor", "nand", "nor", "not", "true", "false":
			return nil, errf(t.pos, "unexpected keyword %q", t.text)
		}
		return &NodeRef{ID: t.text, pos: t.pos}, nil
	case tokEOF:
		return nil, errf(t.pos, "unexpected end of expression")
	default:
		return nil, errf(t.pos, "unexpected %q", t.text)
	}
}

// parseFlag parses `flag(name)` or `flag(name <op> literal)`.
func (p *parser) parseFlag(pos int) (Expr, *ParseError) {
	if _, err := p.expect(tokLParen, "'(' after flag"); err != nil {
		return nil, err
	}
	nameTok, err := p.expect(tokIdent, "flag name")
	if err != nil {
		return nil, err
	}
	if isKeyword(nameTok.text) {
		return nil, errf(nameTok.pos, "keyword %q cannot be a flag name", nameTok.text)
	}
	f := &FlagCmp{Name: nameTok.text, pos: pos}
	if p.peek().kind == tokCmp {
		f.Op = p.next().text
		lit, lerr := p.parseLiteral()
		if lerr != nil {
			return nil, lerr
		}
		f.Val = lit
	}
	if _, err := p.expect(tokRParen, "')'"); err != nil {
		return nil, err
	}
	return f, nil
}

func (p *parser) parseLiteral() (Literal, *ParseError) {
	t := p.next()
	switch t.kind {
	case tokNumber:
		n, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return Literal{}, errf(t.pos, "bad number %q", t.text)
		}
		return Literal{Raw: t.text, Num: n, IsNum: true}, nil
	case tokString:
		return Literal{Raw: strconv.Quote(t.text), Str: t.text}, nil
	case tokIdent:
		switch strings.ToLower(t.text) {
		case "true":
			return Literal{Raw: "true", Bool: true, IsBool: true}, nil
		case "false":
			return Literal{Raw: "false", IsBool: true}, nil
		}
		// Bare word compares as a string, e.g. flag(mood == happy).
		return Literal{Raw: strconv.Quote(t.text), Str: t.text}, nil
	default:
		return Literal{}, errf(t.pos, "expected literal, got %q", t.text)
	}
}

// parseAllAny desugars all(a, b, ...) / any(a, b, ...) into and/or chains.
func (p *parser) parseAllAny(which string, pos int) (Expr, *ParseError) {
	if _, err := p.expect(tokLParen, "'(' after "+which); err != nil {
		return nil, err
	}
	op := "and"
	if which == "any" {
		op = "or"
	}
	var e Expr
	for {
		item, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if e == nil {
			e = item
		} else {
			e = &Binary{Op: op, L: e, R: item, pos: pos}
		}
		t := p.next()
		if t.kind == tokRParen {
			return e, nil
		}
		if t.kind != tokComma {
			return nil, errf(t.pos, "expected ',' or ')', got %q", t.text)
		}
	}
}
