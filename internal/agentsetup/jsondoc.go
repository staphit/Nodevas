package agentsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// object is a JSON object that remembers its key order. Agent settings files
// are the person's own: rewriting one with its keys sorted would turn a
// three-line change into a diff of the whole file.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *object) set(key string, v any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

func (o *object) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// child returns the object under key, creating it when absent. Anything else
// already there is the person's and is refused rather than replaced.
func (o *object) child(key string) (*object, error) {
	v, ok := o.vals[key]
	if !ok || v == nil {
		c := newObject()
		o.set(key, c)
		return c, nil
	}
	c, isObj := v.(*object)
	if !isObj {
		return nil, fmt.Errorf("%q is not a JSON object; not touching it", key)
	}
	return c, nil
}

// obj builds an object from alternating key, value pairs.
func obj(pairs ...any) *object {
	o := newObject()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// parseDoc reads a settings file's text. Empty text is an empty object; a
// leading byte-order mark (Windows editors write one) is ignored; a root that
// is not an object is an error, so the caller leaves the file alone.
func parseDoc(raw []byte) (*object, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(raw)) == 0 {
		return newObject(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, fmt.Errorf("not plain JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not plain JSON: trailing data after the top-level value")
	}
	root, ok := v.(*object)
	if !ok {
		return nil, errors.New("top-level value is not a JSON object; not touching it")
	}
	return root, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := newObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(key, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		return tok, nil
	}
}

// encodeDoc writes v as two-space indented JSON with a trailing newline,
// without the HTML escaping encoding/json does by default (a hook command
// with `&` in it must stay readable).
func encodeDoc(v any) []byte {
	var b bytes.Buffer
	encodeValue(&b, v, "")
	b.WriteByte('\n')
	return b.Bytes()
}

func encodeValue(b *bytes.Buffer, v any, indent string) {
	inner := indent + "  "
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			b.WriteString(inner)
			encodeString(b, k)
			b.WriteString(": ")
			encodeValue(b, t.vals[k], inner)
			if i < len(t.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range t {
			b.WriteString(inner)
			encodeValue(b, e, inner)
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	case string:
		encodeString(b, t)
	case nil:
		b.WriteString("null")
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(raw)
	}
}

func encodeString(b *bytes.Buffer, s string) {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))
}

// sameJSON reports whether two values encode identically.
func sameJSON(a, b any) bool {
	return bytes.Equal(encodeDoc(a), encodeDoc(b))
}

// hollow reports whether v holds nothing but empty objects and arrays: what a
// removal from a file that never existed leaves behind.
func hollow(v any) bool {
	switch t := v.(type) {
	case *object:
		for _, k := range t.keys {
			if !hollow(t.vals[k]) {
				return false
			}
		}
		return true
	case []any:
		return len(t) == 0
	case nil:
		return true
	}
	return false
}

// hookGroup is one event's entry in a Claude-Code-style hooks map:
// `{"matcher": …, "hooks": [{"type": "command", "command": …, …}]}`.
type hookGroup struct {
	event string
	group *object
}

// mergeHookGroups puts our handler groups into a hooks map (Claude Code,
// Codex and Gemini CLI share the shape). Only handlers isOurs recognises are
// removed — someone may have added theirs to the same group — and an event
// whose one handler of ours is already exactly what we want is left alone, so a
// second run changes nothing. With want empty it only removes.
func mergeHookGroups(hooks *object, want []hookGroup, isOurs func(command string) bool) {
	wanted := map[string]*object{}
	order := []string{}
	for _, g := range want {
		wanted[g.event] = g.group
		order = append(order, g.event)
	}
	mine := func(h any) bool {
		ho, ok := h.(*object)
		if !ok {
			return false
		}
		cmd, _ := ho.vals["command"].(string)
		return cmd != "" && isOurs(cmd)
	}
	settled := map[string]bool{}
	for _, event := range append([]string(nil), hooks.keys...) {
		list, ok := hooks.vals[event].([]any)
		if !ok {
			continue
		}
		ours := 0
		var oursIn *object
		for _, g := range list {
			grp, ok := g.(*object)
			if !ok {
				continue
			}
			inner, _ := grp.vals["hooks"].([]any)
			for _, h := range inner {
				if mine(h) {
					ours++
					oursIn = grp
				}
			}
			if _, has := grp.vals["hooks"]; !has && mine(grp) {
				ours++
				oursIn = nil
			}
		}
		if w := wanted[event]; w != nil && ours == 1 && oursIn != nil && sameJSON(oursIn, w) {
			settled[event] = true
			continue
		}
		if ours == 0 {
			continue
		}
		kept := []any{}
		for _, g := range list {
			grp, ok := g.(*object)
			if !ok {
				kept = append(kept, g)
				continue
			}
			inner, hasInner := grp.vals["hooks"].([]any)
			if !hasInner {
				if !mine(grp) {
					kept = append(kept, g)
				}
				continue
			}
			left := []any{}
			for _, h := range inner {
				if !mine(h) {
					left = append(left, h)
				}
			}
			if len(left) == 0 {
				continue
			}
			grp.set("hooks", left)
			kept = append(kept, g)
		}
		if len(kept) == 0 {
			hooks.del(event)
		} else {
			hooks.set(event, kept)
		}
	}
	for _, event := range order {
		if settled[event] {
			continue
		}
		list, _ := hooks.vals[event].([]any)
		if _, exists := hooks.vals[event]; exists && list == nil {
			// Not a list: the person's own shape, left alone.
			continue
		}
		hooks.set(event, append(list, wanted[event]))
	}
}
