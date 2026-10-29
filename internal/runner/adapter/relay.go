package adapter

// PermissionRelayApplies reports whether the runner's permissionRelay setting
// changes a headless run of an agent kind under permission setting mode (""
// is the adapter's default); acp is OpenCode driven over ACP. Only where a
// tool would otherwise be refused for want of someone to ask, and never
// where the owner's mode is itself the limit:
//   - Claude Code, except in plan and dontAsk (never ask by the owner's say);
//   - the Antigravity CLI in accept-edits: its gate asks instead of denying
//     shell commands (plan stays the owner's limit; auto and full have no gate);
//   - OpenCode over ACP in edit: the shell asks instead of being denied.
//
// Codex, Gemini CLI and `opencode run` have no way to relay a prompt.
func PermissionRelayApplies(kind, mode string, acp bool) bool {
	switch kind {
	case "claude":
		return mode != "plan" && mode != "dontAsk"
	case "antigravity":
		return mode == "" || mode == "accept-edits"
	case "opencode":
		return acp && (mode == "" || mode == DefaultOpenCodePermission)
	}
	return false
}
