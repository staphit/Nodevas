package system

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/identity"
	"nodevas/internal/logging/logtest"
	"nodevas/internal/testpg"

	"github.com/gin-gonic/gin"
)

// A refused sign-in's row is what it always was; its line says whose account
// the attempt named, and why it was refused in the log's finer words, which
// for a server fault is error rather than a refused credential.
func TestARefusedSignInLineNamesTheAccountAndTheFinerReason(t *testing.T) {
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	trail := audit.New(database)
	api := &API{audit: trail}
	logs := logtest.Capture(t)
	ann := auth.SignInRefusal{Account: identity.Actor{ID: "u-1", Name: "ann"}, Email: "ann@example.test"}
	refuse := func(refusal auth.SignInRefusal, err error) int {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		request.RemoteAddr = "203.0.113.7:4242"
		api.finishSignIn(notifySecurityContext(response, request), audit.MethodOTP,
			auth.SignInResult{Refusal: refusal}, err, nil)
		return response.Code
	}

	burnt := ann
	burnt.Reason = auth.RefusedTooManyAttempts
	if code := refuse(burnt, auth.ErrBadCredentials); code != http.StatusUnauthorized {
		t.Fatalf("burnt passcode = %d", code)
	}
	if code := refuse(ann, errors.New("read account: database is locked")); code != http.StatusInternalServerError {
		t.Fatalf("outage = %d", code)
	}
	lines := logs.Find(t, map[string]any{"nodevas.audit.action": audit.ActionSignInFailed})
	if len(lines) != 2 {
		t.Fatalf("%d refusal lines, want 2:\n%s", len(lines), logs.Text())
	}
	for _, line := range lines {
		logtest.Want(t, line, map[string]any{
			"event.category": "authentication", "event.action": "auth.signin", "event.outcome": "failure",
			"user.id": "u-1", "user.name": "ann", "user.email": "ann@example.test", "client.ip": "203.0.113.7",
			"nodevas.auth.method": "otp", "nodevas.audit.detail": `{"reason":"bad-credentials"}`,
		})
	}
	// A refusal is a warning; the server's own fault is an error.
	logtest.Want(t, lines[0], map[string]any{"event.reason": "too-many-attempts", "log.level": "warn"})
	logtest.Want(t, lines[1], map[string]any{"event.reason": "error", "log.level": "error"})

	rows, err := trail.Query(context.Background(), audit.Filter{ActionPrefix: audit.ActionSignInFailed})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if row.ActorID != "" || row.ActorName != "" || row.ClientIP != "203.0.113.7" ||
			row.Detail["reason"] != "bad-credentials" || len(row.Detail) != 1 {
			t.Errorf("row = %+v", row)
		}
	}
}

// A client that went away before its sign-in was judged is filed under reason
// cancelled, as it always was. The line gives a failed sign-in's reason in
// the words every service shares, which have none closer than error, but it
// is a warning: the server did nothing wrong. What goes back is only that the
// request ended, never the text of whatever read it interrupted.
func TestACancelledSignInLineSaysErrorInTheSharedWords(t *testing.T) {
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	trail := audit.New(database)
	api := &API{audit: trail}
	logs := logtest.Capture(t)
	response := httptest.NewRecorder()
	gone, hangUp := context.WithCancel(context.Background())
	hangUp()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil).WithContext(gone)
	api.finishSignIn(notifySecurityContext(response, request), audit.MethodOTP,
		auth.SignInResult{}, fmt.Errorf("read account: %w", context.Canceled), nil)
	if response.Code != http.StatusUnauthorized || response.Body.String() != `{"error":"context canceled"}` {
		t.Fatalf("cancelled sign-in = %d %s", response.Code, response.Body)
	}

	logtest.Want(t, logs.One(t, map[string]any{"nodevas.audit.action": audit.ActionSignInFailed}), map[string]any{
		"log.level":    "warn",
		"event.action": "auth.signin", "event.outcome": "failure", "event.reason": "error",
		"nodevas.audit.detail": `{"reason":"cancelled"}`,
	})
}

// A context error is a client that went away only when the request's own
// context is done. A driver's deadline under a read, the connect timeout a
// hung server runs into, wraps one while the client is still waiting: that is
// the server's fault, answered with a bare 500 like any other, and its line
// says error at level error. Answered as a refusal, the driver's text, which
// names the database, its user and its address, would reach whoever typed an
// address at the door.
func TestADriverDeadlineWhileTheClientWaitsIsTheServersFault(t *testing.T) {
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	trail := audit.New(database)
	api := &API{audit: trail}
	logs := logtest.Capture(t)
	stalled := fmt.Errorf("read accounts: failed to connect to `user=nodevas database=nodevas`: "+
		"192.0.2.10:5432: timeout: %w", context.DeadlineExceeded)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	request.RemoteAddr = "203.0.113.7:4242"
	api.finishSignIn(notifySecurityContext(response, request), audit.MethodOTP,
		auth.SignInResult{Refusal: auth.SignInRefusal{Email: "nobody@example.test"}}, stalled, nil)
	if response.Code != http.StatusInternalServerError || response.Body.String() != `{"error":"Internal Server Error"}` {
		t.Fatalf("sign-in with the database stalled = %d %s, want a bare 500", response.Code, response.Body)
	}
	logtest.Want(t, logs.One(t, map[string]any{"nodevas.audit.action": audit.ActionSignInFailed}), map[string]any{
		"log.level": "error", "event.action": "auth.signin", "event.outcome": "failure",
		"event.reason": "error", "user.email": "nobody@example.test",
		"nodevas.audit.detail": `{"reason":"bad-credentials"}`,
	})

	alive := context.Background()
	gone, hangUp := context.WithCancel(context.Background())
	hangUp()
	for _, tc := range []struct {
		ctx            context.Context
		err            error
		delivery, auth string
	}{
		{alive, stalled, "error", "bad-credentials"},
		{alive, context.Canceled, "error", "bad-credentials"},
		{gone, stalled, "cancelled", "cancelled"},
		{gone, context.Canceled, "cancelled", "cancelled"},
		{gone, auth.ErrNoSuchAccount, "unknown-account", "bad-credentials"},
	} {
		if got := deliveryFailureReason(tc.ctx, tc.err); got != tc.delivery {
			t.Errorf("deliveryFailureReason(%v, %v) = %q, want %q", tc.ctx.Err(), tc.err, got, tc.delivery)
		}
		if got := authFailureReason(tc.ctx, tc.err); got != tc.auth {
			t.Errorf("authFailureReason(%v, %v) = %q, want %q", tc.ctx.Err(), tc.err, got, tc.auth)
		}
	}
}

// countingMailer is a relay that delivers nothing and counts what it was given.
type countingMailer struct {
	mu   sync.Mutex
	sent int
}

func (m *countingMailer) Send(context.Context, string, string, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent++
	return nil
}

func (m *countingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent
}

// An account lookup that fails during a passcode request or a sign-in is the
// server's fault, and its line says error, at level error, rather than
// unknown-account: an outage must not read as somebody probing for accounts,
// and the sign-in alerts that skip error stay quiet through one. The client
// sees what an outage gives every address alike — the passcode request's 202,
// the sign-in's 500 — and nothing of the failure's text. Readable again, an
// address nobody registered is the warning, 202 and 401 it always was.
func TestAFailedAccountLookupIsLoggedAsTheServersFault(t *testing.T) {
	database := testpg.New(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	users := auth.NewUserStoreDB(database)
	if err := users.AddWithEmail(ctx, "ann", "correct-horse-battery", "", "ann@example.test"); err != nil {
		t.Fatal(err)
	}
	mailer := &countingMailer{}
	api := &API{auth: auth.NewSessionAuth(users), mailer: mailer, audit: audit.New(database)}
	logs := logtest.Capture(t)
	send := func(handler func(*gin.Context), path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "203.0.113.7:4242"
		handler(notifySecurityContext(response, request))
		return response
	}
	const passcode = "K7Q2M9XP"

	if _, err := database.ExecContext(ctx, `ALTER TABLE accounts RENAME TO accounts_unreadable`); err != nil {
		t.Fatalf("hide accounts: %v", err)
	}
	asked := send(api.postRequestOTP, "/api/auth/otp/request", `{"email":" Ann@Example.test"}`)
	if asked.Code != http.StatusAccepted || asked.Body.String() != `{"ok":true}` {
		t.Fatalf("passcode request with accounts unreadable = %d %s, want 202", asked.Code, asked.Body)
	}
	signIn := send(api.postLogin, "/api/auth/login", `{"email":"Ann@Example.test","otp":"`+passcode+`"}`)
	if signIn.Code != http.StatusInternalServerError || signIn.Body.String() != `{"error":"Internal Server Error"}` {
		t.Fatalf("sign-in with accounts unreadable = %d %s, want a bare 500", signIn.Code, signIn.Body)
	}
	codeLines := logs.Find(t, map[string]any{"nodevas.audit.action": audit.ActionPasscodeRequest})
	signInLines := logs.Find(t, map[string]any{"nodevas.audit.action": audit.ActionSignInFailed})
	if len(codeLines) != 1 || len(signInLines) != 1 {
		t.Fatalf("%d passcode lines and %d sign-in lines, want one each:\n%s", len(codeLines), len(signInLines), logs.Text())
	}
	logtest.Want(t, codeLines[0], map[string]any{
		"log.level": "error", "event.category": "authentication", "event.action": "auth.code.request",
		"event.outcome": "failure", "event.reason": "error", "user.email": "ann@example.test",
		"client.ip": "203.0.113.7", "nodevas.audit.detail": `{"delivered":false}`,
	})
	logtest.Want(t, signInLines[0], map[string]any{
		"log.level": "error", "event.category": "authentication", "event.action": "auth.signin",
		"event.outcome": "failure", "event.reason": "error", "user.email": "ann@example.test",
		"client.ip": "203.0.113.7", "nodevas.auth.method": "otp", "nodevas.audit.detail": `{"reason":"bad-credentials"}`,
	})
	for _, line := range append(codeLines, signInLines...) {
		logtest.Absent(t, line, "user.id", "user.name")
	}
	logs.Never(t, passcode)
	if mailer.count() != 0 {
		t.Fatalf("%d passcodes mailed while accounts were unreadable", mailer.count())
	}

	if _, err := database.ExecContext(ctx, `ALTER TABLE accounts_unreadable RENAME TO accounts`); err != nil {
		t.Fatalf("restore accounts: %v", err)
	}
	if asked := send(api.postRequestOTP, "/api/auth/otp/request", `{"email":"nobody@example.test"}`); asked.Code != http.StatusAccepted {
		t.Fatalf("passcode request for nobody = %d %s, want 202", asked.Code, asked.Body)
	}
	if refused := send(api.postLogin, "/api/auth/login", `{"email":"nobody@example.test","otp":"`+passcode+`"}`); refused.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in for nobody = %d %s, want 401", refused.Code, refused.Body)
	}
	logtest.Want(t, logs.One(t, map[string]any{"event.action": "auth.code.request", "event.reason": "unknown-account"}),
		map[string]any{"log.level": "warn", "event.outcome": "failure", "user.email": "nobody@example.test"})
	logtest.Want(t, logs.One(t, map[string]any{"event.action": "auth.signin", "event.reason": "unknown-account"}),
		map[string]any{"log.level": "warn", "event.outcome": "failure", "user.email": "nobody@example.test"})
	if lines := logs.Find(t, map[string]any{"event.reason": "error"}); len(lines) != 2 {
		t.Fatalf("%d error lines, want the two from the outage:\n%s", len(lines), logs.Text())
	}
}

// A row that is found but cannot be read, a role nobody can interpret, exists
// only for an address that is registered. Its sign-in gets exactly what an
// address nobody registered gets, the same 401 and the same body, so the
// answer says nothing about which one was typed; its line alone says that the
// fault was the server's, at level error. The passcode request answers 202 for
// both, as for everything but a throttle. For both requests the cause reaches
// the operator, on a line of its own, and never the client.
func TestAnUnreadableAccountSignsInLikeAnUnknownAddress(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	users := auth.NewUserStoreDB(database)
	for _, person := range []struct{ name, email string }{{"ann", "ann@example.test"}, {"bea", "bea@example.test"}} {
		if err := users.AddWithEmail(ctx, person.name, "correct-horse-battery", "", person.email); err != nil {
			t.Fatal(err)
		}
	}
	// Constraints are checked on write, so a hand edit is the only way such a
	// row comes to be.
	loosen := `ALTER TABLE accounts DROP CONSTRAINT accounts_role_check`
	if _, err := database.ExecContext(ctx, loosen); err != nil {
		t.Fatalf("loosen the role check: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE accounts SET role = 'owner' WHERE name = 'bea'`); err != nil {
		t.Fatalf("tamper with bea: %v", err)
	}
	api := &API{auth: auth.NewSessionAuth(users), mailer: &countingMailer{}, audit: audit.New(database)}
	logs := logtest.Capture(t)
	send := func(handler func(*gin.Context), path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "203.0.113.7:4242"
		handler(notifySecurityContext(response, request))
		return response
	}
	const passcode = "K7Q2M9XP"

	answers := map[string]string{}
	for _, address := range []string{"bea@example.test", "nobody@example.test"} {
		if asked := send(api.postRequestOTP, "/api/auth/otp/request", `{"email":"`+address+`"}`); asked.Code != http.StatusAccepted || asked.Body.String() != `{"ok":true}` {
			t.Fatalf("passcode request for %s = %d %s, want 202", address, asked.Code, asked.Body)
		}
		signIn := send(api.postLogin, "/api/auth/login", `{"email":"`+address+`","otp":"`+passcode+`"}`)
		answers[address] = fmt.Sprintf("%d %s", signIn.Code, signIn.Body)
	}
	if want := `401 {"error":"invalid user name or password"}`; answers["bea@example.test"] != want || answers["nobody@example.test"] != want {
		t.Fatalf("sign-in answers = %q, want %q for both", answers, want)
	}

	logtest.Want(t, logs.One(t, map[string]any{"event.action": "auth.signin", "user.email": "bea@example.test"}), map[string]any{
		"log.level": "error", "event.outcome": "failure", "event.reason": "error",
		"nodevas.audit.detail": `{"reason":"bad-credentials"}`,
	})
	logtest.Want(t, logs.One(t, map[string]any{"event.action": "auth.code.request", "user.email": "bea@example.test"}), map[string]any{
		"log.level": "error", "event.outcome": "failure", "event.reason": "error",
	})
	logtest.Want(t, logs.One(t, map[string]any{"event.action": "auth.signin", "user.email": "nobody@example.test"}), map[string]any{
		"log.level": "warn", "event.reason": "unknown-account",
	})
	for _, step := range []string{"passcode request failed: ", "sign-in refused: "} {
		var causes []string
		for _, record := range logs.Records(t) {
			if message, _ := record["message"].(string); strings.HasPrefix(message, step) {
				causes = append(causes, message)
			}
		}
		if len(causes) != 1 || !strings.Contains(causes[0], `invalid role "owner"`) {
			t.Fatalf("%q lines = %q, want one naming the unreadable role; log:\n%s", step, causes, logs.Text())
		}
	}
	logs.Never(t, passcode)
}

// The same against a PostgreSQL server that hangs: every read dials it and
// runs into the driver's connect timeout while the client is still waiting.
// Both addresses, registered or not, get the passcode request's 202 and the
// sign-in's bare 500, and both lines say error at level error. None of the
// driver's text, which names the database, its user and its address, reaches
// the client.
func TestAStalledDatabaseAnswersEveryAddressAlike(t *testing.T) {
	stall := testpg.UseStalledPostgres(t)
	if stall == nil {
		t.Skipf("set %s to run against PostgreSQL", testpg.Env)
	}
	// Through the stalling proxy UseStalledPostgres put in the environment.
	database, err := db.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	users := auth.NewUserStoreDB(database)
	if err := users.AddWithEmail(ctx, "ann", "correct-horse-battery", "", "ann@example.test"); err != nil {
		t.Fatal(err)
	}
	mailer := &countingMailer{}
	api := &API{auth: auth.NewSessionAuth(users), mailer: mailer, audit: audit.New(database)}
	logs := logtest.Capture(t)
	send := func(handler func(*gin.Context), path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.RemoteAddr = "203.0.113.7:4242"
		handler(notifySecurityContext(response, request))
		return response
	}
	const passcode = "K7Q2M9XP"
	database.Reader().SetMaxIdleConns(0)
	stall.Hang()

	for _, address := range []string{"ann@example.test", "nobody@example.test"} {
		if asked := send(api.postRequestOTP, "/api/auth/otp/request", `{"email":"`+address+`"}`); asked.Code != http.StatusAccepted || asked.Body.String() != `{"ok":true}` {
			t.Fatalf("passcode request for %s = %d %s, want 202", address, asked.Code, asked.Body)
		}
		signIn := send(api.postLogin, "/api/auth/login", `{"email":"`+address+`","otp":"`+passcode+`"}`)
		if signIn.Code != http.StatusInternalServerError || signIn.Body.String() != `{"error":"Internal Server Error"}` {
			t.Fatalf("sign-in for %s = %d %s, want a bare 500", address, signIn.Code, signIn.Body)
		}
		for _, action := range []string{"auth.code.request", "auth.signin"} {
			logtest.Want(t, logs.One(t, map[string]any{"event.action": action, "user.email": address}), map[string]any{
				"log.level": "error", "event.outcome": "failure", "event.reason": "error",
			})
		}
	}
	logs.Never(t, passcode)
	if mailer.count() != 0 {
		t.Fatalf("%d passcodes mailed with the database stalled", mailer.count())
	}
}
