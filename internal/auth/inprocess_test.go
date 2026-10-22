package auth

import (
	"context"
	"net/http/httptest"
	"testing"

	"nodevas/internal/identity"
)

func TestInProcessAgentRoundTrips(t *testing.T) {
	ctx := WithInProcessAgent(context.Background(), identity.AgentOrchestrator)
	class, ok := InProcessAgent(ctx)
	if !ok || class != identity.AgentOrchestrator {
		t.Fatalf("InProcessAgent = %q, %v; want orchestrator, true", class, ok)
	}
	// An explicitly empty class is still reported as set, so the caller can
	// refuse it instead of mistaking it for "not in-process".
	class, ok = InProcessAgent(WithInProcessAgent(context.Background(), identity.AgentHuman))
	if !ok || class != identity.AgentHuman {
		t.Fatalf("empty class = %q, %v; want \"\", true", class, ok)
	}
}

func TestInProcessAgentAbsentOnOrdinaryRequests(t *testing.T) {
	if _, ok := InProcessAgent(context.Background()); ok {
		t.Fatal("a bare context claims to be in-process")
	}
	// A nil context must answer, not panic.
	if _, ok := InProcessAgent(nil); ok { //nolint:staticcheck // SA1012 is the point
		t.Fatal("a nil context claims to be in-process")
	}
	// Nothing a client can put on the wire turns into the marker: the
	// context of a request built from bytes is the server's own.
	request := httptest.NewRequest("POST", "/api/nodes", nil)
	request.Header.Set("X-Nodevas-In-Process", "orchestrator")
	request.Header.Set(AgentRoleHeaderName, "orchestrator")
	if _, ok := InProcessAgent(request.Context()); ok {
		t.Fatal("headers produced the in-process marker")
	}
	// A plain string key with the same spelling is a different key.
	type lookalike struct{}
	forged := context.WithValue(context.Background(), lookalike{}, identity.AgentOrchestrator)
	if _, ok := InProcessAgent(forged); ok {
		t.Fatal("a foreign key produced the in-process marker")
	}
}
