package agentsetup

import "path/filepath"

// antigravityAllowed lets the Antigravity CLI call every nodevas MCP tool
// without asking. Headless (`agy -p`, how a runner runs it) cannot ask, so
// without this rule every nodevas call is denied there.
var antigravityAllowed = []string{"mcp(" + ServerName + "/*)"}

// antigravity wires the Antigravity CLI (agy). It reads its MCP servers and
// hooks from ~/.gemini/config (mcp_config.json, hooks.json) and its
// permission rules from ~/.gemini/antigravity-cli/settings.json. Its hook
// timeouts are in seconds, and it has no session-end event.
func (s *setup) antigravity() error {
	gem := s.env.antigravityGemini()
	if err := s.antigravityMCP(filepath.Join(gem, "config", "mcp_config.json")); err != nil {
		return err
	}
	if err := s.antigravityAllow(filepath.Join(gem, "antigravity-cli", "settings.json")); err != nil {
		return err
	}
	return s.antigravityHooks(filepath.Join(gem, "config", "hooks.json"))
}

// antigravityMCP sets (or removes) mcpServers.nodevas in mcp_config.json. The
// Antigravity CLI's environment says nothing about which agent started the
// server, so NODEVAS_AGENT does.
func (s *setup) antigravityMCP(path string) error {
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
			"env", obj("NODEVAS_AGENT", Antigravity),
		)
		if cur, ok := servers.get(ServerName); !ok || !sameJSON(cur, want) {
			servers.set(ServerName, want)
		}
	}
	if len(servers.keys) == 0 && (!hadServers || serversHadKeys) {
		doc.del("mcpServers")
	}
	did, err := s.writeJSON(path, doc, existed)
	if err != nil {
		return err
	}
	s.changed(path, did)
	return nil
}

// antigravityAllow adds (or removes) antigravityAllowed in the CLI's
// settings.json, which also holds the person's model, theme and trusted
// folders.
func (s *setup) antigravityAllow(path string) error {
	doc, existed, err := readJSON(path)
	if err != nil {
		return err
	}
	if err := s.mergeAllow(doc, antigravityAllowed,
		"permissions.allow is not a list; left alone, so headless agy runs cannot call nodevas"); err != nil {
		return err
	}
	did, err := s.writeJSON(path, doc, existed)
	if err != nil {
		return err
	}
	s.changed(path, did)
	if did && !s.opts.Remove {
		s.note("Antigravity CLI now calls every nodevas MCP tool without asking (%s); headless runs need it", antigravityAllowed[0])
	}
	return nil
}

// antigravityHooks sets (or removes) the hook named "nodevas" in hooks.json.
// The file maps hook names to their events, so the whole entry is ours and
// others (another tool's status hooks) are left as they are.
func (s *setup) antigravityHooks(path string) error {
	doc, existed, err := readJSON(path)
	if err != nil {
		return err
	}
	if s.opts.Remove {
		doc.del(ServerName)
	} else {
		handler := func(event string, timeout int) (*object, error) {
			cmd, err := antigravityCmd(s.env.GOOS, s.env.Exe, append([]string{"hook", Antigravity, event}, s.hookFlags()...))
			if err != nil {
				return nil, err
			}
			return obj("type", "command", "command", cmd, "timeout", timeout), nil
		}
		var handlers [4]*object
		for i, h := range []struct {
			event   string
			timeout int
		}{
			{EventSessionStart, 10},
			// PreInvocation runs before every model call: mail that arrived
			// during a turn reaches the model before its next step.
			{EventUserPromptSubmit, 10},
			{EventPostToolUse, 5},
			{EventStop, 10},
		} {
			if handlers[i], err = handler(h.event, h.timeout); err != nil {
				return err
			}
		}
		want := obj(
			"SessionStart", []any{handlers[0]},
			"PreInvocation", []any{handlers[1]},
			"PostToolUse", []any{obj("matcher", "*", "hooks", []any{handlers[2]})},
			"Stop", []any{handlers[3]},
		)
		if cur, ok := doc.get(ServerName); !ok || !sameJSON(cur, want) {
			doc.set(ServerName, want)
		}
	}
	did, err := s.writeJSON(path, doc, existed)
	if err != nil {
		return err
	}
	s.changed(path, did)
	return nil
}
