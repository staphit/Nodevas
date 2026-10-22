// Package provider is the seam between the copilot loop and whichever model
// vendor answers it. The loop speaks only these types; each implementation
// translates them to and from its own wire format.
//
// History is provider-neutral so a conversation survives a provider switch,
// but every assistant turn also keeps the vendor's own content (Raw) so that a
// conversation continued on the same provider and model is replayed byte for
// byte. Some models bind reasoning blocks to the conversation that produced
// them and refuse an edited history, so the neutral form is only the fallback.
package provider

import (
	"context"
	"encoding/json"
)

// Role is who said a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType tags one piece of a message.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolCall   BlockType = "tool_call"
	BlockToolResult BlockType = "tool_result"
)

// Block is one piece of a message. Exactly the fields its Type names are set.
type Block struct {
	Type BlockType `json:"type"`

	// Text is set on BlockText.
	Text string `json:"text,omitempty"`

	// ToolCallID pairs a call with its result.
	ToolCallID string `json:"toolCallId,omitempty"`
	// ToolName and Input are set on BlockToolCall. Input is a JSON object.
	ToolName string          `json:"toolName,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	// Output and IsError are set on BlockToolResult.
	Output  string `json:"output,omitempty"`
	IsError bool   `json:"isError,omitempty"`
}

// Message is one turn of the conversation.
type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"blocks"`

	// Raw is the vendor's own serialization of an assistant turn, replayed
	// verbatim when RawProvider and RawModel match the provider answering
	// now. Empty for user turns and for turns from another vendor.
	Raw         json.RawMessage `json:"raw,omitempty"`
	RawProvider string          `json:"rawProvider,omitempty"`
	RawModel    string          `json:"rawModel,omitempty"`
}

// Tool is a function the model may call. Schema is a JSON Schema object.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Request is one model call: the whole history plus what the model may use.
type Request struct {
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
}

// StopReason says why a model call ended.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Response is the finished assistant turn.
type Response struct {
	// Message is the assistant turn, Raw included, ready to append to history.
	Message Message
	Stop    StopReason
	// Usage is reported when the vendor gives it; zero otherwise.
	InputTokens  int
	OutputTokens int
}

// Delta is a streamed fragment of assistant text, for live display and
// speech. Tool calls are not streamed; they arrive whole in the Response.
type Delta struct {
	Text string
}

// Provider answers one model call, streaming text as it is produced.
type Provider interface {
	// Name identifies the vendor ("anthropic", "openai"), recorded as
	// RawProvider on the turns it produces.
	Name() string
	// Model is the model id this provider calls, recorded as RawModel.
	Model() string
	// Stream runs one call. onDelta is called from the calling goroutine,
	// in order, before Stream returns. A cancelled ctx aborts the call.
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
}

// Config selects and configures a provider. Secrets are resolved by the
// caller; this struct carries the key itself so implementations stay free of
// environment lookups.
type Config struct {
	// Kind is "anthropic" or "openai" (any OpenAI-compatible Chat Completions
	// endpoint: OpenAI, Ollama, LM Studio, vLLM, OpenRouter).
	Kind    string `json:"kind"`
	Model   string `json:"model"`
	BaseURL string `json:"baseUrl,omitempty"`
	APIKey  string `json:"-"`
	// Effort is passed to vendors that take one ("low", "medium", "high").
	// Voice turns favour low latency, so the default is "low".
	Effort string `json:"effort,omitempty"`
}
