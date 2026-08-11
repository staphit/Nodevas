package project

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"nodevas/internal/engine"
)

func TestProjectArchiveRoundTripsAttachments(t *testing.T) {
	source := t.TempDir()
	nodesDir := filepath.Join(source, "nodes")
	filesDir := filepath.Join(nodesDir, "node-0001.files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	graph := []byte("version: 1\ntype: story\nnodes:\n  - id: node-0001\n    title: Attachment\n")
	if err := os.WriteFile(filepath.Join(source, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodesDir, "node-0001.md"), []byte("# Attachment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x01, 0x02, 0xfe, 0xff}
	if err := os.WriteFile(filepath.Join(filesDir, "image.bin"), want, 0o644); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := BuildProjectArchive(&archive, ProjectInfo{Name: "source", Path: source}, nil); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	destination := t.TempDir()
	if _, err := ExtractProjectArchive(reader, destination); err != nil {
		t.Fatalf("extract archive: %v", err)
	}
	if err := ValidateImportedProject(destination); err != nil {
		t.Fatalf("validate imported project: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "nodes", "node-0001.files", "image.bin"))
	if err != nil {
		t.Fatalf("read imported attachment: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("attachment bytes = %v, want %v", got, want)
	}
}

func TestArchivePathAllowsFolderedNodes(t *testing.T) {
	allowed := []string{
		"nodes/alpha.md",
		"nodes/章節一/alpha.md",
		"nodes/章節一/場景/alpha.md",
		"nodes/章節一/alpha.pages/pages.json",
		"nodes/章節一/alpha.files/shot.png",
	}
	for _, name := range allowed {
		if !allowedProjectArchivePath(name) {
			t.Errorf("%q should be allowed in an archive", name)
		}
	}
	refused := []string{
		"nodes/章節一/notes.txt",
		"nodes/../escape.md",
		"nodes/.hidden/alpha.md",
	}
	for _, name := range refused {
		if allowedProjectArchivePath(name) {
			t.Errorf("%q should be refused", name)
		}
	}
}

// A workspace's shared lifecycle states are stripped from graph.yaml on write
// — they live once in <workspace>/.vised/workflow.json — so an exported
// project used to name states it did not define. Importing it into another
// workspace left those states unresolvable.
func TestProjectArchiveCarriesTheSharedStatusesItUses(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	// graph.yaml as it sits on disk: a planned milestone in a shared state,
	// and no definition for it anywhere in the file.
	graph := []byte("version: 1\ntype: story\n" +
		"nodes:\n  - id: node-0001\n    title: Chapter\n" +
		"ui:\n  plans:\n    node-0001:\n      - date: \"2026-01-01\"\n" +
		"        status: custom-status-review\n")
	if err := os.WriteFile(filepath.Join(source, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nodes", "node-0001.md"), []byte("# Chapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	shared := []engine.StatusDefinition{
		{ID: "custom-status-review", Label: "外審中", Color: "#8fd3ff", Shape: "diamond"},
		// Present in the workspace, unused by this project: it must not travel,
		// or every import turns the whole palette into local overrides.
		{ID: "custom-status-unused", Label: "沒人用", Color: "#cccccc", Shape: "circle"},
	}

	var archive bytes.Buffer
	if err := BuildProjectArchive(&archive, ProjectInfo{Name: "source", Path: source}, shared); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	destination := t.TempDir()
	if _, err := ExtractProjectArchive(reader, destination); err != nil {
		t.Fatalf("extract archive: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destination, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	imported, err := engine.ParseGraph(data)
	if err != nil {
		t.Fatalf("parse imported graph: %v", err)
	}
	if imported.UI == nil {
		t.Fatal("imported graph has no ui block")
	}
	got := map[string]engine.StatusDefinition{}
	for _, definition := range imported.UI.CustomStatuses {
		got[definition.ID] = definition
	}
	review, ok := got["custom-status-review"]
	if !ok {
		t.Fatalf("the used shared status did not travel; got %v", imported.UI.CustomStatuses)
	}
	// The whole definition, not just the id: a label-less state is as
	// unreadable to the destination as a missing one.
	if review.Label != "外審中" || review.Color != "#8fd3ff" || review.Shape != "diamond" {
		t.Fatalf("definition arrived incomplete: %+v", review)
	}
	if _, unwanted := got["custom-status-unused"]; unwanted {
		t.Fatal("an unused workspace status was copied into the archive")
	}
}

// The source project's own graph.yaml is not rewritten by an export, and a
// project that uses no shared state produces the file byte for byte.
func TestProjectArchiveLeavesAnUnaffectedGraphAlone(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	graph := []byte("version: 1\ntype: story\nnodes:\n  - id: node-0001\n    title: Plain\n")
	if err := os.WriteFile(filepath.Join(source, "graph.yaml"), graph, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nodes", "node-0001.md"), []byte("# Plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shared := []engine.StatusDefinition{{ID: "custom-status-review", Label: "外審中"}}

	var archive bytes.Buffer
	if err := BuildProjectArchive(&archive, ProjectInfo{Name: "source", Path: source}, shared); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	destination := t.TempDir()
	if _, err := ExtractProjectArchive(reader, destination); err != nil {
		t.Fatalf("extract archive: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, graph) {
		t.Fatalf("graph.yaml was rewritten for a project that uses no shared state:\n%s", got)
	}
	onDisk, err := os.ReadFile(filepath.Join(source, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, graph) {
		t.Fatal("exporting rewrote the source project's graph.yaml")
	}
}
