package runners

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func systemOneInfo() runnerwire.Info {
	info := llmInfo()
	info.Capabilities = []string{runnerwire.CapHeadless, runnerwire.CapDecide}
	info.LLM = nil
	info.Advisor = &runnerwire.LLMInfo{Provider: runnerwire.ProviderSystemOne, Model: "jev-latest",
		BaseURL: "https://api.typesafe.ai", Purpose: runnerwire.LLMPurposeAdvisor}
	return info
}

func decision() runnerwire.DecisionRequest {
	return runnerwire.DecisionRequest{
		State:     json.RawMessage(`"task"`),
		Questions: json.RawMessage(`{"implementer":{"type":"choice","instructions":"who?","criteria":{"o1":"a"}}}`),
	}
}

// A decision goes to the owner's runner with a System One advisor, names its
// hello's model and endpoint, and only that runner's answer counts.
func TestDecideRelaysToTheOwnersSystemOneRunner(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewLLMRelay(reg)
	ctx := context.Background()
	online(t, reg, "ann", advisorInfo(), newLLMConn()) // a model advisor: not a System One one
	online(t, reg, "bob", systemOneInfo(), newLLMConn())
	if _, err := relay.Decide(ctx, ann.ID, decision()); !errors.Is(err, ErrNoDecideRunner) {
		t.Fatalf("ann has no System One runner, bob's must not count: %v", err)
	}
	conn := newLLMConn()
	runner := online(t, reg, "ann", systemOneInfo(), conn)
	other := online(t, reg, "ann", llmInfo(), newLLMConn())

	type result struct {
		resp runnerwire.DecisionResponse
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := relay.Decide(ctx, ann.ID, decision())
		got <- result{resp, err}
	}()
	env := conn.next(t)
	req := env.Decide
	if env.Type != runnerwire.TypeDecideRequest || req == nil || req.AccountID != ann.ID ||
		req.Purpose != runnerwire.LLMPurposeAdvisor || req.Model != "jev-latest" || req.Request.Model != "jev-latest" ||
		req.BaseURL != "https://api.typesafe.ai" || string(req.Request.State) != `"task"` {
		t.Fatalf("sent %+v", env)
	}
	answer := &runnerwire.DecisionResponse{Model: "jev-1.13.0", Answers: map[string]runnerwire.DecisionAnswer{
		"implementer": {Type: "choice", Choice: "o1", Probabilities: map[string]float64{"o1": 1}}}}
	if err := relay.Deliver(other.ID, runnerwire.Envelope{Type: runnerwire.TypeDecideDone,
		DecideDone: &runnerwire.DecideDone{ID: req.ID, Response: answer}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another runner answered: %v", err)
	}
	if err := relay.Deliver(runner.ID, runnerwire.Envelope{Type: runnerwire.TypeDecideDone,
		DecideDone: &runnerwire.DecideDone{ID: req.ID, Response: answer}}); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.err != nil || r.resp.Model != "jev-1.13.0" || r.resp.Answers["implementer"].Choice != "o1" {
		t.Fatalf("decision = %+v, %v", r.resp, r.err)
	}

	// The runner's reason comes back as the error.
	go func() {
		resp, err := relay.Decide(ctx, ann.ID, decision())
		got <- result{resp, err}
	}()
	env = conn.next(t)
	_ = relay.Deliver(runner.ID, runnerwire.Envelope{Type: runnerwire.TypeDecideDone,
		DecideDone: &runnerwire.DecideDone{ID: env.Decide.ID, Error: "System One 401: invalid key"}})
	if r := <-got; r.err == nil || !strings.Contains(r.err.Error(), "invalid key") {
		t.Fatalf("refused decision = %v", r.err)
	}
}

func TestDecideRefusesOversizedRequests(t *testing.T) {
	reg, _ := newRegistry(t)
	relay := NewLLMRelay(reg)
	req := decision()
	req.State = json.RawMessage(`"` + strings.Repeat("x", runnerwire.MaxDecideStateBytes) + `"`)
	if _, err := relay.Decide(context.Background(), ann.ID, req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized decision = %v", err)
	}
}
