package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

// runnerTestServer is an accounts server with runners enabled. ann is the
// administrator; bea and cal are members.
type runnerTestServer struct {
	server  *Server
	handler http.Handler
	inbox   *mailbox
}

func newRunnerTestServer(t *testing.T) *runnerTestServer {
	t.Helper()
	server, _, inbox, database, users := accountServerPartsForTest(t)
	for _, name := range []string{"bea", "cal"} {
		if err := users.AddWithEmail(context.Background(), name, "correct-horse-battery", "", name+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	server.UseRunnersDB(database)
	return &runnerTestServer{server: server, handler: server.Handler(), inbox: inbox}
}

type browser struct {
	cookies []*http.Cookie
	csrf    string
}

func (s *runnerTestServer) signIn(t *testing.T, email string) browser {
	t.Helper()
	cookies, csrf := signIn(t, s.server, s.inbox, email)
	return browser{cookies: cookies, csrf: csrf}
}

func (s *runnerTestServer) do(t *testing.T, b *browser, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if b != nil {
		withCookies(request, b.cookies)
		request.Header.Set(auth.CSRFHeaderName, b.csrf)
	}
	for key, value := range header {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	s.handler.ServeHTTP(response, request)
	return response
}

func (s *runnerTestServer) create(t *testing.T, b browser, name string) runnerwire.CreateResponse {
	t.Helper()
	response := s.do(t, &b, http.MethodPost, "/api/runners", `{"name":"`+name+`"}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body)
	}
	var created runnerwire.CreateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestRunnersAPI(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	cal := s.signIn(t, "cal@example.test")
	ann := s.signIn(t, testEmail)

	created := s.create(t, bea, "bea-pc")
	if !strings.HasPrefix(created.Token, runnerwire.TokenPrefix) || created.Runner.Name != "bea-pc" {
		t.Fatalf("created = %+v", created)
	}
	s.create(t, cal, "cal-pc")

	list := func(b browser) runnerwire.ListResponse {
		response := s.do(t, &b, http.MethodGet, "/api/runners", "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("list status = %d", response.Code)
		}
		if strings.Contains(response.Body.String(), created.Token) {
			t.Fatal("the list returned a token")
		}
		var out runnerwire.ListResponse
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := list(bea); len(got.Runners) != 1 || got.Runners[0].ID != created.Runner.ID {
		t.Fatalf("bea sees %+v", got)
	}
	if got := list(ann); len(got.Runners) != 2 {
		t.Fatalf("the administrator sees %+v", got)
	}

	if code := s.do(t, &cal, http.MethodDelete, "/api/runners/"+created.Runner.ID, "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("cal revoking bea's runner: status = %d", code)
	}
	if code := s.do(t, &bea, http.MethodPost, "/api/runners", `{"name":""}`, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d", code)
	}
	if code := s.do(t, nil, http.MethodGet, "/api/runners", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: status = %d", code)
	}

	// A runner token opens the runner socket and nothing else.
	bearer := map[string]string{"Authorization": "Bearer " + created.Token}
	for _, path := range []string{"/api/runners", "/api/graph", "/api/agents"} {
		if code := s.do(t, nil, http.MethodGet, path, "", bearer).Code; code != http.StatusUnauthorized {
			t.Errorf("GET %s with a runner token: status = %d, want 401", path, code)
		}
	}
	// And a browser session cannot pose as a runner.
	if code := s.do(t, &bea, http.MethodGet, runnerwire.ConnectPath, "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("session on the runner socket: status = %d, want 401", code)
	}

	if code := s.do(t, &bea, http.MethodDelete, "/api/runners/"+created.Runner.ID, "", nil).Code; code != http.StatusNoContent {
		t.Fatalf("bea revoking her runner: status = %d", code)
	}
	if got := list(bea); len(got.Runners) != 0 {
		t.Fatalf("after revoke bea sees %+v", got)
	}
	if code := s.do(t, nil, http.MethodGet, runnerwire.ConnectPath, "", bearer).Code; code != http.StatusUnauthorized {
		t.Fatalf("revoked token on the runner socket: status = %d", code)
	}
}

// runnerSocket dials the connect endpoint the way nodevas runner does.
func runnerSocket(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(base, "http") + runnerwire.ConnectPath
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func sendEnvelope(t *testing.T, conn *websocket.Conn, env runnerwire.Envelope) {
	t.Helper()
	raw, _ := json.Marshal(env)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readEnvelopeForTest(t *testing.T, conn *websocket.Conn) runnerwire.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env runnerwire.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func helloEnvelope() runnerwire.Envelope {
	return runnerwire.Envelope{Type: runnerwire.TypeHello, Hello: &runnerwire.Info{
		Version: "test", Hostname: "desk", OS: "windows", Arch: "amd64",
		Agents: []runnerwire.Agent{{Kind: "claude", Installed: true, Models: []runnerwire.Model{{ID: "default", Default: true}}}},
	}}
}

func TestRunnerSocket(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	created := s.create(t, bea, "bea-pc")
	live := httptest.NewServer(s.handler)
	t.Cleanup(live.Close)

	online := func() bool {
		response := s.do(t, &bea, http.MethodGet, "/api/runners", "", nil)
		var out runnerwire.ListResponse
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return len(out.Runners) == 1 && out.Runners[0].Online
	}

	first := runnerSocket(t, live.URL, created.Token)
	sendEnvelope(t, first, helloEnvelope())
	welcome := readEnvelopeForTest(t, first)
	if welcome.Type != runnerwire.TypeWelcome || welcome.Welcome == nil || welcome.Welcome.RunnerID != created.Runner.ID {
		t.Fatalf("welcome = %+v", welcome)
	}
	sendEnvelope(t, first, runnerwire.Envelope{Type: runnerwire.TypePing})
	if pong := readEnvelopeForTest(t, first); pong.Type != runnerwire.TypePong {
		t.Fatalf("pong = %+v", pong)
	}
	if !online() {
		t.Fatal("runner not online after hello")
	}

	// A second connection for the same runner replaces the first.
	second := runnerSocket(t, live.URL, created.Token)
	sendEnvelope(t, second, helloEnvelope())
	if env := readEnvelopeForTest(t, second); env.Type != runnerwire.TypeWelcome {
		t.Fatalf("second welcome = %+v", env)
	}
	if bye := readEnvelopeForTest(t, first); bye.Type != runnerwire.TypeBye || bye.Reason != runnerwire.ByeReplaced {
		t.Fatalf("first got %+v, want bye replaced", bye)
	}

	// Revoking says so to the live runner, which then stops.
	if code := s.do(t, &bea, http.MethodDelete, "/api/runners/"+created.Runner.ID, "", nil).Code; code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", code)
	}
	if bye := readEnvelopeForTest(t, second); bye.Type != runnerwire.TypeBye || bye.Reason != runnerwire.ByeRevoked {
		t.Fatalf("second got %+v, want bye revoked", bye)
	}
}

func TestRunnerSocketWantsHelloFirst(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	created := s.create(t, bea, "bea-pc")
	live := httptest.NewServer(s.handler)
	t.Cleanup(live.Close)

	conn := runnerSocket(t, live.URL, created.Token)
	sendEnvelope(t, conn, runnerwire.Envelope{Type: runnerwire.TypePing})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read after a ping-first: %v, want policy violation", err)
	}
}

// On a loopback server everyone is the local user, but the runner socket still
// wants a runner token: a runner is a credential, not a place.
func TestLoopbackRunnerSocketStillNeedsAToken(t *testing.T) {
	pm := projectManagerForTest(t)
	database := dbtestOpenForServer(t, pm.Workspace())
	server := serverForTest(t, pm, nil, nil)
	server.UseRunnersDB(database)
	handler := server.Handler()

	request := httptest.NewRequest(http.MethodGet, runnerwire.ConnectPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("loopback connect without a token: %d, want 401", response.Code)
	}

	create := httptest.NewRequest(http.MethodPost, "/api/runners", strings.NewReader(`{"name":"pc"}`))
	create.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("loopback create: %d, body = %s", response.Code, response.Body)
	}
	var created runnerwire.CreateResponse
	_ = json.Unmarshal(response.Body.Bytes(), &created)
	if created.Runner.OwnerID != "local" {
		t.Fatalf("owner = %q, want local", created.Runner.OwnerID)
	}
}

func dbtestOpenForServer(t *testing.T, workspace string) *db.DB {
	t.Helper()
	database, err := db.Open(workspace)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}
