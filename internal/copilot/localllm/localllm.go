// Package localllm finds a local OpenAI-compatible model server on the
// machine it runs on: Ollama or LM Studio on their default loopback ports.
// The copilot uses what it finds when nobody configured a provider or a key,
// so the copilot works out of the box for someone who already runs one.
//
// It probes loopback addresses only, never anything an attacker could point
// it at: the candidates are fixed, and a candidate whose host is not a
// loopback address is skipped outright. Nothing but the model list is
// requested here; the conversation goes there only through the provider the
// caller builds from the result, on the same machine.
package localllm

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nodevas/internal/safego"
)

// Server names a kind of local model server.
const (
	ServerOllama   = "ollama"
	ServerLMStudio = "lmstudio"
)

// Found is a local server with a model to use.
type Found struct {
	// Server is ServerOllama or ServerLMStudio.
	Server string `json:"server"`
	// BaseURL is its OpenAI-compatible endpoint (ending in /v1).
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
}

// Candidate is one place a server may listen. Root is the server's root URL;
// its OpenAI-compatible API is Root + "/v1".
type Candidate struct {
	Server string
	Root   string
}

// DefaultCandidates are Ollama and LM Studio on their default ports, in the
// order they are preferred.
var DefaultCandidates = []Candidate{
	{Server: ServerOllama, Root: "http://127.0.0.1:11434"},
	{Server: ServerLMStudio, Root: "http://127.0.0.1:1234"},
}

// Default waits and cache lifetimes. A probe of a port nothing listens on is
// refused at once; the timeout bounds a firewall that drops the packets.
const (
	DefaultProbeTimeout = 700 * time.Millisecond
	// DefaultFoundTTL: a server that answered is assumed to stay; its model
	// list is re-read this often (a model pulled or removed shows up).
	DefaultFoundTTL = 30 * time.Second
	// DefaultMissingTTL: how soon "nothing found" is checked again, so
	// starting Ollama after the copilot is noticed without a restart.
	DefaultMissingTTL = 10 * time.Second
	// maxListBytes bounds a model list read from a server.
	maxListBytes = 1 << 20
)

// Detector probes the candidates and caches the answer. Safe for concurrent
// use; concurrent callers during a probe share it.
type Detector struct {
	Candidates   []Candidate
	ProbeTimeout time.Duration
	FoundTTL     time.Duration
	MissingTTL   time.Duration
	// Client makes the probes; nil uses one without a proxy or redirects.
	Client *http.Client
	// Now is the clock; nil is time.Now. Tests move it.
	Now func() time.Time

	once          sync.Once
	defaultClient *http.Client

	mu      sync.Mutex
	checked time.Time
	found   Found
	ok      bool
	probing chan struct{}
}

// New returns a Detector for the default candidates.
func New() *Detector {
	return &Detector{Candidates: DefaultCandidates}
}

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Detector) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	d.once.Do(func() {
		d.defaultClient = &http.Client{
			// No proxy: a loopback probe must stay on this machine whatever
			// HTTP_PROXY says.
			Transport: &http.Transport{
				Proxy:           nil,
				DialContext:     (&net.Dialer{Timeout: d.timeout()}).DialContext,
				IdleConnTimeout: time.Minute,
			},
			// A redirect could only lead somewhere other than this machine.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	})
	return d.defaultClient
}

func (d *Detector) timeout() time.Duration {
	if d.ProbeTimeout > 0 {
		return d.ProbeTimeout
	}
	return DefaultProbeTimeout
}

// Detect returns the first candidate that answers with at least one model,
// from the cache while it is fresh.
func (d *Detector) Detect(ctx context.Context) (Found, bool) {
	d.mu.Lock()
	for {
		if !d.checked.IsZero() {
			ttl := d.MissingTTL
			if ttl <= 0 {
				ttl = DefaultMissingTTL
			}
			if d.ok {
				ttl = d.FoundTTL
				if ttl <= 0 {
					ttl = DefaultFoundTTL
				}
			}
			if d.now().Sub(d.checked) < ttl {
				found, ok := d.found, d.ok
				d.mu.Unlock()
				return found, ok
			}
		}
		if d.probing == nil {
			break
		}
		wait := d.probing
		d.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return Found{}, false
		}
		d.mu.Lock()
	}
	done := make(chan struct{})
	d.probing = done
	d.mu.Unlock()

	found, ok := d.probe(ctx)

	d.mu.Lock()
	// A probe cut short by the caller's ctx says nothing about the servers.
	if ctx.Err() == nil {
		d.found, d.ok, d.checked = found, ok, d.now()
	}
	d.probing = nil
	close(done)
	d.mu.Unlock()
	return found, ok
}

// Forget drops the cached answer, so the next Detect probes again.
func (d *Detector) Forget() {
	d.mu.Lock()
	d.checked = time.Time{}
	d.mu.Unlock()
}

// probe asks every candidate at once and takes the first in preference
// order that answered.
func (d *Detector) probe(ctx context.Context) (Found, bool) {
	candidates := d.Candidates
	results := make([]Found, len(candidates))
	oks := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, candidate := range candidates {
		if !Loopback(candidate.Root) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer safego.Recover("local model probe")
			results[i], oks[i] = d.probeOne(ctx, candidate)
		}()
	}
	wg.Wait()
	for i := range candidates {
		if oks[i] {
			return results[i], true
		}
	}
	return Found{}, false
}

func (d *Detector) probeOne(ctx context.Context, candidate Candidate) (Found, bool) {
	root := strings.TrimRight(candidate.Root, "/")
	var names []string
	if candidate.Server == ServerOllama {
		// Ollama's own list; /v1/models is the fallback for older builds.
		var tags struct {
			Models []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"models"`
		}
		if d.getJSON(ctx, root+"/api/tags", &tags) {
			for _, m := range tags.Models {
				names = append(names, firstNonEmpty(m.Name, m.Model))
			}
		}
	}
	if len(names) == 0 {
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if d.getJSON(ctx, root+"/v1/models", &list) {
			for _, m := range list.Data {
				names = append(names, m.ID)
			}
		}
	}
	model := PickModel(names)
	if model == "" {
		return Found{}, false
	}
	return Found{Server: candidate.Server, BaseURL: root + "/v1", Model: model}, true
}

func (d *Detector) getJSON(ctx context.Context, target string, into any) bool {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes))
	if err != nil {
		return false
	}
	return json.Unmarshal(body, into) == nil
}

// toolFamilies are model name prefixes known to call tools well enough for
// the copilot, best first. A server's list does not say which models take
// tools, so the name is all there is to go on.
var toolFamilies = []string{
	"qwen3", "qwen2.5", "llama3.3", "llama3.2", "llama3.1", "llama4", "mistral-nemo",
	"mistral-small", "mistral-large", "gpt-oss", "granite3", "granite4", "command-r",
	"hermes3", "firefunction", "smollm2", "phi4-mini", "devstral", "magistral",
}

// PickModel chooses the model to use: the first whose name is a known
// tool-calling family (in the order of toolFamilies), else the first model
// that is not an embedding model. "" when there is none.
func PickModel(names []string) string {
	var usable []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		lower := strings.ToLower(name)
		if name == "" || strings.Contains(lower, "embed") || strings.Contains(lower, "rerank") ||
			len(name) > 200 || strings.ContainsAny(name, " \t\r\n") {
			continue
		}
		usable = append(usable, name)
	}
	for _, family := range toolFamilies {
		for _, name := range usable {
			// "library/qwen3:8b", "qwen3:8b", "qwen3-coder" all count.
			base := strings.ToLower(name)
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[i+1:]
			}
			if strings.HasPrefix(base, family) {
				return name
			}
		}
	}
	if len(usable) > 0 {
		return usable[0]
	}
	return ""
}

// Loopback reports whether rawURL is http(s) to this machine's loopback
// interface: 127.0.0.0/8, ::1 or localhost.
func Loopback(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Label is how a person reads a server's name.
func Label(server string) string {
	switch server {
	case ServerOllama:
		return "Ollama"
	case ServerLMStudio:
		return "LM Studio"
	}
	return server
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
