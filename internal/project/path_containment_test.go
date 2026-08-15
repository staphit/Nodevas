package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/store"
)

func TestResolveRejectsNestedSymlinkProject(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	group := filepath.Join(workspace, "group")
	if err := os.MkdirAll(group, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(group, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manager := &ProjectManager{workspace: workspace}
	if _, err := manager.Resolve("group/linked"); err == nil {
		t.Fatal("Resolve accepted a nested symlink project")
	}
}

func TestNewProjectStoreCreatesMissingContainedRootForEmptyImport(t *testing.T) {
	workspace := t.TempDir()
	manager := &ProjectManager{workspace: workspace, stores: map[string]*store.Store{}}
	st, err := manager.NewProjectStore("nested/empty")
	if err != nil {
		t.Fatal(err)
	}
	if st.Root() != filepath.Join(workspace, "nested", "empty") {
		t.Fatalf("store root = %q", st.Root())
	}
	info, err := os.Stat(st.Root())
	if err != nil || !info.IsDir() {
		t.Fatalf("missing project root: %v", err)
	}
	count, err := st.ImportDocuments(&engine.Graph{Version: 1, Type: "workflow"}, nil)
	if err != nil {
		t.Fatalf("empty import: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty import count = %d", count)
	}
	for _, path := range []string{st.GraphPath(), filepath.Join(st.Root(), "nodes")} {
		if _, err := store.StatProjectPath(workspace, path); err != nil {
			t.Fatalf("empty import did not create %s: %v", path, err)
		}
	}
}

func TestListDoesNotReadGraphThroughProjectSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	graphData, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "outside", Title: "outside"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "graph.yaml"), graphData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manager := &ProjectManager{workspace: workspace}
	projects, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if project.Name == "linked" {
			t.Fatalf("symlink project leaked into catalog: %+v", project)
		}
	}
}

func TestDirectSearchDoesNotReadSymlinkedNode(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("unique-outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	graphData, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha", Title: "Alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graphData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "nodes", "alpha.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	results := SearchProjectNodesDirect(ProjectInfo{Name: "p", Path: root}, "unique-outside-secret")
	if len(results) != 0 {
		t.Fatalf("search exposed symlink target: %+v", results)
	}
}

func TestInstallArchiveRejectsSymlinkedImportStagingDirectory(t *testing.T) {
	manager := importTestManager(t)
	outside := t.TempDir()
	imports := filepath.Join(manager.Workspace(), ".vised", "imports")
	if err := os.Symlink(outside, imports); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	data := buildTestProjectArchive(t, "Imported")
	if _, _, err := manager.InstallArchive(data, "Imported", "Imported.veproj"); err == nil {
		t.Fatal("archive install accepted a symlinked staging directory")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("archive import wrote outside the workspace: %v", entries)
	}
}

func TestBuildProjectArchiveRejectsSymlinkedAttachment(t *testing.T) {
	root := t.TempDir()
	graphData, err := engine.MarshalGraph(&engine.Graph{
		Version: 1,
		Nodes:   []*engine.Node{{ID: "alpha", Title: "Alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graph.yaml"), graphData, 0o644); err != nil {
		t.Fatal(err)
	}
	filesDir := filepath.Join(root, "nodes", "alpha.files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nodes", "alpha.md"), []byte("# Alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filesDir, "secret.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var archive bytes.Buffer
	err = BuildProjectArchive(&archive, ProjectInfo{Name: "p", Path: root}, nil)
	if err == nil {
		t.Fatal("archive export accepted a symlinked attachment")
	}
	if bytes.Contains(archive.Bytes(), []byte("outside-secret")) {
		t.Fatal("archive exposed symlink target")
	}
}
