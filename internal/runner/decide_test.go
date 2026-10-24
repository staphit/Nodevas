package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/runnerwire"
)

// fakeSystemOne is a System One endpoint: it records what it was asked and
// answers with answer (or status and body).
type fakeSystemOne struct {
	mu     sync.Mutex
	auth   []string
	bodies []map[string]json.RawMessage
	status int
	body   string
}

func (f *fakeSystemOne) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.bodies = append(f.bodies, body)
		status, answer := f.status, f.body
		f.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		if answer == "" {
			answer = `{"model":"laya-test","answers":{"implementer":{"type":"choice","choice":"o2","confidence":0.7,
				"probabilities":{"o1":0.2,"o2":0.8}}},"usage":{"input_tokens":120,"output_tokens":4}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func systemOneConfig(base string) *Config {
	return &Config{AdvisorProvider: runnerwire.ProviderSystemOne, AdvisorBaseURL: base}
}

func decideRequest(id, base, model string) *runnerwire.DecideRequest {
	return &runnerwire.DecideRequest{
		ID: id, AccountID: "u-ann", Purpose: runnerwire.LLMPurposeAdvisor, Model: model, BaseURL: base,
		Request: runnerwire.DecisionRequest{
			Model: model,
			State: json.RawMessage(`"Add a README line"`),
			Questions: json.RawMessage(`{"implementer":{"type":"choice","instructions":"who?",` +
				`"criteria":{"o1":"Claude Code with model opus","o2":"OpenCode with a free model"}}}`),
		},
	}
}

func decideDoneOf(t *testing.T, s *llmSession) *runnerwire.DecideDone {
	t.Helper()
	for {
		env := next(t, s)
		if env.Type == runnerwire.TypeDecideDone {
			return env.DecideDone
		}
	}
}

// Laya on this machine: no key, no model; the answer goes back whole.
func TestDecideAsksTheSystemOneEndpoint(t *testing.T) {
	t.Setenv(AdvisorKeyEnv, "")
	fake := &fakeSystemOne{}
	srv := fake.start(t)
	c := &Client{Config: systemOneConfig(srv.URL)}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()

	s.decide(decideRequest("llm-d1", srv.URL, ""))
	done := decideDoneOf(t, s)
	if done.Error != "" || done.Response == nil || done.Response.Model != "laya-test" ||
		done.Response.Answers["implementer"].Choice != "o2" || done.Response.Answers["implementer"].Probabilities["o2"] != 0.8 ||
		done.Response.Usage.InputTokens != 120 {
		t.Fatalf("done = %+v", done)
	}
	if len(fake.auth) != 1 || fake.auth[0] != "" {
		t.Fatalf("Authorization sent without a key: %q", fake.auth)
	}
	if _, ok := fake.bodies[0]["model"]; ok {
		t.Fatalf("a model was sent though none is set: %s", fake.bodies[0]["model"])
	}
	if string(fake.bodies[0]["state"]) != `"Add a README line"` || !strings.Contains(string(fake.bodies[0]["questions"]), `"o2"`) {
		t.Fatalf("body = %v", fake.bodies[0])
	}
}

// Jev: the key goes as a bearer token, the model as configured.
func TestDecideSendsKeyAndModel(t *testing.T) {
	t.Setenv(AdvisorKeyEnv, "")
	fake := &fakeSystemOne{}
	srv := fake.start(t)
	cfg := systemOneConfig(srv.URL)
	cfg.AdvisorModel, cfg.AdvisorAPIKey = "jev-latest", "ts-key"
	c := &Client{Config: cfg}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()

	s.decide(decideRequest("llm-d2", srv.URL, "jev-latest"))
	if done := decideDoneOf(t, s); done.Error != "" {
		t.Fatalf("done = %+v", done)
	}
	if fake.auth[0] != "Bearer ts-key" || string(fake.bodies[0]["model"]) != `"jev-latest"` {
		t.Fatalf("auth %q, model %s", fake.auth, fake.bodies[0]["model"])
	}
}

// The endpoint's own error message comes back as the reason.
func TestDecideReportsTheEndpointsError(t *testing.T) {
	t.Setenv(AdvisorKeyEnv, "")
	fake := &fakeSystemOne{status: http.StatusUnauthorized, body: `{"detail":"invalid or missing bearer token"}`}
	srv := fake.start(t)
	c := &Client{Config: systemOneConfig(srv.URL)}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()

	s.decide(decideRequest("llm-d3", srv.URL, ""))
	if done := decideDoneOf(t, s); done.Response != nil || done.Error != "System One 401: invalid or missing bearer token" {
		t.Fatalf("done = %+v", done)
	}
	fake.mu.Lock()
	fake.status, fake.body = http.StatusOK, `{"model":"x","answers":{}}`
	fake.mu.Unlock()
	s.decide(decideRequest("llm-d4", srv.URL, ""))
	if done := decideDoneOf(t, s); !strings.Contains(done.Error, "answered no questions") {
		t.Fatalf("empty answers: %+v", done)
	}
}

func TestDecideRefused(t *testing.T) {
	const base = "http://127.0.0.1:1"
	cases := []struct {
		name   string
		config func(*Config)
		req    func(*runnerwire.DecideRequest)
		want   string
	}{
		{"no advisor", func(c *Config) { c.AdvisorProvider = "" }, nil, "advisorProvider is not set"},
		{"a model advisor", func(c *Config) { c.AdvisorProvider, c.AdvisorAPIKey = "anthropic", "sk" }, nil,
			"not a System One endpoint"},
		{"another endpoint", nil, func(r *runnerwire.DecideRequest) { r.BaseURL = "https://evil.example" },
			"does not match this runner's advisor settings"},
		{"another model", nil, func(r *runnerwire.DecideRequest) { r.Model, r.Request.Model = "jev-latest", "jev-latest" },
			"does not match"},
		{"another account", nil, func(r *runnerwire.DecideRequest) { r.AccountID = "u-bob" }, "its owner's advisor only"},
		{"a copilot decision", nil, func(r *runnerwire.DecideRequest) { r.Purpose = runnerwire.LLMPurposeCopilot },
			"advisor only"},
		{"no questions", nil, func(r *runnerwire.DecideRequest) { r.Request.Questions = json.RawMessage(`[]`) },
			"not an object of questions"},
		{"too large", nil, func(r *runnerwire.DecideRequest) {
			r.Request.State = json.RawMessage(`"` + strings.Repeat("x", runnerwire.MaxDecideStateBytes) + `"`)
		}, "too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AdvisorKeyEnv, "")
			cfg := systemOneConfig(base)
			if tc.config != nil {
				tc.config(cfg)
			}
			c := &Client{Config: cfg}
			s := c.newLLMSession(context.Background(), "u-ann")
			defer s.stop()
			req := decideRequest("llm-r", base, "")
			if tc.req != nil {
				tc.req(req)
			}
			s.decide(req)
			if done := decideDoneOf(t, s); !strings.Contains(done.Error, tc.want) || done.Response != nil {
				t.Fatalf("done = %+v, want %q", done, tc.want)
			}
		})
	}
}

// A System One advisor defaults to Laya's address, takes no key, says
// "decide" in the hello (not llm-advisor), and refuses model calls.
func TestSystemOneAdvisorSettings(t *testing.T) {
	t.Setenv(AdvisorKeyEnv, "")
	cfg := &Config{}
	if err := cfg.Set("advisorProvider", []string{"systemone"}); err != nil {
		t.Fatal(err)
	}
	info, key, reason := cfg.AdvisorLLM()
	if reason != "" || key != "" || info.Provider != runnerwire.ProviderSystemOne ||
		info.BaseURL != runnerwire.DefaultSystemOneBaseURL || info.Model != "" {
		t.Fatalf("AdvisorLLM = %+v %q %q", info, key, reason)
	}
	caps := capabilities(cfg)
	if !slices.Contains(caps, runnerwire.CapDecide) || slices.Contains(caps, runnerwire.CapLLMAdvisor) {
		t.Fatalf("capabilities = %q", caps)
	}
	if err := cfg.Set("advisorBaseURL", []string{"http://example.com"}); err == nil {
		t.Fatal("plain http to a public host accepted")
	}
	if err := cfg.Set("copilotProvider", []string{"systemone"}); err == nil {
		t.Fatal("systemone accepted for the copilot")
	}

	c := &Client{Config: cfg}
	s := c.newLLMSession(context.Background(), "u-ann")
	defer s.stop()
	req := advisorRequest("llm-m")
	req.Provider, req.Model, req.BaseURL = runnerwire.ProviderSystemOne, "", runnerwire.DefaultSystemOneBaseURL
	s.request(req)
	if done := doneOf(t, s); !strings.Contains(done.Error, "answers decisions, not model calls") {
		t.Fatalf("model call to a System One advisor: %+v", done)
	}
}
