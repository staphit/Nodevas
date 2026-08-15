package auth

import (
	"errors"
	"net/http/httptest"
	"testing"

	"nodevas/internal/identity"
)

func TestLocalOnlyReadsTheAgentRoleHeader(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  identity.AgentClass
	}{
		{"no header", "", identity.AgentHuman},
		{"worker", "worker", identity.AgentWorker},
		{"orchestrator", "orchestrator", identity.AgentOrchestrator},
	} {
		request := httptest.NewRequest("GET", "/api/graph", nil)
		if tc.value != "" {
			request.Header.Set(AgentRoleHeaderName, tc.value)
		}
		actor, err := LocalOnly{}.Authenticate(request)
		if err != nil {
			t.Fatalf("%s: Authenticate: %v", tc.name, err)
		}
		if actor.Agent != tc.want {
			t.Errorf("%s: agent = %q, want %q", tc.name, actor.Agent, tc.want)
		}
		// Whatever the class, the request still acts as the local account.
		if actor.ID != identity.Local.ID || actor.Role != identity.Local.Role {
			t.Errorf("%s: actor = %+v, want the local account", tc.name, actor)
		}
	}
}

func TestLocalOnlyRefusesAnUnknownAgentRole(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/graph", nil)
	request.Header.Set(AgentRoleHeaderName, "supervisor")
	_, err := LocalOnly{}.Authenticate(request)
	if !errors.Is(err, ErrUnknownAgentRole) {
		t.Fatalf("error = %v, want ErrUnknownAgentRole", err)
	}
}
