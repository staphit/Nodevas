package engine

import (
	"strconv"
	"strings"

	"nodevas/internal/locale"
)

// ApplyEffects executes a node's effect statements against a flag map
// (in place). Supported forms:
//
//	set: name = true | false | 123 | 1.5 | text
//	set: name += 2   (numeric increment; -= too)
//
// Unparseable effects are ignored — effects are authored data, not code.
// Only a journaled done applies them, so an automatic (Start/End) node's
// effects never run; see deriveAutoStatuses.
func ApplyEffects(n *Node, flags map[string]any) {
	if n == nil || flags == nil {
		return
	}
	for _, e := range n.Effects {
		applyEffect(e.Set, flags)
	}
}

// ValidateEffect checks an authored effect without mutating runtime flags.
func ValidateEffect(stmt string) error {
	stmt = strings.TrimSpace(stmt)
	if stmt == "" {
		return locale.Errorf("effect is empty", "效果是空的")
	}
	for _, op := range []string{"+=", "-=", "="} {
		if i := strings.Index(stmt, op); i > 0 {
			name := strings.TrimSpace(stmt[:i])
			value := strings.TrimSpace(stmt[i+len(op):])
			if !ValidNodeID(name) {
				return locale.Errorf("invalid flag name %q", "旗標名稱「%s」無效", name)
			}
			if value == "" {
				return locale.Errorf("effect value is empty", "效果的值是空的")
			}
			if op != "=" {
				if _, err := strconv.ParseFloat(value, 64); err != nil {
					return locale.Errorf("%s requires a numeric value", "%s 需要數值", op)
				}
			}
			return nil
		}
	}
	if !ValidNodeID(stmt) {
		return locale.Errorf("invalid flag name %q", "旗標名稱「%s」無效", stmt)
	}
	return nil
}

func applyEffect(stmt string, flags map[string]any) {
	stmt = strings.TrimSpace(stmt)
	if ValidateEffect(stmt) != nil {
		return
	}
	for _, op := range []string{"+=", "-=", "="} {
		if i := strings.Index(stmt, op); i > 0 {
			name := strings.TrimSpace(stmt[:i])
			val := strings.TrimSpace(stmt[i+len(op):])
			if name == "" || val == "" {
				return
			}
			switch op {
			case "=":
				flags[name] = parseLiteral(val)
			case "+=", "-=":
				delta, err := strconv.ParseFloat(val, 64)
				if err != nil {
					return
				}
				cur, _ := asNumberAny(flags[name])
				if op == "-=" {
					delta = -delta
				}
				flags[name] = cur + delta
			}
			return
		}
	}
	// Bare name: treat as `name = true`.
	flags[stmt] = true
}

func parseLiteral(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n
	}
	return strings.Trim(s, `"'`)
}

func asNumberAny(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}
