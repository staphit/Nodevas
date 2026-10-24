package adapter

import "nodevas/internal/runnerwire"

// Repository-local agent config in read-only runs.
//
// A review checks out a pull request's head and a plan reads the repository
// at a base a collaborator may have pushed to: what is in the worktree is not
// the owner's. Every agent CLI also reads config from the directory it runs
// in, and some of that config runs programs as the owner: hooks, plugins, MCP
// servers, tool discovery commands, a .env that points the CLI's API at
// another host. So a read-only run must not load the worktree's own config,
// while the run's nodevas MCP server, which the runner supplies, keeps
// working.
//
// Where a CLI has a real switch, the adapter uses it:
//   - Claude Code: --setting-sources user (no project or local settings, so no
//     project hooks or plugins) and --strict-mcp-config (no .mcp.json).
//   - OpenCode: OPENCODE_DISABLE_PROJECT_CONFIG=1 (no project opencode.json
//     or .opencode/ in its config loader, and no dependency install into the
//     worktree's .opencode/), plus "lsp": false and "formatter": false.
//
// Where a CLI has none, or its switch does not cover everything, the runner
// removes the paths RepoConfigPaths lists from the worktree's root before the
// agent starts, and again after it exits, before anything else runs there
// (see internal/runner/worktree_repoconfig.go). The run is read only, so
// those files are never part of a commit; the worktree is thrown away after.
// Checked against the real CLIs:
//   - OpenCode 1.18.23: its newer config loader (@opencode/v2/Config) walks up
//     from the run's directory for .opencode/ and opencode.json(c) and imports
//     their plugins whatever OPENCODE_DISABLE_PROJECT_CONFIG or OPENCODE_PURE
//     say; a plugin planted in either ran at the start of `opencode run`.
//   - Gemini CLI 0.65.0: headless, it refuses to start in a folder it does not
//     trust (exit 55), so a run must trust the worktree
//     (GEMINI_CLI_TRUST_WORKSPACE=true), and a trusted workspace's
//     .gemini/settings.json runs its hooks and tools.discoveryCommand and
//     defines MCP servers (one named nodevas replaced the run's); .gemini/.env
//     and .env load into the CLI's environment. Its system settings override
//     (GEMINI_CLI_SYSTEM_SETTINGS_PATH) is skipped unless an administrator
//     owns the file, so a read-only run's nodevas server goes in the
//     worktree's .gemini/settings.json, written after the repository's is
//     removed (RunWrittenRepoConfig).
//   - Antigravity CLI (agy) 1.4.7: no switch; the workspace's .agents/hooks.json
//     hooks and .agents/mcp_config.json servers ran.
//   - Codex 0.156.0: no switch. A project's .codex/config.toml loads when the
//     project is trusted, and a run's worktree inherits the trust the owner
//     gave the main checkout; -c cannot take that trust away. A planted MCP
//     server ran. (Project hooks additionally need per-hook trust.)
//
// Implement and revise runs work on the owner's own branches, as trusted as
// the repository itself, and keep its config.

// ReadOnly reports whether job is a read-only run: a review or a plan. Its
// worktree is thrown away after, and its agent is told to change nothing.
func ReadOnly(job runnerwire.Job) bool {
	return job.Kind == runnerwire.JobReview || job.Kind == runnerwire.JobPlan
}

// repoConfigPaths is every path, relative to the worktree's root and in
// slash form, where one of the agent CLIs reads repository-local config that
// can run a program or add tools. Every CLI's are removed from every
// read-only run, whichever agent it runs: one CLI may start another (a Codex
// review may run commands in its read-only sandbox).
var repoConfigPaths = []string{
	// Claude Code (also switched off by --setting-sources user and
	// --strict-mcp-config).
	".claude/settings.json",
	".claude/settings.local.json",
	".mcp.json",
	// OpenCode: config, plugins (.opencode/plugin(s)/), commands, agents,
	// tools; the TUI's own config, which lists plugins too.
	"opencode.json",
	"opencode.jsonc",
	".opencode",
	"tui.json",
	"tui.jsonc",
	// Gemini CLI: settings (MCP servers, hooks, tool discovery and call
	// commands), commands, policies, agents, extensions, .env; .agents/ for
	// its skills. The Antigravity CLI: customization roots (.agents/ with
	// mcp_config.json, hooks.json, rules, workflows, skills, plugins; the
	// older _agents/ and .agent/).
	".gemini",
	".agents",
	"_agents",
	".agent",
	".env",
	// Codex: project config.toml (MCP servers, model providers), hooks, rules.
	".codex",
}

// RepoConfigPaths is what the runner removes from a read-only run's worktree
// before its agent starts and after it exits.
func RepoConfigPaths() []string {
	return append([]string(nil), repoConfigPaths...)
}

// RunWrittenRepoConfig is what kind's adapter itself writes into a read-only
// run's worktree (one of RepoConfigPaths): the run's own config, which the
// runner expects to find there after the run and removes like the rest.
func RunWrittenRepoConfig(kind string) []string {
	if kind == "gemini" {
		return []string{".gemini"}
	}
	return nil
}
