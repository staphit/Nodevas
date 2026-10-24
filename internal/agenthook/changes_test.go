package agenthook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/engine"
	agentsapi "nodevas/internal/httpapi/agents"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"

	"net/http/httptest"
)

func TestEditedFiles(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: src/a.go\n@@\n-x\n+y\n*** Add File: docs/new.md\n+hi\n*** Delete File: old.txt\n*** End Patch\n"
	cases := []struct {
		name, tool string
		input      map[string]any
		want       []string
	}{
		{"claude edit", "Edit", map[string]any{"file_path": "/w/a.go", "old_string": "x"}, []string{"/w/a.go"}},
		{"claude write", "Write", map[string]any{"file_path": "b.go"}, []string{"b.go"}},
		{"claude multiedit", "MultiEdit", map[string]any{"file_path": "/w/c.go", "edits": []any{}}, []string{"/w/c.go"}},
		{"claude notebook", "NotebookEdit", map[string]any{"notebook_path": "/w/n.ipynb"}, []string{"/w/n.ipynb"}},
		{"gemini write", "write_file", map[string]any{"file_path": "/w/g.go", "content": "x"}, []string{"/w/g.go"}},
		{"gemini replace", "replace", map[string]any{"file_path": "/w/r.go"}, []string{"/w/r.go"}},
		{"codex patch", "apply_patch", map[string]any{"command": []any{"apply_patch", patch}}, []string{"src/a.go", "docs/new.md", "old.txt"}},
		{"opencode edit", "edit", map[string]any{"filePath": "/w/o.go", "oldString": "x"}, []string{"/w/o.go"}},
		{"opencode write", "write", map[string]any{"filePath": "/w/w.go", "content": "x"}, []string{"/w/w.go"}},
		{"opencode patch", "apply_patch", map[string]any{"patchText": patch}, []string{"src/a.go", "docs/new.md", "old.txt"}},
		{"antigravity write", "write_to_file", map[string]any{"TargetFile": "C:/w/t.go", "CodeContent": "x"}, []string{"C:/w/t.go"}},
		{"antigravity replace", "replace_file_content", map[string]any{"TargetFile": "/w/rf.go"}, []string{"/w/rf.go"}},
		{"antigravity multi", "multi_replace_file_content", map[string]any{"TargetFile": "/w/m.go"}, []string{"/w/m.go"}},
		{"antigravity stream write", "write_file", map[string]any{"Path": "/w/p.go"}, []string{"/w/p.go"}},
		{"antigravity read", "read_file", map[string]any{"Path": "/w/a.go"}, nil},
		{"bash", "Bash", map[string]any{"command": "rm -rf /"}, nil},
		{"read", "Read", map[string]any{"file_path": "/w/a.go"}, nil},
	}
	for _, tc := range cases {
		if got := EditedFiles(tc.tool, tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: EditedFiles = %v, want %v", tc.name, got, tc.want)
		}
	}
	in := ParseInput([]byte(`{"session_id":"s","cwd":"/w","tool_name":"Edit","tool_input":{"file_path":"/w/x.go"}}`))
	if in.ToolName != "Edit" || !reflect.DeepEqual(in.Files, []string{"/w/x.go"}) {
		t.Fatalf("ParseInput = %+v", in)
	}
}

// gitRepo makes a repository with one commit and returns its directory.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.test",
			"-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.go"), "package a\n\nvar x = 1\n")
	write(t, filepath.Join(dir, "keep.txt"), "keep\n")
	write(t, filepath.Join(dir, ".gitignore"), "ignored.log\n")
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepoRelative(t *testing.T) {
	repo := gitRepo(t)
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	got := RepoRelative(context.Background(), sub, []string{filepath.Join(repo, "a.go"), "b.go", outside})
	want := []string{"a.go", "pkg/b.go", filepath.ToSlash(filepath.Clean(outside))}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepoRelative = %v, want %v", got, want)
	}
}

func TestSnapshotLeavesTheIndexAlone(t *testing.T) {
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "a.go"), "package a\n\nvar x = 2\nvar y = 3\n")
	write(t, filepath.Join(repo, "new", "b.go"), "package b\n")
	write(t, filepath.Join(repo, "ignored.log"), "noise\n")
	if err := os.Remove(filepath.Join(repo, "keep.txt")); err != nil {
		t.Fatal(err)
	}

	snap, ok := Snapshot(context.Background(), repo)
	if !ok {
		t.Fatal("no snapshot")
	}
	byPath := map[string]runnerwire.FileChange{}
	for _, f := range snap.Files {
		byPath[f.Path] = f
	}
	if len(snap.Files) != 3 || byPath["a.go"].Status != runnerwire.FileModified ||
		byPath["new/b.go"].Status != runnerwire.FileAdded || byPath["keep.txt"].Status != runnerwire.FileDeleted {
		t.Fatalf("files = %+v", snap.Files)
	}
	if byPath["a.go"].Additions != 2 || byPath["a.go"].Deletions != 1 || snap.Additions != 3 || snap.Deletions != 2 {
		t.Fatalf("counts = %+v (total +%d -%d)", byPath["a.go"], snap.Additions, snap.Deletions)
	}
	if len(snap.Base) < 40 || !strings.Contains(snap.Patch, "+var y = 3") || !strings.Contains(snap.Patch, "new/b.go") ||
		snap.PatchTruncated || snap.Commit != "" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// The session's own index is untouched: nothing staged, the new file
	// still untracked.
	staged, _ := exec.Command("git", "-C", repo, "diff", "--cached", "--name-only").Output()
	untracked, _ := exec.Command("git", "-C", repo, "ls-files", "--others", "--exclude-standard").Output()
	if strings.TrimSpace(string(staged)) != "" || strings.TrimSpace(string(untracked)) != "new/b.go" {
		t.Fatalf("index changed: staged %q, untracked %q", staged, untracked)
	}

	if _, ok := Snapshot(context.Background(), t.TempDir()); ok {
		t.Fatal("a snapshot outside any repository")
	}
}

// hookServer is the agents API over a workspace with node n1.
func hookServer(t *testing.T) (*httptest.Server, *agents.Hub, *project.ProjectManager) {
	t.Helper()
	pm, err := project.NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.StopWatcher)
	if _, err := pm.Store().CreateNode(&engine.Node{ID: "n1", Title: "Fix it"}, ""); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	hub := agents.NewHub(nil, nil)
	router := gin.New()
	agentsapi.New(pm, hub).Register(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, hub, pm
}

func TestHookReportsChangesOnlyWhileANodeIsClaimed(t *testing.T) {
	// The snapshot shares one git budget across several calls; on a loaded
	// test machine 4s runs out before the patch, which the hook then marks
	// truncated (as it should). This test is about what is recorded, not
	// the budget.
	saved := gitTimeout
	gitTimeout = time.Minute
	t.Cleanup(func() { gitTimeout = saved })
	repo := gitRepo(t)
	srv, hub, pm := hookServer(t)
	ctx := context.Background()
	ascii := false
	opts := Options{Server: srv.URL, Actor: "mcp:alice", HTTPClient: srv.Client(), Windows: &ascii}
	edit := func(file string) Input {
		return Input{SessionID: "s1", Cwd: repo, ToolName: "Edit", Files: []string{file}}
	}
	list := func() []runnerwire.SessionChange {
		t.Helper()
		// Records are filed under the project's name below the workspace.
		name, err := filepath.Rel(pm.Workspace(), pm.Store().Root())
		if err != nil {
			t.Fatal(err)
		}
		out, err := hub.NodeSessionChanges(ctx, filepath.ToSlash(name), "n1")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	Run(ctx, "claude", EventSessionStart, Input{SessionID: "s1", Cwd: repo}, opts)
	Run(ctx, "claude", EventPostToolUse, edit(filepath.Join(repo, "a.go")), opts)
	Run(ctx, "claude", EventStop, Input{SessionID: "s1", Cwd: repo}, opts)
	if got := list(); len(got) != 0 {
		t.Fatalf("recorded without a claim: %+v", got)
	}

	if _, err := pm.Store().ClaimNode(identity.Local, "n1", "mcp:alice", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "claude", EventPostToolUse, edit(filepath.Join(repo, "a.go")), opts)
	Run(ctx, "claude", EventPostToolUse, Input{SessionID: "s1", Cwd: repo, ToolName: "Bash"}, opts)
	write(t, filepath.Join(repo, "a.go"), "package a\n\nvar x = 2\n")
	Run(ctx, "claude", EventPostToolUse, edit("a.go"), opts)
	got := list()
	if len(got) != 1 || !reflect.DeepEqual(got[0].Files, []string{"a.go"}) || got[0].SessionID != "claude:s1" ||
		got[0].Snapshot != nil || got[0].Agent != "claude" {
		t.Fatalf("after edits: %+v", got)
	}

	Run(ctx, "claude", EventStop, Input{SessionID: "s1", Cwd: repo}, opts)
	got = list()
	if len(got) != 1 || got[0].Snapshot == nil || len(got[0].Snapshot.Files) != 1 ||
		got[0].Snapshot.Files[0].Path != "a.go" || !strings.Contains(got[0].Snapshot.Patch, "+var x = 2") {
		t.Fatalf("after stop: %+v", got)
	}
	// Stop leaves the session idle, still on its node.
	s, ok := hub.SessionByID(pm.Store().Root(), "claude:s1")
	if !ok || s.State != agents.StateIdle || s.NodeID != "n1" {
		t.Fatalf("session after stop = %+v %v", s, ok)
	}

	// Released: nothing more is recorded.
	if err := pm.Store().ReleaseClaim("n1", "mcp:alice"); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "claude", EventPostToolUse, edit(filepath.Join(repo, "keep.txt")), opts)
	if got := list(); len(got[0].Files) != 1 {
		t.Fatalf("recorded after the release: %+v", got[0].Files)
	}
}
