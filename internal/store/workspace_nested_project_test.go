package store

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// nestedProjects puts two projects inside root: hr right below it, and ops
// in a plain folder, team. Each is a project of its own, with its own
// members (internal/members), and its own Files tab.
func nestedProjects(t *testing.T, root string) {
	t.Helper()
	workspaceFile(t, root, "hr/graph.yaml", []byte("version: 1\nnodes:\n  - id: pay\n    title: Salaries\n"))
	workspaceFile(t, root, "hr/nodes/pay.md", []byte("SALARY-TABLE\n"))
	workspaceFile(t, root, "team/notes.txt", []byte("team notes\n"))
	workspaceFile(t, root, "team/ops/graph.yaml", []byte("version: 1\n"))
	workspaceFile(t, root, "team/ops/nodes/runbook.md", []byte("OPS-RUNBOOK\n"))
	workspaceFile(t, root, "docs/notes.txt", []byte("notes\n"))
}

// The Files tab is a project's own files: a folder below it that holds a
// graph.yaml is another project, which a reader of this one may not be
// allowed to read, so it is neither listed nor read through this one.
func TestWorkspaceFilesLeaveNestedProjectsOut(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	nestedProjects(t, root)

	listing, err := s.WorkspaceFiles()
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, entry := range listing.Entries {
		listed[entry.Path] = true
		if entry.Path == "hr" || strings.HasPrefix(entry.Path, "hr/") || entry.Path == "team/ops" || strings.HasPrefix(entry.Path, "team/ops/") {
			t.Errorf("listed %s, inside a nested project", entry.Path)
		}
	}
	for _, want := range []string{"docs", "docs/notes.txt", "team", "team/notes.txt"} {
		if !listed[want] {
			t.Errorf("%s is missing from the listing", want)
		}
	}

	refused := []string{"hr/nodes/pay.md", "hr/graph.yaml", "team/ops/graph.yaml", "team/ops/nodes/runbook.md"}
	if runtime.GOOS == "windows" {
		// Other spellings of the same folders.
		refused = append(refused, "HR/nodes/pay.md", "hr./nodes/pay.md", "team/OPS/nodes/runbook.md")
	}
	for _, rel := range refused {
		file, err := s.ReadWorkspaceFile(rel)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("read %s = %v, want not found", rel, err)
		}
		if strings.Contains(file.Content, "SALARY") || strings.Contains(file.Content, "RUNBOOK") {
			t.Errorf("read %s returned a nested project's file: %q", rel, file.Content)
		}
	}
	if file, err := s.ReadWorkspaceFile("team/notes.txt"); err != nil || file.Content != "team notes\n" {
		t.Fatalf("read team/notes.txt = %+v, %v", file, err)
	}
}

// The Git tab's pending changes leave nested projects out the same way: git
// sees their folders as untracked files of this project.
func TestWorkspaceGitLeavesNestedProjectsOut(t *testing.T) {
	s, root := newGitStore(t, time.Hour)
	nestedProjects(t, root)
	out, err := s.HistoryGit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pending := map[string]bool{}
	for _, entry := range out.Pending {
		pending[entry.Path] = true
		if strings.HasPrefix(entry.Path, "hr") || strings.HasPrefix(entry.Path, "team/ops") {
			t.Errorf("pending lists %s, a nested project", entry.Path)
		}
	}
	if !pending["docs/"] && !pending["docs/notes.txt"] {
		t.Errorf("pending = %+v, want this project's own docs folder", out.Pending)
	}
}
