package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// annEmail is the address otpStoreForTest registers for "ann".
const annEmail = "ann@example.test"

func userStoreForTest(t *testing.T) *UserStore {
	t.Helper()
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
	actor, _, _, ok := users.AccountByEmail(context.Background(), annEmail)
	if !ok {
		t.Fatal("AccountByEmail failed")
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

// The visitor credential opens only its own door. Offered to the account
// endpoints it is just a wrong address and a wrong passcode; offered to its
// own endpoint it signs in, without ending anybody else's visit.
func TestTheVisitorCredentialOpensOnlyTheVisitorDoor(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	if err := sessions.SetVisitor(context.Background(), "777", "LOOKONLY"); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := sessions.LoginWithOTP(nil, "777", "LOOKONLY"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("account login with the visitor credential = %v, want ErrBadCredentials", err)
	}
	if _, err := sessions.RequestOTP(nil, "777"); !errors.Is(err, ErrNoSuchAccount) {
		t.Fatalf("passcode request with the visitor pin = %v, want ErrNoSuchAccount", err)
	}

	actor, first, _, err := sessions.LoginVisitor(nil, "777", "look only")
	if err != nil {
		t.Fatalf("visitor login: %v", err)
	}
	if actor.ID != VisitorID {
		t.Fatalf("actor = %+v, want the visitor", actor)
	}
	if _, _, _, err := sessions.LoginVisitor(nil, "777", "LOOKONLY"); err != nil {
		t.Fatalf("second visitor: %v", err)
	}
	if _, err := sessions.Authenticate(requestWith(first)); err != nil {
		t.Fatalf("a second visitor signed the first out: %v", err)
	}
	if _, _, _, err := sessions.LoginVisitor(nil, "777", "WRONGONE"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong visitor passcode = %v, want ErrBadCredentials", err)
	}
}
