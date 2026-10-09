// The visitor credential: one shared, read-only way in.
//
// Everything else in this package is built around the opposite assumption —
// a sign-in belongs to one named person and is completed with a passcode
// delivered to a mailbox only they can read. The visitor is deliberately none
// of that. It is a fixed PIN and a fixed passcode, configured by the operator,
// and shared by everyone who is meant to be able to look.
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
//   - It has its own endpoint, POST /api/auth/visitor, and the account
//     endpoints never consult it. A real account cannot be reached with it,
//     and an account sign-in cannot accidentally become a visitor one: the two
//     share the login throttles and the session machinery, and nothing else.
//
// It lives in the settings table rather than in this process, and is read on
// every attempt rather than cached, for the same reason UserStore caches
// nothing: turning it off has to mean off now. An operator running
// `nodevas visitor off` on a machine where the site is under a link somebody
// posted cannot be told to restart the service and take everyone's editing
// session with it. The cost is one indexed primary-key lookup on a local
// SQLite file per sign-in attempt, which the rate limits are charged before.
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
	"net/http"
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

// MinVisitorPinLength is short on purpose. A visitor PIN is meant to be told
// to a room, printed on a slide, or typed from memory, and it guards nothing
// that a write could damage. It is still not allowed to be empty or one
// character, because the throttles are the only thing between it and
// exhaustive guessing.
const MinVisitorPinLength = 3

// MaxVisitorPinBytes bounds what a sign-in attempt may offer as the visitor
// PIN, before it is compared with anything. A PIN is short; a megabyte of one
// is somebody probing, not somebody typing.
const MaxVisitorPinBytes = 256

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
	if len(pin) > MaxVisitorPinBytes || len(otp) > MaxOTPBytes {
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

// LoginVisitor signs in with the shared read-only credential.
//
// It is charged against the same global and per-source budgets an account
// sign-in uses, so the two endpoints cannot be played off against each other.
// There is no per-credential budget: the credential is shared, and a bucket
// for it would let one stranger lock every visitor out by spending it.
//
// A wrong PIN, a wrong passcode and visitor access being off all give the same
// ErrBadCredentials. A visitor session never ends anybody else's — the account
// path signs out an account's other devices, and a credential everybody shares
// cannot go through that.
func (a *SessionAuth) LoginVisitor(r *http.Request, pin, passcode string) (identity.Actor, string, string, error) {
	source := ""
	if r != nil {
		source = ClientIP(r)
	}
	ctx := requestContext(r)
	if !a.allowLogin("", source) {
		return identity.Actor{}, "", "", ErrTooManyLogins
	}
	if len(pin) == 0 || len(pin) > MaxVisitorPinBytes || len(passcode) == 0 || len(passcode) > MaxOTPBytes {
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	if !a.visitorLogin(ctx, pin, passcode) {
		// The settings read is the one thing here that can fail for a reason
		// other than a wrong credential, and a closed tab is that reason.
		if err := contextFailure(ctx); err != nil {
			return identity.Actor{}, "", "", err
		}
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	return a.openSession(VisitorActor, visitorRevision)
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
