package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// openaiProvider speaks the Chat Completions wire format over plain net/http.
// That one format is what OpenAI, Ollama, LM Studio, vLLM and OpenRouter all
// share, and a vendor SDK would pull in OpenAI-only assumptions (and another
// dependency) for no gain on the others.
type openaiProvider struct {
	// client has no overall Timeout: a streamed turn with tool deliberation can
	// legitimately run for minutes, so the caller's ctx is the only deadline.
	client *http.Client
	base   string
	key    string
	model  string
	effort string
}

func newOpenAI(cfg Config) *openaiProvider {
	return &openaiProvider{
		client: &http.Client{},
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		key:    cfg.APIKey,
		model:  cfg.Model,
		effort: cfg.Effort,
	}
}

func (p *openaiProvider) Name() string  { return KindOpenAI }
func (p *openaiProvider) Model() string { return p.model }

// Wire types. Only the fields this provider reads or writes are modelled.

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role string `json:"role"`
	// Content is always a string, empty beside tool calls: OpenAI accepts null
	// there, but several compatible servers reject it, and all accept "".
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaRequest struct {
	Model    string            `json:"model"`
	Messages []json.RawMessage `json:"messages"`
	Tools    []oaTool          `json:"tools,omitempty"`
	Stream   bool              `json:"stream"`
	// include_usage asks for a final chunk carrying token counts, which a
	// streamed response otherwise omits.
	StreamOptions       map[string]bool `json:"stream_options"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	// Some compatible servers report a failure mid-stream as a chunk with an
	// error object instead of a non-2xx status.
	Error json.RawMessage `json:"error"`
}

// reasoningModel reports whether the model takes reasoning_effort and
// max_completion_tokens. Other models, and most local ones, reject both, so
// they are sent only where they are known to be understood. A routing prefix
// such as OpenRouter's "openai/" is ignored.
func reasoningModel(model string) bool {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	model = strings.ToLower(model)
	if strings.HasPrefix(model, "gpt-5") {
		return true
	}
	// o1, o3, o4-mini... but not "openhermes" or "olmo".
	return len(model) > 1 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9'
}

func (p *openaiProvider) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	body := oaRequest{
		Model:         p.model,
		Stream:        true,
		StreamOptions: map[string]bool{"include_usage": true},
	}
	reasoning := reasoningModel(p.model)
	if p.effort != "" && reasoning {
		body.ReasoningEffort = p.effort
	}
	if req.MaxTokens > 0 {
		if reasoning {
			body.MaxCompletionTokens = req.MaxTokens
		} else {
			body.MaxTokens = req.MaxTokens
		}
	}

	msgs, err := p.toMessages(req)
	if err != nil {
		return Response{}, err
	}
	body.Messages = msgs
	for _, t := range req.Tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.Schema
		if len(ot.Function.Parameters) == 0 {
			ot.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, ot)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("openai: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("openai: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.key)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return Response{}, fmt.Errorf("openai: %s: %s", resp.Status, errorMessage(b))
	}

	out, err := p.readStream(resp.Body, onDelta)
	if err != nil && ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	return out, err
}

// partialCall accumulates one tool call whose id, name and arguments arrive in
// fragments spread over many chunks, keyed by the call's index.
type partialCall struct {
	id, name string
	args     strings.Builder
}

func (p *openaiProvider) readStream(r io.Reader, onDelta func(Delta)) (Response, error) {
	var (
		text   strings.Builder
		calls  = map[int]*partialCall{}
		finish string
		done   bool
		out    Response
	)
	br := bufio.NewReader(r)
	for eof := false; !done && !eof; {
		// ReadString rather than a Scanner: a single chunk carrying a large
		// tool argument can exceed any fixed line limit.
		line, err := br.ReadString('\n')
		if errors.Is(err, io.EOF) {
			eof = true // still handle a final line with no trailing newline
		} else if err != nil {
			return Response{}, fmt.Errorf("openai: read stream: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // blank separators, comments, "event:" lines
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			done = true
			break
		}
		if data == "" {
			continue
		}
		var chunk oaChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Response{}, fmt.Errorf("openai: decode chunk: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return Response{}, fmt.Errorf("openai: %s", errorMessage([]byte(`{"error":`+string(chunk.Error)+`}`)))
		}
		if chunk.Usage != nil {
			out.InputTokens = chunk.Usage.PromptTokens
			out.OutputTokens = chunk.Usage.CompletionTokens
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				text.WriteString(c.Delta.Content)
				if onDelta != nil {
					onDelta(Delta{Text: c.Delta.Content})
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				pc := calls[tc.Index]
				if pc == nil {
					pc = &partialCall{}
					calls[tc.Index] = pc
				}
				if tc.ID != "" {
					pc.id = tc.ID
				}
				pc.name += tc.Function.Name
				pc.args.WriteString(tc.Function.Arguments)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	// A stream cut off before both [DONE] and a finish reason is a dropped
	// connection, not a finished turn; acting on its half-built tool calls
	// would be wrong.
	if !done && finish == "" {
		return Response{}, fmt.Errorf("openai: stream ended early: %w", io.ErrUnexpectedEOF)
	}

	out.Message, out.Stop = p.assemble(text.String(), calls, finish)
	return out, nil
}

// assemble builds the finished turn: neutral blocks for the loop and the
// OpenAI-shaped assistant message as Raw, so a same-model continuation sends
// back exactly what the server produced.
func (p *openaiProvider) assemble(text string, calls map[int]*partialCall, finish string) (Message, StopReason) {
	msg := Message{Role: RoleAssistant, RawProvider: KindOpenAI, RawModel: p.model}
	raw := oaMessage{Role: "assistant", Content: text}
	if text != "" {
		msg.Blocks = append(msg.Blocks, Block{Type: BlockText, Text: text})
	}

	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		pc := calls[i]
		if pc.id == "" {
			// Some local servers omit ids; the loop pairs results by id, so
			// one is made up that is stable within the turn.
			pc.id = "call_" + strconv.Itoa(i)
		}
		args := pc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		input := json.RawMessage(args)
		if !json.Valid(input) {
			// Keep malformed arguments visible as a JSON string, so the tool
			// fails with a readable error rather than running with nothing.
			input, _ = json.Marshal(args)
		}
		msg.Blocks = append(msg.Blocks, Block{Type: BlockToolCall, ToolCallID: pc.id, ToolName: pc.name, Input: input})
		var tc oaToolCall
		tc.ID, tc.Type = pc.id, "function"
		tc.Function.Name, tc.Function.Arguments = pc.name, args
		raw.ToolCalls = append(raw.ToolCalls, tc)
	}
	if b, err := json.Marshal(raw); err == nil {
		msg.Raw = b
	}

	var stop StopReason
	switch finish {
	case "stop":
		stop = StopEndTurn
	case "tool_calls", "function_call":
		stop = StopToolUse
	case "length":
		stop = StopMaxTokens
	case "content_filter":
		stop = StopRefusal
	case "":
		// [DONE] without a finish reason: infer it from what arrived.
		stop = StopEndTurn
		if len(calls) > 0 {
			stop = StopToolUse
		}
	default:
		stop = StopOther
	}
	// Some servers report "stop" even when the turn ended in tool calls; the
	// calls are what the loop has to act on.
	if stop == StopEndTurn && len(calls) > 0 {
		stop = StopToolUse
	}
	return msg, stop
}

// toMessages converts the neutral history to Chat Completions messages.
func (p *openaiProvider) toMessages(req Request) ([]json.RawMessage, error) {
	var out []json.RawMessage
	add := func(m oaMessage) error {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		out = append(out, b)
		return nil
	}
	if req.System != "" {
		if err := add(oaMessage{Role: "system", Content: req.System}); err != nil {
			return nil, err
		}
	}
	for i, m := range req.Messages {
		if m.Role == RoleAssistant {
			if len(m.Raw) > 0 && m.RawProvider == KindOpenAI && m.RawModel == p.model {
				if !json.Valid(m.Raw) {
					return nil, fmt.Errorf("openai: history message %d: raw turn is not JSON", i)
				}
				out = append(out, append(json.RawMessage(nil), m.Raw...))
				continue
			}
			am := oaMessage{Role: "assistant"}
			var texts []string
			for _, b := range m.Blocks {
				switch b.Type {
				case BlockText:
					texts = append(texts, b.Text)
				case BlockToolCall:
					args := string(b.Input)
					if args == "" {
						args = "{}"
					}
					var tc oaToolCall
					tc.ID, tc.Type = b.ToolCallID, "function"
					tc.Function.Name, tc.Function.Arguments = b.ToolName, args
					am.ToolCalls = append(am.ToolCalls, tc)
				}
			}
			am.Content = strings.Join(texts, "")
			if am.Content == "" && len(am.ToolCalls) == 0 {
				continue // an empty turn (refusal) has nothing to send back
			}
			if err := add(am); err != nil {
				return nil, err
			}
			continue
		}

		// Tool results go first, one "tool" message each, because they must
		// directly follow the assistant message whose calls they answer.
		var texts []string
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolResult:
				content := b.Output
				if b.IsError {
					// Chat Completions has no error flag on a tool message; say it
					// in the content so the model does not take it as success.
					content = "Error: " + content
				}
				if err := add(oaMessage{Role: "tool", ToolCallID: b.ToolCallID, Content: content}); err != nil {
					return nil, err
				}
			case BlockText:
				if b.Text != "" {
					texts = append(texts, b.Text)
				}
			}
		}
		if len(texts) > 0 {
			if err := add(oaMessage{Role: "user", Content: strings.Join(texts, "\n")}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// errorMessage pulls the human-readable message out of an error body. OpenAI
// nests it under error.message, Ollama sends error as a bare string, and a
// proxy may send plain text; the result is capped so a stack trace or HTML
// page does not flood the transcript.
func errorMessage(body []byte) string {
	var withObj struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var withStr struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &withObj) == nil && withObj.Error.Message != "" {
		msg = withObj.Error.Message
	} else if json.Unmarshal(body, &withStr) == nil && withStr.Error != "" {
		msg = withStr.Error
	}
	if msg == "" {
		msg = "(empty response body)"
	}
	const limit = 500
	if r := []rune(msg); len(r) > limit {
		msg = string(r[:limit]) + "…"
	}
	return msg
}
