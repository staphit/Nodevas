package auth

import (
	"errors"
	"testing"
)

// The database finds an account by an address with invisible or full-width
// characters added, but the request budgets are keyed on the address as
// typed. Each such spelling must not open a fresh budget on the same mailbox:
// it is answered as an address nobody registered.
func TestAnAddressVariantReachesNoAccount(t *testing.T) {
	sessions, _ := otpStoreForTest(t)
	if _, err := sessions.RequestOTP(nil, annEmail); err != nil {
		t.Fatal(err)
	}
	variants := []string{
		"an­n@example.test",
		"ａｎｎ@example.test",
		"an​n@example.test",
		"ann@exa­mple.test",
	}
	for _, variant := range variants {
		if challenge, err := sessions.RequestOTP(nil, variant); err == nil {
			t.Errorf("%+q minted a passcode for %s inside the cooldown", variant, challenge.Email)
		}
	}
	pastCooldown(sessions)
	challenge, err := sessions.RequestOTP(nil, annEmail)
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range variants {
		result, err := sessions.SignInWithOTP(nil, variant, challenge.Code)
		if !errors.Is(err, ErrBadCredentials) || result.Refusal.Reason != RefusedUnknownAccount {
			t.Errorf("signing in as %+q = %v (%q), want an unknown address", variant, err, result.Refusal.Reason)
		}
	}
}
