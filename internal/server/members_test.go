package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/realtime"
)

// membersFixture is an accounts server with two projects, main and other.
// ann is the server administrator; bea, cal and dan are members of the
// server. Nobody is a member of any project yet: both start open.
type membersFixture struct {
	*runnerTestServer
	ann, bea, cal, dan browser
	otherRoot          string
}

func newMembersFixture(t *testing.T) *membersFixture {
	t.Helper()
	server, pm, inbox, database, users := accountServerPartsForTest(t)
	for _, name := range []string{"bea", "cal", "dan"} {
		if err := users.AddWithEmail(context.Background(), name, "correct-horse-battery", "", name+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := pm.Activate("other", true, ""); err != nil {
		t.Fatalf("create other: %v", err)
	}
	otherRoot := pm.Store().Root()
	graph := &engine.Graph{Version: 1, Nodes: []*engine.Node{{ID: "secret-plan", Title: "Secret plan"}}}
	data, err := engine.MarshalGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherRoot, "graph.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pm.Activate("main", false, ""); err != nil {
		t.Fatalf("back to main: %v", err)
	}
	server.UseRunnersDB(database)
	server.UseMembersDB(database)
	s := &runnerTestServer{server: server, handler: server.Handler(), inbox: inbox}
	return &membersFixture{
		runnerTestServer: s,
		ann:              s.signIn(t, testEmail),
		bea:              s.signIn(t, "bea@example.test"),
		cal:              s.signIn(t, "cal@example.test"),
		// Passcode requests are throttled per source address, and three
		// from one address is the budget: dan signs in from another.
		dan:       signInFrom(t, s, "dan@example.test", "192.0.2.77:4000"),
		otherRoot: otherRoot,
	}
}

// signInFrom is signIn from a given client address.
func signInFrom(t *testing.T, s *runnerTestServer, email, remoteAddr string) browser {
	t.Helper()
	post := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = remoteAddr
		response := httptest.NewRecorder()
		s.handler.ServeHTTP(response, request)
		return response
	}
	before := s.inbox.count()
	if response := post("/api/auth/otp/request", `{"email":"`+email+`"}`); response.Code != http.StatusAccepted {
		t.Fatalf("passcode request: %d %s", response.Code, response.Body)
	}
	s.inbox.waitFor(t, before+1)
	response := post("/api/auth/login", `{"email":"`+email+`","otp":"`+s.inbox.code(t)+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	b := browser{cookies: response.Result().Cookies()}
	for _, cookie := range b.cookies {
		if cookie.Name == auth.CSRFCookieName {
			b.csrf = cookie.Value
		}
	}
	return b
}

func (f *membersFixture) code(t *testing.T, b browser, method, path, body string) int {
	t.Helper()
	return f.do(t, &b, method, path, body, nil).Code
}

func (f *membersFixture) setMember(t *testing.T, b browser, user, role string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, &b, http.MethodPut, "/api/projects/members",
		`{"project":"other","user":"`+user+`","role":"`+role+`"}`, nil)
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return body.Code
}

func projectNames(t *testing.T, f *membersFixture, b browser) []string {
	t.Helper()
	response := f.do(t, &b, http.MethodGet, "/api/projects", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d", response.Code)
	}
	var out struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range out.Projects {
		names = append(names, p.Name)
	}
	return names
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// A project without members is open to every signed-in account, as before.
func TestOpenProjectStaysOpen(t *testing.T) {
	f := newMembersFixture(t)
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusOK {
		t.Fatalf("dan reading an open project: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodPost, "/api/nodes?project=other", `{"title":"Dan's node"}`); code >= 400 {
		t.Fatalf("dan writing an open project: %d", code)
	}
	if !contains(projectNames(t, f, f.dan), "other") {
		t.Fatal("an open project is missing from the catalog")
	}
}

func TestRestrictedProjectAccess(t *testing.T) {
	f := newMembersFixture(t)

	// bea restricts the project by adding cal as a viewer, and becomes its
	// first owner by doing so.
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea adding cal: %d %s", response.Code, response.Body)
	}
	var list struct {
		Restricted bool `json:"restricted"`
		Members    []struct {
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"members"`
	}
	response := f.do(t, &f.bea, http.MethodGet, "/api/projects/members?project=other", "", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Restricted || len(list.Members) != 2 || list.Members[0].Name != "bea" || list.Members[0].Role != "owner" ||
		list.Members[1].Name != "cal" || list.Members[1].Role != "viewer" {
		t.Fatalf("members = %+v", list)
	}

	// dan is not a member: refused, and the project is invisible to him.
	response = f.do(t, &f.dan, http.MethodGet, "/api/graph?project=other", "", nil)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "PROJECT_ACCESS_DENIED" {
		t.Fatalf("dan reading: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/nodes/secret-plan?project=other", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading a node: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/agents?project=other", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading the agent roster: %d", code)
	}
	// The usage report covers one project's runs and sessions; the prices are
	// the server's.
	if code := f.code(t, f.dan, http.MethodGet, "/api/usage?project=other", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading the project's usage: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/usage/prices?project=other", ""); code == http.StatusForbidden {
		t.Fatalf("dan reading the prices: %d", code)
	}
	if contains(projectNames(t, f, f.dan), "other") {
		t.Fatal("dan's catalog lists a project he is not a member of")
	}
	search := f.do(t, &f.dan, http.MethodGet, "/api/search?q=secret", "", nil)
	if strings.Contains(strings.ToLower(search.Body.String()), "secret") {
		t.Fatalf("dan's search finds the restricted project: %s", search.Body)
	}
	targets := f.do(t, &f.dan, http.MethodGet, "/api/links/targets?project=main", "", nil)
	if targets.Code != http.StatusOK || strings.Contains(targets.Body.String(), "secret-plan") {
		t.Fatalf("dan's link picker offers the restricted project's nodes: %d %s", targets.Code, targets.Body)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/projects/members?project=other", ""); code != http.StatusNotFound {
		t.Fatalf("dan reading the member list: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/projects/export?project=other", ""); code != http.StatusForbidden {
		t.Fatalf("dan exporting: %d", code)
	}
	if response := f.setMember(t, f.dan, "dan", "owner"); response.Code != http.StatusNotFound {
		t.Fatalf("dan adding himself: %d %s", response.Code, response.Body)
	}
	// The project dan may reach is unaffected.
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=main", ""); code != http.StatusOK {
		t.Fatalf("dan reading main: %d", code)
	}

	// cal is a viewer: reads, never writes, cannot manage.
	if code := f.code(t, f.cal, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusOK {
		t.Fatalf("cal reading: %d", code)
	}
	response = f.do(t, &f.cal, http.MethodPost, "/api/nodes?project=other", `{"title":"Cal's node"}`, nil)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "PROJECT_READ_ONLY" {
		t.Fatalf("cal writing: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.cal, http.MethodPost, "/api/agents/heartbeat?project=other", `{"sessionId":"x"}`); code != http.StatusForbidden {
		t.Fatalf("cal's agent heartbeat: %d", code)
	}
	if response := f.setMember(t, f.cal, "dan", "editor"); response.Code != http.StatusForbidden {
		t.Fatalf("cal managing members: %d", response.Code)
	}
	if !contains(projectNames(t, f, f.cal), "other") {
		t.Fatal("cal's catalog is missing a project he is a member of")
	}

	// Promoted to editor, cal writes.
	if response := f.setMember(t, f.bea, "cal", "editor"); response.Code != http.StatusOK {
		t.Fatalf("promoting cal: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.cal, http.MethodPost, "/api/nodes?project=other", `{"title":"Cal's node"}`); code >= 400 {
		t.Fatalf("cal writing as an editor: %d", code)
	}

	// The administrator reaches and manages every project.
	if code := f.code(t, f.ann, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusOK {
		t.Fatalf("ann reading: %d", code)
	}
	if response := f.setMember(t, f.ann, "dan@example.test", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("ann adding dan by address: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=other", ""); code != http.StatusOK {
		t.Fatalf("dan reading once added: %d", code)
	}

	// The last owner cannot leave or step down while others remain.
	if response := f.setMember(t, f.bea, "bea", "editor"); response.Code != http.StatusConflict {
		t.Fatalf("the last owner stepping down: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/members/open", `{"project":"other"}`); code != http.StatusOK {
		t.Fatalf("opening the project: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodPost, "/api/nodes?project=other", `{"title":"Dan again"}`); code >= 400 {
		t.Fatalf("dan writing once reopened: %d", code)
	}
}

// Agents act as the account behind them: a runner's proxied requests get its
// owner's access, and an agent never manages members.
func TestRestrictedProjectAccessForAgents(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restricting: %d %s", response.Code, response.Body)
	}
	dans := f.create(t, f.dan, "dan-pc")
	bearer := map[string]string{"Authorization": "Bearer " + dans.Token}
	proxied := "/api/runner/proxy/graph?project=other"
	if response := f.do(t, nil, http.MethodGet, proxied, "", bearer); response.Code != http.StatusForbidden {
		t.Fatalf("dan's runner reading a project dan may not: %d %s", response.Code, response.Body)
	}
	beas := f.create(t, f.bea, "bea-pc")
	bearer = map[string]string{"Authorization": "Bearer " + beas.Token}
	if response := f.do(t, nil, http.MethodGet, proxied, "", bearer); response.Code != http.StatusOK {
		t.Fatalf("bea's runner reading her project: %d %s", response.Code, response.Body)
	}
}

func (f *membersFixture) dialAs(t *testing.T, live *httptest.Server, b browser) *wsPeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{"Origin": {live.URL}}
	for _, cookie := range b.cookies {
		header.Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	url := "ws" + strings.TrimPrefix(live.URL, "http") + "/ws"
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	conn.SetReadLimit(64 << 10)
	peer := &wsPeer{t: t, conn: conn, events: make(chan realtime.Event, 64)}
	go func() {
		defer close(peer.events)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var event realtime.Event
			if json.Unmarshal(data, &event) == nil {
				peer.events <- event
			}
		}
	}()
	t.Cleanup(func() { _ = conn.CloseNow() })
	peer.await("hello", nil)
	return peer
}

// seenBefore reports whether kind arrives on peer before the marker event:
// the hub queues each client's events in order, so a marker broadcast after
// the event under test bounds the wait.
func seenBefore(t *testing.T, peer *wsPeer, kind, marker string) bool {
	t.Helper()
	timeout := time.After(5 * time.Second)
	seen := false
	for {
		select {
		case event, ok := <-peer.events:
			if !ok {
				t.Fatal("connection closed")
			}
			if event.Type == kind {
				seen = true
			}
			if event.Type == marker {
				return seen
			}
		case <-timeout:
			t.Fatalf("no %q marker", marker)
		}
	}
}

func TestRestrictedProjectRealtime(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restricting: %d %s", response.Code, response.Body)
	}
	live := httptest.NewServer(f.handler)
	t.Cleanup(live.Close)
	hub := f.server.hub

	// Subscribing to a project dan may not watch is refused.
	dan := f.dialAs(t, live, f.dan)
	dan.send(map[string]any{"type": "subscribe", "project": "other"})
	dan.await("access-denied", nil)

	// A client that never subscribed does not get the project's events
	// either, while a member watching it does.
	danAll := f.dialAs(t, live, f.dan)
	cal := f.dialAs(t, live, f.cal)
	cal.send(map[string]any{"type": "subscribe", "project": "other"})
	cal.await("presence", nil)
	hub.BroadcastRoom(f.otherRoot, "node-changed", "secret-plan")
	hub.Broadcast("marker", "")
	if seenBefore(t, dan, "node-changed", "marker") {
		t.Fatal("a refused subscriber got the project's event")
	}
	if seenBefore(t, danAll, "node-changed", "marker") {
		t.Fatal("dan's catch-all connection got a restricted project's event")
	}
	if !seenBefore(t, cal, "node-changed", "marker") {
		t.Fatal("a member watching the project missed its event")
	}

	// Removing cal moves his live subscription out of the room.
	response := f.do(t, &f.bea, http.MethodDelete, "/api/projects/members?project=other&userId="+userID(t, f, "cal"), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("removing cal: %d %s", response.Code, response.Body)
	}
	cal.await("access-denied", nil)
	hub.BroadcastRoom(f.otherRoot, "node-changed", "secret-plan")
	hub.Broadcast("marker", "")
	if seenBefore(t, cal, "node-changed", "marker") {
		t.Fatal("a removed member still gets the project's events")
	}
}

func userID(t *testing.T, f *membersFixture, name string) string {
	t.Helper()
	response := f.do(t, &f.ann, http.MethodGet, "/api/projects/members?project=other", "", nil)
	var list struct {
		Members []struct {
			UserID string `json:"userId"`
			Name   string `json:"name"`
		} `json:"members"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &list)
	for _, m := range list.Members {
		if m.Name == name {
			return m.UserID
		}
	}
	t.Fatalf("%s is not a member: %s", name, response.Body)
	return ""
}

// Moving a restricted project keeps its members with it, and a copy is as
// restricted as the original.
func TestMembersFollowMovesAndCopies(t *testing.T) {
	f := newMembersFixture(t)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("restricting: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.ann, http.MethodPost, "/api/projects/move", `{"name":"other","newName":"renamed"}`); code != http.StatusOK {
		t.Fatalf("move: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=renamed", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading the moved project: %d", code)
	}
	if code := f.code(t, f.ann, http.MethodPost, "/api/projects/copy", `{"name":"renamed","newName":"copy"}`); code != http.StatusOK {
		t.Fatalf("copy: %d", code)
	}
	if code := f.code(t, f.dan, http.MethodGet, "/api/graph?project=copy", ""); code != http.StatusForbidden {
		t.Fatalf("dan reading the copy: %d", code)
	}
	if code := f.code(t, f.cal, http.MethodGet, "/api/graph?project=copy", ""); code != http.StatusOK {
		t.Fatalf("cal reading the copy: %d", code)
	}
}
