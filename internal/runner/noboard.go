package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"nodevas/internal/runnerwire"
)

// markNoBoard marks a global agent's worktree as having no board
// (runnerwire/noboard.go) while its agent runs: a `nodevas mcp` that the
// agent's CLI starts from the owner's own config, in the worktree but with
// a cleared environment (Codex), finds the mark and refuses to serve. The
// mark lives in the worktree's own git directory, never in the checkout, so
// it is neither the agent's to commit nor in the run's changes. The returned
// func takes it away; a mark left behind (a runner that died) only keeps
// the owner's `nodevas mcp` out of that worktree, and says how to remove it.
func markNoBoard(wt Worktree, runID string) (func(), error) {
	gitDir, _, _ := strings.Cut(wt.GitDirs, "\n")
	if gitDir = strings.TrimSpace(gitDir); gitDir == "" {
		return nil, errors.New("the worktree's git directory is unknown")
	}
	path := filepath.Join(gitDir, runnerwire.NoBoardMarker)
	if err := os.WriteFile(path, []byte(runID), 0o600); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}
