// The visitor credential: one shared, read-only way in.
//
// Everything else in this package is built around the opposite assumption —
// a credential belongs to one named person, is verified against an argon2 hash,
// and admits its holder to a second factor delivered somewhere only they can
// reach. The visitor is deliberately none of that. It is a fixed PIN and a
// fixed passcode, configured by the operator, and shared by everyone who is
// meant to be able to look.
//
// That makes it a published password, and it is treated as one:
//
//   - It grants no write of any kind. Enforcement is a deny-by-default gate in
//     internal/server/middleware.go, not a list of protected handlers, because
//     a list is a thing the next route forgets to join.
//   - It is off unless the operator turns it on. There is no default value
//     here for the same reason there is no default administrator password: a
//     credential that exists without anyone deciding it should is a credential
//     nobody knows is there.
//   - It is checked before the account path, so a real account is never
//     reached by it. Real PINs are at least MinPinLength characters, so a
//     usefully short visitor PIN cannot collide with one.
//
// It lives in the settings table rather than in this process, and is read on
// every attempt rather than cached, for the same reason UserStore caches
// nothing: turning it off has to mean off now. An operator running
// `nodevas visitor off` on a machine where the site is under a link somebody
// posted cannot be told to restart the service and take everyone's editing
// session with it. The cost is one indexed primary-key lookup on a local
// SQLite file per sign-in attempt, which is nothing beside the argon2 pass
// that follows it, and which the rate limits are charged before in any case.
//
// The stored values are not hashed. Hashing a credential the operator
// publishes protects nothing — anyone who can read it from the database can
// read it from the sign-in instructions — and it would cost an argon2 pass on
// the unauthenticated path for every request, which is a denial-of-service
// lever rather than a defence.

package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"nodevas/internal/identity"
)

// VisitorID is the account ID a visitor session carries. It is not a row in
// the accounts table; Authenticate recognises it and asks whether the shared
// credential is still configured instead of looking for an account.
const VisitorID = "visitor"

// visitorRevision stands in for the account revision a real session is pinned
// to. A visitor has no account to change underneath it.
const visitorRevision = "visitor"

// The settings keys. Dotted, like every other key in that table.
const (
	visitorPinKey = "auth.visitor.pin"
	visitorOTPKey = "auth.visitor.otp"
)

// MinVisitorPinLength is far below MinPinLength on purpose. A visitor PIN is
// meant to be told to a room, printed on a slide, or typed from memory, and it
// guards nothing that a write could damage. It is still not allowed to be
// empty or one character, because the throttles are the only thing between it
// and exhaustive guessing.
const MinVisitorPinLength = 3

// MinVisitorOTPLength is where the credential's strength actually lives. The
// PIN is published; the passcode is what a stranger has to guess, so it is
// held to the length of a mailed one rather than to the PIN's rule.
const MinVisitorOTPLength = OTPLength

// VisitorActor is who a visitor session acts as. The name appears in the audit
// trail and in presence, where it should read as a category rather than as a
// person, because it is one.
var VisitorActor = identity.Actor{
	ID:   VisitorID,
	Name: "visitor",
	Role: identity.RoleVisitor,
}

// ErrVisitorFixedPasscode reports that the PIN offered was the visitor's, so
// there is no passcode to mint or mail. The HTTP layer must treat it exactly
// like every other RequestOTP failure — a 202 and nothing else — or the
// endpoint tells an unauthenticated caller which PIN it just guessed.
var ErrVisitorFixedPasscode = errors.New("the visitor passcode is fixed and is not mailed")

// ValidateVisitorCredential checks a proposed credential without storing it,
// so the CLI can refuse a bad one before it writes anything.
//
// Both halves are required together. A visitor PIN with no passcode would be a
// one-factor door, and a passcode with no PIN is unreachable.
func ValidateVisitorCredential(pin, otp string) error {
	if strings.TrimSpace(pin) == "" || strings.TrimSpace(otp) == "" {
		return errors.New("a visitor credential needs both a pin and a passcode")
	}
	if len([]rune(pin)) < MinVisitorPinLength {
		return fmt.Errorf("a visitor pin must be at least %d characters", MinVisitorPinLength)
	}
	if len([]rune(otp)) < MinVisitorOTPLength {
		// Said as its own rule rather than folded in with the PIN's: the PIN is
		// meant to be short and public, and an operator who read only the PIN
		// rule would reasonably expect the passcode to be short too. It is the
		// one half a stranger has to guess.
		return fmt.Errorf(
			"a visitor passcode must be at least %d characters: the pin is published, "+
				"so this is the only half that has to be guessed", MinVisitorOTPLength)
	}
	if len(pin) > MaxPinBytes || len(otp) > MaxOTPBytes {
		return errors.New("the visitor pin or passcode is too long")
	}
	return nil
}

// GenerateVisitorOTP draws a passcode from the same alphabet a mailed one uses.
// Preferring this to a chosen one is the difference between 40 bits and a word
// somebody thought was memorable.
func GenerateVisitorOTP() (string, error) { return newOTP() }

// SetVisitorCredential stores the shared read-only credential. It takes effect
// on the next sign-in attempt against any process using this database; nothing
// has to be restarted.
func (u *UserStore) SetVisitorCredential(ctx context.Context, pin, otp string) error {
	if err := ValidateVisitorCredential(pin, otp); err != nil {
		return err
	}
	if !u.ready() {
		return errors.New("no account database")
	}
	// The passcode is normalised on the way in rather than on every comparison,
	// so what is stored is what will be compared and an operator reading the
	// row sees the form the server actually uses.
	if err := u.database.SetSetting(ctx, visitorPinKey, strings.TrimSpace(pin)); err != nil {
		return err
	}
	return u.database.SetSetting(ctx, visitorOTPKey, normalizeOTP(otp))
}

// ClearVisitorCredential turns visitor access off. Existing visitor sessions
// stop working at their next request — see Authenticate — rather than at their
// TTL, because "off" that takes twelve hours is not off.
func (u *UserStore) ClearVisitorCredential(ctx context.Context) error {
	if !u.ready() {
		return errors.New("no account database")
	}
	if err := u.database.SetSetting(ctx, visitorPinKey, ""); err != nil {
		return err
	}
	return u.database.SetSetting(ctx, visitorOTPKey, "")
}

// VisitorCredential returns the stored credential, both halves empty when
// visitor access is off.
//
// It returns the PIN in the clear because it is published by definition: the
// operator has to be able to read it back to tell somebody what it is, and
// there is no version of this credential that is a secret from the person who
// configured it.
func (u *UserStore) VisitorCredential(ctx context.Context) (pin, otp string, err error) {
	if !u.ready() {
		return "", "", nil
	}
	if pin, err = u.database.Setting(ctx, visitorPinKey, ""); err != nil {
		return "", "", err
	}
	if otp, err = u.database.Setting(ctx, visitorOTPKey, ""); err != nil {
		return "", "", err
	}
	if pin == "" || otp == "" {
		// Half a credential is no credential. This cannot be written through
		// SetVisitorCredential, but it can exist if somebody edits the table by
		// hand, and the safe reading of it is "off".
		return "", "", nil
	}
	return pin, otp, nil
}

// SetVisitor is the whole-server convenience wrapper. Handlers and the CLI use
// the store directly; this exists for callers that hold a SessionAuth.
func (a *SessionAuth) SetVisitor(ctx context.Context, pin, otp string) error {
	if strings.TrimSpace(pin) == "" && strings.TrimSpace(otp) == "" {
		return a.users.ClearVisitorCredential(ctx)
	}
	return a.users.SetVisitorCredential(ctx, pin, otp)
}

// VisitorEnabled reports whether a shared read-only credential is configured.
func (a *SessionAuth) VisitorEnabled(ctx context.Context) bool {
	pin, _, err := a.users.VisitorCredential(ctx)
	return err == nil && pin != ""
}

// visitorPinMatches reports whether this PIN is the visitor's.
func (a *SessionAuth) visitorPinMatches(ctx context.Context, pin string) bool {
	stored, _, err := a.users.VisitorCredential(ctx)
	if err != nil || stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(pin)) == 1
}

// visitorLogin reports whether both halves of the visitor credential were
// offered. The passcode is compared after normalizeOTP so a visitor typing it
// in the case it was written in, or with a space in the middle, is treated the
// same way a mailed passcode is.
func (a *SessionAuth) visitorLogin(ctx context.Context, pin, otp string) bool {
	storedPin, storedOTP, err := a.users.VisitorCredential(ctx)
	if err != nil || storedPin == "" || storedOTP == "" {
		return false
	}
	pinOK := subtle.ConstantTimeCompare([]byte(storedPin), []byte(pin))
	otpOK := subtle.ConstantTimeCompare([]byte(storedOTP), []byte(normalizeOTP(otp)))
	// Both compared before either is judged, so the time taken does not say
	// which half was wrong.
	return pinOK&otpOK == 1
}
