package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/realtime"
	"nodevas/internal/remote"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doJSON runs a request through the full middleware stack, the way a browser
// would reach it, and returns the recorded response.
func doJSON(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestFolderRemoteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	backend, err := remote.NewFolderRemote(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("PK\x03\x04 pretend bundle")
	ref, err := backend.Push(context.Background(), "My Project!!", bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.HasPrefix(ref.ID, "My-Project-") || !strings.HasSuffix(ref.ID, remote.RemoteBundleExt) {
		t.Fatalf("unexpected id %q", ref.ID)
	}
	if ref.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", ref.Size, len(payload))
	}

	refs, err := backend.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != ref.ID {
		t.Fatalf("list = %+v, want the pushed bundle", refs)
	}

	reader, err := backend.Pull(context.Background(), ref.ID)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ref.ID))
	reader.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("pulled bytes differ from pushed")
	}
}

func TestFolderRemoteRejectsUnsafeID(t *testing.T) {
	backend, err := remote.NewFolderRemote(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../escape.veproj", "sub/dir.veproj", "notes.txt", ".."} {
		if _, err := backend.Pull(context.Background(), id); err == nil {
			t.Fatalf("Pull accepted unsafe id %q", id)
		}
	}
}

// TestRemotePushListImport exercises the whole loop through HTTP: configure a
// folder backend, push the active project, list it, and import it back as a
// new project.
func TestRemotePushListImport(t *testing.T) {
	pm := projectManagerForTest(t)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	handler := server.Handler()
	backupDir := t.TempDir()

	if rec := doJSON(t, handler, http.MethodPut, "/api/remote/config",
		map[string]string{"kind": "folder", "folder": backupDir}); rec.Code != http.StatusOK {
		t.Fatalf("configure remote: %d %s", rec.Code, rec.Body)
	}

	pushRec := doJSON(t, handler, http.MethodPost, "/api/remote/push", nil)
	if pushRec.Code != http.StatusCreated {
		t.Fatalf("push: %d %s", pushRec.Code, pushRec.Body)
	}
	var pushed struct {
		Backend string           `json:"backend"`
		Bundle  remote.RemoteRef `json:"bundle"`
	}
	if err := json.Unmarshal(pushRec.Body.Bytes(), &pushed); err != nil {
		t.Fatal(err)
	}
	if pushed.Backend != "folder" || pushed.Bundle.ID == "" {
		t.Fatalf("unexpected push response %+v", pushed)
	}

	listRec := doJSON(t, handler, http.MethodGet, "/api/remote/list", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body)
	}
	var listed struct {
		Bundles []remote.RemoteRef `json:"bundles"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Bundles) != 1 || listed.Bundles[0].ID != pushed.Bundle.ID {
		t.Fatalf("list = %+v, want the pushed bundle", listed.Bundles)
	}

	importRec := doJSON(t, handler, http.MethodPost, "/api/remote/import",
		map[string]string{"id": pushed.Bundle.ID})
	if importRec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", importRec.Code, importRec.Body)
	}
	var imported struct {
		Active string `json:"active"`
		Root   string `json:"root"`
	}
	if err := json.Unmarshal(importRec.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Active == "" || imported.Active == "main" {
		t.Fatalf("import should create a new project, got %q", imported.Active)
	}
	if _, err := os.Stat(filepath.Join(imported.Root, "graph.yaml")); err != nil {
		t.Fatalf("imported project has no graph.yaml: %v", err)
	}
}

func TestRemoteListWithoutConfigFails(t *testing.T) {
	server := serverForTest(t, projectManagerForTest(t), realtime.NewHub(), nil)
	rec := doJSON(t, server.Handler(), http.MethodGet, "/api/remote/list", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("list without a remote = %d, want 400", rec.Code)
	}
}

// A folder target is a host path, so it must be refused once the server is
// reachable from the network.
func TestFolderRemoteRefusedInNetworkMode(t *testing.T) {
	pm := projectManagerForTest(t)
	pm.SetRemote(true)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	rec := doJSON(t, server.Handler(), http.MethodPut, "/api/remote/config",
		map[string]string{"kind": "folder", "folder": t.TempDir()})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("folder config in network mode = %d, want 403", rec.Code)
	}
}

func TestRemoteConfigRejectsUnknownBackend(t *testing.T) {
	server := serverForTest(t, projectManagerForTest(t), realtime.NewHub(), nil)
	rec := doJSON(t, server.Handler(), http.MethodPut, "/api/remote/config",
		map[string]string{"kind": "dropbox"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown backend = %d, want 400", rec.Code)
	}
}

func TestOAuthStateIsSingleUse(t *testing.T) {
	manager := remote.NewManager(projectManagerForTest(t))
	state, _, err := manager.NewOAuthState("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.ConsumeOAuthState(state); !ok {
		t.Fatal("a freshly issued state should validate")
	}
	if _, ok := manager.ConsumeOAuthState(state); ok {
		t.Fatal("a state must not validate twice")
	}
	if _, ok := manager.ConsumeOAuthState("never-issued"); ok {
		t.Fatal("an unknown state must not validate")
	}
}
