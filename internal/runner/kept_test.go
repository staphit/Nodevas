package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A worktree kept because its commit left agent config out stays until a
// person removes it, and they pile up. The runner lists them, and removes on
// request only the ones it kept itself whose only uncommitted change is that
// config: never one a person changed, one whose .git now points elsewhere,
// or any other worktree, and never on its own.
func TestKeptWorktreesAreListedAndPrunedOnlyWhenSafe(t *testing.T) {
	ctx := context.Background()
	env := startJobRunner(t, nil)
	env.hub.hello()
	dataDir := env.client.Config.DataDir
	ids := []string{"run-k1", "run-k2", "run-k3", "run-k4"}
	for _, id := range ids {
		env.hub.offer(testJob(id, "repoconfig: add an MCP server"))
		if f := env.hub.wait(id, runnerwire.TypeFinish); f.Result == nil || f.Result.Status != runnerwire.RunSucceeded {
			t.Fatalf("%s: result %+v", id, f.Result)
		}
	}
	path := func(id string) string { return filepath.Join(dataDir, "worktrees", id) }

	kept, err := KeptWorktrees(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, k := range kept {
		listed = append(listed, k.RunID)
		if !samePath(k.Path, path(k.RunID)) || !slices.Contains(k.Withheld, ".codex/config.toml") {
			t.Errorf("kept %+v", k)
		}
	}
	if slices.Sort(listed); !slices.Equal(listed, ids) {
		t.Fatalf("kept worktrees listed: %v, want %v", listed, ids)
	}

	// k2: the person added a file. k3: the person changed a tracked file.
	// k4: its .git now points somewhere else.
	if err := os.WriteFile(filepath.Join(path("run-k2"), "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path("run-k3"), "README"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path("run-k4"), ".git"), []byte("gitdir: "+filepath.ToSlash(t.TempDir())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A worktree the runner did not keep for withheld config.
	other, err := CreateWorktree(ctx, env.repo, dataDir, "run-other", "n-1", "main")
	if err != nil {
		t.Fatal(err)
	}

	// A dry run removes nothing.
	report, err := PruneKept(ctx, dataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) != 1 || report.Removed[0].RunID != "run-k1" {
		t.Fatalf("dry run would remove %+v, want run-k1 only", report.Removed)
	}
	if _, err := os.Stat(path("run-k1")); err != nil {
		t.Fatalf("a dry run removed run-k1: %v", err)
	}

	report, err = PruneKept(ctx, dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) != 1 || report.Removed[0].RunID != "run-k1" {
		t.Fatalf("removed %+v, want run-k1 only", report.Removed)
	}
	if _, err := os.Stat(path("run-k1")); !os.IsNotExist(err) {
		t.Fatalf("run-k1 is still there: %v", err)
	}
	// Its branch is left alone.
	gitT(t, env.repo, "rev-parse", "--verify", "refs/heads/"+BranchName("n-1", "run-k1"))

	reasons := map[string]string{}
	for _, s := range report.Skipped {
		reasons[s.RunID] = s.Reason
	}
	for id, want := range map[string]string{"run-k2": "notes.txt", "run-k3": "README", "run-k4": ".git"} {
		if !strings.Contains(reasons[id], want) {
			t.Errorf("%s: skipped with %q, want a reason naming %s", id, reasons[id], want)
		}
		if _, err := os.Stat(path(id)); err != nil {
			t.Errorf("%s was removed: %v", id, err)
		}
	}
	if _, err := os.Stat(other.Path); err != nil {
		t.Errorf("a worktree the runner did not keep was removed: %v", err)
	}
	if kept, _ = KeptWorktrees(dataDir); len(kept) != 3 {
		t.Errorf("kept after pruning: %+v", kept)
	}
}
