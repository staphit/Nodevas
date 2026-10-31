package runners_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/copilot"
	"nodevas/internal/copilot/toolbridge"
	runnersapi "nodevas/internal/httpapi/runners"
	"nodevas/internal/identity"
	"nodevas/internal/runner"
	"nodevas/internal/runners"
	"nodevas/internal/testpg"
)

var ann = identity.Actor{ID: "u-ann", Name: "ann", Role: identity.RoleMember}
var bob = identity.Actor{ID: "u-bob", Name: "bob", Role: identity.RoleMember}

// noCommands is a machine with no agent CLIs.
type noCommands struct{}

func (noCommands) LookPath(string) (string, error) { return "", errors.New("not found") }
func (noCommands) Run(context.Context, string, ...string) (string, error) {
	return "", errors.New("not found")
}

type emptyBridge struct{}

func (emptyBridge) Tools() []toolbridge.Tool { return nil }
func (emptyBridge) Call(context.Context, string, json.RawMessage) (toolbridge.Result, error) {
	return toolbridge.Result{}, errors.New("no tools")
}
func (emptyBridge) Close() error { return nil }

// fakeOpenAI is an OpenAI-compatible endpoint that answers every call with
// one streamed sentence and records what it was sent.
type fakeOpenAI struct {
	mu     sync.Mutex
	auths  []string
	bodies []string
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.bodies = append(f.bodies, string(body))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, chunk := range []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":" from your PC."},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`,
	} {
		_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		w.(http.Flusher).Flush()
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// hub serves the runner socket the way the server does: a runner token in,
// the runner it names in the request context.
func hub(t *testing.T, reg *runners.Registry, relay *runners.LLMRelay) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api", func(c *gin.Context) {
		r, err := reg.Authenticate(c.Request.Context(), runners.BearerToken(c.Request))
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Request = c.Request.WithContext(runners.WithRunner(c.Request.Context(), r))
	})
	runnersapi.New(reg, nil).WithLLM(relay).Register(api)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv
}

// TestCopilotTurnRunsOnTheOwnersRunner is the whole path: a copilot turn on
// a hub that holds no model key goes to the speaker's runner, which calls the
// provider with its own key and streams the answer back into the turn.
func TestCopilotTurnRunsOnTheOwnersRunner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database := testpg.New(t)
	reg := runners.NewRegistry(database, nil)
	relay := runners.NewLLMRelay(reg)
	relay.OfflineCheck = 50 * time.Millisecond
	srv := hub(t, reg, relay)

	vendor := &fakeOpenAI{}
	vendorSrv := httptest.NewServer(vendor)
	t.Cleanup(vendorSrv.Close)

	_, token, err := reg.Create(ctx, ann, "ann's PC")
	if err != nil {
		t.Fatal(err)
	}
	client := &runner.Client{
		Config: &runner.Config{
			Hub: srv.URL, Token: token,
			CopilotProvider: "openai", CopilotModel: "gpt-test",
			CopilotBaseURL: vendorSrv.URL + "/v1", CopilotAPIKey: "sk-on-the-pc",
		},
		Commands:     noCommands{},
		PingInterval: time.Second,
	}
	runCtx, stopRunner := context.WithCancel(ctx)
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- client.Run(runCtx) }()
	defer func() {
		stopRunner()
		<-runnerDone
	}()

	// The runner's hello says it makes copilot calls, and with what.
	deadline := time.Now().Add(10 * time.Second)
	var target struct{ provider, model string }
	for {
		r, err := relay.Target(ctx, ann.ID)
		if err == nil {
			target.provider, target.model = r.Info.LLM.Provider, r.Info.LLM.Model
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runner never offered copilot calls: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if target.provider != "openai" || target.model != "gpt-test" {
		t.Fatalf("target = %+v", target)
	}
	// Another account has no runner for its copilot, and never gets ann's.
	if _, err := relay.Target(ctx, bob.ID); !errors.Is(err, runners.ErrNoLLMRunner) {
		t.Fatalf("bob's target = %v", err)
	}

	service := copilot.New(copilot.Options{
		Database: database,
		Defaults: copilot.Defaults{Enabled: true, Provider: "anthropic", Model: "claude-test"},
		OpenBridge: func(context.Context, toolbridge.Options) (toolbridge.Bridge, error) {
			return emptyBridge{}, nil
		},
		Remote: relay,
	})
	status, err := service.Status(ctx, ann, "")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.KeySource != copilot.KeySourceRunner || status.Runner != "ann's PC" ||
		status.Provider != "openai" || status.Model != "gpt-test" {
		t.Fatalf("ann's status = %+v", status)
	}
	if status, _ := service.Status(ctx, bob, ""); status.Enabled || !status.NeedsRunner || !strings.Contains(status.Reason, "nodevas runner") {
		t.Fatalf("bob's status = %+v", status)
	}

	conversation, err := service.CreateConversation(ctx, ann, "demo", copilot.ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var deltas []string
	var finished, failed string
	err = service.Turn(ctx, copilot.TurnRequest{Actor: ann, ConversationID: conversation.ID, Text: "hi"},
		func(event copilot.Event) {
			mu.Lock()
			defer mu.Unlock()
			switch event.Type {
			case copilot.EventTextDelta:
				deltas = append(deltas, event.Text)
			case copilot.EventTurnFinished:
				finished = event.Stop
			case copilot.EventError:
				failed = event.Message
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if failed != "" || finished != "end_turn" || strings.Join(deltas, "") != "Hello from your PC." {
		t.Fatalf("turn: deltas %q, finished %q, error %q", deltas, finished, failed)
	}

	vendor.mu.Lock()
	defer vendor.mu.Unlock()
	if len(vendor.auths) != 1 || vendor.auths[0] != "Bearer sk-on-the-pc" {
		t.Fatalf("the provider saw %q", vendor.auths)
	}
	if !strings.Contains(vendor.bodies[0], `"gpt-test"`) || !strings.Contains(vendor.bodies[0], "hi") {
		t.Fatalf("the provider was asked %s", vendor.bodies[0])
	}
}
