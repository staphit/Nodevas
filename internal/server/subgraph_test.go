package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestASubgraphIsOneBroadcast(t *testing.T) {
	httpServer := liveOpsServer(t)
	peer := joinRoom(t, httpServer, "main")

	body := `{"nodes":[{"key":"a","title":"A"},{"key":"b","title":"B","dependsOn":["a","alpha"]},{"key":"c","title":"C","kind":"note"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/graph/subgraph", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpServer.Config.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var result struct {
		IDs map[string]string `json:"ids"`
		Rev string            `json:"rev"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.IDs) != 3 || result.Rev == "" {
		t.Fatalf("body = %s (%v)", response.Body, err)
	}
	peer.await("graph-changed", nil)
	peer.awaitNone("graph-changed", 500*time.Millisecond)
	peer.awaitNone("node-changed", 100*time.Millisecond)
}

func TestASubgraphRefusalSaysWhere(t *testing.T) {
	httpServer := liveOpsServer(t)
	for _, c := range []struct {
		body, code, field string
		status            int
	}{
		{`{"nodes":[{"key":"a","title":"A","refs":["zz"]}]}`, "INVALID_SUBGRAPH", "nodes[0].refs[0]", http.StatusBadRequest},
		{`{"nodes":[{"key":"a","title":"A","kind":"custom-type-q"}]}`, "UNKNOWN_NODE_TYPE", "", http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/graph/subgraph", strings.NewReader(c.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		httpServer.Config.Handler.ServeHTTP(response, request)
		var payload struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &payload)
		if response.Code != c.status || payload.Code != c.code || payload.Field != c.field {
			t.Fatalf("%s: %d %s", c.body, response.Code, response.Body)
		}
	}
}
