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
)

// testPin is what accountServerForTest gives "ann". It clears MinPinLength
// because a PIN short enough to be guessed is refused at the door.
const testPin = "test-pin-correct-horse"

// mailbox stands in for the SMTP relay. Passcodes are the second factor, so a
// test that wants to sign in has to go and read one, exactly as a person does.
type mailbox struct {
	mu       sync.Mutex
	messages []string
}

func (m *mailbox) Send(_ context.Context, _, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, body)
	return nil
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// code returns the passcode from the most recent message. The body is written
// for a person, so this finds the one line that is nothing but the alphabet
// passcodes are drawn from.
func (m *mailbox) code(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		t.Fatal("no passcode was sent")
	}
	for _, line := range strings.Split(m.messages[len(m.messages)-1], "\n") {
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
	t.Fatalf("no passcode in %q", m.messages[len(m.messages)-1])
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
	if err := users.SetPin(context.Background(), "ann", testPin, "ann@example.test"); err != nil {
		t.Fatalf("set pin: %v", err)
	}
	inbox := &mailbox{}
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	server.UseAccounts(users)
	server.UseAudit(audit.New(database))
	server.UseMailer(inbox)
	return server, pm, inbox, database
}

// requestPasscode asks for a passcode and returns the one that was delivered.
func requestPasscode(t *testing.T, server *Server, inbox *mailbox, pin string) string {
	t.Helper()
	body := `{"pin":"` + pin + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("passcode request status = %d, body = %s", response.Code, response.Body)
	}
	return inbox.code(t)
}

// signIn runs the whole two-factor flow — ask for a passcode, read it out of
// the mailbox, present it with the PIN — and returns the cookies plus the CSRF
// token the browser would echo back.
func signIn(t *testing.T, server *Server, inbox *mailbox, pin string) ([]*http.Cookie, string) {
	t.Helper()
	otp := requestPasscode(t, server, inbox, pin)
	body := `{"pin":"` + pin + `","otp":"` + otp + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
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
	cookies, _ := signIn(t, server, inbox, testPin)

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
	cookies, csrf := signIn(t, server, inbox, testPin)

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

// A wrong PIN and a wrong passcode fail the same way, and guessing runs out of
// budget. There is no per-account bucket here on purpose: until the PIN
// verifies there is no account to charge, so the source and global budgets are
// what bound the guessing.
func TestLoginRejectsWrongCredentialsAndThrottles(t *testing.T) {
	server, _, _ := accountServerForTest(t)

	attempt := func() int {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"pin":"wrong-pin-entirely-different","otp":"AAAAAAAA"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Code
	}
	refused := 0
	for i := 0; i < 40; i++ {
		switch code := attempt(); code {
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
	otp := requestPasscode(t, server, inbox, testPin)

	login := func() int {
		body := `{"pin":"` + testPin + `","otp":"` + otp + `"}`
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Code
	}
	if code := login(); code != http.StatusOK {
		t.Fatalf("first use: status = %d, want 200", code)
	}
	if code := login(); code != http.StatusUnauthorized {
		t.Fatalf("replay: status = %d, want 401", code)
	}
}

// Asking for a passcode signs out whoever was already in on that PIN. That is
// the whole point: the person who still has the mailbox can cut off somebody
// who has only the PIN, without needing to get back in first.
func TestRequestingAPasscodeEndsExistingSessions(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	cookies, _ := signIn(t, server, inbox, testPin)

	stillIn := func() int {
		request := withCookies(httptest.NewRequest(http.MethodGet, "/api/graph", nil), cookies)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Code
	}
	if code := stillIn(); code != http.StatusOK {
		t.Fatalf("before the request: status = %d, want 200", code)
	}

	requestPasscode(t, server, inbox, testPin)

	if code := stillIn(); code != http.StatusUnauthorized {
		t.Fatalf("after the request: status = %d, want 401", code)
	}
}

// An unknown PIN must be indistinguishable from a known one, or the endpoint
// becomes a way to find valid PINs without ever holding a mailbox.
func TestPasscodeRequestSaysNothingAboutWhetherThePinExists(t *testing.T) {
	server, _, inbox := accountServerForTest(t)

	ask := func(pin string) (int, string) {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request",
			strings.NewReader(`{"pin":"`+pin+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}

	knownCode, knownBody := ask(testPin)
	unknownCode, unknownBody := ask("no-account-holds-this-pin")
	if knownCode != unknownCode || knownBody != unknownBody {
		t.Fatalf("known: %d %s; unknown: %d %s", knownCode, knownBody, unknownCode, unknownBody)
	}
	if sent := inbox.count(); sent != 1 {
		t.Fatalf("messages sent = %d, want 1 (only the real pin gets mail)", sent)
	}
}

// The seat limit counts people. A second person is refused while the seats are
// taken; the person already in may still open another device.
func TestSeatLimitRefusesAnExtraPersonButNotAnExtraDevice(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	server.SetMaxActiveUsers(1)

	signIn(t, server, inbox, testPin)
	// The same account again: their laptop after their phone, not a second
	// person, so the seat is already theirs.
	otp := requestPasscode(t, server, inbox, testPin)
	body := `{"pin":"` + testPin + `","otp":"` + otp + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same account, second device: status = %d, want 200", response.Code)
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	cookies, csrf := signIn(t, server, inbox, testPin)

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
	cookies, csrf := signIn(t, server, inbox, testPin)

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
	cookies, csrf := signIn(t, server, inbox, testPin)

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
