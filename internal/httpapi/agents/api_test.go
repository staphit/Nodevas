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
	"carol":       {ID: "carol", Name: "Carol", Role: identity.RoleMember},
	"root":        {ID: "root", Name: "Root", Role: identity.RoleAdmin},
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

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
}

func TestInboundRouteIsHumanAndOwnerOnly(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	const route = "/api/agents/sessions/claude:a1/inbound"

	if rec := call(t, router, "alice-agent", http.MethodPost, route, `{"inbound":"hold"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("agent setting inbound = %d, want 403: %s", rec.Code, rec.Body)
	}
	if rec := call(t, router, "bob", http.MethodPost, route, `{"inbound":"hold"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("another member = %d, want 404: %s", rec.Code, rec.Body)
	}
	if rec := call(t, router, "alice", http.MethodPost, route, `{"inbound":"shut"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad policy = %d, want 400: %s", rec.Code, rec.Body)
	}
	rec := call(t, router, "alice", http.MethodPost, route, `{"inbound":"hold"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner = %d: %s", rec.Code, rec.Body)
	}
	var session agents.Session
	decodeJSON(t, rec, &session)
	if session.Inbound != "hold" || session.ID != "claude:a1" {
		t.Fatalf("session = %+v", session)
	}
	if rec := call(t, router, "root", http.MethodPost, route, `{"inbound":"open"}`); rec.Code != http.StatusOK {
		t.Fatalf("admin = %d: %s", rec.Code, rec.Body)
	}
}

func TestHeldMessagesOverHTTP(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	heartbeat(t, router, "bob-agent", "claude:b1", "bob-bot")
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/sessions/claude:a1/inbound", `{"inbound":"hold"}`); rec.Code != http.StatusOK {
		t.Fatalf("hold: %d %s", rec.Code, rec.Body)
	}

	rec := call(t, router, "bob-agent", http.MethodPost, "/api/agents/messages",
		`{"fromSession":"bob-bot","to":"alice-bot","message":"deploy?","notify":true,"files":[{"name":"plan.txt","content":"step 1"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var sent struct {
		Deliveries []agents.DeliveryStatus `json:"deliveries"`
	}
	decodeJSON(t, rec, &sent)
	if len(sent.Deliveries) != 1 || sent.Deliveries[0].Status != "held" {
		t.Fatalf("deliveries = %+v, want held", sent.Deliveries)
	}
	id := sent.Deliveries[0].MessageID

	var inbox struct {
		Messages []agents.Message `json:"messages"`
	}
	decodeJSON(t, call(t, router, "alice-agent", http.MethodPost, "/api/agents/inbox", `{"session":"alice-bot"}`), &inbox)
	if len(inbox.Messages) != 0 {
		t.Fatalf("held message in the inbox: %+v", inbox.Messages)
	}

	// Agents see neither the held queue nor the message in the full list.
	if rec := call(t, router, "alice-agent", http.MethodGet, "/api/agents/messages?status=held", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("agent listing held = %d, want 403", rec.Code)
	}
	var listed struct {
		Messages []agents.Message `json:"messages"`
	}
	decodeJSON(t, call(t, router, "alice-agent", http.MethodGet, "/api/agents/messages", ""), &listed)
	if len(listed.Messages) != 0 {
		t.Fatalf("agent's message list shows held mail: %+v", listed.Messages)
	}
	// The recipient agent cannot fetch the file while it is held.
	if rec := call(t, router, "alice-agent", http.MethodGet, "/api/agents/messages/"+id+"/files/plan.txt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("agent fetching a held file = %d, want 404", rec.Code)
	}

	// The full list carries held mail, with its status, to those it concerns.
	for actor, want := range map[string]int{"alice": 1, "bob": 1, "root": 1, "carol": 0} {
		decodeJSON(t, call(t, router, actor, http.MethodGet, "/api/agents/messages", ""), &listed)
		if len(listed.Messages) != want || (want == 1 && listed.Messages[0].Status != "held") {
			t.Fatalf("%s's message list = %+v, want %d held", actor, listed.Messages, want)
		}
	}

	decodeJSON(t, call(t, router, "alice", http.MethodGet, "/api/agents/messages?status=held", ""), &listed)
	if len(listed.Messages) != 1 || listed.Messages[0].Status != "held" || !listed.Messages[0].Notify ||
		len(listed.Messages[0].Files) != 1 || listed.Messages[0].Files[0].Name != "plan.txt" {
		t.Fatalf("alice's held queue = %+v", listed.Messages)
	}
	decodeJSON(t, call(t, router, "bob", http.MethodGet, "/api/agents/messages?status=held", ""), &listed)
	if len(listed.Messages) != 0 {
		t.Fatalf("bob sees alice's held queue: %+v", listed.Messages)
	}

	for _, who := range []string{"alice-agent", "bob-agent"} {
		if rec := call(t, router, who, http.MethodPost, "/api/agents/messages/"+id+"/approve", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s approving = %d, want 403", who, rec.Code)
		}
	}
	if rec := call(t, router, "bob", http.MethodPost, "/api/agents/messages/"+id+"/approve", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob approving alice's mail = %d, want 404", rec.Code)
	}
	rec = call(t, router, "alice", http.MethodPost, "/api/agents/messages/"+id+"/approve", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	var approved map[string]any
	decodeJSON(t, rec, &approved)
	if approved["status"] != "delivered" || approved["decidedBy"] != "alice" || approved["decidedAt"] == nil {
		t.Fatalf("approved = %v", approved)
	}
	if rec := call(t, router, "alice", http.MethodPost, "/api/agents/messages/"+id+"/reject", ""); rec.Code != http.StatusConflict {
		t.Fatalf("rejecting a delivered message = %d, want 409", rec.Code)
	}

	decodeJSON(t, call(t, router, "alice-agent", http.MethodPost, "/api/agents/inbox", `{"session":"alice-bot"}`), &inbox)
	if len(inbox.Messages) != 1 || inbox.Messages[0].ID != id {
		t.Fatalf("inbox after approval = %+v", inbox.Messages)
	}
}

func TestMessageFileDownload(t *testing.T) {
	router := testRouter(t)
	heartbeat(t, router, "alice-agent", "claude:a1", "alice-bot")
	rec := call(t, router, "bob-agent", http.MethodPost, "/api/agents/messages",
		`{"fromSession":"bob-bot","to":"alice-bot","message":"page","files":[{"name":"page.html","content":"<script>alert(1)</script>"}]}`)
	var sent struct {
		Deliveries []agents.DeliveryStatus `json:"deliveries"`
	}
	decodeJSON(t, rec, &sent)
	id := sent.Deliveries[0].MessageID
	target := "/api/agents/messages/" + id + "/files/page.html"

	rec = call(t, router, "alice-agent", http.MethodGet, target, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "<script>alert(1)</script>" {
		t.Fatalf("recipient download: %d %s", rec.Code, rec.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":           "text/plain; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    `attachment; filename=page.html`,
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	for actor, want := range map[string]int{"bob": http.StatusOK, "root": http.StatusOK, "carol": http.StatusNotFound} {
		if rec := call(t, router, actor, http.MethodGet, target, ""); rec.Code != want {
			t.Errorf("%s download = %d, want %d", actor, rec.Code, want)
		}
	}
	if rec := call(t, router, "alice-agent", http.MethodGet, "/api/agents/messages/"+id+"/files/other.txt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing file = %d", rec.Code)
	}
}

func TestMessageFileLimitsOverHTTP(t *testing.T) {
	router := testRouter(t)
	big := strings.Repeat("a", agents.MaxFileBytes+1)
	for name, body := range map[string]string{
		"path name": `{"to":"x","message":"m","files":[{"name":"../x","content":"a"}]}`,
		"dot name":  `{"to":"x","message":"m","files":[{"name":".bashrc","content":"a"}]}`,
		"too large": `{"to":"x","message":"m","files":[{"name":"a.txt","content":"` + big + `"}]}`,
	} {
		rec := call(t, router, "alice", http.MethodPost, "/api/agents/messages", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_ARGUMENT") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// Ten files at the limit fit in one request.
	files := make([]string, agents.MaxFiles)
	full := strings.Repeat("a", agents.MaxFileBytes)
	for i := range files {
		files[i] = `{"name":"f` + strings.Repeat("x", i) + `.txt","content":"` + full + `"}`
	}
	rec := call(t, router, "alice", http.MethodPost, "/api/agents/messages",
		`{"to":"x","message":"m","files":[`+strings.Join(files, ",")+`]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ten full files: %d %s", rec.Code, rec.Body)
	}
}

func TestHeartbeatReportsModel(t *testing.T) {
	router := testRouter(t)
	rec := call(t, router, "alice-agent", http.MethodPost, "/api/agents/heartbeat",
		`{"sessionId":"s1","name":"m","model":"claude-sonnet-4-5"}`)
	var got struct {
		Session agents.Session `json:"session"`
	}
	decodeJSON(t, rec, &got)
	if got.Session.Model != "claude-sonnet-4-5" || got.Session.Inbound != "open" {
		t.Fatalf("session = %+v", got.Session)
	}
	rec = call(t, router, "alice-agent", http.MethodPost, "/api/agents/heartbeat",
		`{"sessionId":"s1","model":"`+strings.Repeat("m", 65)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("long model = %d, want 400", rec.Code)
	}
}
