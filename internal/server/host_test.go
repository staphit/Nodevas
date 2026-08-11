package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hostRequest builds a request that claims to have arrived at host, optionally
// carrying an Origin, the way a browser would.
func hostRequest(method, path, host, origin, body string) *http.Request {
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Host = host
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	return request
}

// A page whose DNS resolves to 127.0.0.1 still has to send its own name in the
// Host header, which is the one thing it cannot lie its way out of.
func TestServerRefusesAForeignHost(t *testing.T) {
	server, _ := twoProjectServer(t)
	server.UseListenAddress("127.0.0.1", 5666, []string{"Nodes.Example.com"})

	cases := []struct {
		name   string
		method string
		path   string
		host   string
		origin string
		body   string
		want   int
	}{
		{
			name:   "rebound name on a read",
			method: http.MethodGet,
			path:   "/api/graph",
			host:   "evil.com:5666",
			origin: "http://evil.com:5666",
			want:   http.StatusForbidden,
		},
		{
			name:   "rebound name on a write",
			method: http.MethodPost,
			path:   "/api/nodes",
			host:   "evil.com:5666",
			origin: "http://evil.com:5666",
			body:   `{"id":"rebound","title":"Rebound","body":""}`,
			want:   http.StatusForbidden,
		},
		{
			name:   "rebound name on the websocket",
			method: http.MethodGet,
			path:   "/ws",
			host:   "evil.com:5666",
			origin: "http://evil.com:5666",
			want:   http.StatusForbidden,
		},
		{
			name:   "loopback address",
			method: http.MethodGet,
			path:   "/api/graph",
			host:   "127.0.0.1:5666",
			want:   http.StatusOK,
		},
		{
			name:   "loopback name",
			method: http.MethodGet,
			path:   "/api/graph",
			host:   "localhost:5666",
			want:   http.StatusOK,
		},
		{
			name:   "IPv6 loopback literal",
			method: http.MethodGet,
			path:   "/api/graph",
			host:   "[::1]:5666",
			want:   http.StatusOK,
		},
		{
			name:   "configured public name",
			method: http.MethodGet,
			path:   "/api/graph",
			host:   "nodes.example.com",
			want:   http.StatusOK,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := hostRequest(
				testCase.method, testCase.path, testCase.host, testCase.origin, testCase.body)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status = %d, want %d, body = %s",
					response.Code, testCase.want, response.Body)
			}
		})
	}
}

// A server nobody told where it listens cannot judge a Host, so it must not
// try: that is the embedded and test-harness case.
func TestServerWithoutAListenAddressAcceptsAnyHost(t *testing.T) {
	server, _ := twoProjectServer(t)

	for _, host := range []string{"evil.com:5666", "localhost:5666", "nodes.example.com"} {
		request := hostRequest(http.MethodGet, "/api/graph", host, "", "")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("host %q: status = %d, body = %s", host, response.Code, response.Body)
		}
	}
}

// Loopback is not one origin: every port on it is a different application.
func TestLoopbackOriginMustMatchTheListenPort(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		id     string
		want   int
	}{
		{
			name:   "another local port",
			origin: "http://localhost:3000",
			id:     "other-port",
			want:   http.StatusForbidden,
		},
		{
			name:   "the implicit default port",
			origin: "http://localhost",
			id:     "default-port",
			want:   http.StatusForbidden,
		},
		{
			name:   "our own origin",
			origin: "http://127.0.0.1:5666",
			id:     "same-origin",
			want:   http.StatusCreated,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, _ := twoProjectServer(t)
			server.UseListenAddress("127.0.0.1", 5666, nil)

			body := `{"id":"` + testCase.id + `","title":"Node","body":""}`
			request := hostRequest(
				http.MethodPost, "/api/nodes", "127.0.0.1:5666", testCase.origin, body)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status = %d, want %d, body = %s",
					response.Code, testCase.want, response.Body)
			}
		})
	}
}

// "Bound to every interface, so answer to any name" is the state that quietly
// removes both the rebinding defence and the Origin check that leans on it, so
// the package refuses to enter it at all.
func TestWildcardBindWithoutHostNamesIsRefused(t *testing.T) {
	for _, wildcard := range []string{"", "0.0.0.0", "::"} {
		t.Run(wildcard, func(t *testing.T) {
			server, _ := twoProjectServer(t)
			if err := server.UseListenAddress(wildcard, 5666, nil); !errors.Is(err, ErrWildcardNeedsHostName) {
				t.Fatalf("UseListenAddress(%q) error = %v, want ErrWildcardNeedsHostName", wildcard, err)
			}
			// The caller may ignore that error, so the fallback has to be the
			// safe one rather than "any Host".
			if server.allowedHosts == nil {
				t.Fatal("a wildcard bind left the server answering on any Host")
			}
			request := hostRequest(http.MethodGet, "/api/graph", "evil.com:5666", "", "")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("foreign Host status = %d, want 403", response.Code)
			}
		})
	}
}

// A wildcard bind with a name to check against is the legitimate reverse-proxy
// deployment: the public name works, loopback still works, nothing else does.
func TestWildcardBindWithHostNamesKeepsWorking(t *testing.T) {
	server, _ := twoProjectServer(t)
	if err := server.UseListenAddress("0.0.0.0", 5666, []string{"nodes.example.com"}); err != nil {
		t.Fatalf("UseListenAddress: %v", err)
	}
	for host, want := range map[string]int{
		"nodes.example.com": http.StatusOK,
		"localhost:5666":    http.StatusOK,
		"evil.com:5666":     http.StatusForbidden,
	} {
		request := hostRequest(http.MethodGet, "/api/graph", host, "", "")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("host %q: status = %d, want %d", host, response.Code, want)
		}
	}
}

// A GET that mints OAuth state is a write, and a cross-site navigation to it
// carries no Origin at all — Sec-Fetch-Site is what catches it.
func TestStateChangingGETRejectsACrossSiteNavigation(t *testing.T) {
	cases := []struct {
		name      string
		fetchSite string
		reject    bool
	}{
		{name: "cross-site navigation", fetchSite: "cross-site", reject: true},
		{name: "sibling subdomain", fetchSite: "same-site", reject: true},
		{name: "our own page", fetchSite: "same-origin"},
		{name: "typed or opened by the shell", fetchSite: "none"},
		{name: "a client that sends no fetch metadata", fetchSite: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, _ := twoProjectServer(t)
			server.UseListenAddress("127.0.0.1", 5666, nil)

			request := hostRequest(
				http.MethodGet, "/api/remote/drive/auth", "127.0.0.1:5666", "", "")
			if testCase.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", testCase.fetchSite)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			blocked := response.Code == http.StatusForbidden &&
				strings.Contains(response.Body.String(), "cross-site")
			if blocked != testCase.reject {
				t.Fatalf("Sec-Fetch-Site %q: status = %d body = %s, want rejected = %v",
					testCase.fetchSite, response.Code, response.Body, testCase.reject)
			}
		})
	}
}

// An ordinary read is not state-changing, so the fetch-metadata check must not
// start rejecting the things a browser legitimately does cross-site.
func TestOrdinaryReadsIgnoreFetchMetadata(t *testing.T) {
	server, _ := twoProjectServer(t)
	server.UseListenAddress("127.0.0.1", 5666, nil)

	request := hostRequest(http.MethodGet, "/api/graph", "127.0.0.1:5666", "", "")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

// The CSP is the last line under an XSS, so the two directives that would turn
// one into data exfiltration are pinned by a test rather than by review.
func TestContentSecurityPolicyGrantsNoRemoteChannel(t *testing.T) {
	server, _ := twoProjectServer(t)

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, hostRequest(
		http.MethodGet, "/api/graph", "127.0.0.1:5666", "", ""))
	policy := response.Header().Get("Content-Security-Policy")
	for _, required := range []string{
		"connect-src 'self';", "form-action 'self'", "frame-src 'none'",
	} {
		if !strings.Contains(policy, required) {
			t.Fatalf("CSP %q is missing %q", policy, required)
		}
	}
	for _, forbidden := range []string{"ws:", "wss:", " http:"} {
		if strings.Contains(policy, forbidden) {
			t.Fatalf("CSP %q still allows %q", policy, forbidden)
		}
	}
}

// Without a listen address the port is unknown, so the old any-port loopback
// allowance is all that is left; this is what keeps the other tests honest.
func TestLoopbackOriginWithoutAListenAddressKeepsWorking(t *testing.T) {
	server, _ := twoProjectServer(t)

	body := `{"id":"unconfigured","title":"Node","body":""}`
	request := hostRequest(
		http.MethodPost, "/api/nodes", "127.0.0.1:5666", "http://localhost:3000", body)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}
