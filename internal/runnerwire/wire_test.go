package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestHelloAgentInteractiveAndUntrusted(t *testing.T) {
	if TypeEnd != "end" {
		t.Fatalf("TypeEnd = %q", TypeEnd)
	}
	info := Info{
		Version: "1.0",
		Agents: []Agent{
			{Kind: "claude", Installed: true, Interactive: true, Untrusted: false},
			{Kind: "codex", Installed: true, Interactive: true, Untrusted: true},
		},
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"interactive":true`, `"untrusted":true`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("Info JSON lacks %s:\n%s", want, raw)
		}
	}
	var back Info
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, info) {
		t.Fatalf("Info round trip = %+v, %v", back, err)
	}
	// Untrusted false is omitted on the wire.
	clean, _ := json.Marshal(Agent{Kind: "claude", Installed: true})
	if strings.Contains(string(clean), "interactive") || strings.Contains(string(clean), "untrusted") {
		t.Errorf("plain Agent leaks optional flags: %s", clean)
	}
	// TypeEnd envelope carries RunID and optional NoPR.
	endEnv := Envelope{Type: TypeEnd, RunID: "r1", NoPR: true}
	rawEnd, err := json.Marshal(endEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawEnd), `"type":"end"`) || !strings.Contains(string(rawEnd), `"runId":"r1"`) || !strings.Contains(string(rawEnd), `"noPR":true`) {
		t.Fatalf("End envelope = %s", rawEnd)
	}
}
