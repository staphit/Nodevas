// One-time passcodes for the two-factor sign-in.
//
// A networked server asks for two things: a PIN, which an administrator
// creates and hands over out of band, and a passcode emailed to the address
// bound to that PIN. Neither alone gets in. The PIN proves you were given
// access; the passcode proves you still hold the mailbox it was granted to.
//
// Passcodes are single use, expire in five minutes, and live only in memory —
// a restart invalidates every outstanding one, which is the safe direction.

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math/big"
	"net/http"
	"nodevas/internal/identity"
	"strings"
	"time"
)

const (
	// OTPLength is a compromise between typing it from a phone and surviving
	// the guessing that the request throttle cannot see: 8 characters from a
	// 32-symbol alphabet is 40 bits.
	OTPLength = 8
	// OTPTTL is long enough to switch to a mail client and back, short enough
	// that a passcode left in an inbox is usually already dead.
	OTPTTL = 5 * time.Minute

	// Excludes I, O, 0 and 1: a passcode is read off a screen and typed by
	// hand, and those four are what people get wrong.
	otpAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	// A passcode dies after this many wrong guesses, so the 40 bits above
	// never actually have to hold: an attacker gets five tries per passcode,
	// not unlimited tries against a live one.
	maxOTPAttempts = 5

	// Requesting a passcode signs out every session for that PIN, so the
	// request endpoint is itself an attack: someone holding only the PIN could
	// keep the rightful holder logged out. These budgets are what makes that
	// expensive rather than free.
	otpRequestWindow = 1 * time.Minute
	otpRequestLimit  = 3
	otpDailyWindow   = 1 * time.Hour
	otpDailyLimit    = 12

	// MaxPinBytes bounds what reaches the Argon2 verifier. A PIN is short; a
	// megabyte of one is somebody probing for a memory-cost amplifier.
	MaxPinBytes = 256
	// MaxOTPBytes is likewise a bound, not a format check — the server decides
	// what a passcode looks like after it is compared, not before.
	MaxOTPBytes = 64
)

// ErrNoSuchPin means no account carries that PIN. Handlers must not pass it
// to the client: answering "that PIN does not exist" turns the request
// endpoint into an oracle that finds valid PINs without ever needing an inbox.
var ErrNoSuchPin = errors.New("no account for that pin")

// ErrNoMailer is returned when passcodes cannot be delivered because the
// operator has not configured outgoing mail. It is safe to show: it describes
// the server, not the account.
var ErrNoMailer = errors.New("this server cannot send passcodes: outgoing mail is not configured")

// ErrTooManyOTPRequests is returned when the passcode request budget is spent.
var ErrTooManyOTPRequests = errors.New("too many passcode requests; wait a minute and try again")

// pendingOTP is the one live passcode an account may have.
//
// Only a digest is kept. The passcode leaves this process once, in the email,
// and a memory dump or a stray log of this struct should not hand it over
// again.
type pendingOTP struct {
	digest   [sha256.Size]byte
	expires  time.Time
	attempts int
}

// Challenge is what the caller needs to deliver a passcode: where to send it
// and what to send. It is returned once and never stored.
type Challenge struct {
	Code    string
	Email   string
	Expires time.Time
	// Actor names the account, for the audit line the caller writes. The
	// passcode itself must never be logged.
	Actor string
}

// newOTP draws a passcode from crypto/rand.
//
// The modulo shortcut that usually appears here biases the alphabet, and the
// alphabet is only 32 symbols wide, so rand.Int over the exact range is both
// correct and cheap.
func newOTP() (string, error) {
	limit := big.NewInt(int64(len(otpAlphabet)))
	out := make([]byte, OTPLength)
	for index := range out {
		pick, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[index] = otpAlphabet[pick.Int64()]
	}
	return string(out), nil
}

// normalizeOTP makes comparison case- and spacing-insensitive, because the
// passcode is copied by hand out of an email and often arrives with the case
// mangled or a space in the middle.
func normalizeOTP(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range strings.ToUpper(code) {
		if r == ' ' || r == '-' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func digestOTP(code string) [sha256.Size]byte {
	return sha256.Sum256([]byte(normalizeOTP(code)))
}

// RequestOTP issues a passcode for the account holding this PIN and, as the
// same step, signs out every session that account already had.
//
// Revoking here rather than at the next successful sign-in is deliberate: it
// means one request from the real holder cuts off an intruder who already has
// a session, without the holder needing to get back in first. The cost is that
// the request endpoint can be used to log someone out, which is why the
// budgets above are tight.
//
// The returned Challenge is the caller's to deliver and then forget. An
// unknown PIN gives ErrNoSuchPin, which the HTTP layer swallows.
func (a *SessionAuth) RequestOTP(r *http.Request, pin string) (Challenge, error) {
	source := ""
	if r != nil {
		source = ClientIP(r)
	}
	ctx := requestContext(r)
	if !a.allowOTPRequest(source) {
		return Challenge{}, ErrTooManyOTPRequests
	}
	if len(pin) == 0 || len(pin) > MaxPinBytes {
		return Challenge{}, ErrNoSuchPin
	}
	// The visitor's passcode is fixed, so there is nothing to mint, mail, or
	// revoke — and nothing to revoke is the point: one visitor pressing "send"
	// must not sign out every other visitor, which is what the per-account
	// revocation below would do to a credential everybody shares. The caller
	// answers 202 for this error exactly as it does for an unknown PIN, so
	// pressing the button still tells the client nothing.
	if a.visitorPinMatches(ctx, pin) {
		return Challenge{Actor: VisitorActor.Name}, ErrVisitorFixedPasscode
	}

	actor, _, email, ok := a.users.VerifyPin(ctx, pin)
	if !ok {
		// A caller who hung up did not offer a PIN that failed, so say so
		// rather than ErrNoSuchPin. The HTTP layer answers 202 either way, so
		// this changes nothing an unauthenticated client can see; what it
		// changes is the audit line, which should not record a probe that
		// nobody made.
		if err := contextFailure(ctx); err != nil {
			return Challenge{}, err
		}
		return Challenge{}, ErrNoSuchPin
	}
	if strings.TrimSpace(email) == "" {
		// An account with a PIN but no address can never receive a passcode,
		// so it can never sign in. Say nothing here — this is the same shape
		// of fact as "no such PIN" and must not be distinguishable.
		return Challenge{}, ErrNoSuchPin
	}
	// A second budget, charged per account rather than per source, so an
	// attacker who moves between addresses still cannot hold one person's
	// sessions open-ended hostage.
	if !a.allowOTPForAccount(actor.ID) {
		return Challenge{}, ErrTooManyOTPRequests
	}

	code, err := newOTP()
	if err != nil {
		return Challenge{}, err
	}
	now := time.Now()
	expires := now.Add(OTPTTL)

	a.mu.Lock()
	a.sweepOTPsLocked(now)
	// Revocation is part of issuing a new passcode. Commit it before exposing
	// the passcode or changing the live maps, otherwise a failed DB delete
	// would let old sessions return after a restart.
	if err := a.store.removeUser(actor.ID); err != nil {
		// A resend is also an invalidation request. Even when durable session
		// revocation is unavailable, never leave the previous in-memory code
		// usable after telling the caller this attempt did not issue a new one.
		delete(a.otps, actor.ID)
		a.mu.Unlock()
		return Challenge{Actor: actor.Name}, err
	}
	// One live passcode per account: issuing a second would leave the first
	// usable, and "the last one wins" is what a person expects after pressing
	// resend.
	a.otps[actor.ID] = &pendingOTP{digest: digestOTP(code), expires: expires}
	a.revokeUserLocked(actor.ID)
	a.mu.Unlock()

	return Challenge{Code: code, Email: email, Expires: expires, Actor: actor.Name}, nil
}

// LoginWithOTP completes the sign-in. Both factors are checked; either one
// wrong gives the same ErrBadCredentials, so a caller cannot learn which half
// it got right.
func (a *SessionAuth) LoginWithOTP(r *http.Request, pin, otp string) (identity.Actor, string, string, error) {
	source := ""
	if r != nil {
		source = ClientIP(r)
	}
	ctx := requestContext(r)
	// Charged before Argon2 runs, under the same budgets a password login
	// uses. The account key is unknown until the PIN verifies, so only the
	// global and per-source budgets apply here.
	if !a.allowLogin("", source) {
		return identity.Actor{}, "", "", ErrTooManyLogins
	}
	if len(pin) == 0 || len(pin) > MaxPinBytes || len(otp) == 0 || len(otp) > MaxOTPBytes {
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	// Checked before the accounts, so the shared credential can never reach a
	// real account's hash. Half-right falls through to the account path and
	// fails there as anything else would, which is also what keeps the timing
	// of a wrong visitor passcode from standing out.
	if a.visitorLogin(ctx, pin, otp) {
		return a.openSession(VisitorActor, visitorRevision)
	}

	actor, revision, _, ok := a.users.VerifyPin(ctx, pin)
	if !ok {
		// Same reasoning as the password path: a cancelled read is not a wrong
		// PIN, and recording it as one would put a failed sign-in in the trail
		// for a person who simply closed the tab.
		if err := contextFailure(ctx); err != nil {
			return identity.Actor{}, "", "", err
		}
		return identity.Actor{}, "", "", ErrBadCredentials
	}

	now := time.Now()
	a.mu.Lock()
	pending := a.otps[actor.ID]
	if pending == nil || now.After(pending.expires) {
		delete(a.otps, actor.ID)
		a.mu.Unlock()
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	offered := digestOTP(otp)
	if subtle.ConstantTimeCompare(pending.digest[:], offered[:]) != 1 {
		pending.attempts++
		if pending.attempts >= maxOTPAttempts {
			// Burn it. Guessing must cost a fresh request, which is throttled
			// and which the account holder sees arrive in their inbox.
			delete(a.otps, actor.ID)
		}
		a.mu.Unlock()
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	// The seat limit is checked before the passcode is spent. Spending it
	// first would mean a full server costs the person their passcode, and the
	// only way to get another is a request that signs out their other devices.
	if !a.seatAvailableLocked(actor.ID, now) {
		a.mu.Unlock()
		return identity.Actor{}, "", "", ErrTooManyActiveUsers
	}
	// Single use: gone before the session exists, so a replay cannot race the
	// first use.
	delete(a.otps, actor.ID)
	a.mu.Unlock()

	return a.openSession(actor, revision)
}

// allowOTPRequest charges the global and per-source budgets for a passcode
// request. It reuses the login buckets so the two endpoints cannot be played
// off against each other.
func (a *SessionAuth) allowOTPRequest(source string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepAttemptsLocked(now)

	charges := []rateCharge{{key: "otp-global", window: otpRequestWindow, limit: loginGlobalLimit}}
	if source = normalizeSource(source); source != "" {
		charges = append(charges, rateCharge{key: "otp-ip:" + source, window: otpRequestWindow, limit: otpRequestLimit})
	}
	return a.chargeLocked(charges, now)
}

// allowOTPForAccount charges the per-account budget, which is what bounds how
// often one person's sessions can be cut by somebody who has only their PIN.
func (a *SessionAuth) allowOTPForAccount(userID string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.chargeLocked([]rateCharge{
		{key: "otp-user:" + userID, window: otpRequestWindow, limit: otpRequestLimit},
		{key: "otp-user-hour:" + userID, window: otpDailyWindow, limit: otpDailyLimit},
	}, now)
}

// RevokeUser signs out every session belonging to an account. The CLI calls it
// when a PIN is changed or an account is removed.
func (a *SessionAuth) RevokeUser(userID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.removeUser(userID); err != nil {
		return err
	}
	a.revokeUserLocked(userID)
	return nil
}

func (a *SessionAuth) revokeUserLocked(userID string) {
	for key, session := range a.sessions {
		if session.actor.ID == userID {
			delete(a.sessions, key)
		}
	}
}

func (a *SessionAuth) sweepOTPsLocked(now time.Time) {
	for id, pending := range a.otps {
		if now.After(pending.expires) {
			delete(a.otps, id)
		}
	}
}
