package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// A gate with no inputs reaches the browser as [], never null, whether it came
// from graph.yaml or from a client that left the field out.
func TestLogicGateInputsAreNeverNull(t *testing.T) {
	g, err := ParseGraph([]byte("version: 1\nui:\n  logic_gates:\n    - id: g1\n      operator: and\n      x: 0\n      y: 0\n      inputs:\n"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(g.UI.LogicGates)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"inputs":[]`) {
		t.Fatalf("parsed gate encodes as %s", encoded)
	}

	var sent Graph
	if err := json.Unmarshal([]byte(`{"version":1,"ui":{"logicGates":[{"id":"g2","operator":"or","inputs":null}]}}`), &sent); err != nil {
		t.Fatal(err)
	}
	NormalizeLogicGates(&sent)
	if sent.UI.LogicGates[0].Inputs == nil {
		t.Fatal("a gate sent with null inputs was not normalized")
	}
	NormalizeLogicGates(nil)
	NormalizeLogicGates(&Graph{})
}
