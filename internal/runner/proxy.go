package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nodevas/internal/runnerwire"
)

// Proxy is the runner's loopback door to the hub for its agents' MCP servers.
//
// `nodevas mcp` only talks to a server on this machine and has no way to carry
// a credential; the hub is elsewhere and wants one. So each run's MCP server is
// pointed at http://127.0.0.1:<port>/r/<secret>, and the proxy forwards what
// arrives under a live secret to the hub's runner proxy route, adding the
// runner token. The secret is per run and dies with it, so another local
// process (or a finished run's leftovers) cannot borrow the runner's identity
// just by finding the port.
type Proxy struct {
	hub          *url.URL
	token        string
	accessID     string
	accessSecret string
	client       *http.Client

	listener net.Listener
	server   *http.Server

	mu      sync.Mutex
	secrets map[string]string // secret → run ID
}

// maxProxyBody bounds one forwarded request body; the hub has its own limits,
// this just keeps a runaway agent from streaming forever.
const maxProxyBody = 32 << 20

// StartProxy listens on 127.0.0.1 at a port the OS picks. client carries the
// forwarded requests; nil uses a client with no overall timeout, because the
// agent network's inbox and ask_user calls are long polls.
func StartProxy(cfg *Config, client *http.Client) (*Proxy, error) {
	hub, err := url.Parse(cfg.Hub)
	if err != nil || hub.Host == "" {
		return nil, errors.New("proxy: the hub URL is not set")
	}
	if client == nil {
		client = &http.Client{}
	}
	// Redirects go back to the agent as they are: following one would send the
	// runner token wherever the response pointed.
	forward := *client
	forward.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		hub:          hub,
		token:        cfg.Token,
		accessID:     cfg.AccessClientID,
		accessSecret: cfg.AccessClientSecret,
		client:       &forward,
		listener:     listener,
		secrets:      map[string]string{},
	}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}

// Addr is the proxy's listening address, 127.0.0.1:<port>.
func (p *Proxy) Addr() string { return p.listener.Addr().String() }

// URL is the server URL a run's `nodevas mcp --server` gets.
func (p *Proxy) URL(secret string) string { return "http://" + p.Addr() + "/r/" + secret }

// Register opens a door for one run and returns its secret.
func (p *Proxy) Register(runID string) string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	secret := hex.EncodeToString(raw[:])
	p.mu.Lock()
	p.secrets[secret] = runID
	p.mu.Unlock()
	return secret
}

// Revoke closes a run's door. Requests already being forwarded finish.
func (p *Proxy) Revoke(secret string) {
	p.mu.Lock()
	delete(p.secrets, secret)
	p.mu.Unlock()
}

// Close stops listening.
func (p *Proxy) Close() error { return p.server.Close() }

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /r/<secret>/api/<rest>
	escaped := r.URL.EscapedPath()
	after, ok := strings.CutPrefix(escaped, "/r/")
	if !ok {
		proxyError(w, http.StatusNotFound, "not found")
		return
	}
	secret, rest, _ := strings.Cut(after, "/")
	p.mu.Lock()
	runID, live := p.secrets[secret]
	p.mu.Unlock()
	if !live || len(secret) != 32 {
		proxyError(w, http.StatusNotFound, "not found")
		return
	}
	rest, ok = strings.CutPrefix(rest, "api/")
	if !ok || rest == "" {
		proxyError(w, http.StatusNotFound, "only /api/ is forwarded")
		return
	}
	decoded, err := url.PathUnescape(rest)
	if err != nil || !cleanPath(decoded) {
		// A dot segment would let a path climb out of the hub's proxy route
		// once something along the way normalises it.
		proxyError(w, http.StatusBadRequest, "bad path")
		return
	}

	target := *p.hub
	base := strings.TrimRight(p.hub.Path, "/")
	target.Path = base + runnerwire.ProxyPathPrefix + decoded
	target.RawPath = strings.TrimRight(p.hub.EscapedPath(), "/") + runnerwire.ProxyPathPrefix + rest
	target.RawQuery = r.URL.RawQuery
	target.Fragment = ""

	body := http.MaxBytesReader(w, r.Body, maxProxyBody)
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), body)
	if err != nil {
		proxyError(w, http.StatusBadRequest, err.Error())
		return
	}
	out.ContentLength = r.ContentLength
	for _, name := range forwardedRequestHeaders {
		if v := r.Header.Values(name); len(v) > 0 {
			out.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
		}
	}
	out.Header.Set("Authorization", "Bearer "+p.token)
	// Which run this is comes from the secret, not from the agent: the
	// header is not in forwardedRequestHeaders, so whatever the agent put
	// there is dropped and this is the only value the hub sees.
	if runID != "" {
		out.Header.Set(runnerwire.RunIDHeader, runID)
	}
	if p.accessID != "" {
		out.Header.Set(runnerwire.AccessClientIDHeader, p.accessID)
		out.Header.Set(runnerwire.AccessClientSecretHeader, p.accessSecret)
	}
	out.Header.Set("User-Agent", "nodevas-runner/"+Version())

	resp, err := p.client.Do(out)
	if err != nil {
		if r.Context().Err() != nil {
			return // the agent gave up; nobody to answer
		}
		proxyError(w, http.StatusBadGateway, "cannot reach the hub: "+err.Error())
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		if droppedResponseHeaders[http.CanonicalHeaderKey(name)] {
			continue
		}
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// forwardedRequestHeaders is all the agent gets to say to the hub besides the
// body: the runner sets the credentials itself and never passes the agent's.
var forwardedRequestHeaders = []string{
	"Content-Type",
	"Accept",
	"X-Nodevas-Agent-Role",
	"X-Nodevas-Project",
	"X-Nodevas-Peer",
}

// droppedResponseHeaders describe the hub's connection rather than the
// response (Content-Length too: the body is streamed on); Set-Cookie is dropped
// because the agent has no session to keep.
var droppedResponseHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Content-Length":      true,
	"Set-Cookie":          true,
}

func cleanPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return !strings.ContainsAny(p, "\\\x00")
}

// proxyError answers in the hub's error shape, which `nodevas mcp` reads.
func proxyError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
