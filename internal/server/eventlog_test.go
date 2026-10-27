package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/logging"
	"nodevas/internal/logging/logtest"
)

// loggedHandler is the server's handler inside the request log, the way
// cmd/nodevas serves it, so a test sees the request lines and the event lines
// together. Build it after logtest.Capture: the request log writes to the
// default logger of the moment.
func loggedHandler(server *Server) http.Handler {
	return logging.MiddlewareWithOptions(logging.Options{ClientIP: auth.ClientIP, Quiet: QuietRequest})(server.Handler())
}

func TestSignInAndSignOutReachTheLog(t *testing.T) {
	server, _, inbox := accountServerForTest(t)
	logs := logtest.Capture(t)
	handler := loggedHandler(server)
	send := func(method, path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.RemoteAddr = "203.0.113.7:4242"
		request.Header.Set("User-Agent", "Mozilla/5.0 (test)")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		withCookies(request, cookies)
		if csrf != "" {
			request.Header.Set(auth.CSRFHeaderName, csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	const wrongPasscode = "WRQNGPWD"

	// A passcode for an address nobody registered: the client hears the same
	// 202 as ever, and the log hears why nothing went out, for the address as
	// typed, normalised. Nothing names an account.
	if code := send(http.MethodPost, "/api/auth/otp/request", `{"email":" Nobody@Example.test"}`, nil, "").Code; code != http.StatusAccepted {
		t.Fatalf("passcode request for nobody = %d", code)
	}
	unknown := logs.One(t, map[string]any{"event.action": "auth.code.request", "event.outcome": "failure"})
	logtest.Want(t, unknown, map[string]any{
		"log.level": "warn", "event.category": "authentication", "event.reason": "unknown-account",
		"user.email": "nobody@example.test", "client.ip": "203.0.113.7", "user_agent.original": "Mozilla/5.0 (test)",
		"nodevas.audit.action": audit.ActionPasscodeRequest, "nodevas.auth.method": "otp",
		"nodevas.auth.purpose": "sign-in",
	})
	logtest.Absent(t, unknown, "user.id", "user.name")
	// What is not an address at all is not repeated.
	if code := send(http.MethodPost, "/api/auth/otp/request", `{"email":"`+wrongPasscode+`"}`, nil, "").Code; code != http.StatusAccepted {
		t.Fatalf("passcode request for a non-address = %d", code)
	}
	if lines := logs.Find(t, map[string]any{"event.action": "auth.code.request", "event.reason": "unknown-account"}); len(lines) != 2 {
		t.Fatalf("%d unknown-account lines, want 2", len(lines))
	} else {
		logtest.Absent(t, lines[1], "user.id", "user.name", "user.email")
	}

	before := inbox.count()
	asked := send(http.MethodPost, "/api/auth/otp/request", `{"email":"`+testEmail+`"}`, nil, "")
	if asked.Code != http.StatusAccepted {
		t.Fatalf("passcode request = %d", asked.Code)
	}
	inbox.waitFor(t, before+1)
	passcode := inbox.code(t)
	// The line waits for the relay; the row did not.
	mailed := logs.Await(t, map[string]any{"event.action": "auth.code.request", "event.outcome": "success"})
	logtest.Want(t, mailed, map[string]any{
		"log.level": "info", "user.name": "ann", "user.email": testEmail, "client.ip": "203.0.113.7",
		"http.request.id": asked.Header().Get("X-Request-Id"), "nodevas.audit.detail": `{"delivered":true}`,
	})

	// Two refused sign-ins the client cannot tell apart, and the log can: an
	// address nobody registered, which it names as typed, and a wrong
	// passcode for ann, which it says was for ann.
	stranger := send(http.MethodPost, "/api/auth/login", `{"email":"Stranger@example.test","otp":"`+wrongPasscode+`"}`, nil, "")
	wrong := send(http.MethodPost, "/api/auth/login", `{"email":"`+testEmail+`","otp":"`+wrongPasscode+`"}`, nil, "")
	if stranger.Code != http.StatusUnauthorized || wrong.Code != http.StatusUnauthorized || stranger.Body.String() != wrong.Body.String() {
		t.Fatalf("refusals = %d %s / %d %s, want the same 401", stranger.Code, stranger.Body, wrong.Code, wrong.Body)
	}
	nobody := logs.One(t, map[string]any{"event.action": "auth.signin", "event.reason": "unknown-account"})
	logtest.Want(t, nobody, map[string]any{
		"log.level": "warn", "event.category": "authentication", "event.outcome": "failure",
		"user.email": "stranger@example.test", "client.ip": "203.0.113.7",
		"nodevas.audit.action": audit.ActionSignInFailed, "nodevas.auth.method": "otp",
	})
	logtest.Absent(t, nobody, "user.id", "user.name")
	failed := logs.One(t, map[string]any{"event.action": "auth.signin", "event.reason": "bad-credentials"})
	logtest.Want(t, failed, map[string]any{
		"log.level": "warn", "event.category": "authentication", "event.outcome": "failure", "client.ip": "203.0.113.7",
		"nodevas.audit.action": audit.ActionSignInFailed, "nodevas.auth.method": "otp",
		"user.name": "ann", "user.email": testEmail, "http.request.id": wrong.Header().Get("X-Request-Id"),
	})
	// Their rows are what such rows always were: no actor, one reason.
	refusals, err := server.audit.Query(context.Background(), audit.Filter{ActionPrefix: audit.ActionSignInFailed})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range refusals {
		if row.ActorID != "" || row.ActorName != "" || row.Detail["reason"] != "bad-credentials" || len(row.Detail) != 1 {
			t.Errorf("refusal row = %+v", row)
		}
	}
	if len(refusals) != 2 {
		t.Errorf("%d refusal rows, want 2", len(refusals))
	}

	response := send(http.MethodPost, "/api/auth/login", `{"email":"`+testEmail+`","otp":"`+passcode+`"}`, nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", response.Code, response.Body)
	}
	cookies := response.Result().Cookies()
	var session, csrf string
	for _, cookie := range cookies {
		switch cookie.Name {
		case auth.SessionCookieName:
			session = cookie.Value
		case auth.CSRFCookieName:
			csrf = cookie.Value
		}
	}
	requestID := response.Header().Get("X-Request-Id")
	signedIn := logs.One(t, map[string]any{"event.action": "auth.signin", "event.outcome": "success"})
	logtest.Want(t, signedIn, map[string]any{
		"user.name": "ann", "user.email": testEmail, "client.ip": "203.0.113.7",
		"nodevas.auth.method": "otp", "nodevas.auth.purpose": "sign-in", "http.request.id": requestID,
	})
	userID, _ := signedIn["user.id"].(string)
	if userID == "" {
		t.Fatalf("user.id = %v", signedIn["user.id"])
	}
	if failed["user.id"] != userID {
		t.Errorf("the wrong passcode's user.id = %v, want ann's %s", failed["user.id"], userID)
	}
	// The sign-in's own request line names who it signed in.
	logtest.Want(t, logs.One(t, map[string]any{"event.action": "http.request", "http.request.id": requestID}),
		map[string]any{"user.id": userID, "user.name": "ann", "user.email": testEmail, "url.path": "/api/auth/login",
			"http.response.status_code": float64(http.StatusOK), "event.category": "web"})
	// A refused one names nobody: nobody was signed in.
	logtest.Absent(t, logs.One(t, map[string]any{"event.action": "http.request", "http.request.id": wrong.Header().Get("X-Request-Id")}),
		"user.id", "user.name", "user.email")

	// A write is an audit event under its own name. Its line, and its
	// request's, name the address the session check read anyway.
	write := send(http.MethodPost, "/api/nodes", `{"id":"logged","title":"Logged","body":""}`, cookies, csrf)
	if write.Code != http.StatusCreated {
		t.Fatalf("write = %d", write.Code)
	}
	wrote := logs.One(t, map[string]any{"nodevas.audit.action": "POST nodes"})
	logtest.Want(t, wrote, map[string]any{
		"log.level": "info", "event.category": "audit", "event.action": "POST nodes", "event.outcome": "success",
		"user.id": userID, "user.name": "ann", "user.email": testEmail, "client.ip": "203.0.113.7",
		"http.request.id": write.Header().Get("X-Request-Id"),
	})
	if project, _ := wrote["nodevas.project"].(string); project == "" {
		t.Errorf("nodevas.project = %v, want the project written to", wrote["nodevas.project"])
	}
	logtest.Want(t, logs.One(t, map[string]any{"event.action": "http.request", "http.request.id": write.Header().Get("X-Request-Id")}),
		map[string]any{"user.id": userID, "user.name": "ann", "user.email": testEmail, "url.path": "/api/nodes"})

	out := send(http.MethodPost, "/api/auth/logout", "", cookies, csrf)
	if out.Code != http.StatusOK {
		t.Fatalf("sign-out = %d %s", out.Code, out.Body)
	}
	signedOut := logs.One(t, map[string]any{"event.action": "auth.signout"})
	logtest.Want(t, signedOut, map[string]any{
		"log.level": "info", "event.category": "authentication", "event.outcome": "success",
		"user.id": userID, "user.name": "ann", "user.email": testEmail, "client.ip": "203.0.113.7",
		"nodevas.audit.action": audit.ActionSignOut, "http.request.id": out.Header().Get("X-Request-Id"),
	})
	rows, err := server.audit.Query(context.Background(), audit.Filter{ActionPrefix: audit.ActionSignOut})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ActorID != userID || rows[0].ActorName != "ann" || rows[0].ClientIP != "203.0.113.7" {
		t.Fatalf("sign-out rows = %+v", rows)
	}

	// The cookie is nobody's now: refused, but it never expired.
	if code := send(http.MethodGet, "/api/graph", "", cookies, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("after sign-out = %d", code)
	}
	if expired := logs.Find(t, map[string]any{"event.action": "auth.session.expired"}); len(expired) != 0 {
		t.Fatalf("a signed-out session was logged as expired: %v", expired)
	}

	// One line per event, and not one credential in the whole log.
	for action, want := range map[string]int{
		audit.ActionPasscodeRequest: 3, audit.ActionSignInFailed: 2, audit.ActionSignIn: 1, audit.ActionSignOut: 1,
	} {
		if got := len(logs.Find(t, map[string]any{"nodevas.audit.action": action})); got != want {
			t.Errorf("%d lines for %s, want %d", got, action, want)
		}
	}
	logs.Never(t, passcode, wrongPasscode, session, csrf)
	for _, record := range logs.Records(t) {
		if record["service.name"] != "nodevas" {
			t.Errorf("a line without service.name: %v", record)
		}
	}
}

// A membership change changes what an account may reach: its row, and so its
// line, names the member, and the project the change was made to rather than
// the active one.
func TestAMembershipChangeNamesTheMemberAndTheProject(t *testing.T) {
	f := newMembersFixture(t)
	logs := logtest.Capture(t)
	// Served inside the request log, as cmd/nodevas serves it.
	f.handler = logging.MiddlewareWithOptions(logging.Options{ClientIP: auth.ClientIP, Quiet: QuietRequest})(f.handler)
	if response := f.setMember(t, f.bea, "cal", "viewer"); response.Code != http.StatusOK {
		t.Fatalf("bea adding cal: %d %s", response.Code, response.Body)
	}
	cal := userID(t, f, "cal")
	if response := f.do(t, &f.bea, http.MethodDelete, "/api/projects/members?project=other&userId="+cal, "", nil); response.Code != http.StatusOK {
		t.Fatalf("bea removing cal: %d %s", response.Code, response.Body)
	}
	if code := f.code(t, f.bea, http.MethodPost, "/api/projects/members/open", `{"project":"other"}`); code != http.StatusOK {
		t.Fatalf("bea opening the project: %d", code)
	}

	membership := map[string]any{
		"log.level": "info", "event.category": "iam", "event.action": "iam.account.update", "event.outcome": "success",
		"user.name": "bea", "user.email": "bea@example.test", "nodevas.project": f.otherRoot,
	}
	added := logs.One(t, map[string]any{"nodevas.audit.action": "PUT projects/members"})
	logtest.Want(t, added, membership)
	logtest.Want(t, added, map[string]any{"nodevas.audit.target": cal, "nodevas.audit.detail": `{"role":"viewer"}`})
	removed := logs.One(t, map[string]any{"nodevas.audit.action": "DELETE projects/members"})
	logtest.Want(t, removed, membership)
	logtest.Want(t, removed, map[string]any{"nodevas.audit.target": cal})
	logtest.Absent(t, removed, "nodevas.audit.detail")
	// Every member goes: the project is named, and no one account.
	opened := logs.One(t, map[string]any{"nodevas.audit.action": "POST projects/members/open"})
	logtest.Want(t, opened, membership)
	logtest.Absent(t, opened, "nodevas.audit.target", "nodevas.audit.detail")

	// The rows say the same, filed under the project that changed.
	rows, err := f.server.audit.Query(context.Background(), audit.Filter{Project: f.otherRoot, ActionPrefix: "PUT projects/members"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ActorName != "bea" || rows[0].Target != cal || rows[0].Detail["role"] != "viewer" {
		t.Fatalf("membership rows = %+v", rows)
	}
}

// refusingRelay takes each message down, so a test can still read it, and
// then fails the way a relay that will not take the message does.
type refusingRelay struct{ mailbox }

func (r *refusingRelay) Send(ctx context.Context, to, subject, body string) error {
	_ = r.mailbox.Send(ctx, to, subject, body)
	return errors.New("relay refused the message")
}

func TestAnUndeliveredPasscodeIsAFailedCodeRequest(t *testing.T) {
	server, _, _ := accountServerForTest(t)
	relay := &refusingRelay{}
	server.UseMailer(relay)
	logs := logtest.Capture(t)
	handler := loggedHandler(server)
	send := func(path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.RemoteAddr = "203.0.113.7:4242"
		request.Header.Set("Content-Type", "application/json")
		withCookies(request, cookies)
		if csrf != "" {
			request.Header.Set(auth.CSRFHeaderName, csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	// The client hears the usual 202, and the row says a passcode was minted
	// and handed to delivery, as it always has. The request's one line waits
	// for the relay, and says the passcode never went out.
	asked := send("/api/auth/otp/request", `{"email":"`+testEmail+`"}`, nil, "")
	if asked.Code != http.StatusAccepted {
		t.Fatalf("passcode request = %d", asked.Code)
	}
	relay.waitFor(t, 1)
	passcode := relay.code(t)
	undelivered := logs.Await(t, map[string]any{"nodevas.audit.action": audit.ActionPasscodeRequest})
	logtest.Want(t, undelivered, map[string]any{
		"log.level": "error", "event.category": "authentication", "event.action": "auth.code.request",
		"event.outcome": "failure", "event.reason": "error",
		"user.name": "ann", "user.email": testEmail, "client.ip": "203.0.113.7", "error.message": "relay refused the message",
		"nodevas.auth.method": "otp", "nodevas.auth.purpose": "sign-in", "nodevas.audit.detail": `{"delivered":true}`,
		"http.request.id": asked.Header().Get("X-Request-Id"), "service.name": "nodevas",
	})
	rows, err := server.audit.Query(context.Background(), audit.Filter{ActionPrefix: audit.ActionPasscodeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Detail["delivered"] != true {
		t.Fatalf("passcode rows = %+v", rows)
	}

	// The same for a step-up passcode. The relay kept the first one, so ann
	// can still sign in to ask for it.
	login := send("/api/auth/login", `{"email":"`+testEmail+`","otp":"`+passcode+`"}`, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	csrf := ""
	for _, cookie := range cookies {
		if cookie.Name == auth.CSRFCookieName {
			csrf = cookie.Value
		}
	}
	stepUp := send("/api/auth/step-up/request", `{}`, cookies, csrf)
	if stepUp.Code != http.StatusAccepted {
		t.Fatalf("step-up request = %d %s", stepUp.Code, stepUp.Body)
	}
	relay.waitFor(t, 2)
	stepUpCode := relay.code(t)
	logtest.Want(t, logs.Await(t, map[string]any{"nodevas.audit.action": audit.ActionStepUpRequest}),
		map[string]any{"log.level": "error", "event.category": "audit", "event.action": "auth.stepup.code.request",
			"event.outcome": "failure", "event.reason": "error", "error.message": "relay refused the message",
			"user.name": "ann", "user.email": testEmail, "nodevas.auth.purpose": "step-up",
			"http.request.id": stepUp.Header().Get("X-Request-Id")})

	var sessions []string
	for _, cookie := range cookies {
		sessions = append(sessions, cookie.Value)
	}
	logs.Never(t, append([]string{passcode, stepUpCode}, sessions...)...)
}

func TestAPanicIsOneRecordThatNamesTheRequestWithoutDumpingIt(t *testing.T) {
	logs := logtest.Capture(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(recoverPanics())
	router.GET("/api/boom", func(*gin.Context) { panic("boom") })
	router.GET("/api/hang-up", func(*gin.Context) { panic(http.ErrAbortHandler) })
	handler := logging.MiddlewareWithOptions(logging.Options{})(router)
	const (
		session = "session-cookie-value-never-logged"
		access  = "access-jwt-value-never-logged"
	)
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		request.Header.Set(auth.AccessHeaderName, access)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := get("/api/boom")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	panicked := logs.One(t, map[string]any{"event.code": "http.panic"})
	logtest.Want(t, panicked, map[string]any{
		"log.level": "error", "error.message": "boom", "http.request.method": "GET", "url.path": "/api/boom",
		"http.request.id": response.Header().Get("X-Request-Id"), "service.name": "nodevas",
	})
	if trace, _ := panicked["error.stack_trace"].(string); !strings.Contains(trace, "eventlog_test.go") {
		t.Errorf("error.stack_trace does not reach the panic: %q", trace)
	}
	logs.One(t, map[string]any{"event.action": "http.request", "http.response.status_code": float64(http.StatusInternalServerError)})

	// A client that hung up is not a bug: no record of a panic at all.
	get("/api/hang-up")
	if found := logs.Find(t, map[string]any{"event.code": "http.panic"}); len(found) != 1 {
		t.Fatalf("%d panic records, want only the first: %v", len(found), found)
	}
	logs.Never(t, session, access)
}

func TestQuietRequestsAreTheHealthProbeAndAssets(t *testing.T) {
	request := func(path, peer string, forwarded bool) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = peer + ":5000"
		if forwarded {
			r.Header.Set("X-Forwarded-For", "203.0.113.7")
		}
		return r
	}
	for _, tc := range []struct {
		name  string
		r     *http.Request
		quiet bool
	}{
		{"the container health probe", request("/", "127.0.0.1", false), true},
		{"the app shell through a proxy on this machine", request("/", "127.0.0.1", true), false},
		{"the app shell from a visitor", request("/", "203.0.113.7", false), false},
		{"a static asset", request("/assets/index-abc123.js", "203.0.113.7", true), true},
		{"an API read from this machine", request("/api/graph", "127.0.0.1", false), false},
	} {
		if got := QuietRequest(tc.r); got != tc.quiet {
			t.Errorf("%s: QuietRequest = %v, want %v", tc.name, got, tc.quiet)
		}
	}
}

func TestAccessGrantsAreLoggedOncePerIdentityAndAddressPerWindow(t *testing.T) {
	f := newAccessFixture(t)
	logs := logtest.Capture(t)
	now := time.Now()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(accessMiddleware(f.v, true, func() time.Time { return now }))
	router.GET("/api/state", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	person := f.validToken(t)
	service := f.token(t, map[string]any{
		"aud": []string{"aud-1"}, "iss": f.v.Issuer(), "common_name": "runner-id.access",
		"type": "app", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	from := func(address, token string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		request.RemoteAddr = address + ":41000"
		request.Header.Set(auth.AccessHeaderName, token)
		if code := serveAccess(router, request); code != http.StatusOK {
			t.Fatalf("from %s = %d", address, code)
		}
	}

	for i := 0; i < 3; i++ {
		from("203.0.113.7", person)
	}
	from("198.51.100.20", person)
	from("203.0.113.7", service)
	now = now.Add(accessGrantedEvery)
	from("203.0.113.7", person)

	granted := logs.Find(t, map[string]any{"event.action": "auth.access.granted"})
	if len(granted) != 4 {
		t.Fatalf("%d grant lines, want 4 (one per identity and address, and one after the window):\n%s",
			len(granted), logs.Text())
	}
	logtest.Want(t, granted[0], map[string]any{
		"log.level": "info", "message": "auth.access.granted success", "event.category": "authentication",
		"event.outcome": "success", "user.email": "me@example.com", "client.ip": "203.0.113.7",
	})
	logtest.Want(t, granted[1], map[string]any{"user.email": "me@example.com", "client.ip": "198.51.100.20"})
	logtest.Want(t, granted[2], map[string]any{"user.name": "service:runner-id.access", "client.ip": "203.0.113.7"})
	logtest.Absent(t, granted[2], "user.email")
	logtest.Want(t, granted[3], map[string]any{"user.email": "me@example.com", "client.ip": "203.0.113.7"})
	logs.Never(t, person, service)
}

func TestAccessDenialsAreLoggedWithTheirReason(t *testing.T) {
	f := newAccessFixture(t)
	logs := logtest.Capture(t)
	router := accessRouter(f, true, nil)
	now := time.Now()
	claims := func(audience string, expires time.Time) map[string]any {
		return map[string]any{
			"aud": []string{audience}, "iss": f.v.Issuer(), "email": "me@example.com",
			"type": "app", "iat": now.Add(-2 * time.Hour).Unix(), "exp": expires.Unix(),
		}
	}
	for reason, token := range map[string]string{
		"missing-jwt":  "",
		"invalid-jwt":  "garbage.token.here",
		"bad-audience": f.token(t, claims("someone-else", now.Add(time.Hour))),
		"expired-jwt":  f.token(t, claims("aud-1", now.Add(-time.Hour))),
	} {
		// Every refusal is a line, however fast they come: how many, and
		// from where, is what an alert on them counts.
		for i := 0; i < 3; i++ {
			request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
			request.RemoteAddr = "203.0.113.7:41000"
			if token != "" {
				request.Header.Set(auth.AccessHeaderName, token)
			}
			if code := serveAccess(router, request); code != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want 403", reason, code)
			}
		}
		records := logs.Find(t, map[string]any{"event.action": "auth.access.denied", "event.reason": reason})
		if len(records) != 3 {
			t.Fatalf("%s: %d denial lines for 3 refusals:\n%s", reason, len(records), logs.Text())
		}
		for _, record := range records {
			logtest.Want(t, record, map[string]any{
				"log.level": "warn", "event.category": "authentication", "event.outcome": "failure",
				"client.ip": "203.0.113.7", "url.path": "/api/state", "nodevas.access.required": true,
				"event.code": "access.invalid",
			})
			// A token that failed verification vouches for nobody.
			logtest.Absent(t, record, "user.email", "user.name")
		}
		logs.Never(t, token)
	}
}

// In audit mode nothing is refused, so a broken Access setup would put a line
// beside every request a browser makes: there the same caller and reason is
// one line a minute.
func TestUnverifiedAccessInAuditModeIsLoggedOncePerCallerAndReasonAMinute(t *testing.T) {
	f := newAccessFixture(t)
	logs := logtest.Capture(t)
	now := time.Now()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(accessMiddleware(f.v, false, func() time.Time { return now }))
	router.GET("/api/state", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	from := func(address, token string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		request.RemoteAddr = address + ":41000"
		if token != "" {
			request.Header.Set(auth.AccessHeaderName, token)
		}
		if code := serveAccess(router, request); code != http.StatusOK {
			t.Fatalf("audit mode refused a request from %s: %d", address, code)
		}
	}

	for i := 0; i < 3; i++ {
		from("203.0.113.7", "")
	}
	from("203.0.113.7", "garbage.token.here")
	from("198.51.100.20", "")
	now = now.Add(accessUnverifiedEvery)
	from("203.0.113.7", "")

	lines := logs.Find(t, map[string]any{"event.action": "auth.access.denied"})
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4 (one per address and reason, and one after the minute):\n%s", len(lines), logs.Text())
	}
	for _, line := range lines {
		logtest.Want(t, line, map[string]any{"log.level": "warn", "nodevas.access.required": false, "event.code": "access.invalid"})
	}
	logtest.Want(t, lines[0], map[string]any{"event.reason": "missing-jwt", "client.ip": "203.0.113.7"})
	logtest.Want(t, lines[1], map[string]any{"event.reason": "invalid-jwt", "client.ip": "203.0.113.7"})
	logtest.Want(t, lines[2], map[string]any{"event.reason": "missing-jwt", "client.ip": "198.51.100.20"})
	logtest.Want(t, lines[3], map[string]any{"event.reason": "missing-jwt", "client.ip": "203.0.113.7"})
}

func TestAccessNotesStayBounded(t *testing.T) {
	now := time.Now()
	notes := newAccessNotes(time.Minute, func() time.Time { return now })
	for i := 0; i < 3*maxAccessNotes; i++ {
		if !notes.due(fmt.Sprint(i)) {
			t.Fatalf("a caller never seen before was not due: %d", i)
		}
	}
	if len(notes.seen) > maxAccessNotes {
		t.Fatalf("%d callers remembered, cap %d", len(notes.seen), maxAccessNotes)
	}
	newest := fmt.Sprint(3*maxAccessNotes - 1)
	if notes.due(newest) {
		t.Fatal("a caller inside its window was due again")
	}
	now = now.Add(time.Minute)
	if !notes.due(newest) {
		t.Fatal("a caller past its window was not due")
	}
}
