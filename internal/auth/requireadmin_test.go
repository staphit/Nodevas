package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"nodevas/internal/identity"
)

// Server-wide settings are a person's to change. An agent acting for an
// administrator, through the copilot or a route a runner's proxy reaches,
// is still an agent, as account administration and the runner access token
// already insist; RequireAdmin is what every admin route group shares.
func TestRequireAdminRefusesAgents(t *testing.T) {
	localAgent := identity.Local
	localAgent.Agent = identity.AgentOrchestrator
	for _, actor := range []identity.Actor{
		{ID: "u1", Name: "ann", Role: identity.RoleAdmin, Agent: identity.AgentWorker},
		{ID: "u1", Name: "ann", Role: identity.RoleAdmin, Agent: identity.AgentOrchestrator},
		localAgent,
	} {
		if err := RequireAdmin(requestAs(actor)); !errors.Is(err, ErrAdminRequired) {
			t.Errorf("RequireAdmin(%+v) = %v, want ErrAdminRequired", actor, err)
		}
	}
	for _, actor := range []identity.Actor{
		{ID: "u1", Name: "ann", Role: identity.RoleAdmin},
		identity.Local,
	} {
		if err := RequireAdmin(requestAs(actor)); err != nil {
			t.Errorf("RequireAdmin(%+v) = %v, want nil", actor, err)
		}
	}
	if err := RequireAdmin(httptest.NewRequest(http.MethodGet, "/api/audit", nil)); !errors.Is(err, ErrAdminRequired) {
		t.Errorf("RequireAdmin with nobody attached = %v, want ErrAdminRequired", err)
	}
}

func requestAs(actor identity.Actor) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/api/remote/config", nil)
	return request.WithContext(WithActor(request.Context(), actor))
}
