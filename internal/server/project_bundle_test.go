package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	projectapi "nodevas/internal/httpapi/project"
	"nodevas/internal/project"
	"os"
	"path/filepath"
	"testing"
)

// bundleWorkspaceForTest lays out a folder holding two projects, which is the
// shape a single-project archive cannot represent.
func bundleWorkspaceForTest(t *testing.T) *project.ProjectManager {
	t.Helper()
	pm := projectManagerForTest(t)
	if err := os.MkdirAll(filepath.Join(pm.Workspace(), "Stellaris"), 0o755); err != nil {
		t.Fatalf("create grouping root: %v", err)
	}
	for _, name := range []string{"Stellaris/Story", "Stellaris/Notes"} {
		if err := pm.Activate(name, true, ""); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(pm.Workspace(), "Stellaris", "草稿"), 0o755); err != nil {
		t.Fatalf("create grouping folder: %v", err)
	}
	return pm
}

func exportForTest(t *testing.T, pm *project.ProjectManager, project string) *zip.Reader {
	t.Helper()
	target := "/api/projects/export"
	if project != "" {
		target += "?project=" + project
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	projectapi.New(pm).GetProjectExport(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.Bytes()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("export is not a ZIP: %v", err)
	}
	return reader
}

func archiveNames(reader *zip.Reader) map[string]bool {
	names := make(map[string]bool, len(reader.File))
	for _, entry := range reader.File {
		names[entry.Name] = true
	}
	return names
}

// A grouping folder has no graph.yaml of its own, so exporting it only makes
// sense as a bundle of everything below it.
func TestExportFolderProducesBundle(t *testing.T) {
	pm := bundleWorkspaceForTest(t)
	reader := exportForTest(t, pm, "Stellaris")

	_, manifest, ok, err := project.DetectProjectBundle(reader)
	if err != nil {
		t.Fatalf("detect bundle: %v", err)
	}
	if !ok {
		t.Fatal("folder export is not a bundle")
	}
	if manifest.Name != "Stellaris" {
		t.Errorf("bundle name = %q, want %q", manifest.Name, "Stellaris")
	}
	want := map[string]bool{"Story": true, "Notes": true}
	if len(manifest.Projects) != len(want) {
		t.Fatalf("bundle projects = %v, want %v", manifest.Projects, want)
	}
	for _, project := range manifest.Projects {
		if !want[project] {
			t.Errorf("unexpected bundle project %q", project)
		}
	}
	if len(manifest.Folders) != 1 || manifest.Folders[0] != "草稿" {
		t.Errorf("bundle folders = %v, want [草稿]", manifest.Folders)
	}
	names := archiveNames(reader)
	for _, name := range []string{"Story/graph.yaml", "Notes/graph.yaml"} {
		if !names[name] {
			t.Errorf("archive is missing %q", name)
		}
	}
}

// A leaf project keeps the flat single-project layout for direct imports.
func TestExportLeafProjectStaysSingleArchive(t *testing.T) {
	pm := bundleWorkspaceForTest(t)
	reader := exportForTest(t, pm, "Stellaris/Story")

	if _, _, ok, err := project.DetectProjectBundle(reader); err != nil || ok {
		t.Fatalf("leaf export is a bundle (ok=%v, err=%v)", ok, err)
	}
	if !archiveNames(reader)["graph.yaml"] {
		t.Error("single-project archive is missing graph.yaml at its root")
	}
}

// The workspace root carries its own project plus every subtree.
func TestExportWorkspaceRootCoversEveryProject(t *testing.T) {
	pm := bundleWorkspaceForTest(t)
	reader := exportForTest(t, pm, ".")

	_, manifest, ok, err := project.DetectProjectBundle(reader)
	if err != nil || !ok {
		t.Fatalf("root export is not a bundle (ok=%v, err=%v)", ok, err)
	}
	found := map[string]bool{}
	for _, project := range manifest.Projects {
		found[project] = true
	}
	for _, want := range []string{"main", "Stellaris/Story", "Stellaris/Notes"} {
		if !found[want] {
			t.Errorf("bundle is missing project %q (has %v)", want, manifest.Projects)
		}
	}
	if found["."] {
		t.Error("workspace root has no graph.yaml, so it must not be listed as a project")
	}
}

func postImportForTest(t *testing.T, pm *project.ProjectManager, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/projects/import", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	projectapi.New(pm).PostProjectImport(ginContext(response, request))
	return response
}

// Round trip: a bundle exported from one workspace restores the whole subtree
// in another one, and opens a real project.
func TestImportBundleRestoresSubtree(t *testing.T) {
	source := bundleWorkspaceForTest(t)
	request := httptest.NewRequest(http.MethodGet, "/api/projects/export?project=Stellaris", nil)
	response := httptest.NewRecorder()
	projectapi.New(source).GetProjectExport(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", response.Code, response.Body.String())
	}

	target := projectManagerForTest(t)
	imported := postImportForTest(t, target, "Stellaris.veproj", response.Body.Bytes())
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", imported.Code, imported.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil {
		t.Fatalf("import response: %v", err)
	}
	root := filepath.Join(target.Workspace(), "Stellaris")
	for _, name := range []string{
		filepath.Join(root, "Story", "graph.yaml"),
		filepath.Join(root, "Notes", "graph.yaml"),
		filepath.Join(root, "草稿"),
	} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("restored tree is missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "graph.yaml")); !os.IsNotExist(err) {
		t.Errorf("bundle root was a folder, so it must stay one: %v", err)
	}
	if result["active"] != "Stellaris/Notes" && result["active"] != "Stellaris/Story" {
		t.Errorf("active project after import = %q", result["active"])
	}
	active, err := target.ActiveProject()
	if err != nil {
		t.Fatalf("activeProject after import: %v", err)
	}
	if active.Name != result["active"] {
		t.Errorf("active project = %q, want %q", active.Name, result["active"])
	}
}

// Files that belong to no listed project would land in the workspace unowned,
// so the whole bundle is refused.
func TestExtractProjectBundleRejectsUnlistedPaths(t *testing.T) {
	manifest := project.ProjectBundleManifest{
		Format:   project.ProjectArchiveFormat,
		Version:  project.ProjectBundleVersion,
		Kind:     project.ProjectBundleKind,
		Name:     "handmade",
		Projects: []string{"Story"},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"manifest.json":            string(encoded),
		"Story/graph.yaml":         testArchiveGraph,
		"Story/nodes/node-0001.md": "# Legacy\n",
		"Other/graph.yaml":         testArchiveGraph,
	} {
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
	root, parsed, ok, err := project.DetectProjectBundle(reader)
	if err != nil || !ok {
		t.Fatalf("detect bundle: ok=%v err=%v", ok, err)
	}
	destination := t.TempDir()
	if err := project.ExtractProjectBundle(reader, root, parsed, destination); err == nil {
		t.Fatal("bundle with an unlisted project was accepted")
	}
	if _, err := os.Stat(filepath.Join(destination, "Other", "graph.yaml")); err == nil {
		t.Error("unlisted project was written anyway")
	}
}
