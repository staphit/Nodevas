package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/engine"
)

// A project is created and saved again at once (a new project's default
// workflow), before its history repository has opened. The version the save
// replaced was queued for an "earlier version" commit, and the repository's
// first commit took the work tree as it was by then. The earlier version was
// committed after it, so history read: create (with the workflow), then a
// change that took the workflow away again, which nobody made.
func TestGitHistoryCommitsAnEarlierVersionBeforeTheFirstCommit(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)
	g := newGitHistory(s, time.Hour)
	s.historyMu.Lock()
	s.historyImpl = g
	s.historyMu.Unlock()
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	// Held while the project is written: the repository opens after both
	// writes, which is what a slow first open looks like.
	g.gitMu.Lock()
	created, err := engine.MarshalGraph(&engine.Graph{Version: 1, Type: "workflow"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAtomic(s.GraphPath(), created); err != nil {
		t.Fatal(err)
	}
	graph, rev, err := s.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	graph = graph.Clone()
	graph.Workflow = engine.NewProjectWorkflow("workflow")
	if _, err := s.SaveGraph(ann, graph, rev); err != nil {
		t.Fatal(err)
	}
	g.gitMu.Unlock()
	if err := s.FlushHistory(context.Background()); err != nil {
		t.Fatal(err)
	}

	disk, err := os.ReadFile(s.GraphPath())
	if err != nil {
		t.Fatal(err)
	}
	repo := repoOf(t, root)
	out, err := repo.Run(context.Background(), nil, "log", "--reverse", "--format=%H", "--", "graph.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, sha := range strings.Fields(string(out)) {
		data, err := repo.Run(context.Background(), nil, "show", sha+":graph.yaml")
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, string(data))
	}
	if len(versions) != 2 || versions[0] != string(created) || versions[1] != string(disk) {
		log, _ := repo.Run(context.Background(), nil, "log", "--format=%s", "--stat")
		t.Fatalf("graph.yaml has %d versions in history, want 2: as created, then as saved\nhistory:\n%s", len(versions), log)
	}
}

// A commit of earlier versions (a session's start, an outside edit) moves
// HEAD past the work tree's index. Until the next save's commit, the Git tab
// listed a file such a commit added as deleted (and the same file as new),
// though it was on disk all along.
func TestWorkspaceGitListsNoDeletionAfterAnEarlierVersionCommit(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	before := commitCount(t, root)
	if _, err := s.CreateNode(ann, &engine.Node{ID: "gamma", Title: "Gamma"}, "first\n"); err != nil {
		t.Fatal(err)
	}
	// The node's first save after it was created keeps the created version,
	// in a commit of its own, at once.
	saveNode(t, s, ann, "gamma", "second\n")
	eventually(t, "the created version's commit", func() bool { return commitCount(t, root) > before })
	// The branch moves before the work tree's index follows it, in the same
	// background commit; read once that commit has finished (the window is an
	// hour, so it is the only one in flight). Polling the count alone read
	// the index in between under a loaded run.
	s.historyMu.Lock()
	g := s.historyImpl.(*gitHistory)
	s.historyMu.Unlock()
	g.inflight.Wait()

	out, err := s.HistoryGit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, pending := range out.Pending {
		if pending.Mark == "D" {
			t.Fatalf("Git tab lists %s as deleted though it is on disk: %+v", pending.Path, out.Pending)
		}
		if pending.Path == "nodes/gamma.md" && pending.Mark != "M" {
			t.Fatalf("Git tab lists nodes/gamma.md as %q, want M (saved since the commit): %+v", pending.Mark, out.Pending)
		}
	}
}
