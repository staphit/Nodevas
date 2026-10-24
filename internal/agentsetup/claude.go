package agentsetup

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

// claudeAllowed are the collaboration tools Claude Code may call without a
// prompt in the terminal: ask_user already puts its question in front of a
// person (in the Nodevas UI), and the other three only read or send mail.
// Board writes keep Claude Code's own permission prompts.
var claudeAllowed = []string{
	"mcp__" + ServerName + "__ask_user",
	"mcp__" + ServerName + "__check_inbox",
	"mcp__" + ServerName + "__list_team",
	"mcp__" + ServerName + "__send_message",
}

// claude wires Claude Code: hooks and permissions in ~/.claude/settings.json,
// and the MCP server through its own CLI (`claude mcp add --scope user`),
// which owns ~/.claude.json.
func (s *setup) claude() error {
	path := filepath.Join(s.env.claudeHome(), "settings.json")
	doc, existed, err := readJSON(path)
	if err != nil {
		return err
	}
	groups := []hookGroup{
		{"SessionStart", s.commandGroup(EventSessionStart, "", "", 10)},
		{"UserPromptSubmit", s.commandGroup(EventUserPromptSubmit, "", "", 10)},
		{"PostToolUse", s.commandGroup(EventPostToolUse, "*", "", 5)},
		{"Stop", s.commandGroup(EventStop, "", "", 10)},
		{"SessionEnd", s.commandGroup(EventSessionEnd, "", "", 3)},
	}
	if err := s.mergeHooksInto(doc, groups); err != nil {
		return err
	}
	if err := s.claudeAllow(doc); err != nil {
		return err
	}
	did, err := s.writeJSON(path, doc, existed)
	if err != nil {
		return err
	}
	s.changed(path, did)
	return s.claudeMCP()
}

// claudeAllow adds (or removes) claudeAllowed in permissions.allow.
func (s *setup) claudeAllow(doc *object) error {
	return s.mergeAllow(doc, claudeAllowed,
		"permissions.allow is not a list; left alone, so Claude Code asks before ask_user")
}

// mergeAllow adds (or, with Remove, removes) rules in doc's permissions.allow,
// leaving no empty list or object behind that the person did not have. A
// permissions.allow that is not a list is left alone, with notList as a note.
func (s *setup) mergeAllow(doc *object, rules []string, notList string) error {
	_, hadPerms := doc.get("permissions")
	perms, err := doc.child("permissions")
	if err != nil {
		return err
	}
	permsHadKeys := len(perms.keys) > 0
	defer func() {
		if len(perms.keys) == 0 && (!hadPerms || permsHadKeys) {
			doc.del("permissions")
		}
	}()
	raw, hadAllow := perms.get("allow")
	allow, isList := raw.([]any)
	if hadAllow && !isList {
		s.note("%s", notList)
		return nil
	}
	kept := []any{}
	for _, v := range allow {
		rule, _ := v.(string)
		if !s.opts.Remove || !slices.Contains(rules, rule) {
			kept = append(kept, v)
		}
	}
	if !s.opts.Remove {
		for _, rule := range rules {
			if !slices.Contains(kept, any(rule)) {
				kept = append(kept, rule)
			}
		}
	}
	switch {
	case len(kept) > 0:
		perms.set("allow", kept)
	case !hadAllow || len(allow) > 0:
		perms.del("allow")
	}
	return nil
}

// claudeMCP registers `nodevas mcp` as a user-scope MCP server, replacing an
// entry of that name that runs something else or with other arguments.
func (s *setup) claudeMCP() error {
	var bin string
	err := errors.New("no LookPath")
	if s.env.LookPath != nil && s.env.Run != nil {
		bin, err = s.env.LookPath("claude")
	}
	if err != nil {
		if !s.opts.Remove {
			s.note("claude CLI not found: add the MCP server yourself with `claude mcp add --scope user %s -- %s`",
				ServerName, strings.Join(append([]string{s.env.Exe}, s.mcpArgs()...), " "))
		}
		return nil
	}
	out, getErr := s.env.Run(bin, "mcp", "get", ServerName)
	existing := getErr == nil
	current := existing && s.claudeMCPCurrent(out)
	if existing && (s.opts.Remove || !current) {
		if !s.opts.DryRun {
			if _, err := s.env.Run(bin, "mcp", "remove", ServerName, "--scope", "user"); err != nil {
				return err
			}
		}
		if s.opts.Remove {
			s.line("removed the %s MCP server", ServerName)
			return nil
		}
	}
	if s.opts.Remove {
		s.line("no %s MCP server to remove", ServerName)
		return nil
	}
	if current {
		s.line("%s MCP server already set", ServerName)
		return nil
	}
	if !s.opts.DryRun {
		args := append([]string{"mcp", "add", "--scope", "user", ServerName, "--", s.env.Exe}, s.mcpArgs()...)
		if _, err := s.env.Run(bin, args...); err != nil {
			return err
		}
	}
	verb := "added"
	if existing {
		verb = "replaced"
	}
	if s.opts.DryRun {
		verb = "would have " + verb
	}
	s.line("%s the %s MCP server (user scope)", verb, ServerName)
	return nil
}

// claudeMCPCurrent reports whether `claude mcp get` output describes exactly
// our command line.
func (s *setup) claudeMCPCurrent(out string) bool {
	if !mentionsExe(out, s.env.Exe) {
		return false
	}
	return strings.Contains(out, strings.Join(s.mcpArgs(), " "))
}
