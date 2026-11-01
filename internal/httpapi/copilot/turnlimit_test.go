package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/copilot"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/testpg"
)

// heldProvider answers once release closes, telling entered when a turn
// reaches it.
type heldProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (heldProvider) Name() string  { return "fake" }
func (heldProvider) Model() string { return "fake-1" }
func (p heldProvider) Stream(ctx context.Context, _ provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	p.entered <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return provider.Response{}, ctx.Err()
	}
	onDelta(provider.Delta{Text: "Done."})
	return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{
		{Type: provider.BlockText, Text: "Done."},
	}}, Stop: provider.StopEndTurn}, nil
}

// An account that already runs its share of turns on a shared server's key
// is told plainly it is busy (429 copilot_busy), before any stream starts,
// not handed a server error.
func TestTurnOverTheServerKeyCapIsBusy(t *testing.T) {
	model := heldProvider{entered: make(chan struct{}, 8), release: make(chan struct{})}
	service := copilot.New(copilot.Options{
		Database: testpg.New(t),
		Keys:     &memoryKeys{},
		Defaults: copilot.Defaults{Enabled: true, Provider: "anthropic", Model: "m", APIKey: "sk-test"},
		NewProvider: func(provider.Config) (provider.Provider, error) {
			return model, nil
		},
		OpenBridge: func(context.Context, toolbridge.Options) (toolbridge.Bridge, error) {
			return readBridge{}, nil
		},
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		actor := actors[c.GetHeader("X-Test-Actor")]
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	New(service).Register(router.Group("/api"))

	var held []chan *httptest.ResponseRecorder
	for range 2 {
		conversation := createConversation(t, router, "alice")
		done := make(chan *httptest.ResponseRecorder, 1)
		held = append(held, done)
		go func() {
			request := httptest.NewRequest(http.MethodPost, "/api/copilot/conversations/"+conversation.ID+"/turns", strings.NewReader(`{"text":"hi"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Test-Actor", "alice")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			done <- response
		}()
		select {
		case <-model.entered:
		case response := <-done:
			t.Fatalf("held turn = %d %s, want it running", response.Code, response.Body)
		case <-time.After(30 * time.Second):
			t.Fatal("held turn never reached the model")
		}
	}

	conversation := createConversation(t, router, "alice")
	response := call(t, router, "alice", http.MethodPost,
		"/api/copilot/conversations/"+conversation.ID+"/turns", `{"text":"hi"}`)
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusTooManyRequests || body.Code != "copilot_busy" ||
		strings.HasPrefix(response.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("third turn = %d %q %s, want 429 copilot_busy as plain JSON",
			response.Code, response.Header().Get("Content-Type"), response.Body)
	}

	close(model.release)
	for _, done := range held {
		if response := <-done; response.Code != http.StatusOK {
			t.Fatalf("held turn = %d %s", response.Code, response.Body)
		}
	}
}
