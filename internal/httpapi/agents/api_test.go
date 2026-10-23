package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// actors are the identities a test request can present via X-Test-Actor.
var actors = map[string]identity.Actor{
	"alice":       {ID: "alice", Name: "Alice", Role: identity.RoleMember},
	"bob":         {ID: "bob", Name: "Bob", Role: identity.RoleMember},
	"alice-agent": {ID: "alice", Name: "Alice", Role: identity.RoleMember, Agent: identity.AgentWorker},
	"bob-agent":   {ID: "bob", Name: "Bob", Role: identity.RoleMember, Agent: identity.AgentWorker},
}

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Stands in for withAuth: the actor rides on the request context.
	router.Use(func(c *gin.Context) {
		actor := actors[c.GetHeader("X-Test-Actor")]
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	New(nil, agents.NewHub(nil, nil)).Register(router.Group("/api"))
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

func heartbeat(t *testing.T, router http.Handler, actor, sessionID, name string) {
	t.Helper()
	body := `{"sessionId":"` + sessionID + `","name":"` + name + `","agent":"claude"}`
	if rec := call(t, router, actor, http.MethodPost, "/api/agents/heartbeat", body); rec.Code != http.StatusOK {
		t.Fatalf("heartbeat %s: %d %s", sessionID, rec.Code, rec.Body)
	}
}

func TestSessionBelongsToItsActor(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")

	// Bob replaying Alice's session ID must not take it over.
	rec := call(t, router, "bob-agent", http.MethodPost, "/api/agents/heartbeat",
		`{"sessionId":"claude:a1","name":"stolen","agent":"claude"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("hijacking heartbeat = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestInboxIsReadableOnlyByOwner(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	heartbeat(t, router, "bob-agent", "claude:b1", "bob-bot")

	rec := call(t, router, "bob-agent", http.MethodPost, "/api/agents/messages",
		`{"fromSession":"bob-bot","to":"alice-bot","message":"secret plan"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}

	// Bob naming Alice's session cannot drain her inbox.
	rec = call(t, router, "bob-agent", http.MethodPost, "/api/agents/inbox", `{"session":"alice-bot"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign inbox = %d, want 403: %s", rec.Code, rec.Body)
	}

	rec = call(t, router, "alice-agent", http.MethodPost, "/api/agents/inbox", `{"session":"alice-bot"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("own inbox: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Messages []agents.Message `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Body != "secret plan" {
		t.Fatalf("inbox = %+v, want the one message", got.Messages)
	}
}

func TestAgentCannotAnswerAsk(t *testing.T) {
	router := testRouter(t)
	rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/asks/whatever/answer",
		`{"decision":"approve"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent answering ask = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestAgentCannotBroadcast(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/messages",
		`{"fromSession":"alice-bot","to":"*","message":"hi all"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent broadcast = %d, want 403: %s", rec.Code, rec.Body)
	}
}
