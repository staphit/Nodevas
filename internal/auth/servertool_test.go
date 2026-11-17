package auth

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestCopilotServerToolMarker(t *testing.T) {
	if CopilotServerTool(context.Background()) {
		t.Fatal("a bare context is marked")
	}
	if !CopilotServerTool(WithCopilotServerTool(context.Background())) {
		t.Fatal("WithCopilotServerTool did not mark the context")
	}
	// No header of an incoming request can set it.
	r := httptest.NewRequest("POST", "/api/runs", nil)
	for _, name := range []string{"X-Copilot-Server-Tool", "X-Nodevas-Copilot", "copilotServerToolKey"} {
		r.Header.Set(name, "true")
	}
	if CopilotServerTool(r.Context()) {
		t.Fatal("an incoming request is marked")
	}
}
