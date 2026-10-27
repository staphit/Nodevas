package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/logging/logtest"
)

// Server accounts from the web UI (internal/httpapi/system/accounts.go): an
// administrator lists, invites, re-roles and removes accounts, with a fresh
// step-up for every change, through the same UserStore the CLI uses.

// refusingMailbox is the relay for an invitation that does not go out: mail
// to refuse is refused, everything else (passcodes) is delivered.
type refusingMailbox struct {
	*mailbox
	refuse string
}

func (m *refusingMailbox) Send(ctx context.Context, to, subject, body string) error {
	if strings.EqualFold(to, m.refuse) {
		return errors.New("550 relay says no")
	}
	return m.mailbox.Send(ctx, to, subject, body)
}

type accountsFixture struct {
	*runnerTestServer
	ann, bea browser
}

// newAccountsFixture is an accounts server with ann (administrator), bea and
// cal (members), and ann and bea signed in. Mail to refuse is refused.
func newAccountsFixture(t *testing.T, refuse string) *accountsFixture {
	t.Helper()
	server, _, inbox, _, users := accountServerPartsForTest(t)
	for _, name := range []string{"bea", "cal"} {
		if err := users.AddWithEmail(context.Background(), name, "correct-horse-battery", "", name+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	server.UseMailer(&refusingMailbox{mailbox: inbox, refuse: refuse})
	s := &runnerTestServer{server: server, handler: server.Handler(), inbox: inbox}
	return &accountsFixture{runnerTestServer: s, ann: s.signIn(t, testEmail), bea: s.signIn(t, "bea@example.test")}
}

type accountsListForTest struct {
	Accounts []struct {
		ID, Name, Email, Role string
		TwoFactor             bool `json:"twoFactor"`
	} `json:"accounts"`
	Self           string `json:"self"`
	MailConfigured bool   `json:"mailConfigured"`
	SignInURL      string `json:"signInUrl"`
	Seats          struct{ Active, Max int }
}

func (f *accountsFixture) list(t *testing.T) accountsListForTest {
	t.Helper()
	response := f.do(t, &f.ann, http.MethodGet, "/api/auth/accounts", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list = %d %s", response.Code, response.Body)
	}
	var out accountsListForTest
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (l accountsListForTest) id(t *testing.T, name string) string {
	t.Helper()
	for _, account := range l.Accounts {
		if account.Name == name {
			return account.ID
		}
	}
	t.Fatalf("no account %s in %+v", name, l.Accounts)
	return ""
}

func (f *accountsFixture) invite(t *testing.T, b browser, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, &b, http.MethodPost, "/api/auth/accounts", body, map[string]string{"Origin": "https://example.com"})
}

type auditEntryForTest struct {
	Actor, Op, Target string
	Detail            map[string]any
}

func serverTrail(t *testing.T, f *accountsFixture) []auditEntryForTest {
	t.Helper()
	response := f.do(t, &f.ann, http.MethodGet, "/api/audit?scope=server&limit=200", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("audit = %d %s", response.Code, response.Body)
	}
	var out struct{ Entries []auditEntryForTest }
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Entries
}

func findAudit(entries []auditEntryForTest, op, target string) (auditEntryForTest, bool) {
	for _, entry := range entries {
		if entry.Op == op && entry.Target == target {
			return entry, true
		}
	}
	return auditEntryForTest{}, false
}

func TestAccountsAPIIsForAdministrators(t *testing.T) {
	f := newAccountsFixture(t, "")
	if code := f.do(t, nil, http.MethodGet, "/api/auth/accounts", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", code)
	}
	// A member sees nothing and changes nothing, stepped up or not.
	stepUp(t, f.runnerTestServer, f.bea)
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/auth/accounts", ""},
		{http.MethodPost, "/api/auth/accounts", `{"name":"eve","email":"eve@example.test","role":"admin"}`},
		{http.MethodPut, "/api/auth/accounts/role", `{"id":"x","role":"admin"}`},
		{http.MethodDelete, "/api/auth/accounts?id=x", ""},
	} {
		response := f.do(t, &f.bea, request.method, request.path, request.body, nil)
		if response.Code != http.StatusForbidden || errorCode(t, response) != "PERMISSION_DENIED" {
			t.Errorf("member %s %s = %d %s", request.method, request.path, response.Code, response.Body)
		}
	}
	// CSRF applies as to every other write.
	noCSRF := browser{cookies: f.ann.cookies}
	if code := f.do(t, &noCSRF, http.MethodPost, "/api/auth/accounts", `{"name":"eve","email":"eve@example.test"}`, nil).Code; code != http.StatusForbidden {
		t.Fatalf("invite without CSRF = %d", code)
	}

	list := f.list(t)
	if len(list.Accounts) != 3 || list.Self != list.id(t, "ann") || !list.MailConfigured {
		t.Fatalf("list = %+v", list)
	}
	for _, account := range list.Accounts {
		if account.Email != account.Name+"@example.test" || account.TwoFactor {
			t.Errorf("account = %+v", account)
		}
	}
	if raw := f.do(t, &f.ann, http.MethodGet, "/api/auth/accounts", "", nil).Body.String(); strings.Contains(raw, "argon2") || strings.Contains(raw, "hash") {
		t.Fatalf("the list carries password hashes: %s", raw)
	}
}

func TestAccountsAPIOnLoopbackSaysThereAreNone(t *testing.T) {
	pm := projectManagerForTest(t)
	server := serverForTest(t, pm, nil, nil)
	s := &runnerTestServer{server: server, handler: server.Handler()}
	response := s.do(t, nil, http.MethodGet, "/api/auth/accounts", "", nil)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "does not use accounts") {
		t.Fatalf("loopback list = %d %s", response.Code, response.Body)
	}
	if code := s.do(t, nil, http.MethodPost, "/api/auth/accounts", `{"name":"eve","email":"eve@example.test"}`, nil).Code; code != http.StatusNotFound {
		t.Fatalf("loopback invite = %d", code)
	}
}

func TestInviteNeedsAStepUpAndMailsTheWayIn(t *testing.T) {
	f := newAccountsFixture(t, "")
	logs := logtest.Capture(t)
	body := `{"name":"dan","email":"Dan@Example.test","role":"member"}`

	response := f.invite(t, f.ann, body)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "STEP_UP_REQUIRED" {
		t.Fatalf("invite without step-up = %d %s", response.Code, response.Body)
	}
	if len(f.list(t).Accounts) != 3 {
		t.Fatal("an account was made without a step-up")
	}

	stepUp(t, f.runnerTestServer, f.ann)
	before := f.inbox.count()
	response = f.invite(t, f.ann, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", response.Code, response.Body)
	}
	var invited struct {
		Account struct{ ID, Name, Email, Role string }
		Email   struct{ Status, To string }
		SignIn  string `json:"signInUrl"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &invited); err != nil {
		t.Fatal(err)
	}
	if invited.Account.Name != "dan" || invited.Account.Role != "member" || invited.Account.Email != "Dan@Example.test" ||
		invited.Email.Status != "sent" || invited.SignIn != "https://example.com" {
		t.Fatalf("invited = %+v", invited)
	}
	f.inbox.waitFor(t, before+1)
	mail := f.inbox.last(t)
	if mail.to != "Dan@Example.test" || !strings.Contains(mail.body, "https://example.com") ||
		!strings.Contains(mail.body, "Dan@Example.test") || !strings.Contains(mail.body, "passcode") {
		t.Fatalf("invitation = %+v", mail)
	}

	// The same rules as `nodevas user add`: one account per name, one per
	// mailbox (case-insensitively), and a name the CLI would refuse.
	for _, refused := range []struct {
		body string
		code int
	}{
		{`{"name":"DAN","email":"other@example.test"}`, http.StatusConflict},
		{`{"name":"dana","email":"dan@example.test"}`, http.StatusConflict},
		{`{"name":"no spaces","email":"x@example.test"}`, http.StatusBadRequest},
		{`{"name":"x","email":"not an address"}`, http.StatusBadRequest},
		{`{"name":"x","email":""}`, http.StatusBadRequest},
		{`{"name":"x","email":"x@example.test","role":"owner"}`, http.StatusBadRequest},
	} {
		if response := f.invite(t, f.ann, refused.body); response.Code != refused.code {
			t.Errorf("invite %s = %d %s, want %d", refused.body, response.Code, response.Body, refused.code)
		}
	}

	// The invited person signs in the way the mail says.
	signInFrom(t, f.runnerTestServer, "dan@example.test", "192.0.2.80:4000")

	entry, ok := findAudit(serverTrail(t, f), "account.invite", invited.Account.ID)
	if !ok || entry.Actor != "ann" || entry.Detail["account"] != "dan" || entry.Detail["role"] != "member" ||
		entry.Detail["emailSent"] != true {
		t.Fatalf("audit row = %+v (found %v)", entry, ok)
	}
	line := logs.One(t, map[string]any{"nodevas.audit.action": "account.invite"})
	logtest.Want(t, line, map[string]any{"event.category": "iam", "event.action": "iam.account.update",
		"event.outcome": "success", "user.name": "ann"})
}

func TestInviteWithoutMailStillMakesTheAccount(t *testing.T) {
	f := newAccountsFixture(t, "eve@example.test")
	stepUp(t, f.runnerTestServer, f.ann)
	response := f.invite(t, f.ann, `{"name":"eve","email":"eve@example.test","role":"admin"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", response.Code, response.Body)
	}
	var invited struct {
		Account struct{ ID, Role string }
		Email   struct{ Status, To string }
	}
	_ = json.Unmarshal(response.Body.Bytes(), &invited)
	if invited.Email.Status != "failed" || invited.Email.To != "eve@example.test" || invited.Account.Role != "admin" {
		t.Fatalf("invited = %+v", invited)
	}
	if strings.Contains(response.Body.String(), "550") {
		t.Fatal("the relay's answer reached the client")
	}
	if f.list(t).id(t, "eve") != invited.Account.ID {
		t.Fatal("the account was not kept")
	}
	entry, ok := findAudit(serverTrail(t, f), "account.invite", invited.Account.ID)
	if !ok || entry.Detail["emailSent"] != false || entry.Detail["delivery"] != "failed" {
		t.Fatalf("audit row = %+v", entry)
	}
}

func TestInviteIgnoresTheSeatLimitLikeTheCLI(t *testing.T) {
	f := newAccountsFixture(t, "")
	f.server.SetMaxActiveUsers(2)
	stepUp(t, f.runnerTestServer, f.ann)
	response := f.invite(t, f.ann, `{"name":"dan","email":"dan@example.test"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("invite at the seat limit = %d %s", response.Code, response.Body)
	}
	var invited struct{ Seats struct{ Active, Max int } }
	_ = json.Unmarshal(response.Body.Bytes(), &invited)
	if invited.Seats.Active != 2 || invited.Seats.Max != 2 {
		t.Fatalf("seats = %+v", invited.Seats)
	}
	if seats := f.list(t).Seats; seats.Active != 2 || seats.Max != 2 {
		t.Fatalf("list seats = %+v", seats)
	}
	// The limit is applied where it always was: at sign-in.
	request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(`{"email":"dan@example.test"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.81:4000"
	before := f.inbox.count()
	f.handler.ServeHTTP(httptest.NewRecorder(), request)
	f.inbox.waitFor(t, before+1)
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"email":"dan@example.test","otp":"`+f.inbox.code(t)+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "192.0.2.81:4000"
	answer := httptest.NewRecorder()
	f.handler.ServeHTTP(answer, login)
	if answer.Code == http.StatusOK || !strings.Contains(answer.Body.String(), "maximum number of people") {
		t.Fatalf("sign-in past the seat limit = %d %s", answer.Code, answer.Body)
	}
}

func TestAccountRoleAndRemoval(t *testing.T) {
	f := newAccountsFixture(t, "")
	list := f.list(t)
	ann, bea, cal := list.id(t, "ann"), list.id(t, "bea"), list.id(t, "cal")

	if response := f.do(t, &f.ann, http.MethodPut, "/api/auth/accounts/role", `{"id":"`+bea+`","role":"admin"}`, nil); response.Code != http.StatusForbidden || errorCode(t, response) != "STEP_UP_REQUIRED" {
		t.Fatalf("role without step-up = %d %s", response.Code, response.Body)
	}
	if response := f.do(t, &f.ann, http.MethodDelete, "/api/auth/accounts?id="+cal, "", nil); response.Code != http.StatusForbidden || errorCode(t, response) != "STEP_UP_REQUIRED" {
		t.Fatalf("remove without step-up = %d %s", response.Code, response.Body)
	}
	stepUp(t, f.runnerTestServer, f.ann)

	// ann is the only administrator: she can neither demote nor remove
	// herself, and the store would refuse the last administrator anyway.
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/auth/accounts/role", `{"id":"` + ann + `","role":"member"}`},
		{http.MethodDelete, "/api/auth/accounts?id=" + ann, ""},
	} {
		response := f.do(t, &f.ann, request.method, request.path, request.body, nil)
		if response.Code != http.StatusConflict || errorCode(t, response) != "FAILED_PRECONDITION" {
			t.Errorf("self %s = %d %s", request.method, response.Code, response.Body)
		}
	}
	if code := f.do(t, &f.ann, http.MethodPut, "/api/auth/accounts/role", `{"id":"nobody","role":"admin"}`, nil).Code; code != http.StatusNotFound {
		t.Fatalf("unknown account = %d", code)
	}
	if code := f.do(t, &f.ann, http.MethodPut, "/api/auth/accounts/role", `{"id":"`+bea+`","role":"owner"}`, nil).Code; code != http.StatusBadRequest {
		t.Fatalf("bad role = %d", code)
	}

	response := f.do(t, &f.ann, http.MethodPut, "/api/auth/accounts/role", `{"id":"`+bea+`","role":"admin"}`, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"role":"admin"`) {
		t.Fatalf("promote = %d %s", response.Code, response.Body)
	}
	// The role is part of the session's revision: bea signs in again to act
	// as an administrator.
	if code := f.do(t, &f.bea, http.MethodGet, "/api/graph", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("bea's old session after the role change = %d", code)
	}

	cal2 := signInFrom(t, f.runnerTestServer, "cal@example.test", "192.0.2.82:4000")
	if response := f.do(t, &f.ann, http.MethodDelete, "/api/auth/accounts?id="+cal, "", nil); response.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", response.Code, response.Body)
	}
	if code := f.do(t, &cal2, http.MethodGet, "/api/graph", "", nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("a removed account's session = %d", code)
	}
	if len(f.list(t).Accounts) != 2 {
		t.Fatal("cal is still listed")
	}
	if code := f.do(t, &f.ann, http.MethodDelete, "/api/auth/accounts?id="+cal, "", nil).Code; code != http.StatusNotFound {
		t.Fatalf("remove twice = %d", code)
	}

	trail := serverTrail(t, f)
	if entry, ok := findAudit(trail, "account.role", bea); !ok || entry.Detail["role"] != "admin" || entry.Detail["from"] != "member" {
		t.Errorf("role row = %+v", entry)
	}
	if entry, ok := findAudit(trail, "account.remove", cal); !ok || entry.Detail["account"] != "cal" {
		t.Errorf("remove row = %+v", entry)
	}
	for _, entry := range trail {
		if strings.HasPrefix(entry.Op, "account.") && entry.Target == ann {
			t.Errorf("a refused change was audited: %+v", entry)
		}
	}
}
