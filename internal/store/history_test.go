package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// A snapshot must be readable without restoring it, so the UI can show what a
// version contains before overwriting the current file with it.
func TestStoreReadHistoryReturnsSnapshotContent(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)

	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "nodes", "alpha.md")
	if err := store.WriteVersioned(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteVersioned(path, []byte("second\n")); err != nil {
		t.Fatal(err)
	}

	versions, err := store.ListHistory("nodes/alpha.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) == 0 {
		t.Fatal("the second write should have snapshotted the first")
	}

	data, err := store.ReadHistory("nodes/alpha.md", versions[0].Name)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(data) != "first\n" {
		t.Fatalf("snapshot content = %q, want the previous version", string(data))
	}

	// The current file is untouched by a read.
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "second\n" {
		t.Fatalf("current file = %q, want it left alone", string(current))
	}
}

// Subpages are snapshotted like any other file, so their versions have to be
// listable and restorable too — otherwise the snapshots pile up on disk and
// nobody can get them back.
func TestStoreSubpageHistoryRoundTrip(t *testing.T) {
	root := t.TempDir()
	graph, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)

	page, _, rev, err := store.CreateNodePage(identity.Local, "alpha", "設計稿", PageFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	rev, err = store.SaveNodePage(identity.Local, "alpha", page.ID, "first\n", rev)
	if err != nil {
		t.Fatal(err)
	}
	// The second save lands inside historyCoalesceWindow and overwrites nothing
	// but this store's own previous write, which is what an auto-saving editor
	// does all day. It must not add a version of its own; see snapshotHistory.
	if _, err := store.SaveNodePage(identity.Local, "alpha", page.ID, "second\n", rev); err != nil {
		t.Fatal(err)
	}

	rel := "nodes/alpha.pages/" + page.ID + ".md"
	versions, err := store.ListHistory(rel)
	if err != nil {
		t.Fatalf("list subpage history: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("got %d versions, want one covering the whole editing burst", len(versions))
	}

	// The one snapshot is the version the burst started from — the starter page
	// as created, before any of the edits.
	data, err := store.ReadHistory(rel, versions[0].Name)
	if err != nil {
		t.Fatalf("read subpage snapshot: %v", err)
	}
	if strings.Contains(string(data), "first") || strings.Contains(string(data), "second") {
		t.Fatalf("snapshot content = %q, want the version before the burst", string(data))
	}

	if err := store.RestoreHistory(rel, versions[0].Name); err != nil {
		t.Fatalf("restore subpage: %v", err)
	}
	_, content, _, err := store.LoadNodePage("alpha", page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if content != string(data) {
		t.Fatalf("restored page = %q, want %q", content, string(data))
	}
}

// Coalescing may never eat a version this store did not write. That is the case
// the feature exists to protect: a collaborator's save, an external editor, or
// the "overwrite with mine" answer to a conflict, all of which replace content
// whose only remaining copy would be the snapshot.
func TestStoreHistoryAlwaysSnapshotsForeignContent(t *testing.T) {
	root := t.TempDir()
	graph, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)

	page, _, rev, err := store.CreateNodePage(identity.Local, "alpha", "設計稿", PageFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNodePage(identity.Local, "alpha", page.ID, "mine\n", rev); err != nil {
		t.Fatal(err)
	}

	rel := "nodes/alpha.pages/" + page.ID + ".md"
	before, err := store.ListHistory(rel)
	if err != nil {
		t.Fatal(err)
	}

	// Somebody else puts their work there. Immediately — well inside the
	// coalescing window — we overwrite it.
	pagePath := filepath.Join(root, "nodes", "alpha.pages", page.ID+".md")
	if err := os.WriteFile(pagePath, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNodePage(identity.Local, "alpha", page.ID, "mine again\n", Rev([]byte("theirs\n"))); err != nil {
		t.Fatal(err)
	}

	after, err := store.ListHistory(rel)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("got %d versions, want %d: their version has to be kept", len(after), len(before)+1)
	}
	data, err := store.ReadHistory(rel, after[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "theirs\n" {
		t.Fatalf("newest snapshot = %q, want the overwritten foreign version", string(data))
	}
}

func TestStoreReadHistoryRejectsPathEscapes(t *testing.T) {
	store := NewStore(t.TempDir())

	for _, tc := range []struct{ rel, version string }{
		{"nodes/alpha.md", "../../secret.md"},
		{"../outside.md", "20260101-000000.md"},
		{"nodes/alpha.md", "20260101-000000.txt"},
		{"nodes/alpha.pages/../../secret.md", "20260101-000000.000000000.md"},
		{"nodes/alpha.pages/page-0001.exe", "20260101-000000.000000000.md"},
		{"nodes/alpha.pages/page-0001.md/nested.md", "20260101-000000.000000000.md"},
		{"nodes/alpha.notpages/page-0001.md", "20260101-000000.000000000.md"},
	} {
		if _, err := store.ReadHistory(tc.rel, tc.version); err == nil {
			t.Fatalf("ReadHistory(%q, %q) should be refused", tc.rel, tc.version)
		}
	}
}
