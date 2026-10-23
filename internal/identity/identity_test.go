package identity

import (
	"encoding/json"
	"testing"
)

func TestRoleConstantsMatchTheirWireStrings(t *testing.T) {
	// The role travels as JSON and is compared against stored values, so
	// renaming a constant's string silently locks people out of their role.
	for _, tc := range []struct {
		role Role
		want string
	}{
		{RoleAdmin, "admin"},
		{RoleMember, "member"},
	} {
		if string(tc.role) != tc.want {
			t.Errorf("role = %q, want %q", tc.role, tc.want)
		}
	}
}

func TestOnlyAdminsAndTheLocalActorAdminister(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"admin", Actor{ID: "u1", Name: "Ada", Role: RoleAdmin}, true},
		{"member", Actor{ID: "u2", Name: "Bo", Role: RoleMember}, false},
		{"unknown role", Actor{ID: "u4", Name: "Di", Role: Role("editor")}, false},
		{"no role", Actor{ID: "u5", Name: "Eve"}, false},
		// The desktop actor is trusted by the OS account it runs as, so it
		// administers whatever role it happens to be carrying.
		{"loopback actor", Actor{ID: "local", Name: "local", Role: RoleAdmin}, true},
		{"loopback id without a role", Actor{ID: "local"}, true},
		{"loopback id carrying a member role", Actor{ID: "local", Role: RoleMember}, true},
		{"zero actor", Actor{}, false},
	} {
		if got := tc.actor.IsAdmin(); got != tc.want {
			t.Errorf("%s: IsAdmin() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLocalActorAdministers(t *testing.T) {
	if Local.ID != "local" || Local.Name != "local" || Local.Role != RoleAdmin {
		t.Fatalf("Local = %+v", Local)
	}
	if !Local.IsAdmin() {
		t.Error("the loopback actor must administer a desktop server")
	}
}

func TestOnlyAgentsReadAsAgents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"human", Actor{ID: "u1", Agent: AgentHuman}, false},
		{"zero actor", Actor{}, false},
		{"worker", Actor{ID: "local", Agent: AgentWorker}, true},
		{"orchestrator", Actor{ID: "local", Agent: AgentOrchestrator}, true},
	} {
		if got := tc.actor.IsAgent(); got != tc.want {
			t.Errorf("%s: IsAgent() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAgentClassRoundTripsThroughJSON(t *testing.T) {
	actor := Actor{ID: "local", Name: "local", Role: RoleAdmin, Agent: AgentWorker}
	data, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"id":"local","name":"local","role":"admin","agent":"worker"}`; got != want {
		t.Fatalf("marshalled to %s, want %s", got, want)
	}
	var back Actor
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back != actor {
		t.Fatalf("round-trip gave %+v, want %+v", back, actor)
	}
	// A human actor must not persist `"agent":""`, which old records lack.
	data, err = json.Marshal(Actor{ID: "u1", Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"id":"u1","name":"Ada"}`; got != want {
		t.Fatalf("human actor marshalled to %s, want %s", got, want)
	}
}

func TestActorRoundTripsThroughJSON(t *testing.T) {
	// Actors are persisted in the audit trail, so a field renamed on the Go
	// side must not change what old records decode to.
	actor := Actor{ID: "u1", Name: "Ada", Role: RoleMember}
	data, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"id":"u1","name":"Ada","role":"member"}`; got != want {
		t.Fatalf("marshalled to %s, want %s", got, want)
	}
	var back Actor
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back != actor {
		t.Fatalf("round-trip gave %+v, want %+v", back, actor)
	}
}

func TestUnsetRoleIsOmittedFromJSON(t *testing.T) {
	data, err := json.Marshal(Actor{ID: "u1", Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	// omitempty keeps a role-less actor from persisting `"role":""`, which
	// would read as a real role that is neither admin nor member.
	if got, want := string(data), `{"id":"u1","name":"Ada"}`; got != want {
		t.Fatalf("marshalled to %s, want %s", got, want)
	}
	var back Actor
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Role != "" {
		t.Fatalf("absent role decoded as %q", back.Role)
	}
	if back.IsAdmin() {
		t.Error("a role-less record must not gain administration")
	}
}
