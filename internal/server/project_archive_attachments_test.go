package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/project"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestArchive builds a .veproj by hand so a test can put files in it that
// the exporter would never write.
func writeTestArchive(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest, err := json.Marshal(project.ProjectArchiveManifest{
		Format:       project.ProjectArchiveFormat,
		Version:      project.ProjectArchiveVersion,
		GraphVersion: 1,
		Name:         "handmade",
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{"manifest.json": string(manifest)}
	for name, body := range files {
		entries[name] = body
	}
	for name, body := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

const testArchiveGraph = "version: 1\nnodes:\n  - id: node-0001\n    title: Legacy\n"

// Archives written before attachments were packed have no .files directory at
// all; importing one must still work.
func TestImportArchiveWithoutAttachments(t *testing.T) {
	reader := writeTestArchive(t, map[string]string{
		"graph.yaml":         testArchiveGraph,
		"nodes/node-0001.md": "# Legacy\n\nbody\n",
		"run/journal.jsonl":  "",
	})
	destination := t.TempDir()
	if _, err := project.ExtractProjectArchive(reader, destination); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := project.ValidateImportedProject(destination); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "nodes", "node-0001.md")); err != nil {
		t.Errorf("node document is missing: %v", err)
	}
}

// An attachment name that escapes its folder must never be written.
func TestImportArchiveRejectsUnsafeAttachmentNames(t *testing.T) {
	for _, name := range []string{
		"nodes/node-0001.files/../../escape.txt",
		"nodes/node-0001.files/nested/deep.txt",
		"nodes/../escape.md",
	} {
		reader := writeTestArchive(t, map[string]string{
			"graph.yaml":         testArchiveGraph,
			"nodes/node-0001.md": "# Legacy\n",
			name:                 "payload",
		})
		destination := t.TempDir()
		_, err := project.ExtractProjectArchive(reader, destination)
		if err == nil {
			// Refusing the whole archive is one acceptable answer; silently
			// skipping the entry is the other. Writing it is not.
			escaped := filepath.Join(filepath.Dir(destination), "escape.txt")
			if _, statErr := os.Stat(escaped); statErr == nil {
				t.Fatalf("%q escaped the import directory", name)
			}
			if _, statErr := os.Stat(filepath.Join(destination, filepath.FromSlash(name))); statErr == nil {
				t.Fatalf("%q was written verbatim", name)
			}
		}
	}
}

// The whole point of packing attachments: after an import the server can still
// serve the image a document points at.
func TestImportedAttachmentIsServedOverHTTP(t *testing.T) {
	server, root := exportServerForTest(t)
	filesDir := filepath.Join(root, "nodes", "alpha.files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	if err := os.WriteFile(filepath.Join(filesDir, "shot.png"), want, 0o644); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := project.BuildProjectArchive(&archive, project.ProjectInfo{Name: "source", Path: root}, nil); err != nil {
		t.Fatalf("build archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	// Import over the same project directory: the store the server already
	// holds then serves the restored attachment.
	if _, err := project.ExtractProjectArchive(reader, root); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := project.ValidateImportedProject(root); err != nil {
		t.Fatalf("validate: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/nodes/alpha/files/shot.png", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body)
	}
	if !bytes.Equal(response.Body.Bytes(), want) {
		t.Errorf("served %d bytes, want the original %d", response.Body.Len(), len(want))
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, "shot.png") {
		t.Errorf("content disposition = %q", disposition)
	}
}
