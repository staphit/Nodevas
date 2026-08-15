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
		{RoleVisitor, "visitor"},
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
		{"visitor", Actor{ID: "u3", Name: "Cy", Role: RoleVisitor}, false},
		{"unknown role", Actor{ID: "u4", Name: "Di", Role: Role("editor")}, false},
		{"no role", Actor{ID: "u5", Name: "Eve"}, false},
		// The desktop actor is trusted by the OS account it runs as, so it
		// administers whatever role it happens to be carrying.
		{"loopback actor", Actor{ID: "local", Name: "local", Role: RoleAdmin}, true},
		{"loopback id without a role", Actor{ID: "local"}, true},
		{"loopback id demoted to visitor", Actor{ID: "local", Role: RoleVisitor}, true},
		{"zero actor", Actor{}, false},
	} {
		if got := tc.actor.IsAdmin(); got != tc.want {
			t.Errorf("%s: IsAdmin() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOnlyVisitorsAreDeniedWrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"admin", Actor{ID: "u1", Role: RoleAdmin}, true},
		{"member", Actor{ID: "u2", Role: RoleMember}, true},
		{"visitor", Actor{ID: "u3", Role: RoleVisitor}, false},
		// A role nobody has taught this package about must still be able to
		// write: writing is opt-out by design, so a role added elsewhere fails
		// loudly rather than losing access without a word.
		{"unknown role", Actor{ID: "u4", Role: Role("reviewer")}, true},
		{"empty role", Actor{ID: "u5"}, true},
		{"zero actor", Actor{}, true},
		// Case matters, so a near-miss spelling is not quietly read as visitor.
		{"visitor in the wrong case", Actor{ID: "u6", Role: Role("Visitor")}, true},
	} {
		if got := tc.actor.CanWrite(); got != tc.want {
			t.Errorf("%s: CanWrite() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLocalActorAdministersAndWrites(t *testing.T) {
	if Local.ID != "local" || Local.Name != "local" || Local.Role != RoleAdmin {
		t.Fatalf("Local = %+v", Local)
	}
	if !Local.IsAdmin() {
		t.Error("the loopback actor must administer a desktop server")
	}
	if !Local.CanWrite() {
		t.Error("the loopback actor must be able to write")
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
	// would read as a real role that is neither admin nor visitor.
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
	// An actor stored before roles existed keeps write access, which is the
	// whole point of phrasing CanWrite as a denial.
	if !back.CanWrite() {
		t.Error("a role-less record must not lose write access")
	}
	if back.IsAdmin() {
		t.Error("a role-less record must not gain administration")
	}
}
