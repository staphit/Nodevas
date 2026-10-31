package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A permission prompt whose tool input was cut, by the hub or by whoever
// relayed it (the runner, the MCP sidecar), says so: the person cannot see
// all of what they allow. Allowing it for the run allows this call only,
// whatever the tool, since the rest of the input is unknown.
func TestCutToolInputIsFlaggedAndAllowedOnce(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		alice := Viewer{ID: "alice"}
		registerRun(t, hub, "r1", "app-claude-r1", "run-1")

		hubCut := shellIn("Edit", `{"file_path":"a.go","new_string":"`+strings.Repeat("x", 2*MaxAskToolInputBytes)+`"}`, "Edit a.go?")
		clientCut := shellIn("Bash", `{"command":"echo short"}`, "Run echo?")
		clientCut.ToolInputTruncated = true
		fileCut := shellIn("Write", `{"file_path":"b.go","content":"package b"}`, "Write b.go?")
		fileCut.ToolInputTruncated = true

		for name, in := range map[string]AskInput{"hub cut": hubCut, "runner cut": clientCut, "runner cut file": fileCut} {
			first, done := startAsk(t, hub, in, 5*time.Second)
			if !first.ToolInputTruncated {
				t.Fatalf("%s: listed ask %+v is not flagged as cut", name, first)
			}
			if len(first.ToolInput) > MaxAskToolInputBytes {
				t.Fatalf("%s: stored %d bytes of input", name, len(first.ToolInput))
			}
			if first.RunScope() != RunScopeOnce {
				t.Fatalf("%s: run scope = %q, want %q", name, first.RunScope(), RunScopeOnce)
			}
			raw, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"toolInputTruncated":true`) || !strings.Contains(string(raw), `"runScope":"once"`) {
				t.Fatalf("%s: JSON %s", name, raw)
			}
			if _, err := hub.AnswerWith(ctx, "alpha", first.ID, AnswerInput{Decision: DecisionApproveRun}, alice); err != nil {
				t.Fatal(err)
			}
			if got := waitDecided(t, done); got.Status != AskApproved || !got.ToolInputTruncated {
				t.Fatalf("%s: decided = %+v", name, got)
			}

			// The same call again is asked again: nothing was cached.
			again, doneAgain := startAsk(t, hub, in, 5*time.Second)
			if again.Status != AskPending {
				t.Fatalf("%s: again = %+v, want asked", name, again)
			}
			if _, err := hub.Answer(ctx, "alpha", again.ID, "deny", "", alice); err != nil {
				t.Fatal(err)
			}
			waitDecided(t, doneAgain)
		}

		// A whole input is not flagged, and its JSON leaves the field out.
		whole, done := startAsk(t, hub, shellIn("Edit", `{"file_path":"c.go"}`, "Edit c.go?"), 5*time.Second)
		if whole.ToolInputTruncated || whole.RunScope() != RunScopeTool {
			t.Fatalf("whole input = %+v (scope %q)", whole, whole.RunScope())
		}
		if raw, _ := json.Marshal(whole); strings.Contains(string(raw), "toolInputTruncated") {
			t.Fatalf("whole input JSON %s", raw)
		}
		if _, err := hub.Answer(ctx, "alpha", whole.ID, "deny", "", alice); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)
	})
}

// Only permission prompts carry a tool input; the flag means nothing on
// other kinds and is not kept.
func TestCutFlagIgnoredOnOtherKinds(t *testing.T) {
	eachHub(t, func(t *testing.T, hub *Hub) {
		ctx := context.Background()
		in := AskInput{Project: "alpha", SessionID: "s1", SessionName: "s1", ActorID: "alice", Question: "Deploy?", ToolInputTruncated: true}
		ask, done := startAsk(t, hub, in, 5*time.Second)
		if ask.ToolInputTruncated {
			t.Fatalf("approval flagged as cut: %+v", ask)
		}
		if _, err := hub.Answer(ctx, "alpha", ask.ID, "deny", "", Viewer{ID: "alice"}); err != nil {
			t.Fatal(err)
		}
		waitDecided(t, done)
	})
}
