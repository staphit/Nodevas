package agentsetup

import "path/filepath"

// gemini wires Gemini CLI: the MCP server and hooks both live in
// .gemini/settings.json under GEMINI_CLI_HOME (default ~). Gemini's hook
// timeouts are in milliseconds.
func (s *setup) gemini() error {
	path := filepath.Join(s.env.geminiHome(), "settings.json")
	doc, existed, err := readJSON(path)
	if err != nil {
		return err
	}
	_, hadServers := doc.get("mcpServers")
	servers, err := doc.child("mcpServers")
	if err != nil {
		return err
	}
	serversHadKeys := len(servers.keys) > 0
	if s.opts.Remove {
		servers.del(ServerName)
	} else {
		args := make([]any, 0, len(s.mcpArgs()))
		for _, a := range s.mcpArgs() {
			args = append(args, a)
		}
		want := obj(
			"command", s.env.Exe,
			"args", args,
			"env", obj("NODEVAS_AGENT", Gemini),
			// Room for check_inbox and ask_user long-polls.
			"timeout", 660000,
			"trust", true,
		)
		if cur, ok := servers.get(ServerName); !ok || !sameJSON(cur, want) {
			servers.set(ServerName, want)
		}
	}
	if len(servers.keys) == 0 && (!hadServers || serversHadKeys) {
		doc.del("mcpServers")
	}

	name := func(event string) string { return ServerName + "-" + event }
	groups := []hookGroup{
		{"SessionStart", s.commandGroup(EventSessionStart, "*", name(EventSessionStart), 10000)},
		{"BeforeAgent", s.commandGroup(EventUserPromptSubmit, "*", name(EventUserPromptSubmit), 10000)},
		{"AfterTool", s.commandGroup(EventPostToolUse, "*", name(EventPostToolUse), 5000)},
		{"AfterAgent", s.commandGroup(EventStop, "*", name(EventStop), 10000)},
		{"SessionEnd", s.commandGroup(EventSessionEnd, "*", name(EventSessionEnd), 3000)},
	}
	if err := s.mergeHooksInto(doc, groups); err != nil {
		return err
	}
	did, err := s.writeJSON(path, doc, existed)
	if err != nil {
		return err
	}
	s.changed(path, did)
	if did && !s.opts.Remove {
		s.note("Gemini CLI only runs nodevas in trusted folders (`/permissions trust`)")
	}
	return nil
}
