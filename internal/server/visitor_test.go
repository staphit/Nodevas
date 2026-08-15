package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"strings"
	"testing"
)

// The shared read-only credential these tests sign in with. The PIN is short
// on purpose — that is the point of a visitor PIN — and it is nowhere near any
// account PIN, which must be at least auth.MinPinLength characters.
const (
	visitorPin = "777"
	visitorOTP = "LOOKONLY"
)

// visitorServer is accountServerForTest with the shared credential turned on.
func visitorServer(t *testing.T) (*Server, *mailbox) {
	t.Helper()
	server, _, inbox := accountServerForTest(t)
	if err := server.SetVisitor(visitorPin, visitorOTP); err != nil {
		t.Fatalf("SetVisitor: %v", err)
	}
	return server, inbox
}

// signInAsVisitor presents both halves of the shared credential directly. No
// passcode is requested, because none is ever sent — which is the behaviour
// under test as much as it is a shortcut.
func signInAsVisitor(t *testing.T, server *Server) ([]*http.Cookie, string) {
	t.Helper()
	body := `{"pin":"` + visitorPin + `","otp":"` + visitorOTP + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("visitor login status = %d, body = %s", response.Code, response.Body)
	}
	csrf := ""
	cookies := response.Result().Cookies()
	for _, cookie := range cookies {
		if cookie.Name == auth.CSRFCookieName {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("visitor login did not issue a CSRF token")
	}
	return cookies, csrf
}

func TestVisitorSignsInWithoutAMailedPasscode(t *testing.T) {
	server, inbox := visitorServer(t)

	cookies, _ := signInAsVisitor(t, server)
	if inbox.count() != 0 {
		t.Fatalf("%d messages sent; the visitor passcode must never be mailed", inbox.count())
	}

	request := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("visitor read status = %d, body = %s", response.Code, response.Body)
	}
	// Asked of the server rather than read off the request: the middleware
	// hands the actor down on a copy of the request, so the one built here
	// never sees it. This is also the answer the UI gates on.
	if role := actorRole(t, server, cookies); role != string(identity.RoleVisitor) {
		t.Fatalf("role = %q, want %q", role, identity.RoleVisitor)
	}
}

// actorRole asks /api/auth/status who these cookies belong to.
func actorRole(t *testing.T, server *Server, cookies []*http.Cookie) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, withCookies(request, cookies))
	if response.Code != http.StatusOK {
		t.Fatalf("auth status = %d, body = %s", response.Code, response.Body)
	}
	var payload struct {
		Authenticated bool `json:"authenticated"`
		Actor         struct {
			Role string `json:"role"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth status: %v", err)
	}
	if !payload.Authenticated {
		t.Fatal("the session was not recognised")
	}
	return payload.Actor.Role
}

// Pressing the send button with the visitor PIN must look like pressing it
// with any other PIN. A different status, or a passcode arriving in a mailbox,
// would tell an unauthenticated caller which kind of PIN they just typed.
func TestVisitorPasscodeRequestIsIndistinguishable(t *testing.T) {
	server, inbox := visitorServer(t)

	for _, pin := range []string{visitorPin, "no-such-pin-at-all"} {
		body := `{"pin":"` + pin + `"}`
		request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("pin %q: status = %d, want 202", pin, response.Code)
		}
	}
	if inbox.count() != 0 {
		t.Fatalf("%d messages sent; neither PIN has a mailbox", inbox.count())
	}
}

// One visitor asking for a passcode must not sign the others out. The account
// path revokes every session for the PIN as part of issuing a new passcode,
// and a credential everybody shares cannot go through that.
func TestVisitorPasscodeRequestKeepsOtherVisitorsSignedIn(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, _ := signInAsVisitor(t, server)

	body := `{"pin":"` + visitorPin + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(httptest.NewRecorder(), request)

	read := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, read)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, the earlier visitor session was cut", response.Code)
	}
}

// The credential is off unless the operator configures it, and half of one is
// refused rather than accepted as a one-factor door.
func TestVisitorIsOffByDefault(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	body := `{"pin":"` + visitorPin + `","otp":"` + visitorOTP + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with no visitor configured", response.Code)
	}

	if err := server.SetVisitor(visitorPin, ""); err == nil {
		t.Fatal("a pin with no passcode was accepted")
	}
	if err := server.SetVisitor("", visitorOTP); err == nil {
		t.Fatal("a passcode with no pin was accepted")
	}
}

// Half the credential is no credential. This is the guess an attacker who has
// been told the PIN — which is the normal case, it is published — would make.
func TestVisitorPinAloneDoesNotSignIn(t *testing.T) {
	server, _ := visitorServer(t)

	body := `{"pin":"` + visitorPin + `","otp":"WRONGONE"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

// The visitor credential must not shadow a real account. It is checked first,
// so this asserts the account path still runs when the PIN is not the
// visitor's.
func TestVisitorCredentialDoesNotDisplaceAccounts(t *testing.T) {
	server, inbox := visitorServer(t)

	cookies, _ := signIn(t, server, inbox, testPin)
	request := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("account holder status = %d, body = %s", response.Code, response.Body)
	}
	if role := actorRole(t, server, cookies); role == string(identity.RoleVisitor) {
		t.Fatal("an account holder was signed in as a visitor")
	}
}

// The central claim: a visitor changes nothing. Deny-by-default means this
// list does not have to be exhaustive to be a real guarantee, but it covers
// one route of every shape the app serves.
func TestVisitorCannotWriteAnything(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, csrf := signInAsVisitor(t, server)

	writes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/graph", `{}`},
		{http.MethodPost, "/api/graph/ops", `{}`},
		{http.MethodPost, "/api/nodes", `{"title":"x"}`},
		{http.MethodPut, "/api/nodes/abc", `{}`},
		{http.MethodDelete, "/api/nodes/abc", ``},
		{http.MethodPost, "/api/nodes/delete", `{}`},
		{http.MethodPost, "/api/nodes/abc/duplicate", `{}`},
		{http.MethodPatch, "/api/nodes/abc/pages/1", `{}`},
		{http.MethodPost, "/api/history/restore", `{}`},
		{http.MethodPost, "/api/trash/restore", `{}`},
		{http.MethodPost, "/api/projects/open", `{}`},
		{http.MethodPost, "/api/workspaces/add", `{}`},
		{http.MethodPost, "/api/fs/mkdir", `{}`},
		{http.MethodPut, "/api/notify/settings", `{}`},
	}
	for _, write := range writes {
		request := httptest.NewRequest(write.method, write.path, strings.NewReader(write.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(auth.CSRFHeaderName, csrf)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, withCookies(request, cookies))
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", write.method, write.path, response.Code)
		}
	}
}

// Reading the whole project out in one request is not viewing it. Both export
// routes are refused even though neither changes anything, which is why the
// method check above cannot be the only rule.
func TestVisitorCannotExportOrBrowseTheHost(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, csrf := signInAsVisitor(t, server)

	refused := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/projects/export"},
		{http.MethodPost, "/api/export"},
		{http.MethodGet, "/api/fs/dirs"},
		{http.MethodGet, "/api/audit"},
		{http.MethodGet, "/api/remote/config"},
	}
	for _, call := range refused {
		request := httptest.NewRequest(call.method, call.path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(auth.CSRFHeaderName, csrf)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, withCookies(request, cookies))
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", call.method, call.path, response.Code)
		}
	}
}

// View access includes the bytes needed to render a document. Calling that
// "no downloads" would be copy protection the web cannot provide: a visitor
// can save anything the browser can display. The real boundary is that one
// visible attachment is readable while bulk export and operator APIs stay
// forbidden.
func TestVisitorMaySaveAVisibleAttachmentButCannotBulkExportOrAdminister(t *testing.T) {
	server, _ := visitorServer(t)
	st := server.pm.Store()
	id, err := st.CreateNode(&engine.Node{ID: "visitor-visible", Title: "Visible"}, "visible")
	if err != nil {
		t.Fatal(err)
	}
	name, err := st.SaveAttachment(id, "notes.txt", strings.NewReader("visible attachment"))
	if err != nil {
		t.Fatal(err)
	}
	cookies, _ := signInAsVisitor(t, server)

	attachment := withCookies(httptest.NewRequest(http.MethodGet,
		"/api/nodes/"+id+"/files/"+name, nil), cookies)
	attachmentResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(attachmentResponse, attachment)
	if attachmentResponse.Code != http.StatusOK || attachmentResponse.Body.String() != "visible attachment" {
		t.Fatalf("attachment status=%d body=%q", attachmentResponse.Code, attachmentResponse.Body.String())
	}
	if disposition := attachmentResponse.Header().Get("Content-Disposition"); !strings.Contains(disposition, "filename=notes.txt") {
		t.Fatalf("attachment disposition = %q, want visible filename", disposition)
	}

	for _, path := range []string{"/api/projects/export", "/api/audit", "/api/audit/health", "/api/fs/dirs"} {
		response := httptest.NewRecorder()
		request := withCookies(httptest.NewRequest(http.MethodGet, path, nil), cookies)
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Errorf("GET %s status = %d, want 403", path, response.Code)
		}
	}
}

// What a visitor came for still works.
func TestVisitorCanRead(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, _ := signInAsVisitor(t, server)

	for _, path := range []string{"/api/graph", "/api/projects", "/api/state", "/api/trash"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, withCookies(request, cookies))
		if response.Code == http.StatusForbidden {
			t.Errorf("GET %s was refused; a visitor must be able to read it", path)
		}
	}
}

// The credential lives in the database and is read on every attempt, so
// turning it off has to mean off now — including for the people already
// looking. A revocation that waits out a twelve-hour session TTL is not a
// revocation, and the situation an operator uses it in is one where the link
// went somewhere they did not expect.
func TestVisitorCanBeTurnedOffWhileTheServerRuns(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, _ := signInAsVisitor(t, server)

	if err := server.SetVisitor("", ""); err != nil {
		t.Fatalf("SetVisitor(off): %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, withCookies(request, cookies))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: the live session outlived the credential", response.Code)
	}

	// And nobody new gets in either.
	body := `{"pin":"` + visitorPin + `","otp":"` + visitorOTP + `"}`
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", loginResponse.Code)
	}
}

// Turning it back on works without anything being restarted, which is the
// whole reason the credential is not a process-level setting.
func TestVisitorCanBeTurnedOnWhileTheServerRuns(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	body := `{"pin":"` + visitorPin + `","otp":"` + visitorOTP + `"}`
	first := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	first.Header.Set("Content-Type", "application/json")
	firstResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 before the credential exists", firstResponse.Code)
	}

	if err := server.SetVisitor(visitorPin, visitorOTP); err != nil {
		t.Fatalf("SetVisitor(on): %v", err)
	}
	signInAsVisitor(t, server)
}

// A visitor passcode is the only half a stranger has to guess, because the PIN
// is published. A short one is refused rather than quietly accepted.
func TestVisitorPasscodeMustBeLongEnough(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	if err := server.SetVisitor(visitorPin, "SHORT"); err == nil {
		t.Fatal("a five-character visitor passcode was accepted")
	}
	if err := server.SetVisitor("77", visitorOTP); err == nil {
		t.Fatal("a two-character visitor pin was accepted")
	}
}

// Signing out is the one state change a visitor keeps. Without it a shared
// browser cannot be handed back.
func TestVisitorCanSignOut(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, csrf := signInAsVisitor(t, server)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.Header.Set(auth.CSRFHeaderName, csrf)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, withCookies(request, cookies))
	if response.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", response.Code, response.Body)
	}

	after := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	afterResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(afterResponse, withCookies(after, cookies))
	if afterResponse.Code != http.StatusUnauthorized {
		t.Fatalf("status after logout = %d, want 401", afterResponse.Code)
	}
}
