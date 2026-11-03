package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestTheBuiltinTableMatchesTheSharedFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "node_types_builtin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Types []NodeType `json:"types"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if got := BuiltinNodeTypes(); !reflect.DeepEqual(got, fixture.Types) {
		t.Fatalf("built-in table drifted from the fixture:\n got %+v\nwant %+v", got, fixture.Types)
	}
}

func TestResolvedNodeTypesAppliesOverridesAndKeepsCustomOrder(t *testing.T) {
	g := &Graph{NodeTypes: []NodeTypeDef{
		{ID: "custom-type-2", Label: "伏筆", Shape: "diamond", AgentMode: AgentNone, Completion: CompletionNone, CountsInProgress: boolPtr(false)},
		{ID: "gate", Shape: "round", Color: "#ff9f9f"},
		{ID: "custom-type-1", Label: "角色", AgentMode: AgentDispatch, Completion: CompletionAnyone, CountsInProgress: boolPtr(true)},
	}}
	types := g.ResolvedNodeTypes()
	if len(types) != len(builtinNodeTypes)+2 {
		t.Fatalf("len = %d", len(types))
	}
	gate := types[4]
	if gate.ID != "gate" || gate.Shape != "round" || gate.Color != "#ff9f9f" || gate.AgentMode != AgentNone || !gate.Builtin {
		t.Fatalf("gate override = %+v", gate)
	}
	if types[9].ID != "custom-type-2" || types[10].ID != "custom-type-1" {
		t.Fatalf("custom order = %s, %s", types[9].ID, types[10].ID)
	}
	if types[10].Shape != "round" || types[10].Builtin {
		t.Fatalf("custom default shape = %+v", types[10])
	}
	if got := g.TypeOf(&Node{Kind: "gate"}); got.Shape != "round" {
		t.Fatalf("TypeOf ignores the override: %+v", got)
	}
}

func TestTypeOfFallsBackToTask(t *testing.T) {
	g := &Graph{}
	for _, kind := range []string{"", "task", "bug", "custom-type-9", " Task "} {
		if got := g.TypeOf(&Node{Kind: kind}); got.ID != "task" {
			t.Errorf("TypeOf(%q) = %s, want task", kind, got.ID)
		}
	}
	if got := g.TypeOf(&Node{Kind: "review"}); got.ID != "gate" {
		t.Errorf("review alias resolves to %s", got.ID)
	}
	if g.KnownNodeKind("bug") || g.KnownNodeKind("custom-type-9") || !g.KnownNodeKind("") || !g.KnownNodeKind("note") {
		t.Error("KnownNodeKind wrong")
	}
}

func TestNormalizeNodeKindAliases(t *testing.T) {
	cases := map[string]string{
		"":              "",
		" Review ":      "gate",
		"decision":      "choice",
		"TASK":          "task",
		"custom-type-A": "custom-type-A",
	}
	for in, want := range cases {
		if got := NormalizeNodeKind(in); got != want {
			t.Errorf("NormalizeNodeKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateNodeTypeDefs(t *testing.T) {
	custom := func(mutate func(*NodeTypeDef)) []NodeTypeDef {
		def := NodeTypeDef{ID: "custom-type-1", Label: "伏筆", AgentMode: AgentNone, Completion: CompletionAnyone, CountsInProgress: boolPtr(true)}
		mutate(&def)
		return []NodeTypeDef{def}
	}
	many := make([]NodeTypeDef, 0, MaxCustomNodeTypes+1)
	for i := 1; i <= MaxCustomNodeTypes+1; i++ {
		many = append(many, NodeTypeDef{ID: fmt.Sprintf("custom-type-%d", i), Label: fmt.Sprintf("T%d", i),
			AgentMode: AgentNone, Completion: CompletionAnyone, CountsInProgress: boolPtr(true)})
	}
	cases := []struct {
		name string
		defs []NodeTypeDef
		want string
	}{
		{"valid", custom(func(*NodeTypeDef) {}), ""},
		{"valid built-in override", []NodeTypeDef{{ID: "gate", Shape: "round", Color: "#fff"}}, ""},
		{"bad prefix", custom(func(d *NodeTypeDef) { d.ID = "type-1" }), "invalid custom node type id"},
		{"built-in with agent mode", []NodeTypeDef{{ID: "gate", AgentMode: AgentDispatch}}, "only shape and color"},
		{"duplicate label", append(custom(func(*NodeTypeDef) {}), NodeTypeDef{ID: "custom-type-2", Label: " 伏筆 ",
			AgentMode: AgentNone, Completion: CompletionAnyone, CountsInProgress: boolPtr(true)}), "duplicate custom node type label"},
		{"duplicate id", append(custom(func(*NodeTypeDef) {}), custom(func(*NodeTypeDef) {})...), "duplicate node type"},
		{"none with dispatch", custom(func(d *NodeTypeDef) {
			d.Completion, d.AgentMode, d.CountsInProgress = CompletionNone, AgentDispatch, boolPtr(false)
		}), "cannot be worked by agents"},
		{"none counting", custom(func(d *NodeTypeDef) { d.Completion = CompletionNone }), "cannot count in progress"},
		{"autopilot with auto", custom(func(d *NodeTypeDef) { d.Completion, d.AgentMode = CompletionAuto, AgentAutopilot }), "Autopilot type"},
		{"nil counts", custom(func(d *NodeTypeDef) { d.CountsInProgress = nil }), "counts in progress"},
		{"missing mode", custom(func(d *NodeTypeDef) { d.AgentMode = "" }), "invalid agent mode"},
		{"too many", many, "too many custom node types"},
		{"bad shape", custom(func(d *NodeTypeDef) { d.Shape = "star" }), "unknown shape"},
		{"url color", custom(func(d *NodeTypeDef) { d.Color = "url(https://x)" }), "invalid color"},
		{"empty label", custom(func(d *NodeTypeDef) { d.Label = "  " }), "empty or oversized label"},
	}
	for _, c := range cases {
		err := ValidateNodeTypeDefs(c.defs)
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
}

func TestNodeTypesRoundTripThroughYAML(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []*Node{{ID: "a", Kind: "custom-type-1"}}, NodeTypes: []NodeTypeDef{
		{ID: "custom-type-1", Label: "角色", Shape: "dashed", AgentMode: AgentNone, Completion: CompletionNone, CountsInProgress: boolPtr(false)},
		{ID: "gate", Shape: "round"},
	}}
	out, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "node_types:") || !strings.Contains(string(out), "counts_in_progress: false") ||
		!strings.Contains(string(out), "agent_mode: none") {
		t.Fatalf("yaml keys wrong:\n%s", out)
	}
	back, err := ParseGraph(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.NodeTypes, g.NodeTypes) {
		t.Fatalf("round trip = %+v", back.NodeTypes)
	}
	js, _ := json.Marshal(g)
	if !strings.Contains(string(js), `"nodeTypes":[{"id":"custom-type-1","label":"角色","shape":"dashed","agentMode":"none","completion":"none","countsInProgress":false}`) {
		t.Fatalf("json shape wrong: %s", js)
	}
}
