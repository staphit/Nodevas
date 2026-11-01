package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// fromAddress sends one request from remote, as b when b is not nil.
func (s *runnerTestServer) fromAddress(t *testing.T, b *browser, remote, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = remote
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if b != nil {
		withCookies(request, b.cookies)
		request.Header.Set("X-CSRF-Token", b.csrf)
	}
	response := httptest.NewRecorder()
	s.handler.ServeHTTP(response, request)
	return response
}

// A stranger spending a second's worth of requests a minute from a few
// addresses must not shut the sign-in door, or the step-up one, on everybody
// else. The per-source, per-address and per-passcode budgets bound guessing;
// a hub-wide one only hands the stranger a lockout.
func TestSignInFloodLocksNobodyOut(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	before := s.inbox.count()
	if response := s.do(t, &bea, http.MethodPost, "/api/auth/step-up/request", `{}`, nil); response.Code != http.StatusAccepted {
		t.Fatalf("step-up request = %d %s", response.Code, response.Body)
	}
	s.inbox.waitFor(t, before+1)
	stepUpCode := s.inbox.code(t)

	for i := 0; i < 60; i++ {
		remote := fmt.Sprintf("198.51.100.%d:40000", i%3+1)
		body := fmt.Sprintf(`{"email":"x%d@nowhere.test","otp":"AAAAAAAA"}`, i)
		s.fromAddress(t, nil, remote, http.MethodPost, "/api/auth/login", body)
	}
	for i := 0; i < 60; i++ {
		remote := fmt.Sprintf("198.51.100.%d:40000", i%20+10)
		body := fmt.Sprintf(`{"email":"y%d@nowhere.test"}`, i)
		s.fromAddress(t, nil, remote, http.MethodPost, "/api/auth/otp/request", body)
	}

	if response := s.do(t, &bea, http.MethodPost, "/api/auth/step-up", `{"code":"`+stepUpCode+`"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("bea's right step-up code after the flood = %d %s", response.Code, response.Body)
	}
	before = s.inbox.count()
	if response := s.fromAddress(t, nil, "192.0.2.77:40000", http.MethodPost, "/api/auth/otp/request",
		`{"email":"cal@example.test"}`); response.Code != http.StatusAccepted {
		t.Fatalf("cal's passcode request after the flood = %d %s", response.Code, response.Body)
	}
	s.inbox.waitFor(t, before+1)
	if response := s.fromAddress(t, nil, "192.0.2.77:40000", http.MethodPost, "/api/auth/login",
		`{"email":"cal@example.test","otp":"`+s.inbox.code(t)+`"}`); response.Code != http.StatusOK {
		t.Fatalf("cal's right passcode after the flood = %d %s", response.Code, response.Body)
	}
}

// One IPv6 host holds a whole /64; a per-source budget it can step around
// by changing its last 64 bits is no budget.
func TestSignInSourceBudgetCoversAnIPv6Slash64(t *testing.T) {
	s := newRunnerTestServer(t)
	var last *httptest.ResponseRecorder
	for i := 1; i <= 21; i++ {
		remote := fmt.Sprintf("[2001:db8:1:2::%x]:40000", i)
		body := fmt.Sprintf(`{"email":"x%d@nowhere.test","otp":"AAAAAAAA"}`, i)
		last = s.fromAddress(t, nil, remote, http.MethodPost, "/api/auth/login", body)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("21st sign-in from one /64 = %d %s", last.Code, last.Body)
	}
}

// Device-link redeems by strangers are not one actor: a burst of bad codes
// from one address must not refuse a good code from another.
func TestBadRedeemsFromOneAddressDoNotBlockAnother(t *testing.T) {
	s := newRunnerTestServer(t)
	bea := s.signIn(t, "bea@example.test")
	stepUp(t, s, bea)
	_, payload := s.makeLink(t, bea, "laptop")
	for i := 0; i < 80; i++ {
		body := fmt.Sprintf(`{"code":"junk-%d"}`, i)
		s.fromAddress(t, nil, "198.51.100.40:40000", http.MethodPost, runnerwire.LinkPath, body)
	}
	body, _ := json.Marshal(runnerwire.RedeemLinkRequest{Code: payload.Code, Name: "new-laptop", OS: "linux"})
	if response := s.fromAddress(t, nil, "192.0.2.50:40000", http.MethodPost, runnerwire.LinkPath, string(body)); response.Code != http.StatusCreated {
		t.Fatalf("a good code from another address = %d %s", response.Code, response.Body)
	}
}
