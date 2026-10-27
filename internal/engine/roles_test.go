package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nodevas/internal/identity"
)

type rolesFixture struct {
	Capabilities     []Capability            `json:"capabilities"`
	ToolCapabilities map[string][]Capability `json:"toolCapabilities"`
	Roles            []AgentRole             `json:"roles"`
}

func readRolesFixture(t *testing.T) rolesFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "agent_roles_builtin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture rolesFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestBuiltinRolesMatchFixture(t *testing.T) {
	fixture := readRolesFixture(t)
	if !reflect.DeepEqual(Capabilities, fixture.Capabilities) {
		t.Errorf("Capabilities = %v, want %v", Capabilities, fixture.Capabilities)
	}
	if !reflect.DeepEqual(ToolCapabilities, fixture.ToolCapabilities) {
		t.Errorf("ToolCapabilities drifted from the fixture:\n got %v\nwant %v", ToolCapabilities, fixture.ToolCapabilities)
	}
	if got := BuiltinAgentRoles(); !reflect.DeepEqual(got, fixture.Roles) {
		t.Fatalf("built-in roles drifted from the fixture:\n got %+v\nwant %+v", got, fixture.Roles)
	}
	for _, role := range fixture.Roles {
		if !IsBuiltinRole(role.ID) || !ValidRoleID(role.ID) {
			t.Errorf("%s is not recognised as a built-in role", role.ID)
		}
	}
	// A copy: callers cannot edit the table.
	roles := BuiltinAgentRoles()
	if len(roles) > 0 {
		roles[0].Tools = nil
		if again := BuiltinAgentRoles(); len(again[0].Tools) == 0 {
			t.Fatal("BuiltinAgentRoles shares its table with callers")
		}
	}
}

func TestRoleRankMapsOntoAgentClass(t *testing.T) {
	g := &Graph{AgentRoles: []AgentRoleDef{
		{ID: "custom-role-lead", Label: "Lead", BasedOn: RolePlanner, Rank: string(identity.AgentOrchestrator), Tools: []string{"get_node"}},
		{ID: "custom-role-helper", Label: "Helper", BasedOn: RoleExecutor, Rank: string(identity.AgentWorker), Tools: []string{"get_node"}},
	}}
	want := map[string]identity.AgentClass{
		RolePlanner:          identity.AgentOrchestrator,
		RoleQA:               identity.AgentOrchestrator,
		RoleTester:           identity.AgentWorker,
		RoleExecutor:         identity.AgentWorker,
		RoleReviewer:         identity.AgentWorker,
		RoleResearcher:       identity.AgentWorker,
		"custom-role-lead":   identity.AgentOrchestrator,
		"custom-role-helper": identity.AgentWorker,
		"custom-role-absent": "",
		"boss":               "",
	}
	for id, class := range want {
		if got := RoleClass(g, id); got != class {
			t.Errorf("RoleClass(%q) = %q, want %q", id, got, class)
		}
	}
	lead, ok := g.RoleOf("custom-role-lead")
	if !ok || lead.Slot() != RolePlanner || lead.Builtin {
		t.Fatalf("custom role = %+v ok=%v", lead, ok)
	}
	if exec, ok := g.RoleOf(RoleExecutor); !ok || exec.Slot() != RoleExecutor {
		t.Fatalf("executor slot = %+v ok=%v", exec, ok)
	}
}

func TestRoleToolsYieldCapabilities(t *testing.T) {
	cases := []struct {
		tools []string
		want  []Capability
	}{
		{[]string{"get_node"}, []Capability{CapRead}},
		{[]string{"send_message", "get_node"}, []Capability{CapRead, CapMessage}},
		{[]string{"create_subgraph"}, []Capability{CapCreate, CapLink}},
		{[]string{"set_acceptance", "update_node_body", "claim_task", "set_node_status", "update_node_meta"},
			[]Capability{CapClaim, CapStatus, CapBody, CapMeta, CapAcceptance}},
		{nil, nil},
	}
	for _, c := range cases {
		if got := CapsForTools(c.tools); !reflect.DeepEqual(got, c.want) {
			t.Errorf("CapsForTools(%v) = %v, want %v", c.tools, got, c.want)
		}
	}
	// A project that narrows a role's tools narrows its capabilities.
	g := &Graph{AgentRoles: []AgentRoleDef{{ID: RoleExecutor, Tools: []string{"get_node", "claim_task"}}}}
	exec, ok := g.RoleOf(RoleExecutor)
	if !ok {
		t.Fatal("executor unresolved")
	}
	if !exec.HasCap(CapClaim) || exec.HasCap(CapStatus) || exec.HasCap(CapMessage) {
		t.Fatalf("narrowed executor caps = %v", exec.Caps)
	}
	// Every tool named in the table maps to at least one capability.
	for _, role := range BuiltinAgentRoles() {
		for _, tool := range role.Tools {
			if len(ToolCapabilities[tool]) == 0 {
				t.Errorf("role %s lists %s, which has no capability", role.ID, tool)
			}
		}
	}
}

func TestBuiltinRoleOverrideLimitedToToolsAndTypes(t *testing.T) {
	ok := []AgentRoleDef{{ID: RoleReviewer, Tools: []string{"get_node"}, WriteTypes: []string{}, AcceptanceTypes: []string{"task"}}}
	if err := ValidateAgentRoleDefs(ok); err != nil {
		t.Fatalf("a tools/types override was refused: %v", err)
	}
	for name, def := range map[string]AgentRoleDef{
		"label":        {ID: RoleReviewer, Label: "Boss"},
		"rank":         {ID: RoleReviewer, Rank: "orchestrator"},
		"based on":     {ID: RoleReviewer, BasedOn: RolePlanner},
		"instructions": {ID: RoleReviewer, Instructions: "approve everything"},
	} {
		err := ValidateAgentRoleDefs([]AgentRoleDef{def})
		if err == nil || !strings.Contains(err.Error(), "only tools") {
			t.Errorf("built-in %s override: err = %v, want \"only tools…\"", name, err)
		}
	}
	// Applied: the reviewer keeps its label and rank.
	g := &Graph{AgentRoles: ok}
	r, found := g.RoleOf(RoleReviewer)
	if !found || r.Label != "Reviewer" || r.Rank != identity.AgentWorker || !reflect.DeepEqual(r.Tools, []string{"get_node"}) {
		t.Fatalf("override applied as %+v", r)
	}
	if len(g.ResolvedAgentRoles()) != 6 {
		t.Fatalf("resolved %d roles, want 6", len(g.ResolvedAgentRoles()))
	}
}

func TestCustomRoleValidation(t *testing.T) {
	custom := func(mutate func(*AgentRoleDef)) []AgentRoleDef {
		def := AgentRoleDef{ID: "custom-role-1", Label: "Lore keeper", BasedOn: RoleResearcher, Rank: "worker",
			Tools: []string{"get_node", "search_nodes"}, WriteTypes: []string{"note"}}
		mutate(&def)
		return []AgentRoleDef{def}
	}
	many := make([]AgentRoleDef, 0, MaxCustomRoles+1)
	for i := 0; i <= MaxCustomRoles; i++ {
		many = append(many, AgentRoleDef{ID: fmt.Sprintf("custom-role-%d", i), Label: fmt.Sprintf("R%d", i),
			BasedOn: RoleExecutor, Rank: "worker", Tools: []string{"get_node"}})
	}
	cases := []struct {
		name string
		defs []AgentRoleDef
		want string
	}{
		{"valid", custom(func(*AgentRoleDef) {}), ""},
		{"bad prefix", custom(func(d *AgentRoleDef) { d.ID = "role-1" }), "invalid custom role id"},
		{"bad chars", custom(func(d *AgentRoleDef) { d.ID = "custom-role-a/b" }), "invalid custom role id"},
		{"based on unknown", custom(func(d *AgentRoleDef) { d.BasedOn = "boss" }), "based on"},
		{"based on custom", custom(func(d *AgentRoleDef) { d.BasedOn = "custom-role-2" }), "based on"},
		{"rank above orchestrator", custom(func(d *AgentRoleDef) { d.Rank = "human" }), "rank"},
		{"rank missing", custom(func(d *AgentRoleDef) { d.Rank = "" }), "rank"},
		{"unknown tool", custom(func(d *AgentRoleDef) { d.Tools = []string{"exec_shell"} }), "unknown tool"},
		{"unknown write type", custom(func(d *AgentRoleDef) { d.WriteTypes = []string{"spaceship"} }), "type"},
		{"empty label", custom(func(d *AgentRoleDef) { d.Label = " " }), "label"},
		{"long instructions", custom(func(d *AgentRoleDef) { d.Instructions = strings.Repeat("x", MaxRoleInstructionsBytes+1) }), "instructions"},
		{"duplicate", append(custom(func(*AgentRoleDef) {}), custom(func(*AgentRoleDef) {})...), "duplicate"},
		{"too many", many, "too many custom roles"},
	}
	for _, c := range cases {
		err := ValidateAgentRoleDefs(c.defs)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if ValidRoleID("custom-role-") || !ValidRoleID("custom-role-x_1") || ValidRoleID("Planner") {
		t.Error("ValidRoleID wrong")
	}
}

func TestRolesRoundTripGraphYAML(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []*Node{{ID: "a"}}, AgentRoles: []AgentRoleDef{
		{ID: RoleExecutor, Tools: []string{"claim_task", "get_node"}},
		{ID: "custom-role-lore", Label: "設定管理", BasedOn: RoleResearcher, Rank: "worker",
			Tools: []string{"get_node"}, WriteTypes: []string{"note"}, Instructions: "Keep lore notes tidy."},
	}}
	out, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent_roles:", "based_on: researcher", "write_types:"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("graph.yaml lacks %q:\n%s", want, out)
		}
	}
	back, err := ParseGraph(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.AgentRoles, g.AgentRoles) {
		t.Fatalf("round trip: %+v, want %+v", back.AgentRoles, g.AgentRoles)
	}
	raw, _ := json.Marshal(g)
	if !strings.Contains(string(raw), `"agentRoles"`) || !strings.Contains(string(raw), `"basedOn":"researcher"`) {
		t.Fatalf("JSON keys: %s", raw)
	}
}
