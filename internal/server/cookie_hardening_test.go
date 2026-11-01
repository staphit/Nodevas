package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessionCookieOf is the value of the cookie named name among cookies.
func sessionCookieOf(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// A sibling subdomain can set a cookie of the same name for the parent
// domain, with a longer path, which the browser sends first. Whichever one
// the server read, the person would be working in somebody else's account,
// so a request carrying two session cookies is refused.
func TestTwoSessionCookiesAreRefused(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	cal := s.signIn(t, "cal@example.test")
	request := httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	request.Header.Set("Cookie", "nodevas_session="+sessionCookieOf(cal.cookies, "nodevas_session")+
		"; nodevas_session="+sessionCookieOf(bea.cookies, "nodevas_session")+"; nodevas_csrf="+bea.csrf)
	response := httptest.NewRecorder()
	s.handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("two session cookies = %d %s", response.Code, response.Body)
	}
}

// Over TLS the session and CSRF cookies carry the __Host- prefix, which a
// browser accepts only Secure, for Path=/ and without a Domain: no sibling
// can set one. A plain-named session cookie on a TLS request is somebody
// else's, and counts for nothing.
func TestSecureSignInUsesHostPrefixedCookies(t *testing.T) {
	s := newRunnerTestServer(t)
	post := func(path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "https://hub.example.test"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		withCookies(request, cookies)
		response := httptest.NewRecorder()
		s.handler.ServeHTTP(response, request)
		return response
	}
	before := s.inbox.count()
	if response := post("/api/auth/otp/request", `{"email":"bea@example.test"}`, nil); response.Code != http.StatusAccepted {
		t.Fatalf("passcode request = %d", response.Code)
	}
	s.inbox.waitFor(t, before+1)
	login := post("/api/auth/login", `{"email":"bea@example.test","otp":"`+s.inbox.code(t)+`"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	for _, name := range []string{"__Host-nodevas_session", "__Host-nodevas_csrf"} {
		var found *http.Cookie
		for _, cookie := range cookies {
			if cookie.Name == name {
				found = cookie
			}
		}
		if found == nil || !found.Secure || found.Path != "/" || found.Domain != "" || found.Value == "" {
			t.Fatalf("cookie %s = %+v, want Secure, Path=/, no Domain", name, found)
		}
	}
	get := func(cookie string) int {
		request := httptest.NewRequest(http.MethodGet, "https://hub.example.test/api/runners", nil)
		request.Header.Set("Cookie", cookie)
		response := httptest.NewRecorder()
		s.handler.ServeHTTP(response, request)
		return response.Code
	}
	session := sessionCookieOf(cookies, "__Host-nodevas_session")
	if code := get("__Host-nodevas_session=" + session); code != http.StatusOK {
		t.Fatalf("request with the __Host- session = %d", code)
	}
	cal := s.signIn(t, "cal@example.test")
	tossed := sessionCookieOf(cal.cookies, "nodevas_session")
	if code := get("nodevas_session=" + tossed); code != http.StatusUnauthorized {
		t.Fatalf("a plain session cookie over TLS = %d, want 401", code)
	}
}
