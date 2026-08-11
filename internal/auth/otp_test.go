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
	if err := users.SetPin(context.Background(), "ann", "ann-pin-long-enough", "ann@example.test"); err != nil {
		t.Fatalf("set ann pin: %v", err)
	}
	return NewSessionAuth(users), users
}

func TestAPasscodeSignsInOnceAndThenIsGone(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	challenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	if challenge.Email != "ann@example.test" {
		t.Fatalf("email = %q", challenge.Email)
	}
	if len(challenge.Code) != OTPLength {
		t.Fatalf("code = %q", challenge.Code)
	}

	if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", challenge.Code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("replay: err = %v, want ErrBadCredentials", err)
	}
}

// A passcode read off a phone screen arrives with the case mangled and often
// with a space in the middle. Refusing those would make the second factor a
// transcription test.
func TestAPasscodeIsAcceptedRegardlessOfCaseAndSpacing(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	mangled := " " + strings.ToLower(challenge.Code[:4]) + " " + challenge.Code[4:] + " "

	if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", mangled); err != nil {
		t.Fatalf("mangled %q: %v", mangled, err)
	}
}

func TestAWrongPasscodeIsRefusedAndDiesAfterAFewGuesses(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}

	for i := 0; i < maxOTPAttempts; i++ {
		if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", "AAAAAAAA"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("guess %d: err = %v", i, err)
		}
	}
	// The real code is now worthless: guessing must cost a fresh request,
	// which is throttled and which the account holder sees arrive.
	if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("after the guesses the real code still worked: %v", err)
	}
}

func TestAnExpiredPasscodeIsRefused(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	actor, _, _, ok := users.VerifyPin(context.Background(), "ann-pin-long-enough")
	if !ok {
		t.Fatal("VerifyPin failed")
	}

	sessions.mu.Lock()
	sessions.otps[actor.ID].expires = time.Now().Add(-time.Second)
	sessions.mu.Unlock()

	if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", challenge.Code); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("err = %v, want ErrBadCredentials", err)
	}
}

// The rule the operator asked for: asking for a new passcode cuts off whoever
// was already signed in on that PIN.
func TestRequestingAPasscodeRevokesExistingSessions(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	challenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	actor, _, _, err2 := sessions.LoginWithOTP(nil, "ann-pin-long-enough", challenge.Code)
	if err2 != nil {
		t.Fatalf("login: %v", err2)
	}
	_ = actor

	sessions.mu.Lock()
	before := len(sessions.sessions)
	sessions.mu.Unlock()
	if before != 1 {
		t.Fatalf("sessions = %d, want 1", before)
	}

	if _, err := sessions.RequestOTP(nil, "ann-pin-long-enough"); err != nil {
		t.Fatalf("second RequestOTP: %v", err)
	}

	sessions.mu.Lock()
	after := len(sessions.sessions)
	sessions.mu.Unlock()
	if after != 0 {
		t.Fatalf("sessions after a new passcode = %d, want 0", after)
	}
}

// An unknown PIN must not be distinguishable from a known one by the error a
// caller can see. The HTTP layer answers 202 either way; this is the layer
// underneath keeping its side of that bargain.
func TestAnUnknownPinYieldsTheSwallowedError(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	if _, err := sessions.RequestOTP(nil, "nobody-holds-this-pin"); !errors.Is(err, ErrNoSuchPin) {
		t.Fatalf("err = %v, want ErrNoSuchPin", err)
	}
}

func TestPasscodeRequestsAreThrottledPerAccount(t *testing.T) {
	sessions, _ := otpStoreForTest(t)

	for i := 0; i < otpRequestLimit; i++ {
		if _, err := sessions.RequestOTP(nil, "ann-pin-long-enough"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if _, err := sessions.RequestOTP(nil, "ann-pin-long-enough"); !errors.Is(err, ErrTooManyOTPRequests) {
		t.Fatalf("err = %v, want ErrTooManyOTPRequests", err)
	}
}

// The seat limit counts people. Somebody already signed in may open a second
// device; a different person is refused while the seats are full.
func TestSeatLimitCountsPeopleNotDevices(t *testing.T) {
	users := userStoreForTest(t)
	for _, person := range []struct{ name, pin, email string }{
		{"ann", "ann-pin-long-enough", "ann@example.test"},
		{"bob", "bob-pin-long-enough", "bob@example.test"},
	} {
		if err := users.Add(context.Background(), person.name, "correct-horse-battery"); err != nil {
			t.Fatalf("add %s: %v", person.name, err)
		}
		if err := users.SetPin(context.Background(), person.name, person.pin, person.email); err != nil {
			t.Fatalf("pin %s: %v", person.name, err)
		}
	}
	sessions := NewSessionAuth(users)
	sessions.SetMaxActiveUsers(1)

	signIn := func(pin string) error {
		challenge, err := sessions.RequestOTP(nil, pin)
		if err != nil {
			return err
		}
		_, _, _, err = sessions.LoginWithOTP(nil, pin, challenge.Code)
		return err
	}

	if err := signIn("ann-pin-long-enough"); err != nil {
		t.Fatalf("ann: %v", err)
	}
	if err := signIn("bob-pin-long-enough"); !errors.Is(err, ErrTooManyActiveUsers) {
		t.Fatalf("bob: err = %v, want ErrTooManyActiveUsers", err)
	}
	if sessions.ActiveUsers() != 1 {
		t.Fatalf("active users = %d, want 1", sessions.ActiveUsers())
	}
}

// A refused sign-in must not have spent the passcode: a full server should
// cost the person a wait, not a fresh request — and a fresh request is what
// signs their other devices out.
func TestASeatRefusalDoesNotBurnThePasscode(t *testing.T) {
	users := userStoreForTest(t)
	for _, person := range []struct{ name, pin, email string }{
		{"ann", "ann-pin-long-enough", "ann@example.test"},
		{"bob", "bob-pin-long-enough", "bob@example.test"},
	} {
		if err := users.Add(context.Background(), person.name, "correct-horse-battery"); err != nil {
			t.Fatalf("add %s: %v", person.name, err)
		}
		if err := users.SetPin(context.Background(), person.name, person.pin, person.email); err != nil {
			t.Fatalf("pin %s: %v", person.name, err)
		}
	}
	sessions := NewSessionAuth(users)
	sessions.SetMaxActiveUsers(1)

	// Ann takes the only seat.
	annChallenge, err := sessions.RequestOTP(nil, "ann-pin-long-enough")
	if err != nil {
		t.Fatalf("ann RequestOTP: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, "ann-pin-long-enough", annChallenge.Code); err != nil {
		t.Fatalf("ann login: %v", err)
	}

	bobChallenge, err := sessions.RequestOTP(nil, "bob-pin-long-enough")
	if err != nil {
		t.Fatalf("bob RequestOTP: %v", err)
	}
	if _, _, _, err := sessions.LoginWithOTP(nil, "bob-pin-long-enough", bobChallenge.Code); !errors.Is(err, ErrTooManyActiveUsers) {
		t.Fatalf("bob while full: err = %v, want ErrTooManyActiveUsers", err)
	}

	// Ann leaves; Bob's original passcode still works, which is what proves
	// the refusal did not consume it.
	sessions.mu.Lock()
	sessions.sessions = map[string]*Session{}
	sessions.mu.Unlock()
	if _, _, _, err := sessions.LoginWithOTP(nil, "bob-pin-long-enough", bobChallenge.Code); err != nil {
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

	_, _, _, err := sessions.LoginWithOTP(request, "ann-pin-long-enough", "AAAAAAAA")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoginWithOTP on a cancelled request = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrBadCredentials) {
		t.Fatal("a cancelled request must not read as a rejected credential")
	}

	if _, err := sessions.RequestOTP(request, "ann-pin-long-enough"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestOTP on a cancelled request = %v, want context.Canceled", err)
	}

	if _, _, _, err := sessions.Login(ctx, "ann", "correct-horse-battery"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Login on a cancelled context = %v, want context.Canceled", err)
	}
}
