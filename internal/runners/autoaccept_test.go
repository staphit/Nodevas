package runners

import (
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestValidateInfoBoundsTheAutoAcceptPolicy(t *testing.T) {
	policy := func(edit func(*runnerwire.AutoAcceptPolicy)) runnerwire.Info {
		p := &runnerwire.AutoAcceptPolicy{
			Enabled: true, RequireStepUp: true,
			Repos: []string{"r1"}, Agents: []string{"claude"}, Modes: []string{"", runnerwire.ModeInteractive},
		}
		edit(p)
		return runnerwire.Info{Version: "1", AutoAccept: p}
	}
	if err := ValidateInfo(policy(func(*runnerwire.AutoAcceptPolicy) {})); err != nil {
		t.Fatalf("a valid policy: %v", err)
	}
	many := func(n int, value string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = value
		}
		return out
	}
	for name, info := range map[string]runnerwire.Info{
		"repos":  policy(func(p *runnerwire.AutoAcceptPolicy) { p.Repos = many(runnerwire.MaxRepos+1, "r") }),
		"agents": policy(func(p *runnerwire.AutoAcceptPolicy) { p.Agents = many(MaxAutoAcceptAgents+1, "claude") }),
		"modes":  policy(func(p *runnerwire.AutoAcceptPolicy) { p.Modes = many(MaxAutoAcceptModes+1, "") }),
		"long":   policy(func(p *runnerwire.AutoAcceptPolicy) { p.Repos = []string{strings.Repeat("r", 257)} }),
		"text":   policy(func(p *runnerwire.AutoAcceptPolicy) { p.Agents = []string{"\xff"} }),
	} {
		if err := ValidateInfo(info); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}
