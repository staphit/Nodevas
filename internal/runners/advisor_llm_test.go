package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func advisorInfo() runnerwire.Info {
	info := llmInfo()
	info.Capabilities = []string{runnerwire.CapHeadless, runnerwire.CapLLMAdvisor}
	info.LLM = nil
	info.Advisor = &runnerwire.LLMInfo{Provider: "anthropic", Model: "claude-haiku-5-5", Purpose: runnerwire.LLMPurposeAdvisor}
	return info
}

// An advisor call goes to the owner's runner that has an advisor model, a
// copilot call to the one with a copilot key; another account's advisor never
// counts, and an unknown purpose is refused.
func TestLLMTargetForPurpose(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewLLMRelay(reg)
	ctx := context.Background()
	copilotRunner := online(t, reg, "ann", llmInfo(), newLLMConn())
	online(t, reg, "bob", advisorInfo(), newLLMConn())
	if _, err := relay.TargetFor(ctx, ann.ID, runnerwire.LLMPurposeAdvisor); !errors.Is(err, ErrNoAdvisorRunner) {
		t.Fatalf("ann has no advisor runner, bob's must not count: %v", err)
	}
	advisorRunner := online(t, reg, "ann", advisorInfo(), newLLMConn())
	if got, err := relay.TargetFor(ctx, ann.ID, runnerwire.LLMPurposeAdvisor); err != nil || got.ID != advisorRunner.ID {
		t.Fatalf("advisor target = %v, %v; want %s", got.ID, err, advisorRunner.ID)
	}
	if got, err := relay.Target(ctx, ann.ID); err != nil || got.ID != copilotRunner.ID {
		t.Fatalf("copilot target = %v, %v; want %s", got.ID, err, copilotRunner.ID)
	}
	if _, err := relay.TargetFor(ctx, ann.ID, "other"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown purpose: %v", err)
	}
}

func TestHelloAdvisorFieldsAreBounded(t *testing.T) {
	info := advisorInfo()
	info.Advisor.Model = strings.Repeat("m", 10000)
	if err := ValidateInfo(info); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge advisor model name = %v", err)
	}
	info = advisorInfo()
	info.Advisor.BaseURL = "https://" + strings.Repeat("h", 10000)
	if err := ValidateInfo(info); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge advisor base URL = %v", err)
	}
}
