package copilot

import (
	"strings"
	"testing"
)

// A typed request was answered in the style written for speech; source now
// decides it.
func TestSystemPromptStyleFollowsSource(t *testing.T) {
	typed := systemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "text")
	spoken := systemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "voice")
	if strings.Contains(typed, "speakable") || !strings.Contains(typed, "lists are fine") {
		t.Errorf("typed prompt style:\n%s", typed)
	}
	if !strings.Contains(spoken, "speakable") || strings.Contains(spoken, "lists are fine") {
		t.Errorf("spoken prompt style:\n%s", spoken)
	}
	if !strings.Contains(spoken, "Speech recognition makes mistakes") || strings.Contains(typed, "Speech recognition") {
		t.Error("only a spoken request is warned about recognition mistakes")
	}
}

// P6: the coding agent's plan comes back as a draft, and the autopilot
// waits for the person to create it.
func TestSystemPromptSaysCreateBeforeAutopilot(t *testing.T) {
	prompt := systemPrompt("Bea", "main", ModeDefault, TurnContext{}, []string{"start_breakdown", "start_autopilot"}, "text")
	for _, want := range []string{"draft card", "presses Create", "never call it while the plan is still a draft"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("dispatch guidance lacks %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptGuidesAuthoring(t *testing.T) {
	prompt := systemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "text")
	for _, want := range []string{"get_graph_outline", "one create_subgraph", "dependsOn", "duplicate"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("authoring guidance lacks %q:\n%s", want, prompt)
		}
	}
}

func TestBrowserSystemPromptStyleAndKinds(t *testing.T) {
	typed := browserSystemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "text")
	spoken := browserSystemPrompt("Bea", "main", ModeDefault, TurnContext{}, nil, "voice")
	if strings.Contains(typed, "read aloud") || !strings.Contains(spoken, "read aloud") {
		t.Errorf("browser prompt style: typed %q, spoken %q", typed, spoken)
	}
	for _, want := range []string{"task", "goal", "scene", "note", "dependsOn"} {
		if !strings.Contains(typed, want) {
			t.Errorf("browser prompt lacks %q:\n%s", want, typed)
		}
	}
	if strings.Contains(typed, "start_breakdown") {
		t.Error("start_breakdown described though not offered")
	}
	offered := browserSystemPrompt("Bea", "main", ModeDefault, TurnContext{}, []string{"start_breakdown"}, "text")
	if !strings.Contains(offered, "start_breakdown") || !strings.Contains(offered, "confirm") {
		t.Errorf("browser prompt with start_breakdown offered:\n%s", offered)
	}
}
