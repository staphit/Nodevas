package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A history commit moves the branch by replacing its ref file. On Windows a
// git that reads the ref just then finds no branch at all and stops with
// "your current branch 'main' does not have any commits yet": the Git tab's
// request failed with a 500 about once in 1,600 under a load of saves. The
// read is retried instead: a ref missing for a moment is back by then.
func TestWorkspaceGitRidesOutABranchRefBeingReplaced(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "alpha v2\n")
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(root, DataDir, "git", "refs", "heads", "main")
	if _, err := os.Stat(ref); err != nil {
		t.Skipf("branch ref is not a loose file here: %v", err)
	}
	aside := ref + ".aside"
	if err := os.Rename(ref, aside); err != nil {
		t.Fatal(err)
	}
	restored := make(chan struct{})
	go func() {
		defer close(restored)
		time.Sleep(40 * time.Millisecond)
		_ = os.Rename(aside, ref)
	}()
	t.Cleanup(func() { <-restored })

	out, err := s.HistoryGit(context.Background())
	if err != nil {
		t.Fatalf("Git tab read while the branch ref was being replaced: %v", err)
	}
	if len(out.Commits) == 0 {
		t.Fatalf("Git tab read while the branch ref was being replaced listed no commits")
	}
}
