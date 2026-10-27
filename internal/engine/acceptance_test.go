package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAcceptanceValidation(t *testing.T) {
	items := func(n int) []AcceptanceItem {
		out := make([]AcceptanceItem, n)
		for i := range out {
			out[i] = AcceptanceItem{ID: fmt.Sprintf("a%d", i+1), Text: fmt.Sprintf("criterion %d", i+1)}
		}
		return out
	}
	g := &Graph{NodeTypes: []NodeTypeDef{
		{ID: "custom-type-bug", Label: "Bug", AgentMode: AgentDispatch, Completion: CompletionAnyone, CountsInProgress: boolPtr(true)},
	}}
	cases := []struct {
		name string
		kind string
		acc  *Acceptance
		want string
	}{
		{"task", "", &Acceptance{Items: items(3)}, ""},
		{"scene", "scene", &Acceptance{Items: items(1)}, ""},
		{"event", "event", &Acceptance{Items: items(1)}, ""},
		{"goal", "goal", &Acceptance{Items: items(2)}, ""},
		{"custom dispatch", "custom-type-bug", &Acceptance{Items: items(1)}, ""},
		{"thirty", "", &Acceptance{Items: items(30)}, ""},
		{"thirty-one", "", &Acceptance{Items: items(31)}, "at most 30"},
		{"long text", "", &Acceptance{Items: []AcceptanceItem{{ID: "a1", Text: strings.Repeat("字", 401)}}}, "400"},
		{"400 runes", "", &Acceptance{Items: []AcceptanceItem{{ID: "a1", Text: strings.Repeat("字", 400)}}}, ""},
		{"empty text", "", &Acceptance{Items: []AcceptanceItem{{ID: "a1", Text: "  "}}}, "empty"},
		{"multi-line", "", &Acceptance{Items: []AcceptanceItem{{ID: "a1", Text: "one\ntwo"}}}, "one line"},
		{"bad id", "", &Acceptance{Items: []AcceptanceItem{{ID: "x1", Text: "t"}}}, "id"},
		{"id too long", "", &Acceptance{Items: []AcceptanceItem{{ID: "a1000", Text: "t"}}}, "id"},
		{"duplicate id", "", &Acceptance{Items: []AcceptanceItem{{ID: "a1", Text: "t"}, {ID: "a1", Text: "u"}}}, "duplicate"},
		{"note refused", "note", &Acceptance{Items: items(1)}, "type"},
		{"gate refused", "gate", &Acceptance{Items: items(1)}, "type"},
		{"start refused", "start", &Acceptance{Items: items(1)}, "type"},
	}
	for _, c := range cases {
		n := &Node{ID: "n", Kind: c.kind, Acceptance: c.acc}
		err := ValidateNodeAcceptance(g, n)
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
	if !g.AcceptanceAllowed(&Node{Kind: "goal"}) || g.AcceptanceAllowed(&Node{Kind: "note"}) {
		t.Error("AcceptanceAllowed wrong")
	}
}

func TestAcceptanceIDsAreStable(t *testing.T) {
	got := AssignAcceptanceIDs([]AcceptanceItem{{ID: "a2", Text: "kept"}, {Text: "new"}, {ID: "a7", Text: "kept too"}, {Text: "newer"}})
	want := []AcceptanceItem{{ID: "a2", Text: "kept"}, {ID: "a8", Text: "new"}, {ID: "a7", Text: "kept too"}, {ID: "a9", Text: "newer"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AssignAcceptanceIDs = %+v, want %+v", got, want)
	}
	if first := AssignAcceptanceIDs([]AcceptanceItem{{Text: "x"}}); len(first) != 1 || first[0].ID != "a1" {
		t.Fatalf("first id = %+v", first)
	}
}

func TestAcceptanceRoundTripsGraphYAML(t *testing.T) {
	acc := &Acceptance{Items: []AcceptanceItem{{ID: "a1", Text: "埋下關鍵伏筆"}, {ID: "a2", Text: "keep Lin's POV"}}, Locked: true, By: "planner (run abc)"}
	g := &Graph{Version: 1, Nodes: []*Node{{ID: "s1", Kind: "scene", Acceptance: acc}}}
	out, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "acceptance:") || !strings.Contains(string(out), "locked: true") {
		t.Fatalf("graph.yaml:\n%s", out)
	}
	back, err := ParseGraph(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Nodes[0].Acceptance, acc) {
		t.Fatalf("round trip = %+v", back.Nodes[0].Acceptance)
	}
	raw, _ := json.Marshal(g.Nodes[0])
	if !strings.Contains(string(raw), `"acceptance":{"items":[{"id":"a1"`) {
		t.Fatalf("JSON: %s", raw)
	}
}
