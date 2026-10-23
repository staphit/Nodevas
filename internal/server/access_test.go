package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/realtime"
)

type accessFixture struct {
	key *rsa.PrivateKey
	v   *auth.AccessVerifier
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(certs.Close)
	v, err := auth.NewAccessVerifier(auth.AccessOptions{
		TeamDomain: certs.URL,
		AUD:        "aud-1",
		HTTPClient: certs.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &accessFixture{key: key, v: v}
}

func (f *accessFixture) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	head := enc(map[string]string{"alg": "RS256", "kid": "k1"})
	body := enc(claims)
	digest := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *accessFixture) validToken(t *testing.T) string {
	now := time.Now()
	return f.token(t, map[string]any{
		"aud": []string{"aud-1"}, "iss": f.v.Issuer(), "email": "me@example.com",
		"type": "app", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
}

func accessRouter(f *accessFixture, required bool, seen *string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AccessMiddleware(f.v, required))
	handler := func(c *gin.Context) {
		if seen != nil {
			*seen = auth.AccessEmailFrom(c.Request.Context())
		}
		c.String(http.StatusOK, "ok")
	}
	r.GET("/api/state", handler)
	r.GET("/api/ws", handler)
	return r
}

func serveAccess(r http.Handler, req *http.Request) int {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

// fromEdge is a request as cloudflared delivers it: from the tunnel network,
// with the client's address in the forwarding headers.
func fromEdge(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "172.30.10.2:41000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("Cf-Connecting-Ip", "203.0.113.7")
	return req
}

func TestAccessMiddlewareRequired(t *testing.T) {
	f := newAccessFixture(t)
	var seen string
	r := accessRouter(f, true, &seen)

	if code := serveAccess(r, fromEdge("/api/state")); code != http.StatusForbidden {
		t.Fatalf("no token: %d, want 403", code)
	}
	bad := fromEdge("/api/state")
	bad.Header.Set(auth.AccessHeaderName, "garbage.token.here")
	if code := serveAccess(r, bad); code != http.StatusForbidden {
		t.Fatalf("bad token: %d, want 403", code)
	}

	good := fromEdge("/api/state")
	good.Header.Set(auth.AccessHeaderName, f.validToken(t))
	if code := serveAccess(r, good); code != http.StatusOK {
		t.Fatalf("valid token: %d, want 200", code)
	}
	if seen != "me@example.com" {
		t.Fatalf("access identity on context = %q", seen)
	}
}

func TestAccessMiddlewareBlocksWebSocketUpgradeWithoutToken(t *testing.T) {
	f := newAccessFixture(t)
	r := accessRouter(f, true, nil)
	req := fromEdge("/api/ws")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if code := serveAccess(r, req); code != http.StatusForbidden {
		t.Fatalf("ws upgrade without token: %d, want 403", code)
	}
	// The browser's Access cookie is what a WebSocket carries.
	req.AddCookie(&http.Cookie{Name: auth.AccessCookieName, Value: f.validToken(t)})
	if code := serveAccess(r, req); code != http.StatusOK {
		t.Fatalf("ws upgrade with cookie: %d, want 200", code)
	}
}

func TestAccessMiddlewareAuditMode(t *testing.T) {
	f := newAccessFixture(t)
	r := accessRouter(f, false, nil)
	if code := serveAccess(r, fromEdge("/api/state")); code != http.StatusOK {
		t.Fatalf("audit mode without token: %d, want 200", code)
	}
}

func TestAccessMiddlewareLoopbackExemption(t *testing.T) {
	f := newAccessFixture(t)
	r := accessRouter(f, true, nil)

	direct := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	direct.RemoteAddr = "127.0.0.1:50000"
	if code := serveAccess(r, direct); code != http.StatusOK {
		t.Fatalf("direct loopback: %d, want 200 (healthcheck, local mcp)", code)
	}

	// cloudflared beside the server on loopback, a trusted proxy, still has
	// to present Access's token.
	if err := auth.SetTrustedProxyCIDRs([]string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = auth.SetTrustedProxyCIDRs([]string{"127.0.0.1/32", "::1/128"}) })
	proxied := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	proxied.RemoteAddr = "127.0.0.1:50001"
	proxied.Header.Set("X-Forwarded-For", "203.0.113.7")
	if code := serveAccess(r, proxied); code != http.StatusForbidden {
		t.Fatalf("proxied via loopback: %d, want 403", code)
	}
}

func TestAccessMiddlewareNilVerifierPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AccessMiddleware(nil, true))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if code := serveAccess(r, fromEdge("/")); code != http.StatusNoContent {
		t.Fatalf("nil verifier: %d", code)
	}
}

// The full handler, not just the middleware: Access sits in the router chain,
// ahead of sign-in, for API routes and the static app alike.
func TestHandlerEnforcesAccessWhenRequired(t *testing.T) {
	f := newAccessFixture(t)
	srv := serverForTest(t, projectManagerForTest(t), realtime.NewHub(), nil)
	srv.UseAccess(f.v, true)
	handler := srv.Handler()

	get := func(path, token string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.7:4242"
		if token != "" {
			req.Header.Set("Cf-Access-Jwt-Assertion", token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/api/auth/status", "/api/state", "/"} {
		if code := get(path, ""); code != http.StatusForbidden {
			t.Errorf("GET %s without Access JWT = %d, want 403", path, code)
		}
	}
	if code := get("/api/auth/status", f.validToken(t)); code == http.StatusForbidden {
		t.Errorf("GET /api/auth/status with a valid Access JWT = 403")
	}
}

// A runner reaches the hub with an Access service token: the edge swaps the
// client ID and secret for a JWT that names the token, not a person.
func TestAccessMiddlewareAcceptsServiceToken(t *testing.T) {
	f := newAccessFixture(t)
	var seen string
	r := accessRouter(f, true, &seen)
	now := time.Now()
	req := fromEdge("/api/state")
	req.Header.Set(auth.AccessHeaderName, f.token(t, map[string]any{
		"aud": []string{"aud-1"}, "iss": f.v.Issuer(), "common_name": "runner-id.access",
		"type": "app", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}))
	if code := serveAccess(r, req); code != http.StatusOK {
		t.Fatalf("service token: %d, want 200", code)
	}
	if seen != "service:runner-id.access" {
		t.Fatalf("access identity = %q", seen)
	}
}
