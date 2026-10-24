package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// Key sources: where the API key for the copilot's model calls lives.
const (
	// KeySourceServer: the server holds the key (stored or from its
	// environment) and calls the provider itself.
	KeySourceServer = "server"
	// KeySourceRunner: the key lives on the person's own runner, which makes
	// the call (runnerwire/llm.go); the server holds no model credential.
	KeySourceRunner = "runner"
	// KeySourceAuto: server when it has a key, runner otherwise.
	KeySourceAuto = "auto"
)

// RunnerLLM is how the copilot reaches model calls made on runners.
// *runners.LLMRelay satisfies it.
type RunnerLLM interface {
	// Target is the runner that answers accountID's copilot.
	Target(ctx context.Context, accountID string) (runnerwire.Runner, error)
	// Call runs one model call there, streaming text to onDelta in order on
	// the calling goroutine.
	Call(ctx context.Context, runnerID string, req runnerwire.LLMRequest, onDelta func(string)) (runnerwire.LLMDone, error)
}

// errNoRemote: no server key, and the server was built without the runner
// relay that would reach one on a runner.
var errNoRemote = errors.New("no API key is configured and this server cannot reach runners: " +
	"set NODEVAS_COPILOT_API_KEY or add one in the copilot settings")

// effectiveKeySource resolves "auto": a server with a key (or with a keyless
// OpenAI-compatible endpoint configured, such as Ollama) keeps calling the
// provider itself, as before key sources existed; otherwise the runner.
func effectiveKeySource(settings Settings, key string) string {
	switch settings.KeySource {
	case KeySourceServer, KeySourceRunner:
		return settings.KeySource
	}
	if key != "" || (settings.Provider == provider.KindOpenAI && settings.BaseURL != "") {
		return KeySourceServer
	}
	return KeySourceRunner
}

// runnerTarget finds the account's runner and what it calls.
func (s *Service) runnerTarget(ctx context.Context, accountID string) (runnerwire.Runner, runnerwire.LLMInfo, error) {
	if s.opts.Remote == nil {
		return runnerwire.Runner{}, runnerwire.LLMInfo{}, errNoRemote
	}
	runner, err := s.opts.Remote.Target(ctx, accountID)
	if err != nil {
		return runnerwire.Runner{}, runnerwire.LLMInfo{}, err
	}
	if runner.Info == nil || runner.Info.LLM == nil {
		return runnerwire.Runner{}, runnerwire.LLMInfo{}, errors.New("your runner has no copilot key")
	}
	return runner, *runner.Info.LLM, nil
}

// runnerProvider is a provider.Provider whose calls run on a runner.
type runnerProvider struct {
	remote    RunnerLLM
	runnerID  string
	info      runnerwire.LLMInfo
	accountID string
	effort    string
}

func (p *runnerProvider) Name() string  { return p.info.Provider }
func (p *runnerProvider) Model() string { return p.info.Model }

func (p *runnerProvider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	messages, err := json.Marshal(req.Messages)
	if err != nil {
		return provider.Response{}, err
	}
	tools, err := json.Marshal(req.Tools)
	if err != nil {
		return provider.Response{}, err
	}
	call := runnerwire.LLMRequest{
		AccountID: p.accountID,
		Provider:  p.info.Provider, Model: p.info.Model, BaseURL: p.info.BaseURL,
		Effort: p.effort, System: req.System, Messages: messages, Tools: tools,
		MaxTokens: req.MaxTokens,
	}
	if len(messages)+len(tools)+len(req.System) > runnerwire.MaxLLMRequestBytes-64<<10 {
		return provider.Response{}, errors.New("this conversation is too long to send to your runner; start a new one")
	}
	done, err := p.remote.Call(ctx, p.runnerID, call, func(text string) {
		if onDelta != nil {
			onDelta(provider.Delta{Text: text})
		}
	})
	if err != nil {
		return provider.Response{}, err
	}
	var message provider.Message
	if err := json.Unmarshal(done.Message, &message); err != nil {
		return provider.Response{}, fmt.Errorf("your runner sent an answer the copilot cannot read: %w", err)
	}
	if message.Role != provider.RoleAssistant {
		return provider.Response{}, errors.New("your runner's answer is not an assistant turn")
	}
	// The vendor serialization is replayed verbatim only to the same
	// provider and model; one labelled otherwise is not trusted for that.
	if message.RawProvider != p.info.Provider || message.RawModel != p.info.Model {
		message.Raw, message.RawProvider, message.RawModel = nil, "", ""
	}
	stop := provider.StopReason(done.Stop)
	switch stop {
	case provider.StopEndTurn, provider.StopToolUse, provider.StopMaxTokens, provider.StopRefusal, provider.StopOther:
	default:
		stop = provider.StopOther
	}
	return provider.Response{
		Message: message, Stop: stop,
		InputTokens: max(done.InputTokens, 0), OutputTokens: max(done.OutputTokens, 0),
	}, nil
}
