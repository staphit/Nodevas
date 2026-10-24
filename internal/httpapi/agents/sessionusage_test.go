package agents

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/db/dbtest"
	"nodevas/internal/usage"
)

func TestSessionUsageNeedsAStore(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/sessions/claude:a1/usage",
		`{"models":[{"model":"claude-opus-5-5","inputTokens":1}]}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("usage without a store = %d %s", rec.Code, rec.Body)
	}
}

func TestSessionUsageBelongsToItsActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		actor := actors[c.GetHeader("X-Test-Actor")]
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	api := New(nil, agents.NewHub(nil, nil))
	var notified []string
	api.UseUsage(usage.NewStore(dbtest.Open(t)), func(key string) { notified = append(notified, key) })
	api.Register(router.Group("/api"))

	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	body := `{"models":[{"model":"claude-opus-5-5","inputTokens":1}]}`
	if rec := call(t, router, "bob-agent", http.MethodPost, "/api/agents/sessions/claude:a1/usage", body); rec.Code != http.StatusForbidden {
		t.Fatalf("bob reporting alice's usage = %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/sessions/claude:a1/usage", body); rec.Code != http.StatusOK {
		t.Fatalf("alice's usage = %d %s", rec.Code, rec.Body)
	}
	if len(notified) != 1 {
		t.Fatalf("notified %v, want one report", notified)
	}
	if rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/sessions/claude:a1/usage",
		`{"models":[{"model":"m","costUsd":-1}]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative cost = %d %s", rec.Code, rec.Body)
	}
}
