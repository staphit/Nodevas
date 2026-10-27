package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/logging/logtest"
)

func TestAnExpiredSessionIsLoggedOnceWhenItIsPresented(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	token := signedIn(t, sessions)
	sessions.mu.Lock()
	sessions.sessions[sessionKey(token)].expires = time.Now().Add(-time.Second)
	sessions.mu.Unlock()

	logs := logtest.Capture(t)
	for attempt := 0; attempt < 2; attempt++ {
		request := requestWith(token)
		request.RemoteAddr = "203.0.113.7:4242"
		if _, err := sessions.Authenticate(request); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("attempt %d: Authenticate = %v, want unauthenticated", attempt, err)
		}
	}

	// The refusal forgets the session, so the second attempt is a plain
	// unknown cookie and says nothing.
	record := logs.One(t, map[string]any{"event.action": "auth.session.expired"})
	logtest.Want(t, record, map[string]any{
		"log.level":      "info",
		"message":        "auth.session.expired failure",
		"service.name":   "nodevas",
		"event.category": "authentication",
		"event.outcome":  "failure",
		"user.name":      "ann",
		"client.ip":      "203.0.113.7",
	})
	if id, _ := record["user.id"].(string); id == "" {
		t.Errorf("user.id = %v, want the account's id", record["user.id"])
	}
	logs.Never(t, token, sessionKey(token))
}

// A delete that does not commit keeps the expired session in the map, refused,
// for a later check to retry. Presenting it again, before and after the retry
// commits, adds no line.
func TestAnExpiredSessionWhoseDeleteFailsIsStillLoggedOnce(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	token := signedIn(t, sessions)
	key := sessionKey(token)
	sessions.mu.Lock()
	sessions.sessions[key].expires = time.Now().Add(-time.Second)
	sessions.mu.Unlock()
	sessions.store.runTx = func(context.Context, func(*sql.Tx) error) error {
		return errors.New("injected session delete failure")
	}
	kept := func() bool {
		sessions.mu.Lock()
		defer sessions.mu.Unlock()
		_, ok := sessions.sessions[key]
		return ok
	}

	logs := logtest.Capture(t)
	present := func() {
		t.Helper()
		if _, err := sessions.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("Authenticate = %v, want unauthenticated", err)
		}
	}
	present()
	present()
	if !kept() {
		t.Fatal("a delete that did not commit forgot the session")
	}
	sessions.store.runTx = nil
	present()
	if kept() {
		t.Fatal("the retried delete kept the session")
	}
	present()

	if lines := logs.Find(t, map[string]any{"event.action": "auth.session.expired"}); len(lines) != 1 {
		t.Fatalf("%d expired-session lines, want 1:\n%s", len(lines), logs.Text())
	}
}

// A refused sign-in gets one answer whatever went wrong, so a caller cannot
// tell an unknown address from a wrong passcode. The refusal tells the log
// which it was, and whose account the attempt named.
func TestARefusedSignInTellsTheLogWhyAndForWhom(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	ann, _, _, err := users.AccountByEmail(context.Background(), annEmail)
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	refusal := func(email, code string) SignInRefusal {
		t.Helper()
		result, err := sessions.SignInWithOTP(nil, email, code)
		if err != ErrBadCredentials || result.Actor != (identity.Actor{}) || result.Token != "" || result.Pending != nil {
			t.Fatalf("SignInWithOTP(%q) = %+v, %v; want ErrBadCredentials and nothing else", email, result, err)
		}
		return result.Refusal
	}
	forAnn := func(reason string) SignInRefusal {
		return SignInRefusal{Reason: reason, Account: ann, Email: annEmail}
	}
	check := func(what string, got, want SignInRefusal) {
		t.Helper()
		if got != want {
			t.Errorf("%s: refusal = %+v, want %+v", what, got, want)
		}
	}
	const guess = "AAAAAAAA"

	check("an address nobody registered", refusal("nobody@example.test", guess), SignInRefusal{Reason: RefusedUnknownAccount})
	check("not an address at all", refusal("nobody", guess), SignInRefusal{Reason: RefusedUnknownAccount})
	check("no passcode at all", refusal(annEmail, ""), SignInRefusal{Reason: RefusedBadCredentials})
	check("no passcode was sent", refusal(annEmail, guess), forAnn(RefusedExpiredCode))

	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	for attempt := 1; attempt < maxOTPAttempts; attempt++ {
		check("a wrong passcode", refusal(annEmail, guess), forAnn(RefusedBadCredentials))
	}
	check("the guess that burns it", refusal(annEmail, guess), forAnn(RefusedTooManyAttempts))
	check("the right passcode, burnt", refusal(annEmail, challenge.Code), forAnn(RefusedExpiredCode))
}

func TestARefusedSecondFactorTellsTheLogWhyAndForWhom(t *testing.T) {
	f := newTOTPFixture(t)
	secret, _ := f.enroll(t)
	ann, _, _, err := f.users.AccountByEmail(context.Background(), annEmail)
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	result, err := f.sessions.SignInWithOTP(nil, annEmail, f.passcode(t))
	if err != nil || result.Pending == nil {
		t.Fatalf("SignInWithOTP = %+v, %v", result, err)
	}
	ticket := result.Pending.Ticket

	// No pending sign-in: nobody to name.
	done, err := f.sessions.CompleteSignIn(nil, "not-a-ticket", f.code(secret))
	if !errors.Is(err, ErrSignInExpired) || done.Refusal != (SignInRefusal{Reason: RefusedExpiredCode}) {
		t.Fatalf("unknown ticket = %+v, %v", done.Refusal, err)
	}
	wrong := "000000"
	if wrong == f.code(secret) {
		wrong = "111111"
	}
	for attempt := 1; attempt <= maxSecondFactorAttempts; attempt++ {
		want := SignInRefusal{Reason: RefusedBadCredentials, Account: ann}
		if attempt == maxSecondFactorAttempts {
			want.Reason = RefusedTooManyAttempts
		}
		done, err := f.sessions.CompleteSignIn(nil, ticket, wrong)
		if !errors.Is(err, ErrTOTPCode) || done.Refusal != want || done.Actor != (identity.Actor{}) || done.Token != "" {
			t.Fatalf("wrong code %d = %+v, %v; want refusal %+v", attempt, done, err, want)
		}
	}
	// The ticket is gone now, and with it who it was for.
	done, err = f.sessions.CompleteSignIn(nil, ticket, f.code(secret))
	if !errors.Is(err, ErrSignInExpired) || done.Refusal != (SignInRefusal{Reason: RefusedExpiredCode}) {
		t.Fatalf("burnt ticket = %+v, %v", done.Refusal, err)
	}
}

// What was typed at the door reaches the log only once it parses as an
// address, and reads the same however it was typed.
func TestTypedAddressIsAnAddressOrNothing(t *testing.T) {
	for typed, want := range map[string]string{
		"nobody@example.test":          "nobody@example.test",
		"  Nobody@Example.TEST ":       "nobody@example.test",
		"Nobody <Nobody@Example.test>": "nobody@example.test",
		"K7Q2M9XP":                     "",
		"correct horse battery staple": "",
		"":                             "",
		strings.Repeat("a", 400) + "@example.test": "",
	} {
		if got := TypedAddress(typed); got != want {
			t.Errorf("TypedAddress(%q) = %q, want %q", typed, got, want)
		}
	}
}

// The session check reads the account's row anyway; the address it hands back
// is the registered one, for the request's log line.
func TestAuthenticateWithEmailNamesTheRegisteredAddress(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	token := signedIn(t, sessions)
	actor, email, err := sessions.AuthenticateWithEmail(requestWith(token))
	if err != nil || actor.Name != "ann" || email != annEmail {
		t.Fatalf("AuthenticateWithEmail = %+v, %q, %v; want ann, %s", actor, email, err, annEmail)
	}
	if plain, err := sessions.Authenticate(requestWith(token)); err != nil || plain != actor {
		t.Fatalf("Authenticate = %+v, %v; want %+v", plain, err, actor)
	}
	if _, email, err := sessions.AuthenticateWithEmail(requestWith("nobody's")); !errors.Is(err, ErrUnauthenticated) || email != "" {
		t.Fatalf("an unknown cookie = %q, %v", email, err)
	}
}

func TestSecondFactorKindJudgesTheShapeAlone(t *testing.T) {
	for code, want := range map[string]string{
		"123456":         SecondFactorTOTP,
		"123 456":        SecondFactorTOTP,
		"123-456":        SecondFactorTOTP,
		"1234567":        SecondFactorRecovery,
		"12345":          SecondFactorRecovery,
		"abcd-efgh-2345": SecondFactorRecovery,
		"":               SecondFactorRecovery,
	} {
		if got := SecondFactorKind(code); got != want {
			t.Errorf("SecondFactorKind(%q) = %q, want %q", code, got, want)
		}
	}
}
