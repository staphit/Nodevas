package dsl

// Context supplies runtime state for gate evaluation.
type Context struct {
	// Done reports whether a node is completed.
	Done func(nodeID string) bool
	// Flags holds current flag values (bool, float64/int, or string).
	Flags map[string]any
}

// Eval evaluates an expression. A nil expression is vacuously true
// (absent requires = no constraint).
func Eval(e Expr, ctx Context) bool {
	if e == nil {
		return true
	}
	switch v := e.(type) {
	case *NodeRef:
		return ctx.Done != nil && ctx.Done(v.ID)
	case *FlagCmp:
		return evalFlag(v, ctx)
	case *Unary:
		return !Eval(v.X, ctx)
	case *Binary:
		l, r := Eval(v.L, ctx), Eval(v.R, ctx)
		switch v.Op {
		case "and":
			return l && r
		case "or":
			return l || r
		case "xor":
			return l != r
		case "nand":
			return !(l && r)
		case "nor":
			return !(l || r)
		}
	}
	return false
}

func evalFlag(f *FlagCmp, ctx Context) bool {
	val, ok := ctx.Flags[f.Name]
	if f.Op == "" {
		return ok && truthy(val)
	}
	if !ok {
		return false
	}
	if f.Val.IsNum {
		n, isNum := asNumber(val)
		if !isNum {
			return false
		}
		return cmpNum(n, f.Op, f.Val.Num)
	}
	if f.Val.IsBool {
		b, isBool := val.(bool)
		if !isBool {
			return false
		}
		switch f.Op {
		case "==":
			return b == f.Val.Bool
		case "!=":
			return b != f.Val.Bool
		}
		return false
	}
	s, isStr := val.(string)
	if !isStr {
		return false
	}
	switch f.Op {
	case "==":
		return s == f.Val.Str
	case "!=":
		return s != f.Val.Str
	}
	return false
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	default:
		if n, ok := asNumber(v); ok {
			return n != 0
		}
	}
	return v != nil
}

func asNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

func cmpNum(a float64, op string, b float64) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case "<":
		return a < b
	}
	return false
}
