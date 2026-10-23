package agentsetup

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// codex wires Codex: the MCP server in config.toml and hooks in hooks.json,
// both under CODEX_HOME (default ~/.codex).
func (s *setup) codex() error {
	dir := s.env.codexHome()
	cfgPath := filepath.Join(dir, "config.toml")
	raw, _, err := readFile(cfgPath)
	if err != nil {
		return err
	}
	block := ""
	if !s.opts.Remove {
		block = s.codexBlock()
	}
	text, err := replaceTOMLTable(string(raw), "mcp_servers."+ServerName, block)
	if err != nil {
		return fmt.Errorf("%s: %w", cfgPath, err)
	}
	if text == string(raw) {
		s.changed(cfgPath, false)
	} else {
		did, err := s.writeFile(cfgPath, []byte(text))
		if err != nil {
			return err
		}
		s.changed(cfgPath, did)
	}

	hooksPath := filepath.Join(dir, "hooks.json")
	doc, existed, err := readJSON(hooksPath)
	if err != nil {
		return err
	}
	groups := []hookGroup{
		{"SessionStart", s.commandGroup(EventSessionStart, "*", "", 10)},
		{"UserPromptSubmit", s.commandGroup(EventUserPromptSubmit, "", "", 10)},
		{"PostToolUse", s.commandGroup(EventPostToolUse, "*", "", 5)},
		{"Stop", s.commandGroup(EventStop, "", "", 10)},
		{"SessionEnd", s.commandGroup(EventSessionEnd, "", "", 3)},
	}
	if err := s.mergeHooksInto(doc, groups); err != nil {
		return err
	}
	did, err := s.writeJSON(hooksPath, doc, existed)
	if err != nil {
		return err
	}
	s.changed(hooksPath, did)
	if did && !s.opts.Remove {
		s.note(`Codex asks once to trust the nodevas hooks: choose "Trust all and continue"`)
	}
	return nil
}

// codexBlock is the [mcp_servers.nodevas] table. tool_timeout_sec leaves room
// for check_inbox and ask_user long-polls (up to ten minutes).
func (s *setup) codexBlock() string {
	args := make([]string, 0, len(s.mcpArgs()))
	for _, a := range s.mcpArgs() {
		args = append(args, tomlString(a))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\n", ServerName)
	fmt.Fprintf(&b, "command = %s\n", tomlString(s.env.Exe))
	fmt.Fprintf(&b, "args = [%s]\n", strings.Join(args, ", "))
	fmt.Fprintf(&b, "env = { NODEVAS_AGENT = %s }\n", tomlString(Codex))
	b.WriteString("tool_timeout_sec = 660\n")
	return b.String()
}

// tomlString is a TOML basic string: backslash, quote and control
// characters escaped, everything else (non-ASCII included) literal.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var tomlHeader = regexp.MustCompile(`^\s*\[(\[?)\s*([^\[\]]+?)\s*\]\]?\s*(#.*)?$`)

// tomlTableName normalises a dotted header name: `mcp_servers . "nodevas"`
// and `mcp_servers.nodevas` are the same table.
func tomlTableName(raw string) string {
	parts := strings.Split(raw, ".")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 2 && (p[0] == '"' && p[len(p)-1] == '"' || p[0] == '\'' && p[len(p)-1] == '\'') {
			p = p[1 : len(p)-1]
		}
		parts[i] = p
	}
	return strings.Join(parts, ".")
}

// replaceTOMLTable replaces table `name` (with its sub-tables) in text with
// block, or removes it when block is empty, and appends block when the table
// is absent. Everything else in the file is kept byte for byte: Codex's
// config is the person's, with their comments and layout. A table defined
// some other way than a `[name]` header (inline, or by dotted keys) is
// refused rather than guessed at.
func replaceTOMLTable(text, name, block string) (string, error) {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
		block = strings.ReplaceAll(block, "\n", "\r\n")
	}
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	parent, leaf, _ := strings.Cut(name, ".")
	inlineInParent := regexp.MustCompile(`^\s*("` + regexp.QuoteMeta(leaf) + `"|'` + regexp.QuoteMeta(leaf) + `'|` + regexp.QuoteMeta(leaf) + `)\s*[.=]`)
	dottedAtTop := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(parent) + `\s*\.\s*("?` + regexp.QuoteMeta(leaf) + `"?)\s*[.=]`)

	type span struct{ start, end int }
	var spans []span
	current := ""   // table the line belongs to
	inOurs := false // inside name or one of its sub-tables
	multi := ""     // open multi-line string delimiter
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if multi != "" {
			if strings.Count(line, multi)%2 == 1 {
				multi = ""
			}
			continue
		}
		for _, d := range []string{`"""`, `'''`} {
			if strings.Count(line, d)%2 == 1 {
				multi = d
			}
		}
		if m := tomlHeader.FindStringSubmatch(trimmed); m != nil && !strings.HasPrefix(trimmed, "#") {
			current = tomlTableName(m[2])
			was := inOurs
			inOurs = current == name || strings.HasPrefix(current, name+".")
			switch {
			case inOurs && !was:
				spans = append(spans, span{start: i, end: len(lines)})
			case !inOurs && was:
				spans[len(spans)-1].end = i
			}
			continue
		}
		if inOurs {
			continue
		}
		if (current == parent && inlineInParent.MatchString(line)) || (current == "" && dottedAtTop.MatchString(line)) {
			return "", fmt.Errorf("[%s] is defined inline on line %d; edit it by hand or move it to a [%s] table", name, i+1, name)
		}
	}

	if len(spans) == 0 {
		if block == "" {
			return text, nil
		}
		out := text
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += newline
		}
		if strings.TrimSpace(out) != "" && !strings.HasSuffix(out, newline+newline) {
			out += newline
		}
		return out + block, nil
	}

	// Each span runs to the next foreign header; the blank lines and comments
	// just before that header belong to it, not to us.
	for k := range spans {
		if spans[k].end == len(lines) {
			continue
		}
		end := spans[k].end
		for end-1 > spans[k].start {
			prev := strings.TrimSpace(lines[end-1])
			if prev != "" && !strings.HasPrefix(prev, "#") {
				break
			}
			end--
		}
		spans[k].end = end
	}

	var b strings.Builder
	cursor := 0
	for k, sp := range spans {
		for _, l := range lines[cursor:sp.start] {
			b.WriteString(l)
		}
		if k == 0 && block != "" {
			b.WriteString(block)
		}
		cursor = sp.end
	}
	for _, l := range lines[cursor:] {
		b.WriteString(l)
	}
	out := b.String()
	if block == "" {
		// Removing leaves no run of blank lines where the table was.
		out = collapseBlankRuns(out, newline)
		if strings.TrimSpace(out) == "" {
			return "", nil
		}
	}
	return out, nil
}

// collapseBlankRuns turns three or more consecutive newlines into two and
// trims blank lines from the end of the file, keeping its final newline.
func collapseBlankRuns(text, newline string) string {
	triple := newline + newline + newline
	for strings.Contains(text, triple) {
		text = strings.ReplaceAll(text, triple, newline+newline)
	}
	for strings.HasSuffix(text, newline+newline) {
		text = strings.TrimSuffix(text, newline)
	}
	return text
}
