package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/runnerwire"
)

// A global run's worktree is marked while its agent runs, in the git
// directory its .git file names (where `nodevas mcp`, started there by a
// CLI that clears its MCP servers' environment, looks), and the mark goes
// when the agent ends: the kept worktree is the owner's to open. A node's
// run's worktree is never marked.
func TestGlobalRunMarksItsWorktreeWhileItsAgentRuns(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	marker := func(runID string) string {
		wt := filepath.Join(env.client.Config.DataDir, "worktrees", runID)
		return filepath.Join(gitT(t, wt, "rev-parse", "--absolute-git-dir"), runnerwire.NoBoardMarker)
	}
	stop := func(runID string) {
		env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeCancel, RunID: runID, Reason: "done looking"})
		env.hub.wait(runID, runnerwire.TypeFinish)
	}

	env.hub.offer(globalTestJob("run-mark", "sleep:"))
	env.recorded(t, "run-mark", "pid")
	path := marker("run-mark")
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "run-mark" {
		t.Fatalf("marker %s = %q, %v", path, raw, err)
	}
	stop("run-mark")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the marker outlived the agent: %v", err)
	}

	env.hub.offer(testJob("run-node", "sleep:"))
	env.recorded(t, "run-node", "pid")
	if _, err := os.Stat(marker("run-node")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a node's run is marked: %v", err)
	}
	stop("run-node")
}

func TestMarkNoBoardNeedsTheGitDir(t *testing.T) {
	if _, err := markNoBoard(Worktree{Path: t.TempDir()}, "run-1"); err == nil {
		t.Fatal("marked a worktree whose git directory is unknown")
	}
	gitDir := t.TempDir()
	unmark, err := markNoBoard(Worktree{GitDirs: gitDir + "\n" + filepath.Dir(gitDir)}, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(gitDir, runnerwire.NoBoardMarker)
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "run-1" {
		t.Fatalf("marker = %q, %v", raw, err)
	}
	unmark()
	unmark()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("still marked: %v", err)
	}
}
