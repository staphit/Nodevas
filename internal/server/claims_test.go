package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nodevas/internal/engine"
)

func postJSON(t *testing.T, server *Server, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", response.Body, err)
	}
	return body
}

func TestClaimingANodeMovesItAndReportsTheLease(t *testing.T) {
	server, _ := readyQueueServer(t)

	response := postJSON(t, server, "/api/nodes/design/claim", map[string]any{
		"owner": "mcp:agent", "leaseSeconds": 600,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	body := decodeBody(t, response)
	claim, ok := body["claim"].(map[string]any)
	if !ok || claim["owner"] != "mcp:agent" {
		t.Fatalf("claim = %v, want it held by the caller", body["claim"])
	}
	statuses, _ := body["statuses"].(map[string]any)
	if statuses["design"] != string(engine.StatusInProgress) {
		t.Fatalf("statuses = %v, want design in_progress", statuses)
	}
}

// A status alone cannot tell these apart, and a caller's next move differs:
// wait for the other holder, or pick something else entirely.
func TestASecondClaimSaysWhoHoldsItAndWhy(t *testing.T) {
	server, _ := readyQueueServer(t)
	if response := postJSON(t, server, "/api/nodes/design/claim",
		map[string]any{"owner": "first"}); response.Code != http.StatusOK {
		t.Fatalf("first claim: %d %s", response.Code, response.Body)
	}
	taken := postJSON(t, server, "/api/nodes/design/claim", map[string]any{"owner": "second"})
	if taken.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", taken.Code)
	}
	body := decodeBody(t, taken)
	if body["code"] != "ALREADY_CLAIMED" || body["owner"] != "first" {
		t.Fatalf("body = %v, want ALREADY_CLAIMED naming first", body)
	}

	blocked := postJSON(t, server, "/api/nodes/build/claim", map[string]any{"owner": "second"})
	if blocked.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", blocked.Code)
	}
	if code := decodeBody(t, blocked)["code"]; code != "NOT_CLAIMABLE" {
		t.Fatalf("code = %v, want NOT_CLAIMABLE for a blocked node", code)
	}
}

// The editor sends {status, by, note} and knows nothing about claims. It must
// keep working exactly as it did.
func TestTheEditorsStatusCallIsUnchanged(t *testing.T) {
	server, _ := readyQueueServer(t)

	response := postJSON(t, server, "/api/nodes/design/status", map[string]any{
		"status": "done", "by": "patrick", "note": "did it",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	statuses, _ := decodeBody(t, response)["statuses"].(map[string]any)
	if statuses["design"] != "done" {
		t.Fatalf("statuses = %v, want design done", statuses)
	}
}

func TestAnAgentCannotFinishWorkAnotherAgentHolds(t *testing.T) {
	server, _ := readyQueueServer(t)
	if response := postJSON(t, server, "/api/nodes/design/claim",
		map[string]any{"owner": "first"}); response.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", response.Code, response.Body)
	}

	response := postJSON(t, server, "/api/nodes/design/status", map[string]any{
		"status": "done", "by": "second", "owner": "second",
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
	if code := decodeBody(t, response)["code"]; code != "NOT_OWNER" {
		t.Fatalf("code = %v, want NOT_OWNER", code)
	}
}

func TestWhatIsBeingWorkedOnCanBeListed(t *testing.T) {
	server, _ := readyQueueServer(t)
	if response := postJSON(t, server, "/api/nodes/design/claim",
		map[string]any{"owner": "mcp:agent"}); response.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", response.Code, response.Body)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/claims", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	claims, _ := decodeBody(t, response)["claims"].([]any)
	if len(claims) != 1 {
		t.Fatalf("claims = %v, want one", claims)
	}
	held, _ := claims[0].(map[string]any)
	if held["nodeId"] != "design" || held["owner"] != "mcp:agent" {
		t.Fatalf("claim = %v, want design held by the agent", held)
	}
}

func TestAClaimedNodeLeavesTheReadyQueue(t *testing.T) {
	server, _ := readyQueueServer(t)
	if response := postJSON(t, server, "/api/nodes/design/claim",
		map[string]any{"owner": "mcp:agent"}); response.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", response.Code, response.Body)
	}

	if queue := getReadyQueue(t, server, ""); len(queue.Tasks) != 0 {
		t.Fatalf("tasks = %v, want the claimed node gone from the queue", taskIDs(queue.Tasks))
	}
}
