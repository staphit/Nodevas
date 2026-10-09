package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/realtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// testEmail is the address accountServerForTest registers for "ann".
const testEmail = "ann@example.test"

// sentMail is one message the relay was handed.
type sentMail struct {
	to   string
	body string
}

// mailbox stands in for the SMTP relay. The passcode is the whole sign-in, so
// a test that wants to sign in has to go and read one, exactly as a person
// does.
//
// Delivery is asynchronous — the request endpoint answers before the relay is
// dialled, so its timing cannot say whether an address is registered — which
// is why reading the mailbox waits for a message rather than assuming one.
type mailbox struct {
	mu       sync.Mutex
	messages []sentMail
}

func (m *mailbox) Send(_ context.Context, to, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, sentMail{to: to, body: body})
	return nil
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// waitFor blocks until at least n messages have arrived.
func (m *mailbox) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d messages arrived, want %d", m.count(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settle gives any delivery still in flight time to land, for the tests that
// assert a message did not arrive.
func (m *mailbox) settle() { time.Sleep(200 * time.Millisecond) }

// last returns the most recent message.
func (m *mailbox) last(t *testing.T) sentMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		t.Fatal("no passcode was sent")
	}
	return m.messages[len(m.messages)-1]
}

// code returns the passcode from the most recent message. The body is written
// for a person, so this finds the one line that is nothing but the alphabet
// passcodes are drawn from.
func (m *mailbox) code(t *testing.T) string {
	t.Helper()
	message := m.last(t)
	for _, line := range strings.Split(message.body, "\n") {
		line = strings.TrimSpace(line)
		if len(line) != auth.OTPLength {
			continue
		}
		if strings.IndexFunc(line, func(r rune) bool {
			return !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", r)
		}) < 0 {
			return line
		}
	}
	t.Fatalf("no passcode in %q", message.body)
	return ""
}

func accountServerForTest(t *testing.T) (*Server, *project.ProjectManager, *mailbox) {
	server, pm, inbox, _ := accountServerWithAuditDBForTest(t)
	return server, pm, inbox
}

// accountServerWithAuditDBForTest exposes the audit handle only to tests that
// must deterministically toggle SQLite write availability. Product code never
// swaps or reaches through the audit store's database.
func accountServerWithAuditDBForTest(t *testing.T) (*Server, *project.ProjectManager, *mailbox, *db.DB) {
	t.Helper()
	server, pm, inbox, database, _ := accountServerPartsForTest(t)
	return server, pm, inbox, database
}

// accountServerPartsForTest also hands back the account store, for the tests
// that need a second person on the server.
func accountServerPartsForTest(t *testing.T) (*Server, *project.ProjectManager, *mailbox, *db.DB, *auth.UserStore) {
	t.Helper()
	pm := projectManagerForTest(t)
	database, err := db.Open(pm.Workspace())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	users, err := auth.NewUserStore(pm.Workspace())
	// The store holds an open SQLite handle now, and Windows will not let the
	// test's temporary directory be removed while it is open.
	t.Cleanup(func() {
		if users != nil {
			_ = users.Close()
		}
	})
	if err != nil {
		t.Fatalf("NewUserStore: %v", err)
	}
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if err := users.SetEmail(context.Background(), "ann", testEmail); err != nil {
		t.Fatalf("set email: %v", err)
	}
	inbox := &mailbox{}
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	server.UseAccounts(users)
	server.UseAudit(audit.New(database))
	server.UseMailer(inbox)
	return server, pm, inbox, database, users
}

// postAuth sends one unauthenticated JSON request and returns the response.
func postAuth(server *Server, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

// askForPasscode posts to the request endpoint and checks for the 202 every
// address gets, without waiting for any mail.
func askForPasscode(t *testing.T, server *Server, email string) *httptest.ResponseRecorder {
	t.Helper()
	response := postAuth(server, "/api/auth/otp/request", `{"email":"`+email+`"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("passcode request status = %d, body = %s", response.Code, response.Body)
	}
	return response
}

// requestPasscode asks for a passcode and returns the one that was delivered.
func requestPasscode(t *testing.T, server *Server, inbox *mailbox, email string) string {
	t.Helper()
	before := inbox.count()
	askForPasscode(t, server, email)
	inbox.waitFor(t, before+1)
	return inbox.code(t)
}

// login presents an address and a passcode to the sign-in endpoint.
func login(server *Server, email, otp string) *httptest.ResponseRecorder {
	return postAuth(server, "/api/auth/login", `{"email":"`+email+`","otp":"`+otp+`"}`)
}

// signIn runs the whole flow — ask for a passcode, read it out of the mailbox,
// present it with the address — and returns the cookies plus the CSRF token the
// browser would echo back.
func signIn(t *testing.T, server *Server, inbox *mailbox, email string) ([]*http.Cookie, string) {
	t.Helper()
	otp := requestPasscode(t, server, inbox, email)
	response := login(server, email, otp)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body)
	}
	cookies := response.Result().Cookies()
	csrf := ""
	for _, cookie := range cookies {
		if cookie.Name == auth.CSRFCookieName {
			csrf = cookie.Value
		}
		if cookie.Name == auth.SessionCookieName && !cookie.HttpOnly {
			t.Fatal("the session cookie must be HttpOnly")
		}
		if cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie %q is not SameSite=Strict", cookie.Name)
		}
	}
	if csrf == "" {
		t.Fatal("login did not issue a CSRF token")
	}
	return cookies, csrf
}

func withCookies(request *http.Request, cookies []*http.Cookie) *http.Request {
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request
}

// A loopback server keeps working exactly as before: no login, no tokens.
func TestLocalServerNeedsNoCredentials(t *testing.T) {
	server, _ := twoProjectServer(t)

	request := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if actor := auth.ActorFrom(request); actor != identity.Local {
		t.Fatalf("actor = %+v, want the local actor", actor)
	}
}

func TestAccountsServerRefusesAnonymousAPI(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	request := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}

	// The UI itself must still load, or there is nowhere to type a password.
	page := httptest.NewRequest(http.MethodGet, "/", nil)
	pageResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(pageResponse, page)
	if pageResponse.Code == http.StatusUnauthorized {
		t.Fatal("the login page is behind the login")
	}
}

func TestAccountsServerAcceptsSignedInReads(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	cookies, _ := signIn(t, server, inbox, testEmail)

	request := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

// A session cookie alone must not be enough to write: that is exactly what a
// cross-site form post would carry.
func TestAccountsServerRequiresCSRFTokenForWrites(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	cookies, csrf := signIn(t, server, inbox, testEmail)

	body := `{"id":"added","title":"Added","body":""}`
	request := withCookies(
		httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(body)), cookies)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("without a token: status = %d, body = %s", response.Code, response.Body)
	}

	withToken := withCookies(
		httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(body)), cookies)
	withToken.Header.Set("Content-Type", "application/json")
	withToken.Header.Set(auth.CSRFHeaderName, csrf)
	tokenResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(tokenResponse, withToken)
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("with a token: status = %d, body = %s", tokenResponse.Code, tokenResponse.Body)
	}
}

// An unknown address and a wrong passcode fail the same way, and guessing runs
// out of budget. The per-address bucket is charged for addresses nobody
// registered too, so being throttled says nothing about whether one exists.
func TestLoginRejectsWrongCredentialsAndThrottles(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	refused := 0
	for i := 0; i < 40; i++ {
		switch code := login(server, "nobody@example.test", "AAAAAAAA").Code; code {
		case http.StatusUnauthorized:
			continue
		case http.StatusTooManyRequests:
			refused = i
		default:
			t.Fatalf("attempt %d: status = %d, want 401 or 429", i, code)
		}
		break
	}
	if refused == 0 {
		t.Fatal("guessing was never throttled")
	}
}

// The passcode is single use: presenting it a second time must fail even
// though it was correct a moment ago.
func TestPasscodeCannotBeUsedTwice(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	otp := requestPasscode(t, server, inbox, testEmail)

	if code := login(server, testEmail, otp).Code; code != http.StatusOK {
		t.Fatalf("first use: status = %d, want 200", code)
	}
	if code := login(server, testEmail, otp).Code; code != http.StatusUnauthorized {
		t.Fatalf("replay: status = %d, want 401", code)
	}
}

// The two halves of the session rule, end to end. Asking for a passcode needs
// nothing secret, so it must not sign anybody out; completing a sign-in does,
// leaving the new device the only one signed in. The seat limit is set to one
// on purpose: the person's own second sign-in is not a second person, and the
// revocation must not cost them the seat they are signing in with.
//
// One test rather than two because the production path refuses a resend for
// 30 seconds, and this is the one place the suite pays that wait.
func TestRequestingKeepsSessionsAndSigningInEndsTheOthers(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	server.SetMaxActiveUsers(1)
	oldCookies, _ := signIn(t, server, inbox, testEmail)

	status := func(cookies []*http.Cookie) int {
		request := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Code
	}
	if code := status(oldCookies); code != http.StatusOK {
		t.Fatalf("before the request: status = %d, want 200", code)
	}

	time.Sleep(30 * time.Second)
	otp := requestPasscode(t, server, inbox, testEmail)
	if code := status(oldCookies); code != http.StatusOK {
		t.Fatalf("after a passcode request: status = %d, want 200 — requesting signed the session out", code)
	}

	response := login(server, testEmail, otp)
	if response.Code != http.StatusOK {
		t.Fatalf("second sign-in on a full server: status = %d, body = %s", response.Code, response.Body)
	}
	newCookies := response.Result().Cookies()
	if code := status(oldCookies); code != http.StatusUnauthorized {
		t.Fatalf("earlier session after a new sign-in: status = %d, want 401", code)
	}
	if code := status(newCookies); code != http.StatusOK {
		t.Fatalf("new session: status = %d, want 200", code)
	}
}

// An unregistered address must be indistinguishable from a registered one, or
// the endpoint becomes a directory of who has an account.
func TestPasscodeRequestSaysNothingAboutWhetherTheAddressExists(t *testing.T) {
	server, _, inbox := accountServerForTest(t)

	// Three requests in all: the per-source budget allows exactly that many a
	// minute, and a 429 here would be the budget talking, not the address.
	known := askForPasscode(t, server, testEmail)
	for _, other := range []string{"nobody@example.test", "not an address"} {
		unknown := askForPasscode(t, server, other)
		if known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
			t.Fatalf("known: %d %s; %q: %d %s",
				known.Code, known.Body, other, unknown.Code, unknown.Body)
		}
	}
	inbox.waitFor(t, 1)
	inbox.settle()
	if sent := inbox.count(); sent != 1 {
		t.Fatalf("messages sent = %d, want 1 (only the registered address gets mail)", sent)
	}
}

// The recipient is the address on the account, never the string in the
// request: however the person types it at the door, the mail goes where the
// administrator registered it.
func TestThePasscodeIsMailedToTheRegisteredAddress(t *testing.T) {
	server, _, inbox, _, users := accountServerPartsForTest(t)
	if err := users.AddWithEmail(context.Background(),
		"bea", "correct-horse-battery", "", "Bea.Mixed@Example.test"); err != nil {
		t.Fatal(err)
	}

	otp := requestPasscode(t, server, inbox, "  bea.mixed@EXAMPLE.TEST ")
	if to := inbox.last(t).to; to != "Bea.Mixed@Example.test" {
		t.Fatalf("recipient = %q, want the registered address", to)
	}
	if code := login(server, "BEA.MIXED@example.test", otp).Code; code != http.StatusOK {
		t.Fatalf("sign-in with the address in another case: status = %d", code)
	}

	// And Ann's passcode goes to Ann.
	requestPasscode(t, server, inbox, testEmail)
	if to := inbox.last(t).to; to != testEmail {
		t.Fatalf("recipient = %q, want %q", to, testEmail)
	}
}

// The seat limit counts people. A second person is refused while the seats are
// taken, with the 409 that says the server is full rather than that the
// credentials were wrong. (The same person signing in again is covered by
// TestRequestingKeepsSessionsAndSigningInEndsTheOthers.)
func TestSeatLimitRefusesAnExtraPerson(t *testing.T) {
	server, _, inbox, _, users := accountServerPartsForTest(t)
	if err := users.AddWithEmail(context.Background(),
		"bea", "correct-horse-battery", "", "bea@example.test"); err != nil {
		t.Fatal(err)
	}
	server.SetMaxActiveUsers(1)

	signIn(t, server, inbox, testEmail)
	otp := requestPasscode(t, server, inbox, "bea@example.test")
	if code := login(server, "bea@example.test", otp).Code; code != http.StatusConflict {
		t.Fatalf("second person on a full server: status = %d, want 409", code)
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	cookies, csrf := signIn(t, server, inbox, testEmail)

	logout := withCookies(httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil), cookies)
	logout.Header.Set(auth.CSRFHeaderName, csrf)
	logoutResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", logoutResponse.Code, logoutResponse.Body)
	}

	after := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	afterResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(afterResponse, after)
	if afterResponse.Code != http.StatusUnauthorized {
		t.Fatalf("status after logout = %d, want 401", afterResponse.Code)
	}
}

// An account is permission to edit projects, not to browse the server's disk.
func TestNetworkedServerRefusesFilesystemBrowsing(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	cookies, csrf := signIn(t, server, inbox, testEmail)

	dirs := withCookies(httptest.NewRequest(http.MethodGet, "/api/fs/dirs?path=", nil), cookies)
	dirsResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(dirsResponse, dirs)
	if dirsResponse.Code != http.StatusForbidden {
		t.Fatalf("fs/dirs status = %d, want 403", dirsResponse.Code)
	}

	body := strings.NewReader(`{"path":"` + "/tmp" + `","name":"x"}`)
	mkdir := withCookies(
		httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", body), cookies)
	mkdir.Header.Set("Content-Type", "application/json")
	mkdir.Header.Set(auth.CSRFHeaderName, csrf)
	mkdirResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(mkdirResponse, mkdir)
	if mkdirResponse.Code != http.StatusForbidden {
		t.Fatalf("fs/mkdir status = %d, want 403", mkdirResponse.Code)
	}
}

func TestWritesRecordTheActor(t *testing.T) {
	server, pm, inbox := accountServerForTest(t)
	cookies, csrf := signIn(t, server, inbox, testEmail)

	body := `{"id":"audited","title":"Audited","body":""}`
	request := withCookies(
		httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(body)), cookies)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(auth.CSRFHeaderName, csrf)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}

	entries, err := server.audit.Query(context.Background(), audit.Filter{Project: pm.Store().Root()})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the write left no audit entry")
	}
	last := entries[0]
	if last.ActorName != "ann" || !strings.Contains(last.Action, "nodes") {
		t.Fatalf("audit entry = %+v, want ann writing nodes", last)
	}

	// Reads are not writes: they must not fill the trail.
	read := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
	server.Handler().ServeHTTP(httptest.NewRecorder(), read)
	after, err := server.audit.Query(context.Background(), audit.Filter{Project: pm.Store().Root()})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(entries) {
		t.Fatalf("audit grew on a read: %d -> %d", len(entries), len(after))
	}
}
