package agents

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// A run's ask_user names no node unless the agent passes one, but the
// runner proxy vouches for the run's node: the ask carries it, so its card
// and Station can point at the node.
func TestARunsAskCarriesTheRunsNode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	runActor := identity.Actor{ID: "alice", Name: "Alice", Role: identity.RoleMember, Agent: identity.AgentWorker,
		RunID: "run-7", RunNodeID: "n-7"}
	router.Use(func(c *gin.Context) {
		actor := actors[c.GetHeader("X-Test-Actor")]
		if c.GetHeader("X-Test-Actor") == "alice-run" {
			actor = runActor
		}
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	api := New(nil, agents.NewHub(nil, nil))
	api.UseRunOf(func(r *http.Request) string { return auth.ActorFrom(r).RunID })
	api.Register(router.Group("/api"))

	for _, c := range []struct{ body, want string }{
		{`{"sessionId":"run-7","sessionName":"app-agy-r7","kind":"question","question":"Which port?","timeoutSeconds":5}`, "n-7"},
		{`{"sessionId":"run-7","sessionName":"app-agy-r7","kind":"question","question":"Which file?","nodeId":"n-other","timeoutSeconds":5}`, "n-other"},
	} {
		asked := make(chan *httptest.ResponseRecorder, 1)
		go func() { asked <- call(t, router, "alice-run", http.MethodPost, "/api/agents/asks", c.body) }()
		var pending agents.Ask
		for range 300 {
			var got struct {
				Asks []agents.Ask `json:"asks"`
			}
			decodeJSON(t, call(t, router, "alice", http.MethodGet, "/api/agents/asks?status=pending", ""), &got)
			if len(got.Asks) == 1 {
				pending = got.Asks[0]
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if pending.ID == "" {
			t.Fatalf("%s never became pending", c.body)
		}
		if pending.NodeID != c.want || pending.RunID != "run-7" {
			t.Fatalf("ask = node %q run %q, want node %q run run-7", pending.NodeID, pending.RunID, c.want)
		}
		if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+pending.ID+"/answer", `{"decision":"deny"}`); rec.Code != http.StatusOK {
			t.Fatalf("answer = %d %s", rec.Code, rec.Body)
		}
		decidedAsk(t, asked)
	}
}
