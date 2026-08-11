package engine

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// NodeFile is a parsed nodes/<id>.md: YAML frontmatter + markdown body.
// Frontmatter is a convenience snapshot; graph.yaml stays authoritative.
type NodeFile struct {
	Meta map[string]any
	Body string
}

const fmDelim = "---"

// ParseNodeFile splits frontmatter from body. Files without frontmatter
// yield an empty Meta and the whole content as Body.
func ParseNodeFile(data []byte) (*NodeFile, error) {
	s := string(data)
	if !strings.HasPrefix(s, fmDelim+"\n") && !strings.HasPrefix(s, fmDelim+"\r\n") {
		return &NodeFile{Meta: map[string]any{}, Body: s}, nil
	}
	rest := s[len(fmDelim):]
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	// Find closing delimiter at start of a line.
	idx := -1
	for _, marker := range []string{"\n" + fmDelim + "\n", "\n" + fmDelim + "\r\n", "\r\n" + fmDelim + "\r\n", "\r\n" + fmDelim + "\n"} {
		if i := strings.Index(rest, marker); i >= 0 && (idx == -1 || i < idx) {
			idx = i
		}
	}
	if idx == -1 {
		// Also allow file that is nothing but frontmatter.
		trimmed := strings.TrimRight(rest, "\r\n")
		if fmText, ok := strings.CutSuffix(trimmed, "\n"+fmDelim); ok {
			meta := map[string]any{}
			if err := yaml.Unmarshal([]byte(fmText), &meta); err != nil {
				return nil, fmt.Errorf("frontmatter: %w", err)
			}
			return &NodeFile{Meta: meta, Body: ""}, nil
		}
		return nil, fmt.Errorf("frontmatter: missing closing delimiter")
	}
	fmText := rest[:idx]
	body := rest[idx:]
	// Strip the delimiter line, then normalize away leading blank lines —
	// ComposeNodeFile re-adds exactly one, keeping round trips stable.
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, fmDelim)
	body = strings.TrimLeft(body, "\r\n")

	meta := map[string]any{}
	if err := yaml.Unmarshal([]byte(fmText), &meta); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	return &NodeFile{Meta: meta, Body: body}, nil
}

// ComposeNodeFile renders frontmatter + body back to file bytes.
func ComposeNodeFile(nf *NodeFile) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(fmDelim + "\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(nf.Meta); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	buf.WriteString(fmDelim + "\n")
	if body := strings.TrimLeft(nf.Body, "\r\n"); body != "" {
		buf.WriteString("\n")
		buf.WriteString(body)
	}
	return buf.Bytes(), nil
}

// SyncFrontmatter refreshes the snapshot fields in a node file's Meta from
// the authoritative graph definition (design doc §2.3).
func SyncFrontmatter(nf *NodeFile, n *Node) {
	if nf.Meta == nil {
		nf.Meta = map[string]any{}
	}
	nf.Meta["id"] = n.ID
	setOrDelete(nf.Meta, "title", n.Title, n.Title != "")
	setOrDelete(nf.Meta, "kind", n.Kind, n.Kind != "")
	setOrDelete(nf.Meta, "priority", n.Priority, n.Priority != "")
	setOrDelete(nf.Meta, "assignee", n.Assignee, n.Assignee != "")
	setOrDelete(nf.Meta, "requires", n.Requires, n.Requires != "")
	setOrDelete(nf.Meta, "tags", n.Tags, len(n.Tags) > 0)
	if len(n.Effects) > 0 {
		effs := make([]map[string]any, 0, len(n.Effects))
		for _, e := range n.Effects {
			effs = append(effs, map[string]any{"set": e.Set})
		}
		nf.Meta["effects"] = effs
	} else {
		delete(nf.Meta, "effects")
	}
}

func setOrDelete(m map[string]any, key string, val any, keep bool) {
	if keep {
		m[key] = val
	} else {
		delete(m, key)
	}
}
