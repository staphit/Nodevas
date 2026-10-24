package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/runnerwire"
)

// Copilot model calls (runnerwire/llm.go). The hub's copilot sends its model
// calls here and this runner makes them with a key that never leaves this
// machine. What it calls, and with which key, is the owner's setting here;
// the hub only says what to ask. A hub broken into can make this runner spend
// the owner's quota, within the limits below, and nothing else: it can never
// read the key, point it at another endpoint, or use it for someone else.

// CopilotKeyEnv is the environment variable a runner reads its copilot key
// from when none is stored with `nodevas runner copilot-key`.
const CopilotKeyEnv = "NODEVAS_COPILOT_API_KEY"

// Limits on the model calls a runner makes for the hub.
const (
	// maxConcurrentLLM is how many calls run at once. A copilot turn makes
	// one at a time; two leaves room for a second browser tab.
	maxConcurrentLLM = 2
	// llmRatePerMinute caps calls per minute. A turn of 25 tool calls makes
	// 26 calls, rarely within a minute.
	llmRatePerMinute = 40
	// llmCallTimeout bounds one call.
	llmCallTimeout = 10 * time.Minute
	// maxCopilotKeyBytes bounds a stored key.
	maxCopilotKeyBytes = 4096
)

// CopilotProviders are the providers copilotProvider takes.
var CopilotProviders = []string{provider.KindAnthropic, provider.KindOpenAI}

// CopilotKey is the copilot key in effect and where it comes from:
// "stored" (runner config) or "environment" (CopilotKeyEnv); "" when none.
func (c *Config) CopilotKey() (key, source string) {
	if c == nil {
		return "", ""
	}
	if key := strings.TrimSpace(c.CopilotAPIKey); key != "" {
		return key, "stored"
	}
	if key := strings.TrimSpace(os.Getenv(CopilotKeyEnv)); key != "" {
		return key, "environment"
	}
	return "", ""
}

// CopilotLLM is what this runner calls for its owner's copilot, and the key,
// when it answers copilot model calls at all: a provider is set, and there is
// a key (or the endpoint is a self-hosted OpenAI-compatible one, which
// usually takes none). reason says why not otherwise.
func (c *Config) CopilotLLM() (info runnerwire.LLMInfo, key string, reason string) {
	if c == nil || c.CopilotProvider == "" {
		return runnerwire.LLMInfo{}, "", "copilotProvider is not set"
	}
	info = runnerwire.LLMInfo{
		Provider: c.CopilotProvider,
		Model:    c.CopilotModel,
		BaseURL:  strings.TrimRight(strings.TrimSpace(c.CopilotBaseURL), "/"),
	}
	if info.Model == "" {
		info.Model = provider.DefaultModel(info.Provider)
	}
	key, _ = c.CopilotKey()
	keyless := info.Provider == provider.KindOpenAI && info.BaseURL != "" && info.BaseURL != provider.DefaultOpenAIBaseURL
	if key == "" && !keyless {
		return runnerwire.LLMInfo{}, "", "no copilot key: run `nodevas runner copilot-key` or set " + CopilotKeyEnv
	}
	return info, key, ""
}

// setCopilot handles the copilot settings of `nodevas runner set`.
func (c *Config) setCopilot(key, value string, reset bool) error {
	switch key {
	case "copilotProvider":
		if reset || strings.EqualFold(value, "none") || strings.EqualFold(value, "off") {
			c.CopilotProvider = ""
			return nil
		}
		kind := strings.ToLower(value)
		for _, p := range CopilotProviders {
			if kind == p {
				c.CopilotProvider = kind
				return nil
			}
		}
		return fmt.Errorf("copilotProvider must be one of %s, or none", strings.Join(CopilotProviders, ", "))
	case "copilotModel":
		if reset {
			c.CopilotModel = ""
			return nil
		}
		if len(value) > 200 || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.ContainsAny(value, " \t") {
			return errors.New("copilotModel is not a model name")
		}
		c.CopilotModel = value
	case "copilotBaseURL":
		if reset {
			c.CopilotBaseURL = ""
			return nil
		}
		if err := checkCopilotBaseURL(value); err != nil {
			return err
		}
		c.CopilotBaseURL = strings.TrimRight(value, "/")
	}
	return nil
}

// checkCopilotBaseURL accepts https anywhere, and plain http only to this
// machine or the local network: the key travels in every request.
func checkCopilotBaseURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || len(value) > runnerwire.MaxLLMFieldBytes || u.User != nil {
		return errors.New("copilotBaseURL must be an http(s) URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if localHost(u.Hostname()) {
			return nil
		}
		return errors.New("copilotBaseURL may use plain http only to this machine or the local network; use https")
	}
	return errors.New("copilotBaseURL must be an http(s) URL")
}

func localHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// llmLimiter is the per-minute cap, shared by every connection a runner makes.
type llmLimiter struct {
	mu     sync.Mutex
	recent []time.Time
}

func (l *llmLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	kept := l.recent[:0]
	for _, t := range l.recent {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.recent = kept
	if len(l.recent) >= llmRatePerMinute {
		return false
	}
	l.recent = append(l.recent, now)
	return true
}

// llmSession serves model calls on one hub connection. Its messages go out
// through out, which the connection's loop writes; a call still running when
// the connection drops is stopped, as its answer has nowhere to go.
type llmSession struct {
	c       *Client
	ownerID string
	ctx     context.Context
	stopAll context.CancelFunc
	out     chan runnerwire.Envelope

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func (c *Client) newLLMSession(ctx context.Context, ownerID string) *llmSession {
	ctx, cancel := context.WithCancel(ctx)
	return &llmSession{
		c: c, ownerID: ownerID, ctx: ctx, stopAll: cancel,
		out:     make(chan runnerwire.Envelope, 64),
		running: map[string]context.CancelFunc{},
	}
}

// stop ends every call and waits for them.
func (s *llmSession) stop() {
	s.stopAll()
	s.wg.Wait()
}

// send queues a message for the connection; false once it is gone.
func (s *llmSession) send(env runnerwire.Envelope) bool {
	select {
	case s.out <- env:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *llmSession) refuse(id, reason string) {
	s.c.logf("copilot: refused model call %s: %s", shortID(id), reason)
	go s.send(runnerwire.Envelope{Type: runnerwire.TypeLLMDone, LLMDone: &runnerwire.LLMDone{ID: id, Error: reason}})
}

// cancel stops the call id names, if it is running.
func (s *llmSession) cancel(id string) {
	s.mu.Lock()
	stop := s.running[id]
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// request vets a call and starts it. It returns at once; the answer goes out
// through out.
func (s *llmSession) request(req *runnerwire.LLMRequest) {
	if req == nil || req.ID == "" || len(req.ID) > runnerwire.MaxLLMIDBytes {
		return
	}
	info, key, reason := s.c.Config.CopilotLLM()
	switch {
	case reason != "":
		s.refuse(req.ID, "this runner does not answer copilot model calls: "+reason)
		return
	case s.ownerID != "" && req.AccountID != s.ownerID:
		s.refuse(req.ID, "this runner answers its owner's copilot only")
		return
	case req.Provider != info.Provider || req.Model != info.Model ||
		strings.TrimRight(req.BaseURL, "/") != info.BaseURL:
		s.refuse(req.ID, "the request does not match this runner's copilot settings (restart the runner after changing them)")
		return
	}
	switch req.Effort {
	case "", "low", "medium", "high":
	default:
		s.refuse(req.ID, "unknown effort "+req.Effort)
		return
	}
	var messages []provider.Message
	if err := json.Unmarshal(req.Messages, &messages); err != nil || len(messages) == 0 {
		s.refuse(req.ID, "the request has no readable messages")
		return
	}
	var tools []provider.Tool
	if len(req.Tools) > 0 && string(req.Tools) != "null" {
		if err := json.Unmarshal(req.Tools, &tools); err != nil {
			s.refuse(req.ID, "the request's tools are not readable")
			return
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens < 0 {
		maxTokens = 0
	}
	if maxTokens > runnerwire.MaxLLMMaxTokens {
		maxTokens = runnerwire.MaxLLMMaxTokens
	}

	s.mu.Lock()
	if _, dup := s.running[req.ID]; dup {
		s.mu.Unlock()
		return
	}
	if len(s.running) >= maxConcurrentLLM {
		s.mu.Unlock()
		s.refuse(req.ID, "this runner is already making as many copilot model calls as it will at once")
		return
	}
	if !s.c.llmLimiter().allow(time.Now()) {
		s.mu.Unlock()
		s.refuse(req.ID, "this runner's copilot is over its limit of model calls per minute")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, llmCallTimeout)
	s.running[req.ID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, req.ID)
			s.mu.Unlock()
			cancel()
		}()
		s.call(ctx, req.ID, info, key, req.Effort, provider.Request{
			System: req.System, Messages: messages, Tools: tools, MaxTokens: maxTokens,
		})
	}()
}

func (s *llmSession) call(ctx context.Context, id string, info runnerwire.LLMInfo, key, effort string, req provider.Request) {
	done := func(d runnerwire.LLMDone) {
		d.ID = id
		s.send(runnerwire.Envelope{Type: runnerwire.TypeLLMDone, LLMDone: &d})
	}
	newProvider := s.c.NewLLMProvider
	if newProvider == nil {
		newProvider = provider.New
	}
	model, err := newProvider(provider.Config{
		Kind: info.Provider, Model: info.Model, BaseURL: info.BaseURL, APIKey: key, Effort: effort,
	})
	if err != nil {
		done(runnerwire.LLMDone{Error: err.Error()})
		return
	}
	s.c.logf("copilot: model call %s to %s/%s (%d messages)", shortID(id), info.Provider, info.Model, len(req.Messages))
	started := time.Now()
	resp, err := model.Stream(ctx, req, func(d provider.Delta) {
		for text := d.Text; text != ""; {
			chunk := text
			if len(chunk) > runnerwire.MaxLLMDeltaBytes {
				chunk = chunk[:runnerwire.MaxLLMDeltaBytes]
			}
			text = text[len(chunk):]
			s.send(runnerwire.Envelope{Type: runnerwire.TypeLLMDelta, LLMDelta: &runnerwire.LLMDelta{ID: id, Text: chunk}})
		}
	})
	if err != nil {
		if ctx.Err() != nil && s.ctx.Err() == nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errors.New("cancelled")
		}
		s.c.logf("copilot: model call %s failed: %v", shortID(id), err)
		done(runnerwire.LLMDone{Error: truncate(err.Error(), 2000)})
		return
	}
	message, err := json.Marshal(resp.Message)
	if err != nil || len(message) > runnerwire.MaxLLMDoneBytes {
		done(runnerwire.LLMDone{Error: "the model's answer is too large to send to the hub"})
		return
	}
	s.c.logf("copilot: model call %s done in %s (%d in, %d out tokens)", shortID(id),
		time.Since(started).Round(100*time.Millisecond), resp.InputTokens, resp.OutputTokens)
	done(runnerwire.LLMDone{Message: message, Stop: string(resp.Stop),
		InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens})
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "llm-")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
