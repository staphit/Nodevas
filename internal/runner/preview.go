package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"nodevas/internal/runnerwire"
)

// The result preview (easy start): the built-in runner shows a finished
// run's web page in the owner's browser without the page ever reaching the
// hub's origin. It is a loopback listener of its own, apart from the hub and
// the MCP proxy, serving one run's folder as static files under a token
// minted per click (run_preview, workspace.go):
//
//	http://localhost:<port>/p/<token>/<path>
//
// The hub hands out the other loopback name than its own (localhost against
// 127.0.0.1), so the page never shares the hub's cookie jar either. Nothing
// is built, run or listed: a file under the run's folder, read with the
// repo browse rules (no secret, no .git, no link out, regular files only),
// or a 404.

const (
	// previewTTL: a token dies this long after it was last used.
	previewTTL = 30 * time.Minute
	// previewConcurrent requests are served at once; the rest wait.
	previewConcurrent = 16
	// previewTokenBytes is the token's randomness: 128 bits.
	previewTokenBytes = 16
)

// previewCSP lets the run's page run its own scripts and styles and load
// public resources (a map, a font), but talk to nothing on this machine but
// itself: no fetch to the hub or anywhere else, no worker, no plugin, no
// form post, no framing.
const previewCSP = "default-src 'none'; script-src 'self' 'unsafe-inline' https:; style-src 'self' 'unsafe-inline' https:; " +
	"img-src 'self' data: blob: https:; font-src 'self' data: https:; media-src 'self' data: blob: https:; " +
	"connect-src 'self'; frame-src https:; worker-src 'none'; manifest-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// previewTypes are the files served inline, by extension; anything else is
// a download.
var previewTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".map":   "application/json; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/plain; charset=utf-8",
	".csv":   "text/csv; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".bmp":   "image/bmp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".mp3":   "audio/mpeg",
	".m4a":   "audio/mp4",
	".wav":   "audio/wav",
	".ogg":   "audio/ogg",
	".mp4":   "video/mp4",
	".webm":  "video/webm",
	".pdf":   "application/pdf",
	".wasm":  "application/wasm",
}

// previewEntryNames are the pages a folder opens on, in order, at its top or
// in one of previewEntryDirs (a site built or kept there).
var (
	previewEntryNames = []string{"index.html", "index.htm"}
	previewEntryDirs  = []string{"dist", "build", "public", "out", "docs", "site", "www"}
)

// previewRootAsset finds a page loading a script, style, image or frame by an
// address from the site's root (src="/assets/x.js"), as a Vite or Create
// React App build or source page does. Under /p/<token>/ such an address
// misses the run's folder, so the page would open blank: it is no entry.
var previewRootAsset = regexp.MustCompile(`(?is)<(?:script|link|img|source|iframe|video|audio|embed)\b[^>]*?\s(?:src|href)\s*=\s*["']?/(?:[^/]|$)`)

// previewPageScan is how much of a page previewRootAsset reads.
const previewPageScan = 256 << 10

// previewLoadsFromRoot reports whether the page at real loads something by
// an address from the site's root (previewRootAsset).
func previewLoadsFromRoot(real string) bool {
	f, err := openNoBlock(real)
	if err != nil {
		return false
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, previewPageScan))
	return previewRootAsset.Match(head)
}

// previewEntry is the page a run's folder opens on, relative and
// slash-separated: index.html (or .htm) at its top, then in a usual output
// folder, then the only page at its top by any name; never a page that
// loads its parts from the site's root (previewRootAsset), which would open
// blank. web is false when there is none: the folder is opened instead.
func previewEntry(root string) (entry string, web bool) {
	page := func(rel string) bool {
		real, err := resolveRepoPath(root, rel)
		if err != nil {
			return false
		}
		info, err := os.Stat(real)
		return err == nil && info.Mode().IsRegular() && !previewLoadsFromRoot(real)
	}
	for _, name := range previewEntryNames {
		if page(name) {
			return name, true
		}
	}
	for _, dir := range previewEntryDirs {
		for _, name := range previewEntryNames {
			if rel := dir + "/" + name; page(rel) {
				return rel, true
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	only, n := "", 0
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(path.Ext(name))
		if (ext != ".html" && ext != ".htm") || secretName(name) || !page(name) {
			continue
		}
		only, n = name, n+1
	}
	if n == 1 {
		return only, true
	}
	return "", false
}

// previewGrant is what one token opens: a run's folder, found again under
// the data directory at every request, of the registered folder repoID.
type previewGrant struct {
	runID    string
	repoID   string
	dataDir  string
	lastUsed time.Time
}

// previewServer is the listener and its live tokens, in memory only: a
// restart ends every preview.
type previewServer struct {
	listener net.Listener
	server   *http.Server
	sem      chan struct{}
	// now is the clock; tests move it.
	now func() time.Time
	// allowed, asked at every request, says whether a grant of repoID still
	// opens anything: the runner still built in, previews and repo browsing
	// still on, the folder still registered. A grant it refuses ends. Nil
	// allows every grant (tests of the listener alone).
	allowed func(repoID string) bool

	mu     sync.Mutex
	grants map[string]*previewGrant // token → grant
	byRun  map[string]string        // run → its token
}

// startPreviewServer listens on 127.0.0.1 at a port the OS picks; allowed is
// previewServer.allowed.
func startPreviewServer(allowed func(repoID string) bool) (*previewServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &previewServer{
		listener: listener,
		sem:      make(chan struct{}, previewConcurrent),
		now:      time.Now,
		allowed:  allowed,
		grants:   map[string]*previewGrant{},
		byRun:    map[string]string{},
	}
	// Its error log names no request: a token is in every path.
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute,
		ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}

func (p *previewServer) port() int {
	if addr, ok := p.listener.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

func (p *previewServer) close() { _ = p.server.Close() }

// grant mints a token for runID's folder under dataDir, a run of the
// registered folder repoID, replacing the run's earlier one, and says when it
// ends unless used.
func (p *previewServer) grant(runID, repoID, dataDir string) (string, time.Time) {
	var raw [previewTokenBytes]byte
	_, _ = rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.byRun[runID]; ok {
		delete(p.grants, old)
	}
	for t, g := range p.grants {
		if now.Sub(g.lastUsed) >= previewTTL {
			delete(p.grants, t)
			delete(p.byRun, g.runID)
		}
	}
	p.grants[token] = &previewGrant{runID: runID, repoID: repoID, dataDir: dataDir, lastUsed: now}
	p.byRun[runID] = token
	return token, now.Add(previewTTL)
}

// revokeRun ends runID's preview: its result was applied or thrown away.
func (p *previewServer) revokeRun(runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if token, ok := p.byRun[runID]; ok {
		delete(p.grants, token)
		delete(p.byRun, runID)
	}
}

// live reports whether token still opens something, without using it.
func (p *previewServer) live(token string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	g := p.grants[token]
	return g != nil && p.now().Sub(g.lastUsed) < previewTTL
}

// use is token's grant, its last use now; an expired one is dropped.
func (p *previewServer) use(token string) (previewGrant, bool) {
	if len(token) != 2*previewTokenBytes || strings.Trim(token, "0123456789abcdef") != "" {
		return previewGrant{}, false
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	g := p.grants[token]
	if g == nil {
		return previewGrant{}, false
	}
	if now.Sub(g.lastUsed) >= previewTTL {
		delete(p.grants, token)
		delete(p.byRun, g.runID)
		return previewGrant{}, false
	}
	g.lastUsed = now
	return *g, true
}

// drop ends token's grant: its folder is gone or no longer the run's.
func (p *previewServer) drop(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g := p.grants[token]; g != nil {
		delete(p.grants, token)
		if p.byRun[g.runID] == token {
			delete(p.byRun, g.runID)
		}
	}
}

// previewHostOK reports a Host header naming this machine: localhost or a
// loopback address, with or without a port. Anything else is a page from
// elsewhere that a rebound name pointed here.
func previewHostOK(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

func (p *previewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	h.Set("Content-Security-Policy", previewCSP)
	switch {
	case !previewHostOK(r.Host):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		h.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case r.Header.Get("Service-Worker") != "":
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-r.Context().Done():
		return
	}
	p.serve(w, r)
}

// serve answers one GET or HEAD under /p/<token>/: a file of the token's run
// folder, or a 404 that says nothing about why.
func (p *previewServer) serve(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/p/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	token, rel, slash := strings.Cut(rest, "/")
	grant, ok := p.use(token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Turned off, or the folder removed from Nodevas, since it was minted:
	// an open tab does not keep it going.
	if p.allowed != nil && !p.allowed(grant.repoID) {
		p.drop(token)
		http.NotFound(w, r)
		return
	}
	if !slash {
		http.Redirect(w, r, "/p/"+token+"/", http.StatusFound)
		return
	}
	wt, err := findRunWorktree(r.Context(), grant.dataDir, "", grant.runID)
	if err != nil || !wt.Exists {
		p.drop(token)
		http.NotFound(w, r)
		return
	}
	if rel == "" || strings.HasSuffix(rel, "/") {
		rel += "index.html"
	}
	real, err := resolveRepoPath(wt.Path, rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	before, err := os.Stat(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if before.IsDir() {
		// A folder is its index page, at the address with a slash so the
		// page's relative links resolve inside it.
		if index, err := resolveRepoPath(wt.Path, rel+"/index.html"); err == nil {
			if info, err := os.Stat(index); err == nil && info.Mode().IsRegular() {
				http.Redirect(w, r, url.PathEscape(path.Base(rel))+"/", http.StatusFound)
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	if !before.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	// Opened without waiting (a FIFO would hold a slot for good) and checked
	// again once open, in case it was swapped in between.
	f, err := openNoBlock(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		http.NotFound(w, r)
		return
	}
	name := path.Base(rel)
	if kind, ok := previewTypes[strings.ToLower(path.Ext(name))]; ok {
		w.Header().Set("Content-Type", kind)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// previewSlot is the Client's preview server: started on the first page
// minted, never on a runner that is not built in, closed with Client.Run.
type previewSlot struct {
	mu     sync.Mutex
	srv    *previewServer
	closed bool
}

var errPreviewClosed = errors.New("the preview has stopped")

// get is the server, started on first use with allowed
// (previewServer.allowed).
func (s *previewSlot) get(allowed func(repoID string) bool) (*previewServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errPreviewClosed
	}
	if s.srv == nil {
		srv, err := startPreviewServer(allowed)
		if err != nil {
			return nil, err
		}
		s.srv = srv
	}
	return s.srv, nil
}

// peek is the server if it started, else nil.
func (s *previewSlot) peek() *previewServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv
}

// close stops the server for good.
func (s *previewSlot) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.srv != nil {
		s.srv.close()
		s.srv = nil
	}
}

// revokePreview ends runID's preview, if it has one.
func (c *Client) revokePreview(runID string) {
	if srv := c.preview.peek(); srv != nil {
		srv.revokeRun(runID)
	}
}

// previewAllowed is what a preview of a run of repoID needs at every
// request, as at its mint (browseRun): the built-in runner, runPreview and
// repoBrowse on, and repoID still one of its folders.
func (c *Client) previewAllowed(repoID string) bool {
	cfg := c.cfg()
	if !c.BuiltIn() || !cfg.RunPreviewOn() || !cfg.RepoBrowseOn() {
		return false
	}
	return slices.ContainsFunc(cfg.Repos, func(r runnerwire.Repo) bool { return r.ID == repoID })
}

// previewRun is run_preview's answer for a run of repoID whose folder wt is:
// a token for its page, or, with no page, only the folder to open (and no
// listener started for it).
func (c *Client) previewRun(ctx context.Context, dataDir, repoID string, wt runWorktree, runID string) (runnerwire.RunPreview, error) {
	out := runnerwire.RunPreview{Path: wt.Path}
	if ctx.Err() != nil {
		return out, refuseRepo(runnerwire.RepoErrBusy, "the request ended")
	}
	entry, web := previewEntry(wt.Path)
	if !web {
		return out, nil
	}
	srv, err := c.preview.get(c.previewAllowed)
	if err != nil {
		return out, refuseRepo(runnerwire.RepoErrPreviewOff, "the preview cannot start on this runner")
	}
	token, expires := srv.grant(runID, repoID, dataDir)
	out.Web, out.Entry, out.Port, out.Token, out.ExpiresAt = true, entry, srv.port(), token, expires
	return out, nil
}
