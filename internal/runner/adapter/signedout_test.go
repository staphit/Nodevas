package adapter

import (
	"testing"

	"nodevas/internal/runnerwire"
)

// A CLI that is there but signed out: the run fails as agent_unavailable
// (another agent may work), and SignedOut tells it from a CLI that is gone,
// so the app can say how to sign in instead of the CLI's own English.
func TestSignedOut(t *testing.T) {
	// Recorded from Claude Code 2.1.327 with no login (an empty
	// CLAUDE_CONFIG_DIR and no API key): exit 1, nothing spent.
	_, outcome := parseWith(t, Claude{}.NewParser(), "claude_signed_out.jsonl")
	if !outcome.IsError || outcome.Error == "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := (Claude{}).Classify(outcome.Error); got != runnerwire.FailureAgentAbsent {
		t.Fatalf("classified %q", got)
	}
	if !SignedOut("claude", outcome.Error) {
		t.Fatalf("%q is not read as signed out", outcome.Error)
	}

	rows := []struct {
		agent, text string
		want        bool
	}{
		{"claude", "Not logged in · Please run /login", true},
		{"claude", "Invalid API key · Please run /login", true},
		{"claude", "OAuth token has expired. Please obtain a new token or refresh your existing token.", true},
		{"claude", "\n/bin/sh: 1: claude: command not found", false},
		{"claude", "Credit balance is too low", false},
		{"claude", "", false},
		{"codex", "\nError: Not logged in. Run `codex login` first.", true},
		{"codex", "Your access token could not be refreshed because your refresh token has expired. Please log out and sign in again.", true},
		{"codex", "exceeded retry limit, last status: 429 Too Many Requests", false},
		{"gemini", "Please set an Auth method in your settings.json or specify GEMINI_API_KEY", true},
		{"antigravity", "Print mode: not authenticated, trying silent auth", true},
		{"antigravity", "'node' is not recognized as an internal or external command,", false},
		// An OpenCode provider's key is the provider's, not a sign-in to
		// OpenCode itself: no sign-in sentence fits it.
		{"opencode", "ProviderAuthError: Anthropic API key is missing", false},
		{"nope", "Not logged in", false},
	}
	for _, r := range rows {
		if got := SignedOut(r.agent, r.text); got != r.want {
			t.Errorf("SignedOut(%s, %q) = %v, want %v", r.agent, r.text, got, r.want)
		}
	}
}
