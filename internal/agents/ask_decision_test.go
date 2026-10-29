package agents

import (
	"encoding/json"
	"testing"
)

// An ask's JSON names the decision that settled it, so a relay can tell
// "allow for this run" from "allow once" without reading answer.
func TestAskJSONNamesItsDecision(t *testing.T) {
	for want, ask := range map[string]Ask{
		"":                {Kind: AskKindPermission, Status: AskPending},
		"approve":         {Kind: AskKindPermission, Status: AskApproved},
		"approve_run":     {Kind: AskKindPermission, Status: AskApproved, Answer: DecisionApproveRun},
		"deny":            {Kind: AskKindQuestion, Status: AskDenied},
		"answer":          {Kind: AskKindChoice, Status: AskAnswered, Answer: "a"},
		"expired is none": {Kind: AskKindPermission, Status: AskExpired},
	} {
		raw, err := json.Marshal(ask)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Decision *string `json:"decision"`
			Kind     string  `json:"kind"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		expect := want
		if want == "expired is none" {
			expect = ""
		}
		if expect == "" {
			if got.Decision != nil {
				t.Errorf("%s: decision %q, want none: %s", want, *got.Decision, raw)
			}
			continue
		}
		if got.Decision == nil || *got.Decision != expect || got.Kind != ask.Kind {
			t.Errorf("%s: %s", want, raw)
		}
		// It survives a round trip (the MCP client decodes the hub's Ask).
		var back Ask
		if err := json.Unmarshal(raw, &back); err != nil || back.Decision != expect {
			t.Errorf("%s: decoded decision %q, %v", want, back.Decision, err)
		}
	}
}
