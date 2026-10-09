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
// on purpose — that is the point of a visitor PIN.
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

// visitorLogin presents a PIN and passcode at the visitor's own door.
func visitorLogin(server *Server, pin, passcode string) *httptest.ResponseRecorder {
	return postAuth(server, "/api/auth/visitor", `{"pin":"`+pin+`","passcode":"`+passcode+`"}`)
}

// signInAsVisitor presents both halves of the shared credential directly. No
// passcode is requested, because none is ever sent — which is the behaviour
// under test as much as it is a shortcut.
func signInAsVisitor(t *testing.T, server *Server) ([]*http.Cookie, string) {
	t.Helper()
	response := visitorLogin(server, visitorPin, visitorOTP)
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

// The account doors do not know the visitor credential. Offered there it is a
// wrong address: the request endpoint answers the 202 every address gets and
// sends nothing, and the sign-in endpoint refuses it.
func TestAccountEndpointsDoNotAcceptTheVisitorCredential(t *testing.T) {
	server, inbox := visitorServer(t)

	for _, email := range []string{visitorPin, "nobody@example.test"} {
		askForPasscode(t, server, email)
	}
	inbox.settle()
	if inbox.count() != 0 {
		t.Fatalf("%d messages sent; neither has a mailbox", inbox.count())
	}

	body := `{"email":"` + visitorPin + `","otp":"` + visitorOTP + `"}`
	if code := postAuth(server, "/api/auth/login", body).Code; code != http.StatusUnauthorized {
		t.Fatalf("account login with the visitor credential: status = %d, want 401", code)
	}
	// The old request shape is not a back door either: the decoder refuses the
	// field it no longer knows, before anything is compared.
	legacy := `{"pin":"` + visitorPin + `","otp":"` + visitorOTP + `"}`
	if code := postAuth(server, "/api/auth/login", legacy).Code; code != http.StatusBadRequest {
		t.Fatalf("account login with the old pin shape: status = %d, want 400", code)
	}
}

// One visitor signing in must not sign the others out. The account path ends
// an account's other sessions on every sign-in, and a credential everybody
// shares cannot go through that.
func TestAVisitorSignInKeepsOtherVisitorsSignedIn(t *testing.T) {
	server, _ := visitorServer(t)
	cookies, _ := signInAsVisitor(t, server)
	signInAsVisitor(t, server)

	read := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, read)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, the earlier visitor session was cut", response.Code)
	}
}

// The sign-in screen offers the visitor door only when it leads somewhere.
func TestAuthStatusSaysWhetherVisitorAccessIsOn(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	visitor := func() bool {
		request := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		var payload struct {
			Visitor *bool `json:"visitor"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Visitor == nil {
			t.Fatalf("auth status = %s, want a visitor field", response.Body)
		}
		return *payload.Visitor
	}
	if visitor() {
		t.Fatal("visitor reported on before it was configured")
	}
	if err := server.SetVisitor(visitorPin, visitorOTP); err != nil {
		t.Fatal(err)
	}
	if !visitor() {
		t.Fatal("visitor reported off after it was configured")
	}
}

// Guessing at the visitor door runs into the same throttle as the account
// door does.
func TestVisitorGuessingIsThrottled(t *testing.T) {
	server, _ := visitorServer(t)
	for i := 0; i < 80; i++ {
		switch code := visitorLogin(server, visitorPin, "WRONGONE").Code; code {
		case http.StatusUnauthorized:
			continue
		case http.StatusTooManyRequests:
			return
		default:
			t.Fatalf("attempt %d: status = %d, want 401 or 429", i, code)
		}
	}
	t.Fatal("visitor guessing was never throttled")
}

// The credential is off unless the operator configures it, and half of one is
// refused rather than accepted as a one-factor door.
func TestVisitorIsOffByDefault(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	if code := visitorLogin(server, visitorPin, visitorOTP).Code; code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with no visitor configured", code)
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

	if code := visitorLogin(server, visitorPin, "WRONGONE").Code; code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
}

// Visitor access being on must not change what an account sign-in produces.
func TestVisitorCredentialDoesNotDisplaceAccounts(t *testing.T) {
	server, inbox := visitorServer(t)

	cookies, _ := signIn(t, server, inbox, testEmail)
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
	if code := visitorLogin(server, visitorPin, visitorOTP).Code; code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", code)
	}
}

// Turning it back on works without anything being restarted, which is the
// whole reason the credential is not a process-level setting.
func TestVisitorCanBeTurnedOnWhileTheServerRuns(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	if code := visitorLogin(server, visitorPin, visitorOTP).Code; code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 before the credential exists", code)
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
