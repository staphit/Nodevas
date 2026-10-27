package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// B-23: submit_verdict is a reviewer's and QA's tool, served to runs, and
// posts the structured verdict to the hub.
func TestSubmitVerdictTool(t *testing.T) {
	if !slices.Contains(RunTools, "submit_verdict") {
		t.Fatalf("RunTools = %v, want submit_verdict in it", RunTools)
	}

	var mu sync.Mutex
	var posted []map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/verdicts" {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			posted = append(posted, body)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"round":2,"replaced":false}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	reviewer := roleOptionsSession(t, Options{Server: stub.URL, Role: "reviewer"})
	if names, _ := listedTools(t, reviewer); !slices.Contains(names, "submit_verdict") {
		t.Fatalf("reviewer lists %v, want submit_verdict", names)
	}
	if names, _ := listedTools(t, roleOptionsSession(t, Options{Server: stub.URL, Role: "qa"})); !slices.Contains(names, "submit_verdict") {
		t.Fatalf("qa lists %v, want submit_verdict", names)
	}
	if names, _ := listedTools(t, roleOptionsSession(t, Options{Server: stub.URL, Role: "executor"})); slices.Contains(names, "submit_verdict") {
		t.Fatalf("executor lists submit_verdict: %v", names)
	}

	result, err := reviewer.CallTool(context.Background(), &sdk.CallToolParams{Name: "submit_verdict", Arguments: map[string]any{
		"outcome": "fail", "severity": "medium", "reasons": []string{"tests_weak", "criteria_unmet"}, "note": "the 404 case is untested",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("submit_verdict: %s", errorText(result))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posted) != 1 || posted[0]["outcome"] != "fail" || posted[0]["severity"] != "medium" || posted[0]["note"] != "the 404 case is untested" {
		t.Fatalf("posted = %+v", posted)
	}
	if reasons, _ := posted[0]["reasons"].([]any); len(reasons) != 2 || reasons[0] != "tests_weak" {
		t.Fatalf("posted reasons = %+v", posted[0]["reasons"])
	}
}
