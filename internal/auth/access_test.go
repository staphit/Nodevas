package auth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testAUD = "aud-tag-123"

type testKeys struct {
	mu    sync.Mutex
	keys  map[string]*rsa.PrivateKey
	calls atomic.Int32
}

func newTestKeys(t *testing.T, kids ...string) *testKeys {
	t.Helper()
	k := &testKeys{keys: map[string]*rsa.PrivateKey{}}
	for _, kid := range kids {
		k.add(t, kid)
	}
	return k
}

func (k *testKeys) add(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	k.keys[kid] = key
	k.mu.Unlock()
	return key
}

func (k *testKeys) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.calls.Add(1)
	if r.URL.Path != "/cdn-cgi/access/certs" {
		http.NotFound(w, r)
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	var out struct {
		Keys []map[string]string `json:"keys"`
	}
	for kid, key := range k.keys {
		out.Keys = append(out.Keys, map[string]string{
			"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		})
	}
	_ = json.NewEncoder(w).Encode(out)
}

// fixture is a verifier wired to a local JWKS server via a TLS test server,
// so the team domain can stay https as the verifier insists.
type fixture struct {
	keys  *testKeys
	srv   *httptest.Server
	v     *AccessVerifier
	now   time.Time
	clock func() time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{keys: newTestKeys(t, "k1"), now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	f.srv = httptest.NewTLSServer(f.keys)
	t.Cleanup(f.srv.Close)
	f.clock = func() time.Time { return f.now }
	v, err := NewAccessVerifier(AccessOptions{
		TeamDomain: f.srv.URL,
		AUD:        testAUD,
		HTTPClient: f.srv.Client(),
		Now:        func() time.Time { return f.clock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.v = v
	return f
}

func (f *fixture) claims() map[string]any {
	return map[string]any{
		"aud":   []string{testAUD},
		"iss":   f.v.Issuer(),
		"sub":   "user-1",
		"email": "me@example.com",
		"type":  "app",
		"iat":   f.now.Add(-time.Minute).Unix(),
		"nbf":   f.now.Add(-time.Minute).Unix(),
		"exp":   f.now.Add(time.Hour).Unix(),
	}
}

func segment(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head := segment(t, map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	body := segment(t, claims)
	digest := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fixture) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	f.keys.mu.Lock()
	key := f.keys.keys["k1"]
	f.keys.mu.Unlock()
	return signRS256(t, key, "k1", claims)
}

func TestAccessVerifierAcceptsValidToken(t *testing.T) {
	f := newFixture(t)
	got, err := f.v.Verify(context.Background(), f.sign(t, f.claims()))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Email != "me@example.com" || got.Type != "app" || got.Subject != "user-1" {
		t.Fatalf("claims = %+v", got)
	}
	// A string "aud" is as valid as a list.
	c := f.claims()
	c["aud"] = testAUD
	if _, err := f.v.Verify(context.Background(), f.sign(t, c)); err != nil {
		t.Fatalf("string aud: %v", err)
	}
}

func TestAccessVerifierServiceToken(t *testing.T) {
	f := newFixture(t)
	c := f.claims()
	delete(c, "email")
	c["sub"] = ""
	c["common_name"] = "client-id.access"
	got, err := f.v.Verify(context.Background(), f.sign(t, c))
	if err != nil {
		t.Fatal(err)
	}
	if got.CommonName != "client-id.access" || got.Email != "" {
		t.Fatalf("claims = %+v", got)
	}
}

func TestAccessVerifierRejects(t *testing.T) {
	f := newFixture(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)

	cases := map[string]func() string{
		"wrong aud": func() string {
			c := f.claims()
			c["aud"] = []string{"someone-else"}
			return f.sign(t, c)
		},
		"wrong iss": func() string {
			c := f.claims()
			c["iss"] = "https://evil.cloudflareaccess.com"
			return f.sign(t, c)
		},
		"expired": func() string {
			c := f.claims()
			c["exp"] = f.now.Add(-2 * time.Minute).Unix()
			return f.sign(t, c)
		},
		"no exp": func() string {
			c := f.claims()
			delete(c, "exp")
			return f.sign(t, c)
		},
		"not yet valid": func() string {
			c := f.claims()
			c["nbf"] = f.now.Add(5 * time.Minute).Unix()
			return f.sign(t, c)
		},
		"issued in future": func() string {
			c := f.claims()
			c["iat"] = f.now.Add(5 * time.Minute).Unix()
			return f.sign(t, c)
		},
		"alg none": func() string {
			return segment(t, map[string]string{"alg": "none", "kid": "k1"}) + "." + segment(t, f.claims()) + "."
		},
		"HS256 keyed with public modulus": func() string {
			head := segment(t, map[string]string{"alg": "HS256", "kid": "k1"})
			body := segment(t, f.claims())
			f.keys.mu.Lock()
			secret := f.keys.keys["k1"].N.Bytes()
			f.keys.mu.Unlock()
			mac := hmac.New(sha256.New, secret)
			mac.Write([]byte(head + "." + body))
			return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		},
		"bad signature": func() string {
			return signRS256(t, other, "k1", f.claims())
		},
		"unknown kid": func() string {
			return signRS256(t, other, "nope", f.claims())
		},
		"malformed": func() string { return "not.a-jwt" },
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := f.v.Verify(context.Background(), token())
			if !errors.Is(err, ErrAccessInvalid) {
				t.Fatalf("err = %v, want ErrAccessInvalid", err)
			}
		})
	}
	// Within the 60s leeway an expired token still passes.
	c := f.claims()
	c["exp"] = f.now.Add(-30 * time.Second).Unix()
	if _, err := f.v.Verify(context.Background(), f.sign(t, c)); err != nil {
		t.Fatalf("within leeway: %v", err)
	}
}

func TestAccessVerifierKeyCacheAndRotation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for range 3 {
		if _, err := f.v.Verify(ctx, f.sign(t, f.claims())); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.keys.calls.Load(); got != 1 {
		t.Fatalf("certs fetched %d times, want 1 (cached)", got)
	}

	// Access rotates in a new key: an unknown kid triggers one refetch, spaced
	// at least accessRefetchPeriod from the last one.
	rotated := f.keys.add(t, "k2")
	token := signRS256(t, rotated, "k2", f.claims())
	if _, err := f.v.Verify(ctx, token); !errors.Is(err, ErrAccessInvalid) {
		t.Fatalf("kid k2 right after first fetch: err = %v, want refused (refetch rate-limited)", err)
	}
	if got := f.keys.calls.Load(); got != 1 {
		t.Fatalf("certs fetched %d times, want 1 (rate limit)", got)
	}
	f.now = f.now.Add(accessRefetchPeriod + time.Second)
	if _, err := f.v.Verify(ctx, signRS256(t, rotated, "k2", f.claims())); err != nil {
		t.Fatalf("after refetch window: %v", err)
	}
	if got := f.keys.calls.Load(); got != 2 {
		t.Fatalf("certs fetched %d times, want 2", got)
	}
	// A burst of forged kids inside the window costs no extra fetches.
	for i := range 5 {
		_, _ = f.v.Verify(ctx, signRS256(t, rotated, "forged-"+string(rune('a'+i)), f.claims()))
	}
	if got := f.keys.calls.Load(); got != 2 {
		t.Fatalf("certs fetched %d times after forged kids, want 2", got)
	}
	// After the TTL the set is refreshed even for a known kid.
	f.now = f.now.Add(accessKeysTTL)
	if _, err := f.v.Verify(ctx, f.sign(t, f.claims())); err != nil {
		t.Fatal(err)
	}
	if got := f.keys.calls.Load(); got != 3 {
		t.Fatalf("certs fetched %d times after TTL, want 3", got)
	}
}

func TestAccessTokenFromHeaderAndCookie(t *testing.T) {
	f := newFixture(t)
	token := f.sign(t, f.claims())

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := f.v.VerifyRequest(r); !errors.Is(err, ErrAccessMissing) {
		t.Fatalf("no token: err = %v, want ErrAccessMissing", err)
	}
	r.AddCookie(&http.Cookie{Name: AccessCookieName, Value: token})
	if _, err := f.v.VerifyRequest(r); err != nil {
		t.Fatalf("cookie: %v", err)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(AccessHeaderName, token)
	if _, err := f.v.VerifyRequest(r); err != nil {
		t.Fatalf("header: %v", err)
	}
}

func TestNewAccessVerifierOptions(t *testing.T) {
	for _, domain := range []string{"team.cloudflareaccess.com", "https://Team.cloudflareaccess.com", "https://team.cloudflareaccess.com/"} {
		v, err := NewAccessVerifier(AccessOptions{TeamDomain: domain, AUD: "x"})
		if err != nil {
			t.Fatalf("%q: %v", domain, err)
		}
		if v.Issuer() != "https://team.cloudflareaccess.com" {
			t.Fatalf("%q: issuer %q", domain, v.Issuer())
		}
	}
	for _, bad := range []AccessOptions{
		{TeamDomain: "", AUD: "x"},
		{TeamDomain: "team.cloudflareaccess.com", AUD: ""},
		{TeamDomain: "http://team.cloudflareaccess.com", AUD: "x"},
		{TeamDomain: "https://team.cloudflareaccess.com/path", AUD: "x"},
	} {
		if _, err := NewAccessVerifier(bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestDirectLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	if !DirectLoopback(r) {
		t.Fatal("plain loopback request not direct")
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if DirectLoopback(r) {
		t.Fatal("forwarded request through a loopback proxy counted as direct")
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:5555"
	r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	if DirectLoopback(r) {
		t.Fatal("cloudflared on loopback counted as direct")
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.30.10.5:5555"
	if DirectLoopback(r) {
		t.Fatal("network peer counted as direct")
	}
	if !strings.HasPrefix(AccessCookieName, "CF_") {
		t.Fatal("cookie name changed")
	}
}
