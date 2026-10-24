package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// fakeRemote is a runner relay with one runner, or none.
type fakeRemote struct {
	runner  *runnerwire.Runner
	calls   []runnerwire.LLMRequest
	answer  runnerwire.LLMDone
	deltas  []string
	failure error
}

func (f *fakeRemote) Target(_ context.Context, accountID string) (runnerwire.Runner, error) {
	if f.runner == nil || f.runner.OwnerID != accountID {
		return runnerwire.Runner{}, errors.New("your runner is offline — start `nodevas runner` on your PC")
	}
	return *f.runner, nil
}

func (f *fakeRemote) Call(_ context.Context, runnerID string, req runnerwire.LLMRequest, onDelta func(string)) (runnerwire.LLMDone, error) {
	f.calls = append(f.calls, req)
	for _, text := range f.deltas {
		onDelta(text)
	}
	return f.answer, f.failure
}

func runnerFor(owner string) *runnerwire.Runner {
	return &runnerwire.Runner{ID: "r1", Name: "desk", OwnerID: owner, Online: true,
		Info: &runnerwire.Info{Capabilities: []string{runnerwire.CapLLM},
			LLM: &runnerwire.LLMInfo{Provider: "anthropic", Model: "claude-runner"}}}
}

func TestEffectiveKeySource(t *testing.T) {
	cases := []struct {
		settings Settings
		key      string
		want     string
	}{
		{Settings{KeySource: KeySourceAuto, Provider: "anthropic"}, "sk", KeySourceServer},
		{Settings{KeySource: KeySourceAuto, Provider: "anthropic"}, "", KeySourceRunner},
		{Settings{KeySource: KeySourceAuto, Provider: "openai", BaseURL: "http://localhost:11434/v1"}, "", KeySourceServer},
		{Settings{KeySource: KeySourceRunner, Provider: "anthropic"}, "sk", KeySourceRunner},
		{Settings{KeySource: KeySourceServer, Provider: "anthropic"}, "", KeySourceServer},
	}
	for _, tc := range cases {
		if got := effectiveKeySource(tc.settings, tc.key); got != tc.want {
			t.Errorf("effectiveKeySource(%+v, %q) = %s, want %s", tc.settings, tc.key, got, tc.want)
		}
	}
}

func TestKeySourceSettings(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	view, err := h.service.Settings(ctx)
	if err != nil || view.KeySource != KeySourceAuto || view.EffectiveKeySource != KeySourceServer {
		t.Fatalf("default view = %+v, %v", view, err)
	}
	runnerSource := KeySourceRunner
	if view, err = h.service.UpdateSettings(ctx, SettingsUpdate{KeySource: &runnerSource}); err != nil ||
		view.KeySource != KeySourceRunner || view.EffectiveKeySource != KeySourceRunner {
		t.Fatalf("runner view = %+v, %v", view, err)
	}
	bad := "cloud"
	if _, err := h.service.UpdateSettings(ctx, SettingsUpdate{KeySource: &bad}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("bad key source = %v", err)
	}
	auto := ""
	if view, err = h.service.UpdateSettings(ctx, SettingsUpdate{KeySource: &auto}); err != nil || view.KeySource != KeySourceAuto {
		t.Fatalf("back to auto = %+v, %v", view, err)
	}
}

// With the key on the runner the server key is never used: the turn's model
// call goes to the speaker's runner and the provider and model are its own.
func TestTurnOnTheRunner(t *testing.T) {
	remote := &fakeRemote{runner: runnerFor(alice.ID), deltas: []string{"Hi", " there"}}
	message, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant,
		Blocks: []provider.Block{{Type: provider.BlockText, Text: "Hi there"}},
		Raw:    json.RawMessage(`{"x":1}`), RawProvider: "anthropic", RawModel: "claude-runner"})
	remote.answer = runnerwire.LLMDone{Message: message, Stop: "end_turn", InputTokens: 3, OutputTokens: 2}
	h := newHarness(t, func(opts *Options) {
		opts.Remote = remote
		opts.NewProvider = func(provider.Config) (provider.Provider, error) {
			t.Error("the server built a provider with its own key")
			return nil, errors.New("no")
		}
	})
	ctx := context.Background()
	runnerSource := KeySourceRunner
	if _, err := h.service.UpdateSettings(ctx, SettingsUpdate{KeySource: &runnerSource}); err != nil {
		t.Fatal(err)
	}
	status, err := h.service.Status(ctx, alice)
	if err != nil || !status.Enabled || status.Runner != "desk" || status.Model != "claude-runner" {
		t.Fatalf("status = %+v, %v", status, err)
	}

	conversation := h.conversation(alice, ModeDefault)
	var texts []string
	var finished string
	err = h.service.Turn(ctx, TurnRequest{Actor: alice, ConversationID: conversation.ID, Text: "hello"}, func(e Event) {
		switch e.Type {
		case EventTextDelta:
			texts = append(texts, e.Text)
		case EventTurnFinished:
			finished = e.Stop
		}
	})
	if err != nil || finished != "end_turn" || strings.Join(texts, "") != "Hi there" {
		t.Fatalf("turn: %v, stop %q, text %q", err, finished, texts)
	}
	if len(remote.calls) != 1 {
		t.Fatalf("calls = %d", len(remote.calls))
	}
	call := remote.calls[0]
	if call.AccountID != alice.ID || call.Provider != "anthropic" || call.Model != "claude-runner" ||
		!strings.Contains(string(call.Messages), "hello") {
		t.Fatalf("call = %+v", call)
	}

	// Someone without a runner gets the reason, not a turn.
	other := h.conversation(bob, ModeDefault)
	if err := h.service.Turn(ctx, TurnRequest{Actor: bob, ConversationID: other.ID, Text: "hi"},
		func(Event) {}); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "nodevas runner") {
		t.Fatalf("turn without a runner = %v", err)
	}
}

func TestRunnerProviderDistrustsMislabelledRaw(t *testing.T) {
	message, _ := json.Marshal(provider.Message{Role: provider.RoleAssistant,
		Raw: json.RawMessage(`{"x":1}`), RawProvider: "openai", RawModel: "gpt"})
	remote := &fakeRemote{answer: runnerwire.LLMDone{Message: message, Stop: "weird"}}
	p := &runnerProvider{remote: remote, runnerID: "r1",
		info: runnerwire.LLMInfo{Provider: "anthropic", Model: "claude-runner"}}
	resp, err := p.Stream(context.Background(), provider.Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Raw != nil || resp.Message.RawProvider != "" || resp.Stop != provider.StopOther {
		t.Fatalf("response = %+v", resp)
	}
	user, _ := json.Marshal(provider.Message{Role: provider.RoleUser})
	remote.answer = runnerwire.LLMDone{Message: user}
	if _, err := p.Stream(context.Background(), provider.Request{}, nil); err == nil {
		t.Fatal("a user turn from the runner was accepted")
	}
}
