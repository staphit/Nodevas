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

// AdvisorKeyEnv is the environment variable a runner reads its advisor key
// from when none is stored with `nodevas runner advisor-key`.
const AdvisorKeyEnv = "NODEVAS_ADVISOR_API_KEY"

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

// AdvisorProviders are the providers advisorProvider takes: the copilot's, or
// a System One endpoint (Jev, or Laya run locally; decide.go).
var AdvisorProviders = []string{provider.KindAnthropic, provider.KindOpenAI, runnerwire.ProviderSystemOne}

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

// AdvisorKey is the advisor key in effect and where it comes from: "stored"
// (runner config), "environment" (AdvisorKeyEnv) or "copilot" (the copilot
// key, used when both settings name the same provider); "" when none.
func (c *Config) AdvisorKey() (key, source string) {
	if c == nil {
		return "", ""
	}
	if key := strings.TrimSpace(c.AdvisorAPIKey); key != "" {
		return key, "stored"
	}
	if key := strings.TrimSpace(os.Getenv(AdvisorKeyEnv)); key != "" {
		return key, "environment"
	}
	if c.AdvisorProvider != "" && c.AdvisorProvider == c.CopilotProvider {
		if key, _ := c.CopilotKey(); key != "" {
			return key, "copilot"
		}
	}
	return "", ""
}

// AdvisorLLM is what this runner calls for its owner's autopilot advisor
// (W7), and the key, when it answers advisor calls at all; reason says why
// not otherwise. The same rules as CopilotLLM.
func (c *Config) AdvisorLLM() (info runnerwire.LLMInfo, key string, reason string) {
	if c == nil || c.AdvisorProvider == "" {
		return runnerwire.LLMInfo{}, "", "advisorProvider is not set"
	}
	info = runnerwire.LLMInfo{
		Provider: c.AdvisorProvider,
		Model:    c.AdvisorModel,
		BaseURL:  strings.TrimRight(strings.TrimSpace(c.AdvisorBaseURL), "/"),
		Purpose:  runnerwire.LLMPurposeAdvisor,
	}
	if info.Provider == runnerwire.ProviderSystemOne {
		// A System One endpoint: Laya on this machine by default, which takes
		// no key unless its owner set LAYA_API_KEY; the model may be left to
		// the endpoint ("" sends none).
		if info.BaseURL == "" {
			info.BaseURL = runnerwire.DefaultSystemOneBaseURL
		}
		key, _ = c.AdvisorKey()
		return info, key, ""
	}
	if info.Model == "" {
		info.Model = provider.DefaultModel(info.Provider)
	}
	key, _ = c.AdvisorKey()
	keyless := info.Provider == provider.KindOpenAI && info.BaseURL != "" && info.BaseURL != provider.DefaultOpenAIBaseURL
	if key == "" && !keyless {
		return runnerwire.LLMInfo{}, "", "no advisor key: run `nodevas runner advisor-key`, set " + AdvisorKeyEnv +
			", or use the copilot's provider so its key serves both"
	}
	return info, key, ""
}

// modelCallSettings is what answers a model call of purpose: the settings,
// the key, and why this runner does not answer such calls ("" when it does).
func (c *Config) modelCallSettings(purpose string) (info runnerwire.LLMInfo, key, reason string) {
	switch purpose {
	case runnerwire.LLMPurposeCopilot:
		return c.CopilotLLM()
	case runnerwire.LLMPurposeAdvisor:
		return c.AdvisorLLM()
	}
	return runnerwire.LLMInfo{}, "", fmt.Sprintf("unknown purpose %q", truncate(purpose, 40))
}

// purposeLabel names a purpose in messages and the log.
func purposeLabel(purpose string) string {
	if purpose == runnerwire.LLMPurposeAdvisor {
		return "advisor"
	}
	return "copilot"
}

// setCopilot handles the copilot settings of `nodevas runner set`.
func (c *Config) setCopilot(key, value string, reset bool) error {
	return setModelSetting(key, value, reset, CopilotProviders, &c.CopilotProvider, &c.CopilotModel, &c.CopilotBaseURL)
}

// setAdvisor handles the advisor settings of `nodevas runner set` (W7).
func (c *Config) setAdvisor(key, value string, reset bool) error {
	return setModelSetting(key, value, reset, AdvisorProviders, &c.AdvisorProvider, &c.AdvisorModel, &c.AdvisorBaseURL)
}

// setModelSetting sets one of a model call's provider, model or base URL;
// key is the setting's name, ending in Provider, Model or BaseURL.
func setModelSetting(key, value string, reset bool, providers []string, providerField, modelField, baseField *string) error {
	switch {
	case strings.HasSuffix(key, "Provider"):
		if reset || strings.EqualFold(value, "none") || strings.EqualFold(value, "off") {
			*providerField = ""
			return nil
		}
		kind := strings.ToLower(value)
		for _, p := range providers {
			if kind == p {
				*providerField = kind
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s, or none", key, strings.Join(providers, ", "))
	case strings.HasSuffix(key, "Model"):
		if reset {
			*modelField = ""
			return nil
		}
		if len(value) > 200 || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.ContainsAny(value, " \t") {
			return fmt.Errorf("%s is not a model name", key)
		}
		*modelField = value
	case strings.HasSuffix(key, "BaseURL"):
		if reset {
			*baseField = ""
			return nil
		}
		if err := checkModelBaseURL(key, value); err != nil {
			return err
		}
		*baseField = strings.TrimRight(value, "/")
	}
	return nil
}

// checkCopilotBaseURL accepts https anywhere, and plain http only to this
// machine or the local network: the key travels in every request.
func checkCopilotBaseURL(value string) error { return checkModelBaseURL("copilotBaseURL", value) }

// checkModelBaseURL is checkCopilotBaseURL for the setting named name.
func checkModelBaseURL(name, value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || len(value) > runnerwire.MaxLLMFieldBytes || u.User != nil {
		return fmt.Errorf("%s must be an http(s) URL", name)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if localHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("%s may use plain http only to this machine or the local network; use https", name)
	}
	return fmt.Errorf("%s must be an http(s) URL", name)
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
	what := purposeLabel(req.Purpose)
	info, key, reason := s.c.Config.modelCallSettings(req.Purpose)
	switch {
	case reason != "":
		s.refuse(req.ID, "this runner does not answer "+what+" model calls: "+reason)
		return
	case info.Provider == runnerwire.ProviderSystemOne:
		s.refuse(req.ID, "this runner's "+what+" is a System One endpoint: it answers decisions, not model calls")
		return
	case s.ownerID != "" && req.AccountID != s.ownerID:
		s.refuse(req.ID, "this runner answers its owner's "+what+" only")
		return
	case req.Provider != info.Provider || req.Model != info.Model ||
		strings.TrimRight(req.BaseURL, "/") != info.BaseURL:
		s.refuse(req.ID, "the request does not match this runner's "+what+" settings (restart the runner after changing them)")
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
		s.call(ctx, req.ID, what, info, key, req.Effort, provider.Request{
			System: req.System, Messages: messages, Tools: tools, MaxTokens: maxTokens,
		})
	}()
}

func (s *llmSession) call(ctx context.Context, id, what string, info runnerwire.LLMInfo, key, effort string, req provider.Request) {
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
	s.c.logf("%s: model call %s to %s/%s (%d messages)", what, shortID(id), info.Provider, info.Model, len(req.Messages))
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
		s.c.logf("%s: model call %s failed: %v", what, shortID(id), err)
		done(runnerwire.LLMDone{Error: truncate(err.Error(), 2000)})
		return
	}
	message, err := json.Marshal(resp.Message)
	if err != nil || len(message) > runnerwire.MaxLLMDoneBytes {
		done(runnerwire.LLMDone{Error: "the model's answer is too large to send to the hub"})
		return
	}
	s.c.logf("%s: model call %s done in %s (%d in, %d out tokens)", what, shortID(id),
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
