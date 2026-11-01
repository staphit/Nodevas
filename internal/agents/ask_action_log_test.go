package agents

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A permission prompt's question quotes the tool call's input (the runner's
// relay writes "<agent> wants to run <tool>: <input>"). The session action
// log is served to everyone who can read the project, so it keeps only what
// every reader may see of the prompt: its tool (Viewer.Shown).
func TestPermissionPromptInputStaysOutOfTheActionLog(t *testing.T) {
	hub := openTestHub(t)
	ctx := context.Background()
	alice := Viewer{ID: "alice"}
	registerRun(t, hub, "r1", "app-claude-r1", "run-1")

	first := permissionIn("Read", `claude wants to run Read: {"file_path":"/home/alice/.ssh/SECRET-FIRST"}`)
	first.ToolInput = `{"file_path":"/home/alice/.ssh/SECRET-FIRST"}`
	ask, done := startAsk(t, hub, first, 5*time.Second)
	if _, err := hub.AnswerWith(ctx, "alpha", ask.ID, AnswerInput{Decision: DecisionApproveRun}, alice); err != nil {
		t.Fatal(err)
	}
	waitDecided(t, done)

	// Allowed for the run: logged without asking anyone.
	second := permissionIn("Read", `claude wants to run Read: {"file_path":"/home/alice/.aws/SECRET-SECOND"}`)
	second.ToolInput = `{"file_path":"/home/alice/.aws/SECRET-SECOND"}`
	if got, err := hub.CreateAndWait(ctx, second, 5*time.Second); err != nil || got.Note != AllowedForRunNote {
		t.Fatalf("second prompt = %+v, %v; want allowed for the run", got, err)
	}

	actions, err := hub.Actions(ctx, "alpha", "r1", 200)
	if err != nil {
		t.Fatal(err)
	}
	asks := 0
	for _, a := range actions {
		if a.Action != ActionAsk {
			continue
		}
		asks++
		if strings.Contains(a.Detail, "SECRET") {
			t.Errorf("ask action detail quotes the tool input: %q", a.Detail)
		}
		if a.Detail != "Allow Read?" {
			t.Errorf("ask action detail = %q, want %q", a.Detail, "Allow Read?")
		}
	}
	if asks != 2 {
		t.Fatalf("logged %d ask actions, want 2: %+v", asks, actions)
	}
}
