package agents

import (
	"net/http"
	"strings"
	"testing"

	"nodevas/internal/agents"
)

// A relay that cut the tool input says so with toolInputTruncated; the hub
// keeps the flag, ORs in its own cut, and allows such a prompt once only.
func TestCutToolInputOverHTTP(t *testing.T) {
	router := runRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	for name, body := range map[string]string{
		"relay cut": `{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"permission","tool":"Bash",` +
			`"toolInput":"{\"command\":\"echo hi\"}","toolInputTruncated":true,"timeoutSeconds":60}`,
		"hub cut": `{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"permission","tool":"Write",` +
			`"toolInput":"` + strings.Repeat("y", agents.MaxAskToolInputBytes+10) + `","toolInputTruncated":false,"timeoutSeconds":60}`,
	} {
		ask, asked := askOverHTTP(t, router, body)
		if !ask.ToolInputTruncated || ask.RunScope() != agents.RunScopeOnce {
			t.Fatalf("%s: listed ask = %+v (scope %q)", name, ask, ask.RunScope())
		}
		rec := call(t, router, "alice", http.MethodGet, "/api/agents/asks?status=pending", "")
		if !strings.Contains(rec.Body.String(), `"toolInputTruncated":true`) || !strings.Contains(rec.Body.String(), `"runScope":"once"`) {
			t.Fatalf("%s: listing %s", name, rec.Body)
		}
		if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer", `{"decision":"approve_run"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: approve_run = %d: %s", name, rec.Code, rec.Body)
		}
		if got := decidedAsk(t, asked); got.Status != agents.AskApproved || !got.ToolInputTruncated {
			t.Fatalf("%s: decided = %+v", name, got)
		}
		// Asked again, not allowed from a cache.
		again, asked := askOverHTTP(t, router, body)
		if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+again.ID+"/answer", `{"decision":"deny"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: deny = %d: %s", name, rec.Code, rec.Body)
		}
		decidedAsk(t, asked)
	}
}
