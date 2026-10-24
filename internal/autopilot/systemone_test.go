package autopilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// fakeDecider is a DecideRelay: it records the request and answers.
type fakeDecider struct {
	account string
	req     runnerwire.DecisionRequest
	resp    runnerwire.DecisionResponse
	err     error
}

func (f *fakeDecider) Decide(_ context.Context, accountID string, req runnerwire.DecisionRequest) (runnerwire.DecisionResponse, error) {
	f.account, f.req = accountID, req
	return f.resp, f.err
}

func implementerAnswer(choice string, probabilities map[string]float64) runnerwire.DecisionResponse {
	return runnerwire.DecisionResponse{Model: "jev-1.13.0", Answers: map[string]runnerwire.DecisionAnswer{
		"implementer": {Type: "choice", Choice: choice, Confidence: 0.6, Probabilities: probabilities},
		"complexity": {Type: "score", Score: json.RawMessage(`1`), Confidence: 0.71,
			Probabilities: map[string]float64{"trivial": 0.2, "small": 0.6, "medium": 0.2}},
	}}
}

// The question offers the installed candidates as options o1…, describes
// them, and asks the complexity too; the state is the task.
func TestSystemOneQuestions(t *testing.T) {
	relay := &fakeDecider{resp: implementerAnswer("o3", map[string]float64{"o3": 0.9, "o1": 0.1})}
	if _, err := NewSystemOneAdvisor(relay).Choose(context.Background(), chooseInput()); err != nil {
		t.Fatal(err)
	}
	if relay.account != "u-ann" || relay.req.Model != "" {
		t.Fatalf("asked %q with model %q", relay.account, relay.req.Model)
	}
	var state string
	if err := json.Unmarshal(relay.req.State, &state); err != nil || !strings.Contains(state, "Rename the config flag") ||
		!strings.Contains(state, "Rename --foo to --bar") {
		t.Fatalf("state = %s", relay.req.State)
	}
	var questions map[string]struct {
		Type         string          `json:"type"`
		Instructions string          `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria"`
	}
	if err := json.Unmarshal(relay.req.Questions, &questions); err != nil {
		t.Fatal(err)
	}
	var options map[string]string
	if err := json.Unmarshal(questions["implementer"].Criteria, &options); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"o1": "Claude Code with model default — the agent's own default model",
		"o2": "Claude Code with model Opus (opus) — strong for complex work, slower and more expensive",
		"o3": "Claude Code with model Haiku (haiku) — fast and cheap, for small well-defined edits",
		"o4": "Codex with model gpt-5.5",
	}
	if questions["implementer"].Type != "choice" || len(options) != len(want) {
		t.Fatalf("implementer = %+v %v", questions["implementer"], options)
	}
	for k, v := range want {
		if options[k] != v {
			t.Errorf("option %s = %q, want %q", k, options[k], v)
		}
	}
	if questions["complexity"].Type != "score" || !strings.Contains(string(questions["complexity"].Criteria), `"risky"`) {
		t.Fatalf("complexity = %+v", questions["complexity"])
	}
}

// The chain is the options by probability: at most three, fallbacks only
// above 5%; the reason names the model, the pick and the complexity.
func TestSystemOneChoice(t *testing.T) {
	relay := &fakeDecider{resp: implementerAnswer("o2",
		map[string]float64{"o1": 0.03, "o2": 0.55, "o3": 0.04, "o4": 0.38})}
	c, err := NewSystemOneAdvisor(relay).Choose(context.Background(), chooseInput())
	if err != nil {
		t.Fatal(err)
	}
	want := []runnerwire.AgentModel{{Agent: "claude", Model: "opus"}, {Agent: "codex", Model: "gpt-5.5"}}
	if len(c.Implement) != 2 || c.Implement[0] != want[0] || c.Implement[1] != want[1] {
		t.Fatalf("chain = %+v", c.Implement)
	}
	if c.Reason != "System One (jev-1.13.0): claude/opus p=0.55, complexity small (conf 0.71)" {
		t.Fatalf("reason = %q", c.Reason)
	}

	// Without probabilities, the choice alone.
	relay.resp = runnerwire.DecisionResponse{Answers: map[string]runnerwire.DecisionAnswer{
		"implementer": {Type: "choice", Choice: "o4"}}}
	c, err = NewSystemOneAdvisor(relay).Choose(context.Background(), chooseInput())
	if err != nil || len(c.Implement) != 1 || c.Implement[0].Agent != "codex" ||
		c.Reason != "System One (System One): codex/gpt-5.5 p=1.00" {
		t.Fatalf("choice only: %+v %v", c, err)
	}
}

// Anything but a choice among the options is an error, so the orchestrator
// falls back to its rules.
func TestSystemOneFailures(t *testing.T) {
	cases := map[string]*fakeDecider{
		"relay error":    {err: errors.New("no online runner of yours has a System One advisor")},
		"no implementer": {resp: runnerwire.DecisionResponse{Answers: map[string]runnerwire.DecisionAnswer{"x": {}}}},
		"unknown option": {resp: runnerwire.DecisionResponse{Answers: map[string]runnerwire.DecisionAnswer{
			"implementer": {Type: "choice", Choice: "o99", Probabilities: map[string]float64{"o99": 1}}}}},
	}
	for name, relay := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSystemOneAdvisor(relay).Choose(context.Background(), chooseInput()); err == nil {
				t.Fatal("no error")
			}
		})
	}
	in := chooseInput()
	in.Agents = nil
	if _, err := NewSystemOneAdvisor(&fakeDecider{}).Choose(context.Background(), in); !errors.Is(err, errNoAgent) {
		t.Fatalf("no candidates: %v", err)
	}
}

// A policy with advisor "systemone" asks the System One advisor, and the
// rules take over when it fails; the LLM advisor is not asked.
func TestSystemOneAdvisorInTheOrchestrator(t *testing.T) {
	h := newHarness(t, newBoard([]string{"goal", "a", "b"}, []string{"a>goal", "b>goal"}))
	systemOneCalls, llmCalls := 0, 0
	RegisterSystemOneAdvisor(advisorFunc(func(ctx context.Context, in ChooseInput) (Choice, error) {
		systemOneCalls++
		if in.NodeID == "a" {
			return Choice{Implement: []runnerwire.AgentModel{{Agent: "codex"}}, Reason: "system one"}, nil
		}
		return Choice{}, errors.New("laya is not running")
	}))
	RegisterLLMAdvisor(advisorFunc(func(context.Context, ChooseInput) (Choice, error) {
		llmCalls++
		return Choice{}, errors.New("not asked")
	}))
	t.Cleanup(func() { RegisterSystemOneAdvisor(nil); RegisterLLMAdvisor(nil) })
	h.create(runnerwire.AutopilotPolicy{MaxParallel: 2, Advisor: runnerwire.AdvisorSystemOne})
	if systemOneCalls != 2 || llmCalls != 0 ||
		h.runs.lastStart("a").Body.Agent != "codex" || h.runs.lastStart("b").Body.Agent != "claude" {
		t.Fatalf("systemone=%d llm=%d a=%s b=%s", systemOneCalls, llmCalls,
			h.runs.lastStart("a").Body.Agent, h.runs.lastStart("b").Body.Agent)
	}
}

func TestSystemOneComplexity(t *testing.T) {
	cases := []struct {
		answer runnerwire.DecisionAnswer
		want   string
	}{
		{runnerwire.DecisionAnswer{Score: json.RawMessage(`"large"`)}, "large"},
		{runnerwire.DecisionAnswer{Score: json.RawMessage(`2.4`)}, "medium"},
		{runnerwire.DecisionAnswer{Score: json.RawMessage(`3`), Legend: json.RawMessage(`{"3":"big"}`)}, "big"},
		{runnerwire.DecisionAnswer{Score: json.RawMessage(`0`), Legend: json.RawMessage(`["tiny","x"]`)}, "tiny"},
		{runnerwire.DecisionAnswer{Probabilities: map[string]float64{"small": 0.3, "risky": 0.7}}, "risky"},
	}
	for _, tc := range cases {
		if got, _, ok := complexityOf(tc.answer); !ok || got != tc.want {
			t.Errorf("complexityOf(%+v) = %q, want %q", tc.answer, got, tc.want)
		}
	}
	if _, _, ok := complexityOf(runnerwire.DecisionAnswer{}); ok {
		t.Error("an empty answer has a complexity")
	}
}
