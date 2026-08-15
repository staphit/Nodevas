package project

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/realtime"
)

func importTestManager(t *testing.T) *ProjectManager {
	t.Helper()
	pm, err := NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	t.Cleanup(pm.StopWatcher)
	return pm
}

func testProjectGraph(title string) []byte {
	return []byte("version: 1\ntype: story\nnodes:\n  - id: node-0001\n    title: " + title + "\n")
}

// writeTestProject lays out the smallest project a workspace will accept, so a
// test can put one in the way of an import and check it later.
func writeTestProject(t *testing.T, dir, title string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graph.yaml"), testProjectGraph(title), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "nodes", "node-0001.md"), []byte("# "+title+"\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
}

// buildTestBundle writes a bundle archive whose root is a grouping folder, the
// shape a whole-workspace export produces.
func buildTestBundle(t *testing.T, name string, projects, folders []string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest, err := json.MarshalIndent(ProjectBundleManifest{
		Format:   ProjectArchiveFormat,
		Version:  ProjectBundleVersion,
		Kind:     ProjectBundleKind,
		Name:     name,
		Projects: projects,
		Folders:  folders,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := addArchiveFile(writer, "manifest.json", append(manifest, '\n')); err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		prefix := bundleMemberPrefix(project)
		files := map[string][]byte{
			"graph.yaml":         testProjectGraph(project),
			"nodes/node-0001.md": []byte("# " + project + "\n"),
		}
		for _, entry := range []string{"graph.yaml", "nodes/node-0001.md"} {
			if err := addArchiveFile(
				writer, filepath.ToSlash(filepath.Join(prefix, entry)), files[entry],
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func buildTestProjectArchive(t *testing.T, name string) []byte {
	t.Helper()
	source := t.TempDir()
	writeTestProject(t, source, name)
	var archive bytes.Buffer
	if err := BuildProjectArchive(&archive, ProjectInfo{Name: name, Path: source}, nil); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	return archive.Bytes()
}

// The first entry of an archive has to be the manifest: the client reads it out
// of the ZIP before uploading, to ask which import mode the user wants.
func TestArchivesLeadWithTheirManifest(t *testing.T) {
	archives := map[string][]byte{
		"project": buildTestProjectArchive(t, "Stellaris"),
		"bundle":  buildTestBundle(t, "workspace", []string{"Stellaris"}, nil),
	}
	for kind, data := range archives {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: read archive: %v", kind, err)
		}
		if len(reader.File) == 0 || reader.File[0].Name != "manifest.json" {
			t.Fatalf("%s: first archive entry is not manifest.json", kind)
		}
	}
}

func TestInstallArchiveRootModeUnpacksBundleIntoTheWorkspace(t *testing.T) {
	pm := importTestManager(t)
	data := buildTestBundle(t, "workspace", []string{"Stellaris", "Notes/Ideas"}, []string{"Notes"})

	active, _, err := pm.InstallArchiveMode(data, "", "workspace.veproj", ImportModeRoot, "")
	if err != nil {
		t.Fatalf("install bundle at root: %v", err)
	}
	if active != "Stellaris" {
		t.Fatalf("activated %q, want Stellaris", active)
	}
	for _, want := range []string{"Stellaris", filepath.Join("Notes", "Ideas")} {
		if _, err := os.Stat(filepath.Join(pm.Workspace(), want, "graph.yaml")); err != nil {
			t.Fatalf("imported project %q: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "workspace")); !os.IsNotExist(err) {
		t.Fatalf("root import created a wrapper directory: %v", err)
	}
}

func TestInstallArchiveFolderModeStillWrapsBundle(t *testing.T) {
	pm := importTestManager(t)
	data := buildTestBundle(t, "workspace", []string{"Stellaris"}, nil)

	active, _, err := pm.InstallArchiveMode(data, "", "workspace.veproj", ImportModeFolder, "")
	if err != nil {
		t.Fatalf("install bundle as folder: %v", err)
	}
	if active != "workspace/Stellaris" {
		t.Fatalf("activated %q, want workspace/Stellaris", active)
	}
	if _, err := os.Stat(
		filepath.Join(pm.Workspace(), "workspace", "Stellaris", "graph.yaml"),
	); err != nil {
		t.Fatalf("wrapped project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "Stellaris")); !os.IsNotExist(err) {
		t.Fatalf("folder import unpacked into the workspace root: %v", err)
	}
}

func TestInstallArchiveRootModeRenamesInsteadOfOverwriting(t *testing.T) {
	pm := importTestManager(t)
	existing := filepath.Join(pm.Workspace(), "Stellaris")
	writeTestProject(t, existing, "Original")
	keepsake := []byte("# keep me\n")
	if err := os.WriteFile(filepath.Join(existing, "nodes", "node-0001.md"), keepsake, 0o644); err != nil {
		t.Fatal(err)
	}
	data := buildTestBundle(t, "workspace", []string{"Stellaris"}, nil)

	active, _, err := pm.InstallArchiveMode(data, "", "workspace.veproj", ImportModeRoot, "")
	if err != nil {
		t.Fatalf("install bundle at root: %v", err)
	}
	if active != "Stellaris-2" {
		t.Fatalf("activated %q, want Stellaris-2", active)
	}
	if _, err := os.Stat(filepath.Join(pm.Workspace(), "Stellaris-2", "graph.yaml")); err != nil {
		t.Fatalf("renamed import: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(existing, "nodes", "node-0001.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, keepsake) {
		t.Fatalf("the existing project was overwritten: %s", got)
	}
	graph, err := os.ReadFile(filepath.Join(existing, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(graph, testProjectGraph("Original")) {
		t.Fatalf("the existing project's graph was rewritten:\n%s", graph)
	}
}

// A single-project archive has no members to spread out, so "root" is the same
// request as "folder" and must not be refused.
func TestInstallArchiveRootModeAcceptsASingleProject(t *testing.T) {
	pm := importTestManager(t)
	data := buildTestProjectArchive(t, "Stellaris")

	active, destination, err := pm.InstallArchiveMode(data, "", "Stellaris.veproj", ImportModeRoot, "")
	if err != nil {
		t.Fatalf("install project at root: %v", err)
	}
	if active != "Stellaris" {
		t.Fatalf("activated %q, want Stellaris", active)
	}
	if destination != filepath.Join(pm.Workspace(), "Stellaris") {
		t.Fatalf("installed at %q", destination)
	}
	if _, err := os.Stat(filepath.Join(destination, "graph.yaml")); err != nil {
		t.Fatalf("imported project: %v", err)
	}
}
