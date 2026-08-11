package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectOrderRoundTrip(t *testing.T) {
	workspace := t.TempDir()
	want := []string{"Story", "Story/sub", "Game mechanic"}
	if err := SaveProjectOrder(workspace, want); err != nil {
		t.Fatal(err)
	}
	got := LoadProjectOrder(workspace)
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// A later write replaces the list rather than merging into it.
	if err := SaveProjectOrder(workspace, []string{"Story/sub"}); err != nil {
		t.Fatal(err)
	}
	if again := LoadProjectOrder(workspace); len(again) != 1 || again[0] != "Story/sub" {
		t.Errorf("after replace = %v, want [Story/sub]", again)
	}
}

func TestLoadProjectOrderWithoutFile(t *testing.T) {
	workspace := t.TempDir()
	if got := LoadProjectOrder(workspace); len(got) != 0 {
		t.Fatalf("missing file = %v, want empty", got)
	}

	// Garbage on disk is treated the same way: no order, not a broken explorer.
	path := WorkspaceExplorerPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectOrder(workspace); len(got) != 0 {
		t.Fatalf("unreadable file = %v, want empty", got)
	}
}

func TestLoadProjectOrderCapsLength(t *testing.T) {
	workspace := t.TempDir()
	oversized := make([]string, MaxExplorerOrderEntries+10)
	for i := range oversized {
		oversized[i] = "p" + string(rune('a'+i%26))
	}
	if err := SaveProjectOrder(workspace, oversized); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectOrder(workspace); len(got) != MaxExplorerOrderEntries {
		t.Fatalf("len = %d, want %d", len(got), MaxExplorerOrderEntries)
	}
}
