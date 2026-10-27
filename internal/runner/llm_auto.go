package runner

import (
	"context"
	"strings"
	"time"

	"nodevas/internal/copilot/localllm"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// A runner whose owner set no copilotProvider still answers the copilot when
// a local OpenAI-compatible server runs on this machine: Ollama or LM Studio
// on its default loopback port. That model is free and keyless, and the
// hub ranks it below anything configured and below the browser's own model
// (internal/copilot/choose.go). `nodevas runner set copilotProvider off`
// turns this off.

// CopilotProviderOff is the copilotProvider value that turns copilot model
// calls off here, the automatic local model included.
const CopilotProviderOff = "off"

// detectLocalLLM finds the local server; tests replace it.
var detectLocalLLM = localllm.New().Detect

// localDetectWait bounds a detection made while answering a model call;
// the hello's periodic re-detection keeps the cache warm.
const localDetectWait = 2 * time.Second

// autoCopilotLLM is the local model this runner offers when its owner set
// no copilotProvider. Keyless: a key configured here is never sent to it.
func autoCopilotLLM(ctx context.Context, cfg *Config) (runnerwire.LLMInfo, bool) {
	if cfg == nil || cfg.CopilotProvider != "" || detectLocalLLM == nil {
		return runnerwire.LLMInfo{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, localDetectWait)
	defer cancel()
	found, ok := detectLocalLLM(ctx)
	if !ok || !localllm.Loopback(found.BaseURL) || found.Model == "" {
		return runnerwire.LLMInfo{}, false
	}
	return runnerwire.LLMInfo{
		Provider: provider.KindOpenAI,
		Model:    found.Model,
		BaseURL:  strings.TrimRight(found.BaseURL, "/"),
		Auto:     true,
		Local:    found.Server,
	}, true
}

// modelCallSettings is Config.modelCallSettings plus the automatic local
// model for copilot calls.
func (c *Client) modelCallSettings(ctx context.Context, purpose string) (runnerwire.LLMInfo, string, string) {
	info, key, reason := c.Config.modelCallSettings(purpose)
	if reason != "" && purpose == runnerwire.LLMPurposeCopilot {
		if auto, ok := autoCopilotLLM(ctx, c.Config); ok {
			return auto, "", ""
		}
	}
	return info, key, reason
}
