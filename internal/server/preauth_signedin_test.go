package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// cookieAuth signs in a request whose session cookie is "good".
type cookieAuth struct{}

func (cookieAuth) Authenticate(r *http.Request) (identity.Actor, error) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil && cookie.Value == "good" {
		return identity.Actor{ID: "alice", Name: "alice", Role: identity.RoleMember}, nil
	}
	return identity.Actor{}, auth.ErrUnauthenticated
}
func (cookieAuth) NeedsCSRF() bool { return false }
func (cookieAuth) Remote() bool    { return true }

func preAuthRouter(authn auth.Authenticator) *gin.Engine {
	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		PreAuthGlobalRate: 1000, PreAuthGlobalBurst: 1000,
		PreAuthIPRate: 0.001, PreAuthIPBurst: 3,
		ReadGlobalRate: 1000, ReadGlobalBurst: 1000,
		ReadIPRate: 1000, ReadIPBurst: 1000,
		ReadActorRate: 1000, ReadActorBurst: 1000,
	})
	srv := &Server{abuse: guard, auth: authn}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(srv.preAuthAbuseLimits, srv.withAuth, srv.withAbuseLimits)
	router.GET("/api/state", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/api/auth/status", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

func preAuthGet(router http.Handler, path, peer, cookie string) int {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = peer + ":40000"
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code
}

// The pre-auth bucket is for traffic with no identity. A signed-in person is
// held to the per-actor classes instead, so people who share an address
// (an office, or a proxy whose network is missing from --trusted-proxy) do
// not share one 40-request-a-second bucket.
func TestPreAuthBucketSkipsSignedInTraffic(t *testing.T) {
	t.Parallel()
	router := preAuthRouter(cookieAuth{})
	const shared = "198.51.100.20"

	// Anonymous traffic from the address uses up its pre-auth bucket.
	codes := []int{}
	for range 5 {
		codes = append(codes, preAuthGet(router, "/api/auth/status", shared, ""))
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Fatalf("anonymous requests got %v, want 429 once the bucket of 3 is spent", codes)
	}
	// A signed-in person on the same address is not refused by it.
	for i := range 20 {
		if code := preAuthGet(router, "/api/state", shared, "good"); code != http.StatusOK {
			t.Fatalf("signed-in request %d from a shared address got %d, want 200", i, code)
		}
	}
}

// A credential that does not sign in is unauthenticated traffic: it is
// charged to the pre-auth bucket once it fails, so a made-up cookie is not a
// way around the limit.
func TestPreAuthBucketChargesARefusedCredential(t *testing.T) {
	t.Parallel()
	router := preAuthRouter(cookieAuth{})
	codes := []int{}
	for range 6 {
		codes = append(codes, preAuthGet(router, "/api/state", "198.51.100.21", "forged"))
	}
	for i, code := range codes[:3] {
		if code != http.StatusUnauthorized {
			t.Fatalf("forged cookie %d got %d, want 401 (all: %v)", i, code, codes)
		}
	}
	for i, code := range codes[3:] {
		if code != http.StatusTooManyRequests {
			t.Fatalf("forged cookie %d past the bucket got %d, want 429 (all: %v)", i+3, code, codes)
		}
	}
	// Sign-in endpoints are charged up front whatever cookie they carry:
	// their only limit is this bucket.
	codes = codes[:0]
	for range 5 {
		codes = append(codes, preAuthGet(router, "/api/auth/status", "198.51.100.22", "forged"))
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Fatalf("public auth path with a cookie got %v, want 429 once the bucket of 3 is spent", codes)
	}
}

// The person at a standalone hub's keyboard is signed in by being there.
func TestPreAuthBucketSkipsTheDirectLocalUser(t *testing.T) {
	t.Parallel()
	router := preAuthRouter(auth.LocalOnly{})
	for i := range 20 {
		if code := preAuthGet(router, "/api/state", "127.0.0.1", ""); code != http.StatusOK {
			t.Fatalf("local request %d got %d, want 200", i, code)
		}
	}
}

// A request that carries X-Forwarded-For from a peer that is not a trusted
// proxy is either a proxy missing from --trusted-proxy (every user then
// shares the proxy's buckets) or a client making it up. Either way the
// operator hears about it, once per peer.
func TestForwardedHeaderFromAnUntrustedPeerIsReportedOncePerPeer(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var noted []string
	srv := &Server{abuse: newAbuseGuard(), forwarding: newForwardingNotes(func(peer string) {
		mu.Lock()
		noted = append(noted, peer)
		mu.Unlock()
	})}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(srv.preAuthAbuseLimits)
	router.GET("/api/state", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/assets/app.js", func(c *gin.Context) { c.Status(http.StatusOK) })

	send := func(path, peer string, forwarded bool) {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = peer + ":40000"
		if forwarded {
			request.Header.Set("X-Forwarded-For", "203.0.113.50")
		}
		router.ServeHTTP(httptest.NewRecorder(), request)
	}
	send("/api/state", "172.31.250.9", false)
	if len(noted) != 0 {
		t.Fatalf("a request without forwarding headers was reported: %v", noted)
	}
	for range 3 {
		send("/api/state", "172.31.250.10", true)
		send("/assets/app.js", "172.31.250.10", true)
	}
	send("/api/state", "172.31.250.11", true)
	if len(noted) != 2 || noted[0] != "172.31.250.10" || noted[1] != "172.31.250.11" {
		t.Fatalf("reported %v, want each untrusted peer once", noted)
	}

	// A client that makes the header up can come from anywhere; what is
	// remembered stays bounded.
	for i := range 2 * maxForwardingNotes {
		send("/api/state", "10.9."+strconv.Itoa(i/250)+"."+strconv.Itoa(i%250+1), true)
	}
	if len(noted) > maxForwardingNotes+1 {
		t.Fatalf("reported %d peers, want at most %d", len(noted), maxForwardingNotes+1)
	}
}
