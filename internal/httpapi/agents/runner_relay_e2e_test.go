package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nodevas/internal/agents"
	"nodevas/internal/runner/adapter"
)

// The runner's own HubAsker (agy gate, OpenCode ACP) against the real ask
// routes: the body it sends is one the hub accepts, and allow-for-this-run
// comes back as ForRun.
func TestRunnerHubAskerAgainstTheHub(t *testing.T) {
	router := runRouter(t)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Actor") == "" {
			r.Header.Set("X-Test-Actor", "alice-agent")
		}
		router.ServeHTTP(w, r)
	}))
	defer hub.Close()

	asker := adapter.HubAsker{Server: hub.URL, Actor: "run:agy", Agent: "agy", SessionName: "app-agy-r7", Wait: 10 * time.Second}
	decided := make(chan adapter.PermissionDecision, 1)
	go func() {
		decided <- asker.AskPermission(context.Background(), adapter.PermissionRequest{Tool: "run_command", Input: `{"command":"go test ./..."}`})
	}()

	var pending agents.Ask
	for range 300 {
		rec := call(t, router, "alice", http.MethodGet, "/api/agents/asks?status=pending", "")
		var got struct {
			Asks []agents.Ask `json:"asks"`
		}
		decodeJSON(t, rec, &got)
		if len(got.Asks) == 1 {
			pending = got.Asks[0]
			break
		}
		select {
		case d := <-decided:
			t.Fatalf("the relay returned before anyone answered: %+v", d)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.ID == "" {
		t.Fatal("the relay's ask never became pending")
	}
	if pending.Kind != "permission" || pending.Tool != "run_command" || pending.RunID != "run-7" {
		t.Fatalf("pending ask = %+v", pending)
	}
	if rec := call(t, router, "root", http.MethodPost, "/api/agents/asks/"+pending.ID+"/answer", `{"decision":"approve_run"}`); rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	select {
	case d := <-decided:
		if !d.Allow || !d.ForRun {
			t.Fatalf("decision = %+v, want allow for the run", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the relay never heard the answer")
	}
}
