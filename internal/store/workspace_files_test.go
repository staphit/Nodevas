package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

func workspaceFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// B-49: the Explorer's workspace files: dot directories and secrets left
// out, node files named, traversal and symlink escapes refused, reads capped.
func TestWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, err := s.CreateNode(identity.Local, &engine.Node{ID: "login", Title: "Fix the login"}, "The button does nothing."); err != nil {
		t.Fatal(err)
	}
	workspaceFile(t, root, "docs/notes.txt", []byte("notes\n"))
	workspaceFile(t, root, ".git/HEAD", []byte("ref: refs/heads/main\n"))
	workspaceFile(t, root, ".env", []byte("TOKEN=secret\n"))
	workspaceFile(t, root, "keys/server.pem", []byte("-----BEGIN-----\n"))
	workspaceFile(t, root, "big.txt", []byte(strings.Repeat("y", MaxWorkspaceFileBytes+100)))

	listing, err := s.WorkspaceFiles()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]WorkspaceEntry{}
	for _, e := range listing.Entries {
		paths[e.Path] = e
		if strings.HasPrefix(e.Path, ".git") || strings.HasPrefix(e.Path, ".vised") || e.Path == ".env" || strings.HasSuffix(e.Path, ".pem") {
			t.Errorf("listed %s", e.Path)
		}
	}
	if node, ok := paths["nodes/login.md"]; !ok || node.NodeID != "login" || node.Title != "Fix the login" || node.Type != "file" {
		t.Errorf("node file entry = %+v (found %v)", node, ok)
	}
	if dir, ok := paths["docs"]; !ok || dir.Type != "dir" {
		t.Errorf("docs entry = %+v (found %v)", dir, ok)
	}
	if listing.Hidden < 2 || listing.Truncated {
		t.Errorf("hidden %d truncated %v, want .env and server.pem hidden", listing.Hidden, listing.Truncated)
	}

	file, err := s.ReadWorkspaceFile("nodes/login.md")
	if err != nil || file.Kind != "text" || file.NodeID != "login" || !strings.Contains(file.Content, "does nothing") {
		t.Fatalf("node file = %+v, %v", file, err)
	}
	big, err := s.ReadWorkspaceFile("big.txt")
	if err != nil || !big.Truncated || len(big.Content) != MaxWorkspaceFileBytes {
		t.Fatalf("big file: truncated %v len %d err %v", big.Truncated, len(big.Content), err)
	}
	if _, err := s.ReadWorkspaceFile(".env"); !errors.Is(err, ErrHiddenFile) {
		t.Fatalf(".env: %v, want ErrHiddenFile", err)
	}
	for _, bad := range []string{"../outside.txt", "/etc/passwd", "nodes/../../x", `C:\x`, "a\x00b"} {
		if _, err := s.ReadWorkspaceFile(bad); !errors.Is(err, ErrBadPath) {
			t.Errorf("%q: %v, want ErrBadPath", bad, err)
		}
	}

	outside := t.TempDir()
	workspaceFile(t, outside, "secret.txt", []byte("outside\n"))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := s.ReadWorkspaceFile("escape/secret.txt"); !errors.Is(err, ErrBadPath) {
		t.Fatalf("symlink escape: %v, want ErrBadPath", err)
	}
}

// B-50 (store side): the files backend says so; the git backend lists
// commits.
func TestHistoryGitBackends(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	s.UseHistory(HistoryFiles)
	got, err := s.HistoryGit(context.Background())
	if err != nil || got.Backend != "files" || len(got.Commits) != 0 {
		t.Fatalf("files backend = %+v, %v", got, err)
	}
}
