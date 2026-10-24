package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// defaultMaxTokens caps a turn when the caller leaves Request.MaxTokens at
// zero. It is generous because thinking on the current models counts against
// it, and a voice turn that stops mid-sentence is worse than a long one.
const defaultMaxTokens = 16000

// fallbackBeta and the "fallbacks": "default" body field let the API re-run a
// request its safety classifiers decline on a model Anthropic picks for the
// refusal category, inside the same streamed call. Without it a false
// positive on an innocuous board edit simply ends the turn.
const fallbackBeta = "server-side-fallback-2026-07-01"

// fallbackModels are the models that accept the "default" fallback form.
// Sending it to any other model is a 400, so this is an allow-list.
var fallbackModels = map[string]bool{
	"claude-opus-5-5":   true,
	"claude-fable-5-1":  true,
	"claude-opus-5":     true,
	"claude-sonnet-5-5": true,
}

// defaultAnthropicBaseURL is where the SDK goes when no base URL is set. The
// fallback beta exists only there; a proxy or gateway may reject the field.
const defaultAnthropicBaseURL = "https://api.anthropic.com"

type anthropicProvider struct {
	client   anthropic.Client
	model    string
	effort   string
	fallback bool
}

func newAnthropic(cfg Config) *anthropicProvider {
	// WithoutEnvironmentDefaults keeps ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL and
	// profile files out of it: the caller already resolved the key, and a stray
	// environment variable silently overriding the configured endpoint is the
	// kind of surprise a settings page cannot explain.
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithHTTPClient(anthropicHTTPClient()),
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	return &anthropicProvider{
		client:   anthropic.NewClient(opts...),
		model:    cfg.Model,
		effort:   cfg.Effort,
		fallback: fallbackModels[cfg.Model] && (base == "" || base == defaultAnthropicBaseURL),
	}
}

func (p *anthropicProvider) Name() string  { return KindAnthropic }
func (p *anthropicProvider) Model() string { return p.model }

func (p *anthropicProvider) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	messages, err := p.toParams(req.Messages)
	if err != nil {
		return Response{}, err
	}
	tools, err := anthropicTools(req.Tools)
	if err != nil {
		return Response{}, err
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	// Thinking is deliberately left unset: on the current Opus it is always on
	// and an explicit "disabled" is a 400. Effort is the depth control instead.
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: maxTokens,
		Messages:  messages,
		Tools:     tools,
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if p.effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(p.effort)}
	}

	var opts []option.RequestOption
	if p.fallback {
		// The non-beta params have no Fallbacks field; the SDK itself sets
		// "stream" with WithJSONSet, so patching the body the same way is safe.
		opts = append(opts,
			option.WithHeaderAdd("anthropic-beta", fallbackBeta),
			option.WithJSONSet("fallbacks", "default"),
		)
	}

	stream := p.client.Messages.NewStreaming(ctx, params, opts...)
	defer stream.Close()

	var msg anthropic.Message
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return Response{}, fmt.Errorf("anthropic stream: %w", err)
		}
		if ev, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if d, ok := ev.Delta.AsAny().(anthropic.TextDelta); ok && d.Text != "" && onDelta != nil {
				onDelta(Delta{Text: d.Text})
			}
		}
	}
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("anthropic: %w", err)
	}
	return p.response(msg)
}

// response turns the accumulated message into the neutral turn plus the raw
// content needed to replay it unchanged.
func (p *anthropicProvider) response(msg anthropic.Message) (Response, error) {
	kept := replayableContent(msg.Content)

	out := Message{Role: RoleAssistant, RawProvider: KindAnthropic, RawModel: p.model}
	raw := anthropic.MessageParam{Role: anthropic.MessageParamRoleAssistant}
	for _, c := range kept {
		raw.Content = append(raw.Content, c.ToParam())
		switch b := c.AsAny().(type) {
		case anthropic.TextBlock:
			if b.Text != "" {
				out.Blocks = append(out.Blocks, Block{Type: BlockText, Text: b.Text})
			}
		case anthropic.ToolUseBlock:
			input := json.RawMessage(b.JSON.Input.Raw())
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage(b.Input)
			}
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out.Blocks = append(out.Blocks, Block{
				Type: BlockToolCall, ToolCallID: b.ID, ToolName: b.Name, Input: input,
			})
		}
		// Thinking and any block type this SDK does not model stay out of the
		// neutral form; they live on in Raw for same-model replay.
	}
	if len(raw.Content) > 0 {
		b, err := json.Marshal(raw)
		if err != nil {
			return Response{}, fmt.Errorf("anthropic: encode assistant turn: %w", err)
		}
		out.Raw = b
	}

	u := msg.Usage
	return Response{
		Message: out,
		Stop:    anthropicStop(msg.StopReason),
		// Cached prompt tokens are still prompt tokens; summing them keeps the
		// figure comparable with OpenAI's prompt_tokens, which includes cache.
		InputTokens:  int(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens),
		OutputTokens: int(u.OutputTokens),
	}, nil
}

// replayableContent drops what must not be echoed back after a server-side
// fallback. When a model declines mid-output, the blocks it produced before
// the last "fallback" marker are a discarded partial: its text was handed to
// the fallback model as context, but its thinking and tool calls were never
// acted on, and replaying them would be rejected. The marker itself is only
// an audit note, so it goes too.
func replayableContent(content []anthropic.ContentBlockUnion) []anthropic.ContentBlockUnion {
	boundary := -1
	for i, c := range content {
		if c.Type == "fallback" {
			boundary = i
		}
	}
	kept := make([]anthropic.ContentBlockUnion, 0, len(content))
	for i, c := range content {
		if c.Type == "fallback" || (i < boundary && c.Type != "text") {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func anthropicStop(r anthropic.StopReason) StopReason {
	switch r {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return StopEndTurn
	case anthropic.StopReasonToolUse:
		return StopToolUse
	case anthropic.StopReasonMaxTokens, anthropic.StopReasonModelContextWindowExceeded:
		return StopMaxTokens
	case anthropic.StopReasonRefusal:
		return StopRefusal
	}
	return StopOther
}

// toParams converts the neutral history to the SDK's message list.
func (p *anthropicProvider) toParams(msgs []Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for i, m := range msgs {
		if m.Role == RoleAssistant {
			mp, err := p.assistantParam(m)
			if err != nil {
				return nil, fmt.Errorf("anthropic: history message %d: %w", i, err)
			}
			// An empty assistant turn (a refusal before any output) cannot be
			// sent back; dropping it leaves the surrounding turns intact.
			if len(mp.Content) > 0 {
				out = append(out, mp)
			}
			continue
		}

		results, rest := userContent(m)
		// Consecutive user turns are merged so every tool_result answering one
		// assistant turn lands in a single message, ahead of any text: the API
		// requires results to open the user turn that follows the tool calls.
		if n := len(out); n > 0 && out[n-1].Role == anthropic.MessageParamRoleUser {
			prev := out[n-1].Content
			var prevResults, prevRest []anthropic.ContentBlockParamUnion
			for _, c := range prev {
				if c.OfToolResult != nil {
					prevResults = append(prevResults, c)
				} else {
					prevRest = append(prevRest, c)
				}
			}
			out[n-1].Content = concat(prevResults, results, prevRest, rest)
			continue
		}
		if content := concat(results, rest); len(content) > 0 {
			out = append(out, anthropic.NewUserMessage(content...))
		}
	}
	return out, nil
}

// assistantParam replays the stored raw turn when it came from this very
// provider and model, because thinking blocks are signed against the
// conversation and must come back byte for byte. Anything else is rebuilt from
// the neutral blocks, which is lossy but valid on any model.
func (p *anthropicProvider) assistantParam(m Message) (anthropic.MessageParam, error) {
	if len(m.Raw) > 0 && m.RawProvider == KindAnthropic && m.RawModel == p.model {
		var mp anthropic.MessageParam
		if err := json.Unmarshal(m.Raw, &mp); err != nil {
			return mp, fmt.Errorf("decode raw turn: %w", err)
		}
		mp.Role = anthropic.MessageParamRoleAssistant
		return mp, nil
	}
	var content []anthropic.ContentBlockParamUnion
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			// The API rejects empty text blocks.
			if b.Text != "" {
				content = append(content, anthropic.NewTextBlock(b.Text))
			}
		case BlockToolCall:
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			content = append(content, anthropic.NewToolUseBlock(b.ToolCallID, input, b.ToolName))
		}
	}
	return anthropic.NewAssistantMessage(content...), nil
}

// userContent splits a user turn into its tool results and everything else.
func userContent(m Message) (results, rest []anthropic.ContentBlockParamUnion) {
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockToolResult:
			results = append(results, anthropic.NewToolResultBlock(b.ToolCallID, b.Output, b.IsError))
		case BlockText:
			if b.Text != "" {
				rest = append(rest, anthropic.NewTextBlock(b.Text))
			}
		}
	}
	return results, rest
}

func concat(parts ...[]anthropic.ContentBlockParamUnion) []anthropic.ContentBlockParamUnion {
	var out []anthropic.ContentBlockParamUnion
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// anthropicTools maps each tool's JSON Schema onto the SDK's input schema.
// The SDK models properties and required; every other keyword ($defs,
// additionalProperties, descriptions on the root) rides in ExtraFields so the
// schema reaches the model as the MCP server wrote it.
func anthropicTools(tools []Tool) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var schema map[string]any
		if len(t.Schema) > 0 {
			if err := json.Unmarshal(t.Schema, &schema); err != nil {
				return nil, fmt.Errorf("anthropic: tool %s schema: %w", t.Name, err)
			}
		}
		in := anthropic.ToolInputSchemaParam{}
		if props, ok := schema["properties"]; ok {
			in.Properties = props
		} else {
			// An object schema with no properties still needs the key for
			// some validators; an empty map is the honest "no arguments".
			in.Properties = map[string]any{}
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					in.Required = append(in.Required, s)
				}
			}
		}
		for k, v := range schema {
			switch k {
			case "properties", "required", "type":
				continue
			}
			if in.ExtraFields == nil {
				in.ExtraFields = map[string]any{}
			}
			in.ExtraFields[k] = v
		}
		tool := anthropic.ToolParam{Name: t.Name, InputSchema: in}
		if t.Description != "" {
			tool.Description = anthropic.String(t.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return out, nil
}
