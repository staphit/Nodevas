package runs

import (
	"context"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A run whose agent turned out signed out keeps the runner's code beside
// agent_unavailable, so the app can say how to sign in. The code means
// nothing on any other kind, and a code this hub does not know is dropped.
func TestFinishKeepsSignedOutCode(t *testing.T) {
	f := newHandoffFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		kind, code, want string
	}{
		{runnerwire.FailureAgentAbsent, runnerwire.FailureSignedOut, runnerwire.FailureSignedOut},
		{runnerwire.FailureAgentAbsent, "", ""},
		{runnerwire.FailureAgentAbsent, "made_up", ""},
		{runnerwire.FailureQuota, runnerwire.FailureSignedOut, ""},
	} {
		r := f.start(t, nil)
		f.fail(t, r.ID, runnerwire.RunResult{FailureKind: c.kind, FailureCode: c.code, Error: "Not logged in · Please run /login"})
		got, err := f.s.Get(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.FailureKind != c.kind || got.FailureCode != c.want {
			t.Errorf("%s/%s: kept %q/%q, want code %q", c.kind, c.code, got.FailureKind, got.FailureCode, c.want)
		}
	}
}

// An agent its runner says is signed out cannot be picked, and the reason
// says so rather than "not installed".
func TestSignedOutAgentIsNotOffered(t *testing.T) {
	info := &runnerwire.Info{Agents: []runnerwire.Agent{{Kind: "claude", SignedOut: true, Error: "claude: not signed in",
		Models: []runnerwire.Model{{ID: "default", Default: true}}}}}
	_, err := ResolveAgentModel(info, runnerwire.AgentModel{Agent: "claude"})
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("err = %v", err)
	}
	if got := CleanDeclineCode(runnerwire.DeclineAgentSignedOut); got != runnerwire.DeclineAgentSignedOut {
		t.Fatalf("decline code kept as %q", got)
	}
}
