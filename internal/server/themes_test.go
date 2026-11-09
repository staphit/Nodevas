package server

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
	"nodevas/internal/runnerwire"
	"nodevas/internal/themes"
	"nodevas/internal/workspace"
)

func themeZip(t *testing.T, id string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	files := map[string]string{
		"theme.json": `{"schema":1,"id":"` + id + `","name":{"en":"Magic"},"version":"1","canvas":[100,100],
			"rooms":{"lounge":"lounge.webp"},"characters":{}}`,
		"lounge.webp": "RIFF\x10\x00\x00\x00WEBPVP8 body",
	}
	for name, data := range files {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type themeServer struct {
	*runnerTestServer
	bea, ann browser
}

func newThemeServer(t *testing.T) *themeServer {
	t.Helper()
	server, _, inbox, database, users := accountServerPartsForTest(t)
	if err := users.AddWithEmail(t.Context(), "bea", "correct-horse-battery", "", "bea@example.test"); err != nil {
		t.Fatal(err)
	}
	server.UseRunnersDB(database)
	server.UseThemesDB(database)
	s := &runnerTestServer{server: server, handler: server.Handler(), inbox: inbox}
	return &themeServer{runnerTestServer: s, bea: s.signIn(t, "bea@example.test"), ann: s.signIn(t, testEmail)}
}

func (s *themeServer) upload(t *testing.T, b browser, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "pack.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return s.do(t, &b, http.MethodPost, "/api/themes", body.String(), map[string]string{"Content-Type": writer.FormDataContentType()})
}

// The whole way in, through every middleware: the multipart allowance, the
// session, the hub's event and the audit trail.
func TestThemePackThroughTheWholeServer(t *testing.T) {
	s := newThemeServer(t)
	live := httptest.NewServer(s.handler)
	t.Cleanup(live.Close)
	peer := dialHubAs(t, live.URL, s.ann)

	rec := s.upload(t, s.bea, themeZip(t, "magic"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	peer.await(themes.ChangedEvent, func(event realtime.Event) bool { return event.ID == "magic" })
	s.expectAudit(t, "POST themes", "magic")

	rec = s.do(t, &s.ann, http.MethodGet, "/api/themes", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"owner":"bea"`) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}

	// The image is the site's CSP replaced by the sandbox, and readable by
	// whoever is signed in.
	rec = s.do(t, &s.ann, http.MethodGet, "/api/themes/magic/files/lounge.webp", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("image = %d %v", rec.Code, rec.Header())
	}
	if got := rec.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != "sandbox; default-src 'none'" {
		t.Fatalf("CSP = %q", got)
	}

	if rec := s.do(t, &s.bea, http.MethodPut, "/api/themes/default", `{"id":"magic"}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a member setting the default = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do(t, &s.ann, http.MethodPut, "/api/themes/default", `{"id":"magic"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("an administrator setting the default = %d %s", rec.Code, rec.Body)
	}
	s.expectAudit(t, "PUT themes/default", "magic")

	if rec := s.do(t, &s.bea, http.MethodDelete, "/api/themes/magic", "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("the owner deleting = %d %s", rec.Code, rec.Body)
	}
	s.expectAudit(t, "DELETE themes/magic", "magic")
	rec = s.do(t, &s.ann, http.MethodGet, "/api/themes", "", nil)
	if !strings.Contains(rec.Body.String(), `"default":""`) || !strings.Contains(rec.Body.String(), `"packs":[]`) {
		t.Fatalf("after deleting = %s", rec.Body)
	}
}

func (s *themeServer) expectAudit(t *testing.T, action, target string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		err := s.server.database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND target = ?`, action, target).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no audit row %q on %q", action, target)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSignedOutGetsNoThemes(t *testing.T) {
	s := newThemeServer(t)
	if rec := s.do(t, nil, http.MethodGet, "/api/themes", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d %s", rec.Code, rec.Body)
	}
}

// A person's request naming a project they may not read, or a browser still
// showing one that is gone, does not matter: packs are not a project's.
func TestThemesAreNotScopedToAProject(t *testing.T) {
	s := newThemeServer(t)
	for _, path := range []string{"/api/themes?project=no-such-project", "/api/themes"} {
		if rec := s.do(t, &s.bea, http.MethodGet, path, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
		}
	}
}

func themeRoutes() []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodGet, "themes"},
		{http.MethodPost, "themes"},
		{http.MethodGet, "themes/magic"},
		{http.MethodGet, "themes/magic/files/lounge.webp"},
		{http.MethodDelete, "themes/magic"},
		{http.MethodPut, "themes/default"},
	}
}

// Through the runner's proxy, as its agents come: the routes are on no
// allowlist. A role would be refused by the guard's default deny as well.
func TestARunnerAgentReachesNoThemeRoute(t *testing.T) {
	s := newThemeServer(t)
	if rec := s.upload(t, s.ann, themeZip(t, "magic")); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	runner := s.create(t, s.ann, "ann-pc")
	bearer := map[string]string{"Authorization": "Bearer " + runner.Token}
	for _, route := range themeRoutes() {
		rec := s.do(t, nil, route.method, runnerwire.ProxyPathPrefix+route.path, `{"id":"magic"}`, bearer)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("%s %s = %d %s", route.method, route.path, rec.Code, rec.Body)
		}
	}
	if rec := s.do(t, &s.ann, http.MethodGet, "/api/themes/magic", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("the pack after the agent's tries = %d", rec.Code)
	}
}

// The copilot's tool bridge: the person's own cookie, an agent's hands.
func TestTheCopilotBridgeReachesNoThemeRoute(t *testing.T) {
	s := newThemeServer(t)
	if rec := s.upload(t, s.ann, themeZip(t, "magic")); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	for _, route := range themeRoutes() {
		request := httptest.NewRequest(route.method, "/api/"+route.path, strings.NewReader(`{"id":"magic"}`))
		request = request.WithContext(auth.WithInProcessAgent(request.Context(), identity.AgentWorker))
		request.Header.Set("Content-Type", "application/json")
		withCookies(request, s.ann.cookies)
		recorder := httptest.NewRecorder()
		s.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d %s", route.method, route.path, recorder.Code, recorder.Body)
		}
	}
}

// A loopback server's agent, by the header that says it is one, with no role
// and with one.
func TestALocalAgentReachesNoThemeRoute(t *testing.T) {
	pm := projectManagerForTest(t)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	server.UseThemesDB(dbtestOpenForServer(t, pm.Workspace()))
	handler := server.Handler()
	for _, header := range []map[string]string{
		{auth.AgentRoleHeaderName: string(identity.AgentWorker)},
		{auth.AgentRoleHeaderName: string(identity.AgentOrchestrator)},
		{auth.RoleHeaderName: "planner"},
	} {
		for _, route := range themeRoutes() {
			request := httptest.NewRequest(route.method, "/api/"+route.path, strings.NewReader(`{"id":"magic"}`))
			request.Header.Set("Content-Type", "application/json")
			for key, value := range header {
				request.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Errorf("%v %s %s = %d %s", header, route.method, route.path, recorder.Code, recorder.Body)
			}
		}
	}

	// Only the person may, on the same server.
	request := httptest.NewRequest(http.MethodGet, "/api/themes", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the person = %d %s", recorder.Code, recorder.Body)
	}
}

// Leftovers of a crash go when the server starts.
func TestStartingCleansUpAfterACrash(t *testing.T) {
	pm := projectManagerForTest(t)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	dir := filepath.Join(pm.Workspace(), workspace.DataDir, themes.DirName)
	if err := os.MkdirAll(filepath.Join(dir, ".tmp.0123456789abcdef", "rooms"), 0o755); err != nil {
		t.Fatal(err)
	}
	server.UseThemesDB(dbtestOpenForServer(t, pm.Workspace()))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}
