package store

import (
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/engine"
)

// folderProject writes a two-node project and returns its root and store.
func folderProject(t *testing.T) (string, *Store) {
	t.Helper()
	root := t.TempDir()
	graph := &engine.Graph{
		Version: 1,
		Nodes: []*engine.Node{
			{ID: "alpha", Title: "設計稿"},
			{ID: "beta", Title: "實作", Requires: "alpha"},
		},
		Edges: []*engine.Edge{{From: "alpha", To: "beta"}},
	}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"alpha", "beta"} {
		if err := os.WriteFile(
			filepath.Join(root, "nodes", id+".md"),
			[]byte("# "+id+"\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	return root, NewStore(root)
}

func TestMoveNodeToFolderLeavesTheGraphAlone(t *testing.T) {
	root, store := folderProject(t)
	before, err := os.ReadFile(filepath.Join(root, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateNodeFolder("章節一"); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := store.MoveNodesToFolder([]string{"alpha"}, "章節一"); err != nil {
		t.Fatalf("move node: %v", err)
	}

	moved := filepath.Join(root, "nodes", "章節一", "alpha.md")
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("document should sit in the folder: %v", err)
	}
	if got := store.NodePath("alpha"); got != moved {
		t.Fatalf("NodePath = %q, want %q", got, moved)
	}
	content, _, err := store.LoadNodeContent("alpha")
	if err != nil || content == "" {
		t.Fatalf("node should still load after the move: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(root, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("organising nodes must not rewrite graph.yaml")
	}
}

func TestMoveNodeCarriesPagesAndAttachments(t *testing.T) {
	root, store := folderProject(t)
	pages := filepath.Join(root, "nodes", "alpha.pages")
	files := filepath.Join(root, "nodes", "alpha.files")
	for _, dir := range []string{pages, files} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pages, "pages.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "shot.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateNodeFolder("章節一/場景"); err != nil {
		t.Fatalf("create nested folder: %v", err)
	}
	if err := store.MoveNodesToFolder([]string{"alpha"}, "章節一/場景"); err != nil {
		t.Fatalf("move node: %v", err)
	}

	for _, rel := range []string{
		filepath.Join("章節一", "場景", "alpha.pages", "pages.json"),
		filepath.Join("章節一", "場景", "alpha.files", "shot.png"),
	} {
		if _, err := os.Stat(filepath.Join(root, "nodes", rel)); err != nil {
			t.Fatalf("%s should travel with the node: %v", rel, err)
		}
	}
	if store.NodePagesDir("alpha") != filepath.Join(root, "nodes", "章節一", "場景", "alpha.pages") {
		t.Fatalf("pages dir not resolved: %s", store.NodePagesDir("alpha"))
	}
	if store.NodeFilesDir("alpha") != filepath.Join(root, "nodes", "章節一", "場景", "alpha.files") {
		t.Fatalf("files dir not resolved: %s", store.NodeFilesDir("alpha"))
	}
}

func TestRenameAndMoveFolderKeepNodesReachable(t *testing.T) {
	root, store := folderProject(t)
	if _, err := store.CreateNodeFolder("草稿"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNodeFolder("完稿"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveNodesToFolder([]string{"alpha", "beta"}, "草稿"); err != nil {
		t.Fatal(err)
	}

	renamed, err := store.RenameNodeFolder("草稿", "初稿")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed != "初稿" {
		t.Fatalf("renamed = %q", renamed)
	}
	if got := store.NodePath("beta"); got != filepath.Join(root, "nodes", "初稿", "beta.md") {
		t.Fatalf("node lost after rename: %s", got)
	}

	moved, err := store.MoveNodeFolder("初稿", "完稿")
	if err != nil {
		t.Fatalf("move folder: %v", err)
	}
	if moved != "完稿/初稿" {
		t.Fatalf("moved = %q", moved)
	}
	if got := store.NodePath("alpha"); got != filepath.Join(root, "nodes", "完稿", "初稿", "alpha.md") {
		t.Fatalf("node lost after folder move: %s", got)
	}
}

func TestMoveFolderIntoItselfIsRefused(t *testing.T) {
	_, store := folderProject(t)
	if _, err := store.CreateNodeFolder("外層/內層"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveNodeFolder("外層", "外層/內層"); err == nil {
		t.Fatal("moving a folder inside its own subtree must fail")
	}
}

func TestDeleteFolderLiftsNodesInsteadOfDeletingThem(t *testing.T) {
	root, store := folderProject(t)
	if _, err := store.CreateNodeFolder("章節一/場景"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveNodesToFolder([]string{"alpha"}, "章節一/場景"); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteNodeFolder("章節一/場景"); err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nodes", "章節一", "alpha.md")); err != nil {
		t.Fatalf("node should move up, not vanish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nodes", "章節一", "場景")); err == nil {
		t.Fatal("folder should be gone")
	}
	if got := store.NodePath("alpha"); got != filepath.Join(root, "nodes", "章節一", "alpha.md") {
		t.Fatalf("NodePath = %s", got)
	}
}

func TestNodeFoldersReportsTreeAndAssignments(t *testing.T) {
	_, store := folderProject(t)
	if _, err := store.CreateNodeFolder("章節一/場景"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveNodesToFolder([]string{"beta"}, "章節一"); err != nil {
		t.Fatal(err)
	}
	folders, assignments := store.NodeFolders()
	if len(folders) != 2 || folders[0] != "章節一" || folders[1] != "章節一/場景" {
		t.Fatalf("folders = %v", folders)
	}
	if assignments["beta"] != "章節一" {
		t.Fatalf("assignments = %v", assignments)
	}
	if _, ok := assignments["alpha"]; ok {
		t.Fatal("a node at the root has no folder")
	}
}

func TestExternalMoveIsPickedUpWithoutInvalidation(t *testing.T) {
	root, store := folderProject(t)
	// Someone rearranges the files in Explorer or through git: no API call
	// invalidates the cache, so the next read has to notice on its own.
	if err := store.MoveNodesToFolder([]string{"alpha"}, ""); err != nil {
		t.Fatal(err)
	}
	_ = store.NodePath("alpha") // prime the cache with the root location
	dir := filepath.Join(root, "nodes", "手動")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(root, "nodes", "alpha.md"),
		filepath.Join(dir, "alpha.md"),
	); err != nil {
		t.Fatal(err)
	}
	if got := store.NodePath("alpha"); got != filepath.Join(dir, "alpha.md") {
		t.Fatalf("stale path after an external move: %s", got)
	}
}

func TestFolderNameValidation(t *testing.T) {
	valid := []string{"章節一", "draft", "v2 notes", "a-b_c"}
	for _, name := range valid {
		if !ValidFolderName(name) {
			t.Errorf("%q should be a valid folder name", name)
		}
	}
	invalid := []string{
		"", ".", "..", ".hidden", "with/slash", "with\\slash", "trailing.",
		"con", "NUL", "alpha.pages", "alpha.files", "note.md", "  ",
	}
	for _, name := range invalid {
		if ValidFolderName(name) {
			t.Errorf("%q should be rejected", name)
		}
	}
}

func TestCreateFolderRejectsBadNamesAndDuplicates(t *testing.T) {
	_, store := folderProject(t)
	if _, err := store.CreateNodeFolder("../escape"); err == nil {
		t.Fatal("a traversal must not create a folder")
	}
	if _, err := store.CreateNodeFolder("章節一"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNodeFolder("章節一"); err == nil {
		t.Fatal("creating the same folder twice must fail")
	}
}
