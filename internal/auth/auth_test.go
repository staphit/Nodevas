package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/identity"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	encoded, err := hashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "correct-horse") {
		t.Fatal("the stored hash contains the password")
	}
	ok, err := verifyPassword(encoded, "correct-horse-battery")
	if err != nil || !ok {
		t.Fatalf("verify = %v, %v; want true", ok, err)
	}
	ok, err = verifyPassword(encoded, "correct-horse-batterY")
	if err != nil || ok {
		t.Fatalf("verify of a wrong password = %v, %v; want false", ok, err)
	}
}

func TestUserStoreRejectsWeakAndDuplicateAccounts(t *testing.T) {
	workspace := t.TempDir()
	users, err := NewUserStore(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = users.Close() })
	if err := users.Add(context.Background(), "ann", "short"); err == nil {
		t.Fatal("a short password was accepted")
	}
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := users.Add(context.Background(), "ANN", "another-long-password"); err == nil {
		t.Fatal("a name differing only in case was accepted")
	}
	if actor, ok := users.Verify(context.Background(), "ann", "correct-horse-battery"); !ok {
		t.Fatal("the account cannot sign in")
	} else if actor.Role != identity.RoleAdmin {
		t.Fatalf("first account role = %q, want admin", actor.Role)
	}
	if _, ok := users.Verify(context.Background(), "nobody", "correct-horse-battery"); ok {
		t.Fatal("an unknown account signed in")
	}

	// Credentials on disk must not be readable by other accounts on the host.
	path := db.PathFor(workspace)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeIsUnix() && info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "correct-horse-battery") {
		t.Fatal("the database holds the plaintext password")
	}
	records := users.Records(context.Background())
	if len(records) != 1 || records[0].Name != "ann" {
		t.Fatalf("records = %+v", records)
	}
}

func runtimeIsUnix() bool { return runtime.GOOS != "windows" }

// X-Forwarded-Proto is client input until an operator says a proxy rewrites
// it: believed by default, a plaintext caller can claim TLS it never had.
func TestRequestIsSecureIgnoresForwardedProtoUnlessTrusted(t *testing.T) {
	cases := []struct {
		name    string
		trusted bool
		header  string
		want    bool
	}{
		{name: "untrusted proxy header", header: "https", want: false},
		{name: "untrusted proxy header list", header: "https, http", want: false},
		{name: "trusted proxy header", trusted: true, header: "https", want: true},
		{name: "trusted proxy header list", trusted: true, header: "https, http", want: false},
		{name: "trusted plaintext hop", trusted: true, header: "http", want: false},
		{name: "no header at all", trusted: true, want: false},
		{name: "untrusted peer with proxy enabled", trusted: true, header: "https", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// The toggle is process-wide, so put it back before the next test
			// inherits a proxy that is not there.
			t.Cleanup(func() { TrustForwardedProto(false) })
			TrustForwardedProto(testCase.trusted)

			request := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
			if testCase.trusted && testCase.name != "untrusted peer with proxy enabled" {
				request.RemoteAddr = "127.0.0.1:12345"
			}
			if testCase.header != "" {
				request.Header.Set("X-Forwarded-Proto", testCase.header)
			}
			if got := RequestIsSecure(request); got != testCase.want {
				t.Fatalf("RequestIsSecure = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestClientIPUsesOnlyTrustedForwardingChain(t *testing.T) {
	t.Cleanup(func() { TrustForwardedProto(false) })
	if err := SetTrustedProxyCIDRs([]string{"10.0.0.0/8", "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:443"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.3")
	if got := ClientIP(request); got != "198.51.100.7" {
		t.Fatalf("ClientIP = %q, want original untrusted client", got)
	}
	request.Header.Set("X-Forwarded-For", "198.51.100.7, malformed")
	if got := ClientIP(request); got != "10.0.0.2" {
		t.Fatalf("ClientIP malformed chain = %q, want trusted proxy bucket", got)
	}

	request.RemoteAddr = "203.0.113.9:443"
	if got := ClientIP(request); got != "203.0.113.9" {
		t.Fatalf("ClientIP trusted spoof = %q, want direct peer", got)
	}
}

func TestRequireAdminFailsClosedWithoutAttachedActor(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	if !errors.Is(RequireAdmin(request), ErrAdminRequired) {
		t.Fatal("request without authentication context inherited local admin")
	}
	request = request.WithContext(WithActor(request.Context(), identity.Actor{
		ID: "admin", Name: "admin", Role: identity.RoleAdmin,
	}))
	if err := RequireAdmin(request); err != nil {
		t.Fatalf("attached administrator rejected: %v", err)
	}
}

func TestStillAuthorizedUsesLiveRevalidator(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	allowed := true
	request = request.WithContext(WithRevalidator(request.Context(), func() bool { return allowed }))
	if !StillAuthorized(request) {
		t.Fatal("live authorization rejected a valid session")
	}
	allowed = false
	if StillAuthorized(request) {
		t.Fatal("live authorization accepted a revoked session")
	}
}

func TestSessionRevokedAfterExternalRoleChange(t *testing.T) {
	workspace := t.TempDir()
	users, err := NewUserStore(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = users.Close() })
	if err := users.AddWithRole(context.Background(), "admin", "correct-horse-battery", identity.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := users.AddWithRole(context.Background(), "member", "correct-horse-battery", identity.RoleMember); err != nil {
		t.Fatal(err)
	}
	var memberID string
	for _, record := range users.Records(context.Background()) {
		if record.Name == "member" {
			memberID = record.ID
		}
	}
	revision, ok := users.Revision(context.Background(), memberID)
	if !ok {
		t.Fatal("member revision missing")
	}
	authenticator := NewSessionAuth(users)
	// Keyed by the token's digest, the way the cookie will be looked up.
	authenticator.sessions[sessionKey("token")] = &Session{
		actor:    identity.Actor{ID: memberID, Name: "member", Role: identity.RoleMember},
		revision: revision,
		created:  time.Now(),
		expires:  time.Now().Add(time.Hour),
	}

	external, err := NewUserStore(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = external.Close() })
	if err := external.SetRole(context.Background(), "member", identity.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "token"})
	if _, err := authenticator.Authenticate(request); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate after role change = %v, want unauthenticated", err)
	}
}

func TestLoginLimiterBoundsRotatingInputs(t *testing.T) {
	authenticator := NewSessionAuth(&UserStore{})
	for index := 0; index < loginIPLimit; index++ {
		if !authenticator.allowLogin(fmt.Sprintf("user%d", index), "192.0.2.1") {
			t.Fatalf("IP attempt %d rejected early", index)
		}
	}
	if authenticator.allowLogin("next", "192.0.2.1") {
		t.Fatal("IP limit was bypassed with a rotated username")
	}
	if len(authenticator.attempts) > maxLoginBuckets {
		t.Fatalf("attempt buckets = %d, max %d", len(authenticator.attempts), maxLoginBuckets)
	}

	other := NewSessionAuth(&UserStore{})
	for index := 0; index < loginGlobalLimit; index++ {
		if !other.allowLogin(fmt.Sprintf("rotate%d", index), fmt.Sprintf("198.51.100.%d", index)) {
			t.Fatalf("global attempt %d rejected early", index)
		}
	}
	if other.allowLogin("rotated", "203.0.113.1") {
		t.Fatal("global limit was bypassed by rotating both username and IP")
	}
	if len(other.attempts) > maxLoginBuckets {
		t.Fatalf("attempt buckets = %d, max %d", len(other.attempts), maxLoginBuckets)
	}
}
