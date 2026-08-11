package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

func listHistory(t *testing.T, api *API, path string) []store.HistoryVersion {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/history?path="+path, nil)
	response := httptest.NewRecorder()
	api.getHistory(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		Path     string                 `json:"path"`
		Versions []store.HistoryVersion `json:"versions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Path != path {
		t.Fatalf("path = %q, want %q", payload.Path, path)
	}
	return payload.Versions
}

func TestHistoryRestoreBringsBackTheSnapshottedGraph(t *testing.T) {
	api, st := graphTestAPI(t)
	seedGraph(t, st, &engine.Node{ID: "alpha", Title: "Alpha"})

	versions := listHistory(t, api, "graph.yaml")
	if len(versions) == 0 {
		t.Fatal("saving over the project's graph left no history to restore from")
	}
	version := versions[0].Name

	readRequest := httptest.NewRequest(http.MethodGet,
		"/api/history/version?path=graph.yaml&version="+version, nil)
	readResponse := httptest.NewRecorder()
	api.getHistoryVersion(ginContext(readResponse, readRequest))
	if readResponse.Code != http.StatusOK {
		t.Fatalf("version status = %d, body = %s", readResponse.Code, readResponse.Body)
	}
	var snapshot struct {
		Version string `json:"version"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(readResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != version {
		t.Fatalf("version = %q, want %q", snapshot.Version, version)
	}
	if got := readResponse.Header().Get("Cache-Control"); got != httpx.CacheControlImmutable {
		t.Fatalf("Cache-Control = %q, want %q", got, httpx.CacheControlImmutable)
	}

	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/history/restore",
		strings.NewReader(`{"path":"graph.yaml","version":"`+version+`"}`))
	restoreRequest.Header.Set("Content-Type", "application/json")
	restoreResponse := httptest.NewRecorder()
	api.postHistoryRestore(ginContext(restoreResponse, restoreRequest))
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body = %s", restoreResponse.Code, restoreResponse.Body)
	}

	restored, err := engine.ParseGraph([]byte(snapshot.Content))
	if err != nil {
		t.Fatal(err)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != len(restored.Nodes) {
		t.Fatalf("restored graph has %d nodes, the snapshot had %d",
			len(graph.Nodes), len(restored.Nodes))
	}
	// The restore itself is snapshotted, so the state it replaced can be got
	// back too.
	if after := listHistory(t, api, "graph.yaml"); len(after) <= len(versions) {
		t.Fatalf("history did not grow across a restore: %d then %d", len(versions), len(after))
	}
}

func TestHistoryRestoreOfAnUnknownVersionFails(t *testing.T) {
	api, st := graphTestAPI(t)
	seedGraph(t, st, &engine.Node{ID: "alpha", Title: "Alpha"})

	request := httptest.NewRequest(http.MethodPost, "/api/history/restore",
		strings.NewReader(`{"path":"graph.yaml","version":"20200101-000000.000000000.yaml"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.postHistoryRestore(ginContext(response, request))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body)
	}
	graph, _, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if graph.NodeByID("alpha") == nil {
		t.Fatal("a failed restore changed the live graph")
	}
}

func TestHistoryOfAFileWithNoSnapshotsIsAnEmptyList(t *testing.T) {
	api, _ := graphTestAPI(t)

	if versions := listHistory(t, api, "nodes/alpha.md"); len(versions) != 0 {
		t.Fatalf("versions = %+v, want none", versions)
	}
}

func TestRestoredSubpagesRefreshTheNodeTheyBelongTo(t *testing.T) {
	for _, testCase := range []struct {
		path string
		id   string
		ok   bool
	}{
		{"nodes/alpha.md", "alpha", true},
		{"nodes/alpha.pages/p1.md", "alpha", true},
		{"nodes/alpha.yaml", "", false},
		{"graph.yaml", "", false},
	} {
		id, ok := restoredNodeID(testCase.path)
		if id != testCase.id || ok != testCase.ok {
			t.Fatalf("restoredNodeID(%q) = %q, %v; want %q, %v",
				testCase.path, id, ok, testCase.id, testCase.ok)
		}
	}
}

func TestTrashOfAFreshProjectIsEmpty(t *testing.T) {
	api, _ := graphTestAPI(t)

	request := httptest.NewRequest(http.MethodGet, "/api/trash", nil)
	response := httptest.NewRecorder()
	api.getTrash(ginContext(response, request))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		Items []store.TrashItem `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 0 {
		t.Fatalf("items = %+v, want none", payload.Items)
	}
}
