package runnerwire

// A global agent's run has no board (global.go), and the runner starts no
// `nodevas mcp` for it. Its agent CLI may still start one of the owner's own:
// Codex and the Antigravity CLI load the MCP servers in the owner's config,
// and a `nodevas mcp` there would work through whatever the hub on this
// machine trusts. So the runner marks the run two ways, and `nodevas mcp`
// refuses to serve when it finds either (cmd/nodevas/mcp.go):
//   - NoBoardEnv in the agent's environment, which most CLIs hand on to the
//     MCP servers they start;
//   - NoBoardMarker in the private git directory of the run's worktree, for
//     a CLI that starts its MCP servers with a cleared environment (Codex)
//     but in its own working directory, the worktree.
//
// The runner removes the marker when the agent has ended: the worktree it
// keeps is the owner's to open with their own tools afterwards.
const (
	// NoBoardEnv holds the global run's id.
	NoBoardEnv = "NODEVAS_NO_BOARD"
	// NoBoardMarker is a file in the worktree's git directory (where the
	// worktree's .git file points) holding the global run's id.
	NoBoardMarker = "nodevas-no-board"
)
