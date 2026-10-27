package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/audit"
	packsapi "nodevas/internal/httpapi/packs"
	"nodevas/internal/realtime"
)

var packPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

const packSVG = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`

// packZipBody is the install request for a pack named name with two
// sprites, one PNG and one SVG.
func packZipBody(t *testing.T, name string) string {
	t.Helper()
	manifest := `{"format":1,"id":"neon","name":{"en":"` + name + `"},"layout":"rooms",` +
		`"palette":{"ground":"#0e1726"},"sprites":{"agent":"images/a.png","table":"images/t.svg"}}`
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for path, content := range map[string][]byte{
		"neon/pack.json":     []byte(manifest),
		"neon/images/a.png":  packPNG,
		"neon/images/t.svg":  []byte(packSVG),
		"neon/notes/todo.md": []byte("ignored, never served"),
	} {
		out, err := w.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"zip": base64.StdEncoding.EncodeToString(buf.Bytes())})
	return string(body)
}

func packRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type packList struct {
	Packs []struct {
		ID          string            `json:"id"`
		Name        map[string]string `json:"name"`
		Layout      string            `json:"layout"`
		Palette     map[string]string `json:"palette"`
		Sprites     map[string]string `json:"sprites"`
		InstalledAt int64             `json:"installedAt"`
		InstalledBy string            `json:"installedBy"`
	} `json:"packs"`
}

func listPacks(t *testing.T, handler http.Handler) packList {
	t.Helper()
	response := packRequest(t, handler, http.MethodGet, "/api/packs", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list = %d %s", response.Code, response.Body)
	}
	var list packList
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

// On a loopback server the local user is the administrator: install, list,
// serve, replace and remove all work without accounts.
func TestPacksLoopbackLifecycle(t *testing.T) {
	pm := projectManagerForTest(t)
	handler := serverForTest(t, pm, realtime.NewHub(), nil).Handler()

	if list := listPacks(t, handler); len(list.Packs) != 0 {
		t.Fatalf("fresh workspace lists %+v", list)
	}
	install := packRequest(t, handler, http.MethodPost, "/api/packs", packZipBody(t, "Neon"))
	if install.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", install.Code, install.Body)
	}

	list := listPacks(t, handler)
	if len(list.Packs) != 1 {
		t.Fatalf("list = %+v", list)
	}
	pack := list.Packs[0]
	if pack.ID != "neon" || pack.Name["en"] != "Neon" || pack.Layout != "rooms" ||
		pack.Palette["ground"] != "#0e1726" || pack.InstalledBy != "local" || pack.InstalledAt == 0 {
		t.Fatalf("pack = %+v", pack)
	}
	if len(pack.Sprites) != 2 || !strings.HasPrefix(pack.Sprites["agent"], "/api/packs/neon/files/images/a.png?v=") {
		t.Fatalf("sprites = %+v", pack.Sprites)
	}

	for url, wantType := range map[string]string{
		pack.Sprites["agent"]: "image/png",
		pack.Sprites["table"]: "image/svg+xml",
	} {
		response := packRequest(t, handler, http.MethodGet, url, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", url, response.Code, response.Body)
		}
		header := response.Header()
		if got := header.Get("Content-Type"); got != wantType {
			t.Errorf("%s Content-Type = %q, want %q", url, got, wantType)
		}
		if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s X-Content-Type-Options = %q", url, got)
		}
		if got := header.Get("Content-Security-Policy"); got != "sandbox; default-src 'none'; style-src 'unsafe-inline'" {
			t.Errorf("%s Content-Security-Policy = %q", url, got)
		}
		if got := header.Get("Cache-Control"); got != "private, max-age=86400" {
			t.Errorf("%s Cache-Control = %q", url, got)
		}
	}

	for _, path := range []string{
		"/api/packs/neon/files/pack.json",
		"/api/packs/neon/files/install.json",
		"/api/packs/neon/files/notes/todo.md",
		"/api/packs/neon/files/images/../pack.json",
		"/api/packs/neon/files/images/%2e%2e/pack.json",
		"/api/packs/neon/files/images/..%2fpack.json",
		"/api/packs/neon/files//etc/passwd",
		"/api/packs/../neon/files/images/a.png",
		"/api/packs/missing/files/images/a.png",
	} {
		if response := packRequest(t, handler, http.MethodGet, path, ""); response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, response.Code)
		}
	}

	// Installing the same id again replaces it, and moves its URLs on.
	replace := packRequest(t, handler, http.MethodPost, "/api/packs", packZipBody(t, "Neon two"))
	if replace.Code != http.StatusCreated {
		t.Fatalf("reinstall = %d %s", replace.Code, replace.Body)
	}
	after := listPacks(t, handler)
	if len(after.Packs) != 1 || after.Packs[0].Name["en"] != "Neon two" || after.Packs[0].InstalledAt <= pack.InstalledAt {
		t.Fatalf("after reinstall = %+v", after)
	}

	bad := packRequest(t, handler, http.MethodPost, "/api/packs", `{"zip":"`+base64.StdEncoding.EncodeToString([]byte("not a zip"))+`"}`)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "not a readable zip") {
		t.Fatalf("bad zip = %d %s", bad.Code, bad.Body)
	}

	if response := packRequest(t, handler, http.MethodDelete, "/api/packs/neon", ""); response.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", response.Code, response.Body)
	}
	if response := packRequest(t, handler, http.MethodDelete, "/api/packs/neon", ""); response.Code != http.StatusNotFound {
		t.Fatalf("second remove = %d, want 404", response.Code)
	}
	if list := listPacks(t, handler); len(list.Packs) != 0 {
		t.Fatalf("after remove = %+v", list)
	}
}

func TestPacksChangedIsBroadcast(t *testing.T) {
	pm := projectManagerForTest(t)
	httpServer := httptest.NewServer(serverForTest(t, pm, realtime.NewHub(), nil).Handler())
	t.Cleanup(httpServer.Close)
	peer := dialHub(t, httpServer)
	peer.await("hello", nil)

	post := func(method, path, body string) int {
		request, err := http.NewRequest(method, httpServer.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if code := post(http.MethodPost, "/api/packs", packZipBody(t, "Neon")); code != http.StatusCreated {
		t.Fatalf("install = %d", code)
	}
	peer.await(packsapi.ChangedEvent, func(event realtime.Event) bool { return event.ID == "neon" })
	if code := post(http.MethodDelete, "/api/packs/neon", ""); code != http.StatusNoContent {
		t.Fatalf("remove = %d", code)
	}
	peer.await(packsapi.ChangedEvent, func(event realtime.Event) bool { return event.ID == "neon" })
}

// With accounts, anyone signed in lists and reads packs; only an
// administrator installs or removes one, and each is in the audit trail.
func TestPacksNeedAnAdministratorWithAccounts(t *testing.T) {
	server, _, inbox, database, users := accountServerPartsForTest(t)
	if err := users.AddWithEmail(context.Background(), "bea", "correct-horse-battery", "", "bea@example.test"); err != nil {
		t.Fatal(err)
	}
	server.UseMembersDB(database)
	s := &runnerTestServer{server: server, handler: server.Handler(), inbox: inbox}
	ann := s.signIn(t, testEmail)
	bea := s.signIn(t, "bea@example.test")

	if response := s.do(t, nil, http.MethodGet, "/api/packs", "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d, want 401", response.Code)
	}
	if response := s.do(t, &bea, http.MethodPost, "/api/packs", packZipBody(t, "Neon"), nil); response.Code != http.StatusForbidden {
		t.Fatalf("member install = %d %s, want 403", response.Code, response.Body)
	}
	if response := s.do(t, &ann, http.MethodPost, "/api/packs", packZipBody(t, "Neon"), nil); response.Code != http.StatusCreated {
		t.Fatalf("admin install = %d %s", response.Code, response.Body)
	}
	response := s.do(t, &bea, http.MethodGet, "/api/packs", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"installedBy":"ann"`) {
		t.Fatalf("member list = %d %s", response.Code, response.Body)
	}
	if response := s.do(t, &bea, http.MethodGet, "/api/packs/neon/files/images/a.png", "", nil); response.Code != http.StatusOK {
		t.Fatalf("member file = %d", response.Code)
	}
	if response := s.do(t, &bea, http.MethodDelete, "/api/packs/neon", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("member remove = %d, want 403", response.Code)
	}
	if response := s.do(t, &ann, http.MethodDelete, "/api/packs/missing", "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("admin remove unknown = %d, want 404", response.Code)
	}
	if response := s.do(t, &ann, http.MethodDelete, "/api/packs/neon", "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("admin remove = %d %s", response.Code, response.Body)
	}

	events, err := audit.New(database).Query(context.Background(), audit.Filter{ActionPrefix: "pack."})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, event := range events {
		actions[event.Action] = event.Target + "/" + event.Project
	}
	if actions[packsapi.ActionInstall] != "neon/" || actions[packsapi.ActionRemove] != "neon/" || len(events) != 2 {
		t.Fatalf("audit = %+v, want one server-wide pack.install and pack.remove for neon", events)
	}
	// The handlers write those rows; the request trail must not add a
	// second, project-filed one for the same write.
	all, err := audit.New(database).Query(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range all {
		if strings.Contains(event.Action, "packs") {
			t.Fatalf("request trail recorded %q too", event.Action)
		}
	}
}

// A runner token reaches only what `nodevas mcp` needs, and changing the
// workspace's packs is not that.
func TestRunnerProxyCannotChangePacks(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, target := range []string{"/api/packs", "/api/packs/neon"} {
			if runnerProxyAllows(method, target) {
				t.Errorf("runner proxy allows %s %s", method, target)
			}
		}
	}
}
