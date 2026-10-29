package agents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
)

// runRouter is testRouter whose transport vouches every request comes from
// run-7, as the runner proxy does.
func runRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		actor := actors[c.GetHeader("X-Test-Actor")]
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	api := New(nil, agents.NewHub(nil, nil))
	api.UseRunOf(func(*http.Request) string { return "run-7" })
	api.Register(router.Group("/api"))
	return router
}

// askOverHTTP posts body as alice's agent and returns the pending ask, with
// a channel for the long poll's response.
func askOverHTTP(t *testing.T, router http.Handler, body string) (agents.Ask, chan *httptest.ResponseRecorder) {
	t.Helper()
	asked := make(chan *httptest.ResponseRecorder, 1)
	go func() { asked <- call(t, router, "alice-agent", http.MethodPost, "/api/agents/asks", body) }()
	for range 300 {
		rec := call(t, router, "alice", http.MethodGet, "/api/agents/asks?status=pending", "")
		var got struct {
			Asks []agents.Ask `json:"asks"`
		}
		decodeJSON(t, rec, &got)
		if len(got.Asks) == 1 {
			return got.Asks[0], asked
		}
		select {
		case rec := <-asked:
			t.Fatalf("the ask %s returned at once: %d %s", body, rec.Code, rec.Body)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the ask %s never became pending", body)
	return agents.Ask{}, nil
}

func decidedAsk(t *testing.T, asked chan *httptest.ResponseRecorder) agents.Ask {
	t.Helper()
	select {
	case rec := <-asked:
		if rec.Code != http.StatusOK {
			t.Fatalf("ask = %d: %s", rec.Code, rec.Body)
		}
		var got struct {
			Ask agents.Ask `json:"ask"`
		}
		decodeJSON(t, rec, &got)
		return got.Ask
	case <-time.After(5 * time.Second):
		t.Fatal("the ask's long poll did not return")
		return agents.Ask{}
	}
}

func TestQuestionAndChoiceOverHTTP(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")

	ask, asked := askOverHTTP(t, router,
		`{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"question","question":"Which port?","timeoutSeconds":5}`)
	if ask.Kind != agents.AskKindQuestion {
		t.Fatalf("listed = %+v", ask)
	}
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer", `{"decision":"answer"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty answer = %d, want 400: %s", rec.Code, rec.Body)
	}
	// An answer may run to MaxAskAnswerRunes characters of any script.
	long := strings.Repeat("埠", agents.MaxAskAnswerRunes)
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer",
		`{"decision":"answer","answer":"`+long+`","note":"n"}`); rec.Code != http.StatusOK {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body)
	}
	if got := decidedAsk(t, asked); got.Status != agents.AskAnswered || got.Answer != long {
		t.Fatalf("answered = %+v", got.Status)
	}

	choice, asked := askOverHTTP(t, router,
		`{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"choice","question":"Which DB?","options":["Postgres","MySQL"],"timeoutSeconds":5}`)
	if len(choice.Options) != 2 {
		t.Fatalf("listed choice = %+v", choice)
	}
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+choice.ID+"/answer", `{"decision":"answer","answer":"Oracle"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("an answer outside the options = %d, want 400", rec.Code)
	}
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+choice.ID+"/answer", `{"decision":"answer","answer":"Postgres"}`); rec.Code != http.StatusOK {
		t.Fatalf("choose = %d: %s", rec.Code, rec.Body)
	}
	if got := decidedAsk(t, asked); got.Status != agents.AskAnswered || got.Answer != "Postgres" {
		t.Fatalf("chosen = %+v", got)
	}

	if rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/asks",
		`{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"choice","question":"One?","options":["only"],"timeoutSeconds":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a one-option choice = %d, want 400", rec.Code)
	}
}

// A permission prompt's run is the transport's (the body has no say); it waits
// five minutes at most; allowing it for the run allows the next one.
func TestPermissionAskOverHTTP(t *testing.T) {
	router := runRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	body := `{"sessionId":"claude:a1","sessionName":"alice-bot","kind":"permission","tool":"Bash",` +
		`"toolInput":"{\"command\":\"go test ./...\"}","timeoutSeconds":3600}`
	ask, asked := askOverHTTP(t, router, body)
	if ask.Kind != agents.AskKindPermission || ask.Tool != "Bash" || ask.RunID != "run-7" ||
		!strings.Contains(ask.ToolInput, "go test") {
		t.Fatalf("listed permission ask = %+v", ask)
	}
	if got := time.Duration(ask.ExpiresAt-ask.CreatedAt) * time.Millisecond; got != agents.PermissionAskTimeout {
		t.Fatalf("waits %v, want %v", got, agents.PermissionAskTimeout)
	}
	if rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer", `{"decision":"approve_run"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("an agent allowing a tool = %d, want 403", rec.Code)
	}
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer", `{"decision":"approve_run"}`); rec.Code != http.StatusOK {
		t.Fatalf("approve_run = %d: %s", rec.Code, rec.Body)
	}
	if got := decidedAsk(t, asked); got.Status != agents.AskApproved || got.Answer != agents.DecisionApproveRun ||
		got.Decision != agents.DecisionApproveRun {
		t.Fatalf("allowed for the run = %+v", got)
	}
	rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/asks", body)
	var again struct {
		Ask agents.Ask `json:"ask"`
	}
	decodeJSON(t, rec, &again)
	if rec.Code != http.StatusOK || again.Ask.Status != agents.AskApproved || again.Ask.Note != agents.AllowedForRunNote {
		t.Fatalf("the next Bash prompt = %d %+v", rec.Code, again.Ask)
	}
}

// The runner's own relays (agy gate, OpenCode ACP) ask as "run:<agent>" with
// timeoutSeconds 300, and tell allow-for-this-run by ask.decision.
func TestRunnerRelayPermissionAsk(t *testing.T) {
	router := runRouter(t)
	ask, asked := askOverHTTP(t, router, `{"sessionId":"run-7","sessionName":"app-agy-r7","actor":"run:agy","agent":"agy",`+
		`"question":"Allow run_command?","kind":"permission","tool":"run_command","toolInput":"ls","timeoutSeconds":300}`)
	if got := time.Duration(ask.ExpiresAt-ask.CreatedAt) * time.Millisecond; got != 300*time.Second || ask.RunID != "run-7" {
		t.Fatalf("relay ask = %+v (waits %v)", ask, got)
	}
	rec := call(t, router, "root", http.MethodPost, "/api/agents/asks/"+ask.ID+"/answer", `{"decision":"approve_run"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"decision":"approve_run"`) {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	select {
	case rec := <-asked:
		if !strings.Contains(rec.Body.String(), `"decision":"approve_run"`) {
			t.Fatalf("the relay's long poll = %s", rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay's long poll did not return")
	}
}
