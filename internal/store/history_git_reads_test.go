package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The history panels only read the repository, so they must not queue behind
// a background commit: under a steady stream of saves the commits run back to
// back, and a Git tab or a node's History tab that waits for each one in turn
// takes seconds to open.
func TestGitHistoryReadsDoNotWaitForACommit(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	saveNode(t, s, ann, "alpha", "alpha v2\n")
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	head, err := repoOf(t, root).Run(context.Background(), nil, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sha := string(bytes.TrimSpace(head))

	g := s.history().(*gitHistory)
	// A commit in progress holds gitMu for as long as git takes.
	g.gitMu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			g.gitMu.Unlock()
		}
	}
	t.Cleanup(release)

	reads := map[string]func() error{
		"workspace git": func() error { _, err := s.HistoryGit(context.Background()); return err },
		"commit patch": func() error {
			_, err := s.HistoryCommitPatch(context.Background(), sha)
			return err
		},
		"node history": func() error { _, err := s.ListHistory("nodes/alpha.md"); return err },
		"node version": func() error { _, err := s.ReadHistory("nodes/alpha.md", sha); return err },
	}
	for name, read := range reads {
		done := make(chan error, 1)
		go func() { done <- read() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			release()
			<-done
			t.Fatalf("%s waited for the commit in progress", name)
		}
	}
}

// The Git tab's status must not take git's index lock: a commit starting
// meanwhile would find index.lock taken and fail. So it never rewrites the
// index, even when a stat-only change would have it refresh the entry.
func TestWorkspaceGitStatusLeavesTheIndexAlone(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	index := filepath.Join(root, DataDir, "git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	// Same content, new mtime: an ordinary status would refresh the index.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "nodes", "beta.md"), later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HistoryGit(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading the workspace's git status rewrote the index")
	}
}
