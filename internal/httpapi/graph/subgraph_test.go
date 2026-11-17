package graph

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

var (
	httpBea = identity.Actor{ID: "u-bea", Name: "bea", Role: identity.RoleMember}
	httpDan = identity.Actor{ID: "u-dan", Name: "dan", Role: identity.RoleMember}
)

// call runs one handler as actor with a JSON body and the batch id, if any,
// as the :id parameter.
func call(t *testing.T, handler gin.HandlerFunc, actor identity.Actor, path, body, id string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(auth.WithActor(request.Context(), actor))
	response := httptest.NewRecorder()
	c := ginContext(response, request)
	if id != "" {
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	handler(c)
	var payload map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	return response, payload
}

func nodesJSON(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`{"key":"k%d","title":"T%d"}`, i, i)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestSubgraphCheckAnswersWithoutWriting(t *testing.T) {
	api, st := graphTestAPI(t)
	_, rev, err := st.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"nodes":[{"key":"a","title":"A"},{"key":"b","title":"B","dependsOn":["a"]}],"positions":{"a":{"col":2,"row":3}},"batch":{"via":"copilot-draft:d1"}}`
	response, payload := call(t, api.postSubgraphCheck, identity.Local, "/api/graph/subgraph/check", body, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	ids, _ := payload["ids"].(map[string]any)
	if payload["ok"] != true || len(ids) != 2 || payload["rev"] != rev || payload["dependencies"] != 1.0 {
		t.Fatalf("body = %s", response.Body)
	}
	if _, has := payload["batchId"]; has {
		t.Fatalf("a dry run named a batch: %s", response.Body)
	}
	g, after, _ := st.LoadGraph()
	if after != rev || g.NodeByID(ids["a"].(string)) != nil {
		t.Fatal("the dry run wrote the graph")
	}
}

// The dry run refuses with the commit's status, code and field.
func TestSubgraphCheckRefusesAsTheCommitDoes(t *testing.T) {
	api, _ := graphTestAPI(t)
	response, payload := call(t, api.postSubgraph, identity.Local, "/api/graph/subgraph", `{"nodes":[{"key":"a","title":"A"}]}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", response.Code, response.Body)
	}
	a := payload["ids"].(map[string]any)["a"].(string)
	for name, body := range map[string]string{
		"cycle":             fmt.Sprintf(`{"nodes":[{"key":"x","title":"X","dependsOn":[%q]}],"edges":[{"from":"x","to":%q}]}`, a, a),
		"unknown dependsOn": `{"nodes":[{"key":"x","title":"X","dependsOn":["nope"]}]}`,
		"position off grid": `{"nodes":[{"key":"x","title":"X"}],"positions":{"x":{"col":500,"row":0}}}`,
		"draft over cap":    `{"nodes":` + nodesJSON(41) + `,"batch":{"via":"copilot-draft:d1"}}`,
	} {
		check, checkPayload := call(t, api.postSubgraphCheck, identity.Local, "/api/graph/subgraph/check", body, "")
		commit, commitPayload := call(t, api.postSubgraph, identity.Local, "/api/graph/subgraph", body, "")
		if check.Code < 400 || check.Code != commit.Code ||
			checkPayload["code"] != commitPayload["code"] || checkPayload["field"] != commitPayload["field"] {
			t.Errorf("%s: check %d %s, commit %d %s", name, check.Code, check.Body, commit.Code, commit.Body)
		}
	}
}

func TestACopilotDraftIsHeldToFortyNodes(t *testing.T) {
	api, _ := graphTestAPI(t)
	response, payload := call(t, api.postSubgraph, identity.Local, "/api/graph/subgraph",
		`{"nodes":`+nodesJSON(41)+`,"batch":{"via":"copilot-draft:d1"}}`, "")
	if response.Code != http.StatusRequestEntityTooLarge || payload["code"] != "SUBGRAPH_TOO_LARGE" {
		t.Fatalf("41 draft nodes: %d %s", response.Code, response.Body)
	}
	response, _ = call(t, api.postSubgraph, identity.Local, "/api/graph/subgraph", `{"nodes":`+nodesJSON(40)+`,"batch":{"via":"copilot-draft:d1"}}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("40 draft nodes: %d %s", response.Code, response.Body)
	}
	// The general limit stays where it was.
	response, _ = call(t, api.postSubgraph, identity.Local, "/api/graph/subgraph", `{"nodes":`+nodesJSON(41)+`}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("41 plain nodes: %d %s", response.Code, response.Body)
	}
}

func TestSubgraphPositionsAndBatchOverHTTP(t *testing.T) {
	api, st := graphTestAPI(t)
	response, payload := call(t, api.postSubgraph, httpBea, "/api/graph/subgraph",
		`{"nodes":[{"key":"a","title":"A"}],"positions":{"a":{"col":2,"row":3}},"batch":{"via":"copilot-draft:d1"}}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	batchID, _ := payload["batchId"].(string)
	if batchID == "" || payload["undoable"] != true {
		t.Fatalf("body = %s", response.Body)
	}
	a := payload["ids"].(map[string]any)["a"].(string)
	g, _, _ := st.LoadGraph()
	if g.UI == nil || g.UI.Positions[a] != (engine.Position{X: 2, Y: 3}) {
		t.Fatalf("position not applied: %+v", g.UI)
	}

	// Without a batch the answer is as it always was.
	response, payload = call(t, api.postSubgraph, httpBea, "/api/graph/subgraph", `{"nodes":[{"key":"b","title":"B"}]}`, "")
	if _, has := payload["batchId"]; response.Code != http.StatusCreated || has {
		t.Fatalf("plain: %d %s", response.Code, response.Body)
	}
	if _, has := payload["undoable"]; has {
		t.Fatalf("plain answered undoable: %s", response.Body)
	}

	// A cell is a whole number.
	response, _ = call(t, api.postSubgraph, httpBea, "/api/graph/subgraph", `{"nodes":[{"key":"c","title":"C"}],"positions":{"c":{"col":1.5,"row":0}}}`, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("fractional cell: %d %s", response.Code, response.Body)
	}
}

func TestBatchUndoAndRedoOverHTTP(t *testing.T) {
	api, st := graphTestAPI(t)
	response, payload := call(t, api.postSubgraph, httpBea, "/api/graph/subgraph",
		`{"nodes":[{"key":"a","title":"A"},{"key":"b","title":"B","dependsOn":["a"]}],"batch":{"via":"copilot-draft:d1"}}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("commit: %d %s", response.Code, response.Body)
	}
	batchID := payload["batchId"].(string)
	ids := payload["ids"].(map[string]any)

	// Someone else's batch is not found, as an unknown one is.
	response, payload = call(t, api.postBatchUndo, httpDan, "/api/graph/batches/"+batchID+"/undo", "", batchID)
	if response.Code != http.StatusNotFound || payload["code"] != "BATCH_NOT_FOUND" {
		t.Fatalf("dan's undo: %d %s", response.Code, response.Body)
	}
	unknown, unknownPayload := call(t, api.postBatchUndo, httpDan, "/api/graph/batches/b-000000000000000000000000/undo", "", "b-000000000000000000000000")
	if unknown.Code != http.StatusNotFound || unknownPayload["error"] != payload["error"] {
		t.Fatalf("unknown batch: %d %s", unknown.Code, unknown.Body)
	}
	response, _ = call(t, api.postBatchRedo, httpDan, "/api/graph/batches/"+batchID+"/redo", "", batchID)
	if response.Code != http.StatusNotFound {
		t.Fatalf("dan's redo: %d %s", response.Code, response.Body)
	}

	response, payload = call(t, api.postBatchUndo, httpBea, "/api/graph/batches/"+batchID+"/undo", "", batchID)
	trashed, _ := payload["trashed"].([]any)
	refused, _ := payload["refused"].([]any)
	if response.Code != http.StatusOK || len(trashed) != 2 || refused == nil || len(refused) != 0 {
		t.Fatalf("undo: %d %s", response.Code, response.Body)
	}
	if g, _, _ := st.LoadGraph(); g.NodeByID(ids["a"].(string)) != nil {
		t.Fatal("a still on the board")
	}
	response, payload = call(t, api.postBatchUndo, httpBea, "/api/graph/batches/"+batchID+"/undo", "", batchID)
	if trashed, _ := payload["trashed"].([]any); response.Code != http.StatusOK || len(trashed) != 0 {
		t.Fatalf("second undo: %d %s", response.Code, response.Body)
	}

	response, payload = call(t, api.postBatchRedo, httpBea, "/api/graph/batches/"+batchID+"/redo", "", batchID)
	restored, _ := payload["restored"].([]any)
	if response.Code != http.StatusOK || len(restored) != 2 || payload["links"] != 0.0 {
		t.Fatalf("redo: %d %s", response.Code, response.Body)
	}
	g, _, _ := st.LoadGraph()
	if b := g.NodeByID(ids["b"].(string)); b == nil || b.Requires != ids["a"] {
		t.Fatalf("b = %+v", b)
	}
}
