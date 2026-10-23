package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nodevas/internal/identity"
)

// signedInRequest signs ann in and returns a request carrying her session.
func signedInRequest(t *testing.T, sessions *SessionAuth) *http.Request {
	t.Helper()
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	actor, token, _, err := sessions.LoginWithOTP(nil, annEmail, challenge.Code)
	if err != nil {
		t.Fatalf("LoginWithOTP: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/step-up", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	return r.WithContext(WithActor(r.Context(), actor))
}

func TestStepUpRequestVerifyAndExpiry(t *testing.T) {
	sessions, users := otpStoreForTest(t)
	clock := time.Now()
	sessions.now = func() time.Time { return clock }
	sessions.SetStepUpWindow(10 * time.Minute)
	r := signedInRequest(t, sessions)

	if active, _ := sessions.StepUpState(r); active {
		t.Fatal("stepped up before any step-up")
	}
	// Signing in just now does not stand in the way: step-up has its own
	// resend cooldown.
	challenge, err := sessions.RequestStepUp(r)
	if err != nil {
		t.Fatalf("RequestStepUp: %v", err)
	}
	if challenge.Email != annEmail || len(challenge.Code) != OTPLength || challenge.Actor != "ann" {
		t.Fatalf("challenge = %+v", challenge)
	}
	until, err := sessions.VerifyStepUp(r, challenge.Code)
	if err != nil {
		t.Fatalf("VerifyStepUp: %v", err)
	}
	if !until.Equal(clock.Add(10 * time.Minute)) {
		t.Fatalf("until = %v, want %v", until, clock.Add(10*time.Minute))
	}
	at, ok := sessions.SteppedUpAt(r)
	if !ok || !at.Equal(clock) {
		t.Fatalf("SteppedUpAt = %v %v", at, ok)
	}
	// Single use.
	if _, err := sessions.VerifyStepUp(r, challenge.Code); !errors.Is(err, ErrStepUpCode) {
		t.Fatalf("replay err = %v", err)
	}

	// A restart keeps it: it is on the session row.
	restarted := NewSessionAuth(users)
	restarted.now = func() time.Time { return clock }
	if _, ok := restarted.SteppedUpAt(r); !ok {
		t.Fatal("a restart lost the step-up")
	}

	// The window runs out.
	clock = clock.Add(10*time.Minute + time.Second)
	if _, ok := sessions.SteppedUpAt(r); ok {
		t.Fatal("still stepped up past the window")
	}
	if active, _ := sessions.StepUpState(r); active {
		t.Fatal("StepUpState active past the window")
	}
}

func TestStepUpPasscodeExpires(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	clock := time.Now()
	sessions.now = func() time.Time { return clock }
	r := signedInRequest(t, sessions)
	challenge, err := sessions.RequestStepUp(r)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(OTPTTL + time.Second)
	if _, err := sessions.VerifyStepUp(r, challenge.Code); !errors.Is(err, ErrStepUpCode) {
		t.Fatalf("expired passcode err = %v", err)
	}
}

func TestANewSignInDoesNotInheritAStepUp(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	first := signedInRequest(t, sessions)
	challenge, err := sessions.RequestStepUp(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.VerifyStepUp(first, challenge.Code); err != nil {
		t.Fatal(err)
	}
	pastCooldown(sessions)
	second := signedInRequest(t, sessions)
	if _, ok := sessions.SteppedUpAt(second); ok {
		t.Fatal("a new sign-in inherited the old session's step-up")
	}
}

func TestWrongStepUpCodesBurnThePasscodeAndThrottle(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	r := signedInRequest(t, sessions)
	challenge, err := sessions.RequestStepUp(r)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxOTPAttempts; i++ {
		if _, err := sessions.VerifyStepUp(r, "WRONGCOD"); !errors.Is(err, ErrStepUpCode) {
			t.Fatalf("wrong code %d: err = %v", i, err)
		}
	}
	// Burnt: the right code no longer works.
	if _, err := sessions.VerifyStepUp(r, challenge.Code); !errors.Is(err, ErrStepUpCode) {
		t.Fatalf("burnt passcode err = %v", err)
	}
	// And the account's budget runs out.
	var last error
	for i := 0; i < LoginFailureLimit; i++ {
		_, last = sessions.VerifyStepUp(r, "WRONGCOD")
	}
	if !errors.Is(last, ErrTooManyLogins) {
		t.Fatalf("after %d attempts err = %v, want ErrTooManyLogins", LoginFailureLimit+maxOTPAttempts+1, last)
	}
}

func TestAgentsCannotStepUp(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	r := signedInRequest(t, sessions)
	actor := ActorFrom(r)
	actor.Agent = identity.AgentWorker
	agent := r.WithContext(WithActor(r.Context(), actor))
	if _, err := sessions.RequestStepUp(agent); !errors.Is(err, ErrStepUpAgent) {
		t.Fatalf("agent request err = %v", err)
	}
	if _, err := sessions.VerifyStepUp(agent, "ANYTHING"); !errors.Is(err, ErrStepUpAgent) {
		t.Fatalf("agent verify err = %v", err)
	}
	// Even a person's step-up does not carry over to an agent on the same
	// cookie.
	challenge, err := sessions.RequestStepUp(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.VerifyStepUp(r, challenge.Code); err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.SteppedUpAt(agent); ok {
		t.Fatal("an agent read as stepped up")
	}
}

func TestStepUpNeedsASession(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/step-up", nil)
	if _, err := sessions.RequestStepUp(r); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("no session err = %v", err)
	}
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "not-a-session"})
	if _, err := sessions.VerifyStepUp(r, "ABCDEFGH"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown session err = %v", err)
	}
}

func TestStepUpWindowIsBounded(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	sessions.SetStepUpWindow(0)
	if got := sessions.StepUpWindow(); got != DefaultStepUpWindow {
		t.Fatalf("zero window = %v", got)
	}
	sessions.SetStepUpWindow(48 * time.Hour)
	if got := sessions.StepUpWindow(); got != MaxStepUpWindow {
		t.Fatalf("huge window = %v", got)
	}
}
