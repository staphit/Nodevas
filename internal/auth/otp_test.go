package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/db/dbtest"
	"nodevas/internal/identity"
)

// annEmail is the address otpStoreForTest registers for "ann".
const annEmail = "ann@example.test"

func userStoreForTest(t *testing.T) *UserStore {
	t.Helper()
	dbtest.UsePostgres(t)
	users, err := NewUserStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUserStore: %v", err)
	}
	// The store holds an open SQLite handle now, and Windows will not let
	// t.TempDir remove a file another process still has open.
	t.Cleanup(func() { _ = users.Close() })
	return users
}

func otpStoreForTest(t *testing.T) (*SessionAuth, *UserStore) {
	t.Helper()
	users := userStoreForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatalf("add ann: %v", err)
	}
	if err := users.SetEmail(context.Background(), "ann", annEmail); err != nil {
		t.Fatalf("set ann email: %v", err)
	}
	return NewSessionAuth(users), users
}

// pastCooldown moves every resend cooldown into the past, so a test about what
// a second request does need not wait thirty seconds to make one.
func pastCooldown(sessions *SessionAuth) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	for key := range sessions.lastOTPRequest {
		sessions.lastOTPRequest[key] = time.Now().Add(-otpResendCooldown)
	}
}

func liveSessions(sessions *SessionAuth) int {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return len(sessions.sessions)
}

func TestAPasscodeSignsInOnceAndThenIsGone(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	if challenge.Email != annEmail {
		t.Fatalf("email = %q", challenge.Email)
	}
	if len(challenge.Code) != OTPLength {
		t.Fatalf("code = %q", challenge.Code)
	}

	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("replay: err = %v, want ErrBadCredentials", err)
	}
}

// The address typed at the door finds the account; it is never where mail
// goes. Whatever case and spacing the person used, the challenge carries the
// address the administrator registered.
func TestThePasscodeGoesToTheRegisteredAddressNotTheTypedOne(t *testing.T) {
	users := userStoreForTest(t)
	if err := users.Add(context.Background(), "ann", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := users.SetEmail(context.Background(), "ann", "Ann.Smith@Example.test"); err != nil {
		t.Fatal(err)
	}
	sessions := NewSessionAuth(users)

	challenge, err := sessions.RequestOTP(nil, "  ann.smith@EXAMPLE.TEST ")
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	if challenge.Email != "Ann.Smith@Example.test" {
		t.Fatalf("recipient = %q, want the stored address", challenge.Email)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, "ANN.SMITH@example.test", challenge.Code); err != nil {
		t.Fatalf("sign-in with differently cased address: %v", err)
	}
}

// An administrator moving a compromised account to a new mailbox expects the
// old mailbox to lose access at once. A passcode already sitting in it must not
// open the account under its new address.
func TestAPasscodeMailedToAnOldAddressDiesWhenTheAddressChanges(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	const moved = "ann.new@example.test"
	if err := users.SetEmail(context.Background(), "ann", moved); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, moved, challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("old passcode under the new address: err = %v, want ErrBadCredentials", err)
	}
	if liveSessions(sessions) != 0 {
		t.Fatal("a session opened from a passcode mailed to the old address")
	}
}

// A passcode read off a phone screen arrives with the case mangled and often
// with a space in the middle. Refusing those would make the sign-in a
// transcription test.
func TestAPasscodeIsAcceptedRegardlessOfCaseAndSpacing(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	mangled := " " + strings.ToLower(challenge.Code[:4]) + " " + challenge.Code[4:] + " "

	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, mangled); err != nil {
		t.Fatalf("mangled %q: %v", mangled, err)
	}
}

func TestAWrongPasscodeIsRefusedAndDiesAfterAFewGuesses(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}

	for i := 0; i < maxOTPAttempts; i++ {
		if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, "AAAAAAAA"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("guess %d: err = %v", i, err)
		}
	}
	// The real code is now worthless: guessing must cost a fresh request,
	// which is throttled and which the account holder sees arrive.
	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("after the guesses the real code still worked: %v", err)
	}
}

func TestAnExpiredPasscodeIsRefused(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	actor, _, _, err := users.AccountByEmail(context.Background(), annEmail)
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}

	sessions.mu.Lock()
	sessions.otps[actor.ID].expires = time.Now().Add(-time.Second)
	sessions.mu.Unlock()

	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("err = %v, want ErrBadCredentials", err)
	}
}

// An address is public, so asking for a passcode must not be a way to sign
// somebody out. Only completing a sign-in does that.
func TestRequestingAPasscodeLeavesExistingSessionsAlone(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got := liveSessions(sessions); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}

	pastCooldown(sessions)
	if _, err := sessions.RequestOTP(nil, annEmail); err != nil {
		t.Fatalf("second RequestOTP: %v", err)
	}
	if got := liveSessions(sessions); got != 1 {
		t.Fatalf("sessions after a passcode request = %d, want 1: requesting signed somebody out", got)
	}
}

// Completing a sign-in leaves the new device as the only one signed in, both
// in this process and in what a restart would load.
func TestASuccessfulSignInEndsTheAccountsOtherSessions(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	first, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	_, oldToken, _, err := sessions.LoginWithOTP(nil, annEmail, first.Code)
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	passwordToken := signedIn(t, sessions)

	pastCooldown(sessions)
	second, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("second RequestOTP: %v", err)
	}
	_, newToken, _, err := sessions.LoginWithOTP(nil, annEmail, second.Code)
	if err != nil {
		t.Fatalf("second login: %v", err)
	}

	for name, token := range map[string]string{"earlier passcode": oldToken, "password": passwordToken} {
		if _, err := sessions.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s session = %v, want unauthenticated", name, err)
		}
		if _, err := NewSessionAuth(users).Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s session after restart = %v, want unauthenticated", name, err)
		}
	}
	if _, err := sessions.Authenticate(requestWith(newToken)); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if got := liveSessions(sessions); got != 1 {
		t.Fatalf("sessions = %d, want only the new one", got)
	}
}

// Another account's sessions are not this sign-in's business.
func TestASignInLeavesOtherAccountsAlone(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	if err := users.Add(context.Background(), "bob", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	_, bobToken, _, err := sessions.Login(context.Background(), "bob", "correct-horse-battery")
	if err != nil {
		t.Fatalf("bob login: %v", err)
	}
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Authenticate(requestWith(bobToken)); err != nil {
		t.Fatalf("bob was signed out by ann's sign-in: %v", err)
	}
}

// An unknown address must not be distinguishable from a known one by the
// error a caller can see. The HTTP layer answers 202 either way; this is the
// layer underneath keeping its side of that bargain.
func TestAnUnknownAddressYieldsTheSwallowedError(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	for _, address := range []string{"nobody@example.test", "not an address", ""} {
		if _, err := sessions.RequestOTP(nil, address); !errors.Is(err, ErrNoSuchAccount) {
			t.Fatalf("%q: err = %v, want ErrNoSuchAccount", address, err)
		}
	}
}

// The per-address budget is charged before the lookup, so an unregistered
// address runs into the cooldown exactly as a registered one does, and the 429
// cannot be used to tell them apart.
func TestTheResendCooldownDoesNotRevealWhetherAnAddressExists(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	for _, address := range []string{annEmail, "nobody@example.test"} {
		_, _ = sessions.RequestOTP(nil, address)
		if _, err := sessions.RequestOTP(nil, strings.ToUpper(address)); !errors.Is(err, ErrTooManyOTPRequests) {
			t.Fatalf("%s: immediate resend err = %v, want ErrTooManyOTPRequests", address, err)
		}
	}
}

func TestPasscodeRequestsAreThrottledPerAddress(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	if _, err := sessions.RequestOTP(nil, annEmail); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err := sessions.RequestOTP(nil, annEmail); !errors.Is(err, ErrTooManyOTPRequests) {
		t.Fatalf("immediate resend err = %v, want ErrTooManyOTPRequests", err)
	}
}

func TestPasscodeRequestCooldownIsThirtySeconds(t *testing.T) {
	if otpResendCooldown != 30*time.Second {
		t.Fatalf("otp resend cooldown = %s, want 30s", otpResendCooldown)
	}
}

// Anyone can add an address to the cooldown map now, so it must not grow
// without bound: entries past the cooldown are swept.
func TestTheCooldownMapForgetsExpiredAddresses(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	_, _ = sessions.RequestOTP(nil, "first@example.test")
	pastCooldown(sessions)
	_, _ = sessions.RequestOTP(nil, "second@example.test")

	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	if _, kept := sessions.lastOTPRequest["first@example.test"]; kept {
		t.Fatal("an address past its cooldown is still remembered")
	}
}

// Sign-in guessing is charged per address as well as per source, and an
// address nobody registered is charged the same way.
func TestSignInGuessingIsThrottledPerAddress(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	for _, address := range []string{annEmail, "nobody@example.test"} {
		throttled := false
		for i := 0; i <= LoginFailureLimit; i++ {
			_, _, _, err := sessions.LoginWithOTP(nil, address, "AAAAAAAA")
			if errors.Is(err, ErrTooManyLogins) {
				throttled = true
				break
			}
			if !errors.Is(err, ErrBadCredentials) {
				t.Fatalf("%s guess %d: err = %v", address, i, err)
			}
		}
		if !throttled {
			t.Fatalf("%s: guessing was never throttled", address)
		}
	}
}

// The seat limit counts people. Somebody already signed in may sign in again;
// a different person is refused while the seats are full.
func TestSeatLimitCountsPeopleNotDevices(t *testing.T) {
	users := userStoreForTest(t)
	for _, person := range []struct{ name, email string }{
		{"ann", "ann@example.test"},
		{"bob", "bob@example.test"},
	} {
		if err := users.AddWithEmail(context.Background(), person.name, "correct-horse-battery", "", person.email); err != nil {
			t.Fatalf("add %s: %v", person.name, err)
		}
	}
	sessions := NewSessionAuth(users)
	sessions.SetMaxActiveUsers(1)

	signIn := func(email string) error {
		pastCooldown(sessions)
		challenge, err := sessions.RequestOTP(nil, email)
		if err != nil {
			return err
		}
		_, _, _, err = sessions.LoginWithOTP(nil, email, challenge.Code)
		return err
	}

	if err := signIn("ann@example.test"); err != nil {
		t.Fatalf("ann: %v", err)
	}
	if err := signIn("bob@example.test"); !errors.Is(err, ErrTooManyActiveUsers) {
		t.Fatalf("bob: err = %v, want ErrTooManyActiveUsers", err)
	}
	// Ann again on a full server: her own seat, so the revocation of her
	// first session must not cost her the seat she is signing in with.
	if err := signIn("ann@example.test"); err != nil {
		t.Fatalf("ann again: %v", err)
	}
	if sessions.ActiveUsers() != 1 {
		t.Fatalf("active users = %d, want 1", sessions.ActiveUsers())
	}
}

// A refused sign-in must not have spent the passcode: a full server should
// cost the person a wait, not a fresh request.
func TestASeatRefusalDoesNotBurnThePasscode(t *testing.T) {
	users := userStoreForTest(t)
	for _, person := range []struct{ name, email string }{
		{"ann", "ann@example.test"},
		{"bob", "bob@example.test"},
	} {
		if err := users.AddWithEmail(context.Background(), person.name, "correct-horse-battery", "", person.email); err != nil {
			t.Fatalf("add %s: %v", person.name, err)
		}
	}
	sessions := NewSessionAuth(users)
	sessions.SetMaxActiveUsers(1)

	// Ann takes the only seat.
	annChallenge, err := sessions.RequestOTP(nil, "ann@example.test")
	if err != nil {
		t.Fatalf("ann RequestOTP: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, "ann@example.test", annChallenge.Code); err != nil {
		t.Fatalf("ann login: %v", err)
	}

	bobChallenge, err := sessions.RequestOTP(nil, "bob@example.test")
	if err != nil {
		t.Fatalf("bob RequestOTP: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, "bob@example.test", bobChallenge.Code); !errors.Is(err, ErrTooManyActiveUsers) {
		t.Fatalf("bob while full: err = %v, want ErrTooManyActiveUsers", err)
	}

	// Ann leaves; Bob's original passcode still works, which is what proves
	// the refusal did not consume it.
	sessions.mu.Lock()
	sessions.sessions = map[string]*Session{}
	sessions.mu.Unlock()
	if _, _, _, err := sessions.LoginWithOTP(nil, "bob@example.test", bobChallenge.Code); err != nil {
		t.Fatalf("bob after a seat freed: %v", err)
	}
}

// A browser that goes away mid-sign-in has not offered a credential. The
// distinction matters because the caller above turns ErrBadCredentials into an
// "auth.signin.failed" audit line, and a disconnect is not that event.
func TestACancelledRequestIsNotAFailedSignIn(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil).WithContext(ctx)

	_, _, _, err := sessions.LoginWithOTP(request, annEmail, "AAAAAAAA")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoginWithOTP on a cancelled request = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrBadCredentials) {
		t.Fatal("a cancelled request must not read as a rejected credential")
	}

	if _, err := sessions.RequestOTP(request, annEmail); !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestOTP on a cancelled request = %v, want context.Canceled", err)
	}

	if _, _, _, err := sessions.Login(ctx, "ann", "correct-horse-battery"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Login on a cancelled context = %v, want context.Canceled", err)
	}
}

// unreadableAccounts makes every read of the accounts table fail until the
// returned function puts it back, the way an unreachable database would.
func unreadableAccounts(t *testing.T, users *UserStore) func() {
	t.Helper()
	if _, err := users.database.ExecContext(context.Background(),
		`ALTER TABLE accounts RENAME TO accounts_unreadable`); err != nil {
		t.Fatalf("hide accounts: %v", err)
	}
	return func() {
		t.Helper()
		if _, err := users.database.ExecContext(context.Background(),
			`ALTER TABLE accounts_unreadable RENAME TO accounts`); err != nil {
			t.Fatalf("restore accounts: %v", err)
		}
	}
}

// An account lookup that fails is the server's fault, not an address nobody
// registered. Reported as one, a database outage would turn every passcode
// request and every sign-in into an unknown-account line, which is what
// somebody probing for accounts looks like. A cancelled request still says it
// was cancelled, whatever the database is doing.
func TestAFailedAccountReadIsNotAnUnknownAddress(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	restore := unreadableAccounts(t, users)

	for _, address := range []string{annEmail, "nobody@example.test"} {
		challenge, err := sessions.RequestOTP(nil, address)
		if err == nil || errors.Is(err, ErrNoSuchAccount) || errors.Is(err, context.Canceled) || challenge != (Challenge{}) {
			t.Fatalf("RequestOTP(%q) with accounts unreadable = %+v, %v; want the read's own error", address, challenge, err)
		}
		result, err := sessions.SignInWithOTP(nil, address, "AAAAAAAA")
		if err == nil || errors.Is(err, ErrBadCredentials) || errors.Is(err, ErrNoSuchAccount) {
			t.Fatalf("SignInWithOTP(%q) with accounts unreadable = %v; want the read's own error", address, err)
		}
		if result.Refusal != (SignInRefusal{}) || result.Token != "" || result.Pending != nil {
			t.Fatalf("SignInWithOTP(%q) with accounts unreadable = %+v; want nothing", address, result)
		}
	}
	sessions.mu.Lock()
	minted := len(sessions.otps)
	sessions.mu.Unlock()
	if minted != 0 {
		t.Fatalf("%d passcodes minted while accounts were unreadable", minted)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil).WithContext(ctx)
	request.RemoteAddr = "203.0.113.9:4242"
	if _, err := sessions.RequestOTP(request, "carol@example.test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestOTP cancelled while accounts are unreadable = %v, want context.Canceled", err)
	}
	if _, err := sessions.SignInWithOTP(request, "carol@example.test", "AAAAAAAA"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SignInWithOTP cancelled while accounts are unreadable = %v, want context.Canceled", err)
	}

	// Readable again, an unknown address is the refusal it always was.
	restore()
	if _, err := sessions.RequestOTP(nil, "dave@example.test"); !errors.Is(err, ErrNoSuchAccount) {
		t.Fatalf("RequestOTP for an unknown address = %v, want ErrNoSuchAccount", err)
	}
	result, err := sessions.SignInWithOTP(nil, "dave@example.test", "AAAAAAAA")
	if !errors.Is(err, ErrBadCredentials) || result.Refusal != (SignInRefusal{Reason: RefusedUnknownAccount}) {
		t.Fatalf("SignInWithOTP for an unknown address = %+v, %v; want an unknown-account refusal", result.Refusal, err)
	}
}

// A sign-in reads a registered address's account and its second-factor flag
// in one statement, the same one an address nobody registered gets. Read by a
// second query, the flag cost a found account a round trip more, so its 401
// came measurably later than an unknown address's, before any passcode was
// checked. The table it lives in is how a test sees that: unreadable, it must
// fail every sign-in alike, rather than only those for a registered address.
func TestASignInReadsTheSameForEveryAddress(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	if _, err := users.database.ExecContext(context.Background(),
		`ALTER TABLE account_totp RENAME TO account_totp_unreadable`); err != nil {
		t.Fatalf("hide account_totp: %v", err)
	}
	for _, address := range []string{annEmail, "nobody@example.test"} {
		result, err := sessions.SignInWithOTP(nil, address, "AAAAAAAA")
		if !errors.Is(err, errAccountLookup) || errors.Is(err, ErrBadCredentials) {
			t.Fatalf("SignInWithOTP(%q) with account_totp unreadable = %v; want the lookup's own error, as for every address", address, err)
		}
		if result != (SignInResult{}) {
			t.Fatalf("SignInWithOTP(%q) with account_totp unreadable = %+v; want nothing", address, result)
		}
	}
	if _, err := users.database.ExecContext(context.Background(),
		`ALTER TABLE account_totp_unreadable RENAME TO account_totp`); err != nil {
		t.Fatalf("restore account_totp: %v", err)
	}
	for _, address := range []string{annEmail, "nobody@example.test"} {
		if _, err := sessions.SignInWithOTP(nil, address, "AAAAAAAA"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("SignInWithOTP(%q) = %v, want ErrBadCredentials", address, err)
		}
	}
}

// A database that hangs fails a read through the driver's own connect
// timeout, and that error wraps context.DeadlineExceeded while the request is
// still waiting for its answer. It is still the server's fault: neither an
// address nobody registered nor a client that hung up, and nothing about it
// may let the HTTP layer take it for either.
func TestAStalledDatabaseIsNotACancelledRequest(t *testing.T) {
	stall := dbtest.UseStalledPostgres(t)
	if stall == nil {
		t.Skipf("set %s to run against PostgreSQL", dbtest.Env)
	}
	users, err := NewUserStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUserStore: %v", err)
	}
	t.Cleanup(func() { _ = users.Close() })
	if users.database.Backend() != db.Postgres {
		t.Fatalf("backend = %s, want postgres", users.database.Backend())
	}
	if err := users.AddWithEmail(context.Background(), "ann", "correct-horse-battery", "", annEmail); err != nil {
		t.Fatalf("add ann: %v", err)
	}
	sessions := NewSessionAuth(users)
	// No idle connection to fall back on: every read must dial the stalled
	// server.
	users.database.Reader().SetMaxIdleConns(0)
	stall.Hang()

	contextError := func(err error) bool {
		return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	}
	for _, address := range []string{annEmail, "nobody@example.test"} {
		challenge, err := sessions.RequestOTP(nil, address)
		if err == nil || errors.Is(err, ErrNoSuchAccount) || contextError(err) || challenge != (Challenge{}) {
			t.Fatalf("RequestOTP(%q) with the database stalled = %+v, %v; want a failed read", address, challenge, err)
		}
		result, err := sessions.SignInWithOTP(nil, address, "AAAAAAAA")
		if err == nil || errors.Is(err, ErrBadCredentials) || contextError(err) {
			t.Fatalf("SignInWithOTP(%q) with the database stalled = %v; want a failed read", address, err)
		}
		if result.Refusal != (SignInRefusal{}) || result.Token != "" || result.Pending != nil {
			t.Fatalf("SignInWithOTP(%q) with the database stalled = %+v; want nothing", address, result)
		}
	}
}

// Visitor access is gone. A session it opened before the upgrade — still in the
// sessions table, pinned to the "visitor" id and revision — names no account,
// so the revision check refuses it and drops it.
func TestALeftoverVisitorSessionIsRefused(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	_, token, _, err := sessions.openSession(
		identity.Actor{ID: "visitor", Name: "visitor", Role: identity.Role("visitor")}, "visitor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Authenticate(requestWith(token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("leftover visitor session = %v, want ErrUnauthenticated", err)
	}
}
