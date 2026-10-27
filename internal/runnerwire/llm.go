package runnerwire

import "encoding/json"

// Copilot model calls on the runner. The voice copilot's turn loop, its
// permission gate and its tools run on the hub, but the model call can run on
// the runner of the person talking, with an API key that exists only on that
// person's machine: the hub sends the request it built, the runner calls the
// provider it is configured for and streams the answer back. A hub that is
// broken into then holds no model credential; the worst it can do is spend
// that person's quota through their runner, within the runner's own limits.
//
// The runner, not the hub, decides the provider, its base URL and the model:
// they are its owner's settings next to the key (`nodevas runner set
// copilotProvider …`), reported in its hello. A request names them back only
// so a runner whose settings changed since its hello refuses it, rather than
// sending a conversation somewhere the hub did not expect.
const (
	// Hub → runner: run one model call (LLM).
	TypeLLMRequest = "llm_request"
	// Hub → runner: stop the call LLMID names.
	TypeLLMCancel = "llm_cancel"
	// Runner → hub: a fragment of the answer's text (LLMDelta), in order.
	TypeLLMDelta = "llm_delta"
	// Runner → hub: the call is over (LLMDone), with the whole answer or why
	// there is none. Nothing about the call follows it.
	TypeLLMDone = "llm_done"
)

// CapLLM: the runner answers copilot model calls (its hello has LLM set).
const CapLLM = "llm"

// CapLLMAdvisor: the runner answers advisor model calls (its hello has
// Advisor set).
const CapLLMAdvisor = "llm-advisor"

// What a model call is for (LLMRequest.Purpose, LLMInfo.Purpose). Each has
// its own provider, model and key on the runner; a request for one is never
// served with the other's settings.
const (
	// LLMPurposeCopilot: a voice copilot turn (the default, "").
	LLMPurposeCopilot = ""
	// LLMPurposeAdvisor: W7 autopilot asking a fast model which agent and
	// model should implement a task.
	LLMPurposeAdvisor = "advisor"
)

// Limits on model call messages.
const (
	// MaxLLMIDBytes bounds a call's id.
	MaxLLMIDBytes = 64
	// MaxLLMRequestBytes bounds one llm_request: a conversation's history,
	// tool results included, plus the tool list. The runner reads up to this.
	MaxLLMRequestBytes = 8 << 20
	// MaxLLMDeltaBytes bounds the text in one llm_delta.
	MaxLLMDeltaBytes = 16 << 10
	// MaxLLMDoneBytes bounds the answer in one llm_done.
	MaxLLMDoneBytes = 4 << 20
	// MaxLLMMaxTokens is the most output tokens a call may ask for; the
	// runner lowers a larger request to it.
	MaxLLMMaxTokens = 64000
	// MaxLLMFieldBytes bounds provider, model and base URL strings.
	MaxLLMFieldBytes = 2048
)

// LLMInfo is what a runner says about the model it calls for the copilot.
// Never the key: only that there is one, implicitly, by being reported.
type LLMInfo struct {
	// Provider is "anthropic" or "openai" (any OpenAI-compatible endpoint).
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// BaseURL is set for an OpenAI-compatible endpoint other than OpenAI's.
	BaseURL string `json:"baseUrl,omitempty"`
	// Purpose is LLMPurposeCopilot or LLMPurposeAdvisor.
	Purpose string `json:"purpose,omitempty"`
	// Auto: nobody set copilotProvider on the runner; it found a local
	// OpenAI-compatible server on its own loopback (Local names which:
	// "ollama", "lmstudio") and offers that, keyless. The hub ranks an
	// automatic model below the browser's own and below anything configured.
	Auto  bool   `json:"auto,omitempty"`
	Local string `json:"local,omitempty"`
}

// LLMRequest is one model call. Messages and Tools are the copilot's
// provider-neutral history and tool list (internal/copilot/provider's
// []Message and []Tool), carried as they are.
type LLMRequest struct {
	ID string `json:"id"`
	// AccountID is whose copilot (or autopilot) asked. A runner answers its
	// owner only.
	AccountID string `json:"accountId"`
	// Purpose picks which of the runner's model settings answers it:
	// LLMPurposeCopilot or LLMPurposeAdvisor.
	Purpose string `json:"purpose,omitempty"`
	// Provider, Model and BaseURL are what the hub expects the runner to
	// call, from its hello; the runner refuses a request that disagrees with
	// its settings.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl,omitempty"`
	// Effort is "low", "medium" or "high" (the hub's copilot setting).
	Effort    string          `json:"effort,omitempty"`
	System    string          `json:"system"`
	Messages  json.RawMessage `json:"messages"`
	Tools     json.RawMessage `json:"tools,omitempty"`
	MaxTokens int             `json:"maxTokens,omitempty"`
}

// LLMDelta is a fragment of streamed answer text.
type LLMDelta struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// LLMDone ends a call. On success Message is the assistant turn (a
// provider.Message, vendor serialization included) and Stop why it ended; on
// failure Error says what went wrong and Message is empty.
type LLMDone struct {
	ID           string          `json:"id"`
	Message      json.RawMessage `json:"message,omitempty"`
	Stop         string          `json:"stop,omitempty"`
	InputTokens  int             `json:"inputTokens,omitempty"`
	OutputTokens int             `json:"outputTokens,omitempty"`
	Error        string          `json:"error,omitempty"`
}
