// Package dsl implements the gate condition expression language used in
// `requires:` fields, e.g. `a and (b nor c) and flag(karma >= 3)`.
package dsl

import (
	"fmt"
	"strings"
)

// Expr is a parsed gate expression node.
type Expr interface {
	// Pos returns the byte offset of the expression's start in the source.
	Pos() int
	// String renders the expression back to canonical DSL text.
	String() string
}

// NodeRef references another graph node; it evaluates to true when that
// node's status is done.
type NodeRef struct {
	ID  string
	pos int
}

func (n *NodeRef) Pos() int       { return n.pos }
func (n *NodeRef) String() string { return n.ID }

// FlagCmp is a flag test: `flag(name)` (truthy test, Op == "") or
// `flag(name >= 3)` (comparison against a literal).
type FlagCmp struct {
	Name string
	Op   string // "", "==", "!=", ">=", "<=", ">", "<"
	Val  Literal
	pos  int
}

func (f *FlagCmp) Pos() int { return f.pos }
func (f *FlagCmp) String() string {
	if f.Op == "" {
		return fmt.Sprintf("flag(%s)", f.Name)
	}
	return fmt.Sprintf("flag(%s %s %s)", f.Name, f.Op, f.Val)
}

// Literal is a literal value inside a flag comparison.
type Literal struct {
	Raw    string // canonical text form
	Num    float64
	IsNum  bool
	Bool   bool
	IsBool bool
	Str    string // set when neither number nor bool
}

func (l Literal) String() string { return l.Raw }

// Unary is `not X`.
type Unary struct {
	Op  string // "not"
	X   Expr
	pos int
}

func (u *Unary) Pos() int { return u.pos }
func (u *Unary) String() string {
	if needsParens(u.X, precNot) {
		return fmt.Sprintf("not (%s)", u.X)
	}
	return fmt.Sprintf("not %s", u.X)
}

// Binary is a binary boolean operator: and, or, xor, nand, nor.
type Binary struct {
	Op   string
	L, R Expr
	pos  int
}

func (b *Binary) Pos() int { return b.pos }
func (b *Binary) String() string {
	l, r := b.L.String(), b.R.String()
	p := opPrec(b.Op)
	if needsParens(b.L, p) {
		l = "(" + l + ")"
	}
	// Right side needs parens at equal precedence too (left-assoc).
	if needsParens(b.R, p) || samePrecBinary(b.R, p) {
		r = "(" + r + ")"
	}
	return fmt.Sprintf("%s %s %s", l, b.Op, r)
}

const (
	precOr  = 1 // or, nor
	precXor = 2
	precAnd = 3 // and, nand
	precNot = 4
)

func opPrec(op string) int {
	switch op {
	case "or", "nor":
		return precOr
	case "xor":
		return precXor
	case "and", "nand":
		return precAnd
	}
	return 0
}

func needsParens(e Expr, parentPrec int) bool {
	if b, ok := e.(*Binary); ok {
		return opPrec(b.Op) < parentPrec
	}
	return false
}

func samePrecBinary(e Expr, parentPrec int) bool {
	if b, ok := e.(*Binary); ok {
		return opPrec(b.Op) == parentPrec
	}
	return false
}

// NodeRefs returns the distinct node ids referenced anywhere in the
// expression, in first-appearance order.
func NodeRefs(e Expr) []string {
	var out []string
	seen := map[string]bool{}
	walk(e, func(x Expr) {
		if n, ok := x.(*NodeRef); ok && !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n.ID)
		}
	})
	return out
}

// RenameNodeRefs rewrites every node reference in e through rename, in place.
// A rename that returns "" leaves that reference alone. Callers get the new
// source back from e.String(), which re-parenthesizes as needed — so a
// rename never has to be done on the raw text, where a node id could also
// appear inside a string literal or a flag name.
func RenameNodeRefs(e Expr, rename func(string) string) {
	walk(e, func(x Expr) {
		if n, ok := x.(*NodeRef); ok {
			if next := rename(n.ID); next != "" {
				n.ID = next
			}
		}
	})
}

// PruneNodeRefs returns e without the node references drop reports, or nil
// when nothing meaningful is left. Deleting a node uses it to repair the
// expressions that mentioned it instead of refusing the deletion.
//
// A boolean operator loses the dropped side and keeps the other; `not X` and a
// flag comparison disappear with their operand, because neither has a sensible
// reading once the referenced node is gone.
func PruneNodeRefs(e Expr, drop func(string) bool) Expr {
	switch v := e.(type) {
	case nil:
		return nil
	case *NodeRef:
		if drop(v.ID) {
			return nil
		}
		return v
	case *Unary:
		inner := PruneNodeRefs(v.X, drop)
		if inner == nil {
			return nil
		}
		v.X = inner
		return v
	case *Binary:
		left := PruneNodeRefs(v.L, drop)
		right := PruneNodeRefs(v.R, drop)
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		v.L, v.R = left, right
		return v
	}
	return e
}

// FlagRefs returns the distinct flag names referenced in the expression.
func FlagRefs(e Expr) []string {
	var out []string
	seen := map[string]bool{}
	walk(e, func(x Expr) {
		if f, ok := x.(*FlagCmp); ok && !seen[f.Name] {
			seen[f.Name] = true
			out = append(out, f.Name)
		}
	})
	return out
}

func walk(e Expr, fn func(Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch v := e.(type) {
	case *Unary:
		walk(v.X, fn)
	case *Binary:
		walk(v.L, fn)
		walk(v.R, fn)
	}
}

// ParseError is a syntax error with a byte offset into the source string.
type ParseError struct {
	Offset int    `json:"offset"`
	Msg    string `json:"msg"`
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg)
}

func errf(off int, format string, args ...any) *ParseError {
	return &ParseError{Offset: off, Msg: fmt.Sprintf(format, args...)}
}

func isKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "and", "or", "not", "xor", "nand", "nor", "all", "any", "flag", "true", "false":
		return true
	}
	return false
}
