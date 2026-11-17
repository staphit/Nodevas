package runner

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// previewFixture is a preview server over a data directory holding one run's
// folder (<dataDir>/worktrees/<run>), without git: the preview only reads
// files.
type previewFixture struct {
	srv     *previewServer
	dataDir string
	folder  string
	now     time.Time
	mu      sync.Mutex
}

const (
	previewRun  = "run-preview-1"
	previewRepo = "repo-preview"
)

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	dataDir := t.TempDir()
	folder := filepath.Join(dataDir, "worktrees", previewRun)
	writeFile(t, folder, "index.html", []byte("<h1>my bakery</h1>"))
	srv, err := startPreviewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	f := &previewFixture{srv: srv, dataDir: dataDir, folder: folder, now: time.Now()}
	srv.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	return f
}

func (f *previewFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *previewFixture) grant(t *testing.T, runID string) string {
	t.Helper()
	token, expires := f.srv.grant(runID, previewRepo, f.dataDir)
	if len(token) != 32 || strings.Trim(token, "0123456789abcdef") != "" {
		t.Fatalf("token %q is not 128 bits of hex", token)
	}
	if want := f.srv.now().Add(previewTTL); !expires.Equal(want) {
		t.Fatalf("expires %v, want %v", expires, want)
	}
	return token
}

// get asks the preview handler directly, as a browser on this machine would
// (Host localhost), unless header says otherwise.
func (f *previewFixture) get(method, target string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost"+target, nil)
	req.Host = "localhost:" + strconv.Itoa(f.srv.port())
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	return rec
}

func TestPreviewServesIndexUnderToken(t *testing.T) {
	f := newPreviewFixture(t)
	writeFile(t, f.folder, "css/site.css", []byte("h1{color:red}"))
	token := f.grant(t, previewRun)

	// Over the real listener, on loopback.
	addr := f.srv.listener.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Fatalf("listens on %v", addr)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+addr.String()+"/p/"+token+"/index.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost:" + strconv.Itoa(addr.Port)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "<h1>my bakery</h1>" {
		t.Fatalf("index = %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Cache-Control":                "no-store",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"connect-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "worker-src 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatal("the preview set a cookie")
	}

	// The folder itself is its index; a sub file is served too.
	if rec := f.get(http.MethodGet, "/p/"+token+"/", nil); rec.Code != http.StatusOK || rec.Body.String() != "<h1>my bakery</h1>" {
		t.Fatalf("folder = %d %q", rec.Code, rec.Body)
	}
	if rec := f.get(http.MethodGet, "/p/"+token+"/css/site.css", nil); rec.Code != http.StatusOK ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("css = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	// HEAD answers without a body.
	if rec := f.get(http.MethodHead, "/p/"+token+"/index.html", nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d %q", rec.Code, rec.Body)
	}
	// No listing: a folder without an index is not found.
	writeFile(t, f.folder, "empty/readme.txt", []byte("x"))
	if rec := f.get(http.MethodGet, "/p/"+token+"/empty/", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("listing = %d %q", rec.Code, rec.Body)
	}
	// Use keeps the token alive past its first half hour.
	f.advance(previewTTL - time.Minute)
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusOK {
		t.Fatalf("after 29 minutes = %d", rec.Code)
	}
	f.advance(previewTTL - time.Minute)
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusOK {
		t.Fatalf("29 minutes after last use = %d", rec.Code)
	}
}

func TestPreviewRefusesBadToken(t *testing.T) {
	f := newPreviewFixture(t)
	other := filepath.Join(f.dataDir, "worktrees", "run-other")
	writeFile(t, other, "b-only.html", []byte("other run"))
	token := f.grant(t, previewRun)
	otherToken := f.grant(t, "run-other")

	for name, target := range map[string]string{
		"wrong":     "/p/" + strings.Repeat("ab", 16) + "/index.html",
		"truncated": "/p/" + token[:31] + "/index.html",
		"too long":  "/p/" + token + "0/index.html",
		"upper":     "/p/" + strings.ToUpper(token) + "/index.html",
		"none":      "/p//index.html",
		"no prefix": "/" + token + "/index.html",
		"cross run": "/p/" + token + "/b-only.html",
	} {
		if rec := f.get(http.MethodGet, target, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %s = %d", name, target, rec.Code)
		}
	}
	if rec := f.get(http.MethodGet, "/p/"+otherToken+"/b-only.html", nil); rec.Code != http.StatusOK {
		t.Fatalf("the other run's own token = %d", rec.Code)
	}

	// A new mint for the run replaces its token: one per run.
	again := f.grant(t, previewRun)
	if again == token {
		t.Fatal("the same token minted twice")
	}
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("replaced token = %d", rec.Code)
	}
	// Revoked.
	f.srv.revokeRun(previewRun)
	if rec := f.get(http.MethodGet, "/p/"+again+"/index.html", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked token = %d", rec.Code)
	}
	// Expired: half an hour without use.
	f.advance(previewTTL + time.Second)
	if rec := f.get(http.MethodGet, "/p/"+otherToken+"/b-only.html", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expired token = %d", rec.Code)
	}
	if f.srv.live(otherToken) {
		t.Fatal("an expired token is still held")
	}
}

func TestPreviewPathEscapes(t *testing.T) {
	f := newPreviewFixture(t)
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", []byte("outside the run"))
	writeFile(t, f.dataDir, "worktrees/sibling.txt", []byte("beside the run"))
	token := f.grant(t, previewRun)
	base := "/p/" + token + "/"
	for _, target := range []string{
		base + "../sibling.txt",
		base + "%2e%2e/sibling.txt",
		base + "%2E%2E/sibling.txt",
		base + "a/..%2f..%2fsibling.txt",
		base + "..%5csibling.txt",
		base + "a%5c..%5c..%5csibling.txt",
		base + "/etc/passwd",
		base + "C:/Windows/win.ini",
		base + "C:%5cWindows%5cwin.ini",
		base + "index.html%00.txt",
		base + "index.html:stream",
	} {
		if rec := f.get(http.MethodGet, target, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %q", target, rec.Code, rec.Body)
		}
	}
	// A folder link out (a junction on Windows needs no privilege).
	link(t, outside, filepath.Join(f.folder, "outdir"))
	for _, target := range []string{base + "outdir/secret.txt", base + "outdir/", base + "outdir/missing.txt"} {
		if rec := f.get(http.MethodGet, target, nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "outside") {
			t.Errorf("%s = %d %q", target, rec.Code, rec.Body)
		}
	}
	// A file link out of the folder, and a dangling one.
	t.Run("file links", func(t *testing.T) {
		symlinkOrSkip(t, filepath.Join(outside, "secret.txt"), filepath.Join(f.folder, "out.txt"))
		symlinkOrSkip(t, filepath.Join(outside, "gone.txt"), filepath.Join(f.folder, "dangling.txt"))
		for _, target := range []string{base + "out.txt", base + "dangling.txt"} {
			if rec := f.get(http.MethodGet, target, nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "outside") {
				t.Errorf("%s = %d %q", target, rec.Code, rec.Body)
			}
		}
	})
}

func TestPreviewHidesSecrets(t *testing.T) {
	f := newPreviewFixture(t)
	writeFile(t, f.folder, ".env", []byte("TOKEN=1"))
	writeFile(t, f.folder, "app/.env.local", []byte("TOKEN=2"))
	writeFile(t, f.folder, ".git/config", []byte("[core]"))
	writeFile(t, f.folder, "keys/id_rsa", []byte("KEY"))
	token := f.grant(t, previewRun)
	for _, rel := range []string{".env", "app/.env.local", ".git/config", ".GIT/config", "keys/id_rsa", "%2eenv"} {
		if rec := f.get(http.MethodGet, "/p/"+token+"/"+rel, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %q", rel, rec.Code, rec.Body)
		}
	}
}

func TestPreviewMethodsAndHost(t *testing.T) {
	f := newPreviewFixture(t)
	token := f.grant(t, previewRun)
	target := "/p/" + token + "/index.html"
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodPatch} {
		if rec := f.get(method, target, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d", method, rec.Code)
		}
	}
	port := strconv.Itoa(f.srv.port())
	for _, host := range []string{"evil.test:" + port, "evil.test", "192.168.1.5:" + port, "localhost.evil.test:" + port, ""} {
		if rec := f.get(http.MethodGet, target, map[string]string{"Host": host}); rec.Code != http.StatusForbidden {
			t.Errorf("Host %q = %d", host, rec.Code)
		}
	}
	for _, host := range []string{"localhost:" + port, "127.0.0.1:" + port, "[::1]:" + port, "LOCALHOST:" + port} {
		if rec := f.get(http.MethodGet, target, map[string]string{"Host": host}); rec.Code != http.StatusOK {
			t.Errorf("Host %q = %d", host, rec.Code)
		}
	}
	if rec := f.get(http.MethodGet, target, map[string]string{"Service-Worker": "script"}); rec.Code != http.StatusForbidden {
		t.Fatalf("service worker = %d", rec.Code)
	}
}

func TestPreviewUnknownTypeDownloads(t *testing.T) {
	f := newPreviewFixture(t)
	writeFile(t, f.folder, "data.bin", []byte{0, 1, 2})
	writeFile(t, f.folder, "run.sh", []byte("echo hi"))
	writeFile(t, f.folder, "noext", []byte("plain"))
	writeFile(t, f.folder, "app.js", []byte("console.log(1)"))
	writeFile(t, f.folder, "logo.svg", []byte("<svg/>"))
	token := f.grant(t, previewRun)
	for _, name := range []string{"data.bin", "run.sh", "noext"} {
		rec := f.get(http.MethodGet, "/p/"+token+"/"+name, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" ||
			!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s = %d %v", name, rec.Code, rec.Header())
		}
	}
	for name, want := range map[string]string{"app.js": "text/javascript", "logo.svg": "image/svg+xml", "index.html": "text/html"} {
		rec := f.get(http.MethodGet, "/p/"+token+"/"+name, nil)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), want) ||
			rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("%s = %d %v", name, rec.Code, rec.Header())
		}
	}
}

// A token is checked against the runner as it is now at every request: the
// folder removed from Nodevas, or previews or browsing turned off, ends it,
// however often an open tab asks.
func TestPreviewEndsWhenNoLongerAllowed(t *testing.T) {
	f := newPreviewFixture(t)
	var mu sync.Mutex
	allowed := true
	var asked []string
	f.srv.allowed = func(repoID string) bool {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, repoID)
		return allowed
	}
	token := f.grant(t, previewRun)
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusOK {
		t.Fatalf("allowed = %d", rec.Code)
	}
	mu.Lock()
	allowed = false
	mu.Unlock()
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no longer allowed = %d", rec.Code)
	}
	mu.Lock()
	allowed = true
	got := slices.Clone(asked)
	mu.Unlock()
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusNotFound || f.srv.live(token) {
		t.Fatalf("allowed again, the ended token = %d", rec.Code)
	}
	if len(got) != 2 || got[0] != previewRepo || got[1] != previewRepo {
		t.Fatalf("asked about %q", got)
	}
}

// The Client's check: built in, runPreview and repoBrowse on, the folder
// still registered.
func TestClientPreviewAllowed(t *testing.T) {
	off := false
	cfg := &Config{Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: t.TempDir()}}}
	c := &Client{Config: cfg}
	if c.previewAllowed("r1") {
		t.Fatal("allowed on a runner that is not built in")
	}
	c.builtIn.Store(true)
	if !c.previewAllowed("r1") || c.previewAllowed("r2") {
		t.Fatal("built in: r1 only")
	}
	for name, edit := range map[string]func(*Config){
		"runPreview off": func(c *Config) { c.RunPreview = &off },
		"repoBrowse off": func(c *Config) { c.RepoBrowse = &off },
		"folder removed": func(c *Config) { c.Repos = nil },
	} {
		next := cfg.Clone()
		edit(next)
		c.Config = next
		if c.previewAllowed("r1") {
			t.Errorf("%s: still allowed", name)
		}
	}
}

func TestPreviewDiesWithRunFolder(t *testing.T) {
	f := newPreviewFixture(t)
	token := f.grant(t, previewRun)
	if err := os.RemoveAll(f.folder); err != nil {
		t.Fatal(err)
	}
	if rec := f.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("after the folder went = %d", rec.Code)
	}
	if f.srv.live(token) {
		t.Fatal("the token outlived its folder")
	}

	// A folder swapped for a link elsewhere is not the run's folder.
	f2 := newPreviewFixture(t)
	token = f2.grant(t, previewRun)
	elsewhere := t.TempDir()
	writeFile(t, elsewhere, "index.html", []byte("not the run's"))
	if err := os.RemoveAll(f2.folder); err != nil {
		t.Fatal(err)
	}
	link(t, elsewhere, f2.folder)
	if rec := f2.get(http.MethodGet, "/p/"+token+"/index.html", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("folder swapped for a link = %d %q", rec.Code, rec.Body)
	}
}

func TestPreviewEntry(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		dirs  []string
		entry string
		web   bool
	}{
		{"index at the top", []string{"index.html", "about.html", "dist/index.html"}, nil, "index.html", true},
		{"index.htm", []string{"index.htm", "notes.txt"}, nil, "index.htm", true},
		{"built into dist", []string{"package.json", "dist/index.html"}, nil, "dist/index.html", true},
		{"public", []string{"public/index.html", "src/main.js"}, nil, "public/index.html", true},
		{"one page by another name", []string{"bakery.html", "style.css"}, nil, "bakery.html", true},
		{"two pages, no index", []string{"a.html", "b.html"}, nil, "", false},
		{"no page", []string{"main.py", "README.md"}, nil, "", false},
		{"empty", nil, nil, "", false},
		{"index is a folder", []string{"page.html"}, []string{"index.html"}, "page.html", true},
		{"hidden page only", []string{".env.html"}, nil, "", false},
	}
	// Pages that load their parts from the site's root open blank under
	// /p/<token>/: a Vite or Create React App source or build page.
	rootPages := []struct {
		name  string
		files map[string]string
		entry string
		web   bool
	}{
		{"vite source and build", map[string]string{
			"index.html":      `<html><script type="module" src="/src/main.tsx"></script></html>`,
			"dist/index.html": `<html><script type="module" crossorigin src="/assets/index-abc.js"></script></html>`,
		}, "", false},
		{"cra build", map[string]string{
			"public/index.html": `<html><link rel="icon" href="%PUBLIC_URL%/favicon.ico"><div id="root"></div></html>`,
			"build/index.html":  `<html><link href="/static/css/main.css" rel="stylesheet"><script defer src="/static/js/main.js"></script></html>`,
		}, "public/index.html", true},
		{"relative parts", map[string]string{
			"index.html": `<html><link href="style.css" rel="stylesheet"><script src="./app.js"></script><a href="/">home</a></html>`,
		}, "index.html", true},
		{"a public script by //", map[string]string{
			"index.html": `<html><script src="//cdn.example.test/x.js"></script><img src='https://example.test/a.png'></html>`,
		}, "index.html", true},
		{"root image", map[string]string{
			"index.html": `<html><IMG alt="x" SRC=/logo.png></html>`,
		}, "", false},
	}
	for _, tc := range rootPages {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tc.files {
				writeFile(t, root, name, []byte(body))
			}
			if entry, web := previewEntry(root); entry != tc.entry || web != tc.web {
				t.Fatalf("previewEntry = %q %v, want %q %v", entry, web, tc.entry, tc.web)
			}
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range tc.files {
				writeFile(t, root, name, []byte("x"))
			}
			for _, name := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			entry, web := previewEntry(root)
			if entry != tc.entry || web != tc.web {
				t.Fatalf("previewEntry = %q %v, want %q %v", entry, web, tc.entry, tc.web)
			}
		})
	}
	// A page through a link out of the folder is no entry.
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "index.html", []byte("x"))
	link(t, outside, filepath.Join(root, "dist"))
	if entry, web := previewEntry(root); web || entry != "" {
		t.Fatalf("index in a linked folder = %q %v", entry, web)
	}
	t.Run("file link", func(t *testing.T) {
		root := t.TempDir()
		symlinkOrSkip(t, filepath.Join(outside, "index.html"), filepath.Join(root, "index.html"))
		if entry, web := previewEntry(root); web || entry != "" {
			t.Fatalf("linked index = %q %v", entry, web)
		}
	})
}

func TestPreviewSeparateListener(t *testing.T) {
	proxy, err := StartProxy(&Config{Hub: "https://hub.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	f := newPreviewFixture(t)
	proxyAddr, err := net.ResolveTCPAddr("tcp", proxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	addr := f.srv.listener.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() || addr.Port == 0 || addr.Port == proxyAddr.Port {
		t.Fatalf("preview on %v, proxy on %v", addr, proxyAddr)
	}
	// The proxy's routes are not the preview's, and the other way round.
	token := f.grant(t, previewRun)
	if rec := f.get(http.MethodGet, "/r/"+token+"/api/graph", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a proxy path on the preview = %d", rec.Code)
	}
}

// The slot starts its server once, on first use, and not after close.
func TestPreviewSlotLazy(t *testing.T) {
	var slot previewSlot
	if slot.peek() != nil {
		t.Fatal("started before use")
	}
	first, err := slot.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := slot.get(nil)
	if err != nil || second != first {
		t.Fatalf("second get = %p %v, want %p", second, err, first)
	}
	slot.close()
	if slot.peek() != nil {
		t.Fatal("held after close")
	}
	if _, err := slot.get(nil); err == nil {
		t.Fatal("started again after close")
	}
}
