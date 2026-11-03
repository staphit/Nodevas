package auth

import (
	"errors"
	"net/http/httptest"
	"testing"

	"nodevas/internal/identity"
)

// An external `nodevas mcp --role tester` session on loopback declares its
// role; the class defaults to worker when only a role is sent, and the role
// never lifts the class.
func TestLoopbackRoleHeader(t *testing.T) {
	for _, tc := range []struct {
		name, class, role string
		wantClass         identity.AgentClass
	}{
		{"role only", "", "tester", identity.AgentWorker},
		{"worker executor", "worker", "executor", identity.AgentWorker},
		{"orchestrator planner", "orchestrator", "planner", identity.AgentOrchestrator},
		{"custom", "worker", "custom-role-lore_keeper", identity.AgentWorker},
	} {
		request := httptest.NewRequest("GET", "/api/graph", nil)
		if tc.class != "" {
			request.Header.Set(AgentRoleHeaderName, tc.class)
		}
		request.Header.Set(RoleHeaderName, tc.role)
		actor, err := LocalOnly{}.Authenticate(request)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if actor.Agent != tc.wantClass || actor.AgentRole != tc.role {
			t.Errorf("%s: actor = %+v, want class %q role %q", tc.name, actor, tc.wantClass, tc.role)
		}
		if actor.ID != identity.Local.ID {
			t.Errorf("%s: not the local account: %+v", tc.name, actor)
		}
	}
	// No role header: today's behavior exactly.
	request := httptest.NewRequest("GET", "/api/graph", nil)
	request.Header.Set(AgentRoleHeaderName, "worker")
	if actor, err := (LocalOnly{}).Authenticate(request); err != nil || actor.AgentRole != "" || actor.Agent != identity.AgentWorker {
		t.Fatalf("legacy worker = %+v, %v", actor, err)
	}
}

func TestLoopbackRoleHeaderSyntax(t *testing.T) {
	for _, bad := range []string{"boss", "Planner", "custom-role-", "custom-role-a/b", "planner,qa"} {
		request := httptest.NewRequest("GET", "/api/graph", nil)
		request.Header.Set(RoleHeaderName, bad)
		if _, err := (LocalOnly{}).Authenticate(request); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("role %q: err = %v, want ErrInvalidRole", bad, err)
		}
	}
}
