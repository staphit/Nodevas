package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/copilot"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/db/dbtest"
	"nodevas/internal/identity"
)

var actors = map[string]identity.Actor{
	"alice": {ID: "u-alice", Name: "alice", Role: identity.RoleMember},
	"bob":   {ID: "u-bob", Name: "bob", Role: identity.RoleMember},
	"admin": {ID: "u-admin", Name: "admin", Role: identity.RoleAdmin},
}

type echoProvider struct{}

func (echoProvider) Name() string  { return "fake" }
func (echoProvider) Model() string { return "fake-1" }
func (echoProvider) Stream(_ context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	// One read tool call first, then an answer, so the stream carries every
	// kind of line a simple turn produces.
	lastMessage := req.Messages[len(req.Messages)-1]
	if lastMessage.Blocks[0].Type != provider.BlockToolResult {
		return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{
			{Type: provider.BlockToolCall, ToolCallID: "c1", ToolName: "get_node", Input: json.RawMessage(`{}`)},
		}}, Stop: provider.StopToolUse}, nil
	}
	onDelta(provider.Delta{Text: "Done."})
	return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Blocks: []provider.Block{
		{Type: provider.BlockText, Text: "Done."},
	}}, Stop: provider.StopEndTurn}, nil
}

type readBridge struct{}

func (readBridge) Tools() []toolbridge.Tool {
	return []toolbridge.Tool{{Tool: provider.Tool{Name: "get_node", Schema: json.RawMessage(`{"type":"object"}`)}, Kind: toolbridge.KindRead}}
}
func (readBridge) Call(context.Context, string, json.RawMessage) (toolbridge.Result, error) {
	return toolbridge.Result{Output: "node"}, nil
}
func (readBridge) Close() error { return nil }

type memoryKeys struct{ key []byte }

func (m *memoryKeys) Load() ([]byte, error)   { return m.key, nil }
func (m *memoryKeys) Save(plain []byte) error { m.key = plain; return nil }
func (m *memoryKeys) Clear() error            { m.key = nil; return nil }

func testRouter(t *testing.T, apiKey string) *gin.Engine {
	t.Helper()
	service := copilot.New(copilot.Options{
		Database: dbtest.Open(t),
		Keys:     &memoryKeys{},
		Defaults: copilot.Defaults{Enabled: true, Provider: "anthropic", Model: "m", APIKey: apiKey},
		NewProvider: func(provider.Config) (provider.Provider, error) {
			return echoProvider{}, nil
		},
		OpenBridge: func(context.Context, toolbridge.Options) (toolbridge.Bridge, error) {
			return readBridge{}, nil
		},
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Stands in for withAuth: the actor rides on the request context.
	router.Use(func(c *gin.Context) {
		actor := actors[c.GetHeader("X-Test-Actor")]
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	New(service).Register(router.Group("/api"))
	return router
}

func call(t *testing.T, router http.Handler, actor, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", actor)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func createConversation(t *testing.T, router http.Handler, actor string) copilot.Conversation {
	t.Helper()
	response := call(t, router, actor, http.MethodPost, "/api/copilot/conversations", `{"project":"demo","mode":"default"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body)
	}
	var body struct {
		Conversation copilot.Conversation `json:"conversation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Conversation.ID == "" {
		t.Fatalf("create body = %s (%v)", response.Body, err)
	}
	return body.Conversation
}

func TestAnotherAccountGets404Everywhere(t *testing.T) {
	router := testRouter(t, "sk")
	conversation := createConversation(t, router, "alice")
	base := "/api/copilot/conversations/" + conversation.ID
	for _, request := range []struct{ method, target, body string }{
		{http.MethodGet, base, ""},
		{http.MethodPatch, base, `{"title":"mine now"}`},
		{http.MethodDelete, base, ""},
		{http.MethodPost, base + "/turns", `{"text":"hi"}`},
		{http.MethodPost, base + "/turns/t1/decisions", `{"requestId":"r","decision":"allow"}`},
		{http.MethodPost, base + "/turns/t1/client-results", `{"callId":"c","output":"x"}`},
		{http.MethodPost, base + "/turns/t1/cancel", `{}`},
	} {
		response := call(t, router, "bob", request.method, request.target, request.body)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s by another account = %d %s", request.method, request.target, response.Code, response.Body)
		}
	}
	listed := call(t, router, "bob", http.MethodGet, "/api/copilot/conversations?project=demo", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), conversation.ID) {
		t.Fatalf("bob's list = %d %s", listed.Code, listed.Body)
	}
	// Still there for its owner.
	if response := call(t, router, "alice", http.MethodGet, base, ""); response.Code != http.StatusOK {
		t.Fatalf("owner read = %d %s", response.Code, response.Body)
	}
}

func TestTurnStreamsNDJSONInOrder(t *testing.T) {
	router := testRouter(t, "sk")
	conversation := createConversation(t, router, "alice")
	response := call(t, router, "alice", http.MethodPost,
		"/api/copilot/conversations/"+conversation.ID+"/turns",
		`{"text":"打開登入任務","source":"voice","mode":"default","context":{"selectedNodeIds":["n1"],"view":"graph","language":"zh-TW"},"clientTools":["ui_open_node"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("turn = %d %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/x-ndjson") {
		t.Fatalf("Content-Type = %q", got)
	}
	var kinds []string
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("line %q: %v", scanner.Text(), err)
		}
		kinds = append(kinds, event["type"].(string))
	}
	want := "turn_started,entry,entry,tool_call,tool_result,entry,text_delta,entry,turn_finished"
	if got := strings.Join(kinds, ","); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}

	read := call(t, router, "alice", http.MethodGet, "/api/copilot/conversations/"+conversation.ID, "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"title":"打開登入任務"`) ||
		strings.Contains(read.Body.String(), `"message"`) {
		t.Fatalf("transcript = %s", read.Body)
	}
}

func TestTurnRefusedBeforeTheStreamIsPlainJSON(t *testing.T) {
	router := testRouter(t, "")
	conversation := createConversation(t, router, "alice")
	response := call(t, router, "alice", http.MethodPost,
		"/api/copilot/conversations/"+conversation.ID+"/turns", `{"text":"hi"}`)
	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "API key") ||
		strings.HasPrefix(response.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("turn without a key = %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body)
	}
	status := call(t, router, "alice", http.MethodGet, "/api/copilot/status", "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"enabled":false`) {
		t.Fatalf("status = %d %s", status.Code, status.Body)
	}
}

func TestSettingsAreAdminOnlyAndTheKeyIsWriteOnly(t *testing.T) {
	router := testRouter(t, "")
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		if response := call(t, router, "alice", method, "/api/copilot/settings", `{"model":"x"}`); response.Code != http.StatusForbidden {
			t.Fatalf("member %s settings = %d", method, response.Code)
		}
	}
	response := call(t, router, "admin", http.MethodPut, "/api/copilot/settings",
		`{"provider":"openai","model":"gpt-test","baseUrl":"https://llm.example/v1","effort":"medium","allowBypass":true,"apiKey":"sk-secret"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("admin put = %d %s", response.Code, response.Body)
	}
	read := call(t, router, "admin", http.MethodGet, "/api/copilot/settings", "")
	body := read.Body.String()
	if read.Code != http.StatusOK || strings.Contains(body, "sk-secret") ||
		!strings.Contains(body, `"hasApiKey":true`) || !strings.Contains(body, `"model":"gpt-test"`) {
		t.Fatalf("admin get = %d %s", read.Code, body)
	}
	if bad := call(t, router, "admin", http.MethodPut, "/api/copilot/settings", `{"provider":"gemini"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider = %d", bad.Code)
	}
	// Anyone signed in sees the effect, without the key.
	status := call(t, router, "alice", http.MethodGet, "/api/copilot/status", "")
	if !strings.Contains(status.Body.String(), `"enabled":true`) || !strings.Contains(status.Body.String(), `"allowBypass":true`) {
		t.Fatalf("status after settings = %s", status.Body)
	}
}
