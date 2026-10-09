// One-time passcodes: the whole of the web sign-in.
//
// A networked server asks for an email address and a passcode mailed to it.
// The address finds the account; the passcode proves the person at the
// keyboard can read that account's mailbox right now. That is one factor —
// possession of the mailbox — and it is the owner's deliberate choice: the
// PIN that used to stand in front of it is gone, so an account is exactly as
// safe as the inbox registered for it. See email.go.
//
// Because the address is public, everything reachable without signing in is
// built not to say whether an address is registered: the request answers the
// same way for every address, is throttled the same way for every address,
// and does not wait on mail delivery. And because requesting a passcode needs
// nothing secret, requesting one changes nothing about anybody's existing
// sessions; only completing a sign-in does.
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

	// Anyone who knows an address can ask for a passcode to be sent to it, so
	// the request endpoint is a way to fill somebody's inbox, and every request
	// is a fresh passcode to guess at. These budgets are what makes both
	// expensive rather than free.
	otpRequestWindow = 1 * time.Minute
	otpRequestLimit  = 3
	otpDailyWindow   = 1 * time.Hour
	otpDailyLimit    = 12
	// otpResendCooldown is deliberately separate from the broader budgets:
	// one address may receive at most one newly generated passcode every 30
	// seconds, while global and per-source limits still absorb wider abuse.
	otpResendCooldown = 30 * time.Second

	// MaxOTPBytes is a bound, not a format check — the server decides what a
	// passcode looks like after it is compared, not before.
	MaxOTPBytes = 64
)

// ErrNoSuchAccount means no account signs in with that address, or the
// address is not one at all. Handlers must not pass it to the client:
// answering "that address is not registered" turns the request endpoint into a
// directory of who has an account.
var ErrNoSuchAccount = errors.New("no account for that email address")

// ErrNoMailer is returned when passcodes cannot be delivered because the
// operator has not configured outgoing mail. It is safe to show: it describes
// the server, not the account.
var ErrNoMailer = errors.New("this server cannot send passcodes: outgoing mail is not configured")

// ErrTooManyOTPRequests is returned when the passcode request budget is spent.
var ErrTooManyOTPRequests = errors.New("too many passcode requests; wait before trying again")

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
	Code string
	// Email is the address registered on the account, read from its row. It is
	// never the string the client typed: the two match case-insensitively and
	// may differ in every other way, and mail goes only where the
	// administrator said it should.
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

// RequestOTP issues a passcode for the account registered with this address.
//
// It does not touch the account's sessions. It used to sign every one of them
// out, back when asking needed a PIN and asking was therefore a statement that
// you held it; an address is not a secret, and revoking here would let anyone
// who knows somebody's address log them out every thirty seconds. Ending the
// other sessions happens when a sign-in completes instead — see LoginWithOTP.
//
// Every budget is charged before the account is looked up, keyed on what the
// caller sent rather than on what it found, so a registered address and an
// unregistered one run out of budget identically and the 429 says nothing
// about which it was.
//
// The returned Challenge is the caller's to deliver and then forget. An
// unknown address gives ErrNoSuchAccount, which the HTTP layer swallows.
func (a *SessionAuth) RequestOTP(r *http.Request, email string) (Challenge, error) {
	source := ""
	if r != nil {
		source = ClientIP(r)
	}
	ctx := requestContext(r)
	if !a.allowOTPRequest(source) {
		return Challenge{}, ErrTooManyOTPRequests
	}
	address, err := normalizeEmail(email)
	if err != nil {
		// Not an address, so not one anybody could have registered: refusing it
		// before the per-address budget reveals nothing.
		return Challenge{}, ErrNoSuchAccount
	}
	// Charged per address rather than per source, so an attacker who moves
	// between source addresses still cannot fill one person's inbox or mint
	// them an endless supply of passcodes to guess at.
	if !a.allowOTPForAddress(emailKey(address)) {
		return Challenge{}, ErrTooManyOTPRequests
	}

	actor, _, registered, ok := a.users.AccountByEmail(ctx, address)
	if !ok {
		// A caller who hung up did not offer an address that failed, so say so
		// rather than ErrNoSuchAccount. The HTTP layer answers 202 either way,
		// so this changes nothing an unauthenticated client can see; what it
		// changes is the audit line, which should not record a probe that
		// nobody made.
		if err := contextFailure(ctx); err != nil {
			return Challenge{}, err
		}
		return Challenge{}, ErrNoSuchAccount
	}

	code, err := newOTP()
	if err != nil {
		return Challenge{}, err
	}
	now := time.Now()
	expires := now.Add(OTPTTL)

	a.mu.Lock()
	a.sweepOTPsLocked(now)
	// One live passcode per account: issuing a second would leave the first
	// usable, and "the last one wins" is what a person expects after pressing
	// resend.
	a.otps[actor.ID] = &pendingOTP{digest: digestOTP(code), expires: expires}
	a.mu.Unlock()

	return Challenge{Code: code, Email: registered, Expires: expires, Actor: actor.Name}, nil
}

// LoginWithOTP completes the sign-in and, as the same step, signs out every
// other session the account had, so the device that just proved it holds the
// mailbox is the only one left signed in.
//
// That revocation is what replaces the one RequestOTP used to do. Here it is
// earned: only somebody who read the passcode can trigger it. It is also how
// the account holder cuts off an intruder who got in through their mailbox —
// sign in again — and why a durable revocation failure fails the sign-in
// rather than opening a session beside sessions that a restart would bring
// back.
//
// An unknown address, no passcode outstanding, and a wrong passcode all give
// the same ErrBadCredentials, after the same throttle charges, without a hash
// pass on any of them, so neither the answer nor its timing says which.
func (a *SessionAuth) LoginWithOTP(r *http.Request, email, otp string) (identity.Actor, string, string, error) {
	source := ""
	if r != nil {
		source = ClientIP(r)
	}
	ctx := requestContext(r)
	address, addressErr := normalizeEmail(email)
	key := ""
	if addressErr == nil {
		key = emailKey(address)
	}
	// Charged before anything is looked up, under the same budgets a password
	// login uses, with the address taking the place of the account name. It is
	// keyed on what was typed, not on what was found, so an unregistered
	// address throttles exactly like a registered one.
	if !a.allowEmailLogin(key, source) {
		return identity.Actor{}, "", "", ErrTooManyLogins
	}
	if addressErr != nil || len(otp) == 0 || len(otp) > MaxOTPBytes {
		return identity.Actor{}, "", "", ErrBadCredentials
	}

	actor, revision, _, ok := a.users.AccountByEmail(ctx, address)
	if !ok {
		// Same reasoning as the password path: a cancelled read is not a wrong
		// address, and recording it as one would put a failed sign-in in the
		// trail for a person who simply closed the tab.
		if err := contextFailure(ctx); err != nil {
			return identity.Actor{}, "", "", err
		}
		return identity.Actor{}, "", "", ErrBadCredentials
	}

	token, csrf, err := newSessionTokens()
	if err != nil {
		return identity.Actor{}, "", "", err
	}

	// Everything from here to the new session is one critical section: the
	// passcode check, the seat check, the revocation, spending the passcode and
	// recording the session. Split up, a second sign-in could slip a session
	// in between this one's revocation and its insert and survive both.
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := a.otps[actor.ID]
	if pending == nil || now.After(pending.expires) {
		delete(a.otps, actor.ID)
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
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	if err := a.sweepSessionsLocked(now); err != nil {
		return identity.Actor{}, "", "", err
	}
	// The seat limit is checked before anything is spent or revoked, while the
	// account's own sessions still count as its seat. Checking after the
	// revocation would let a full server sign the person out of their other
	// devices and then refuse them this one.
	if !a.seatAvailableLocked(actor.ID, now) {
		return identity.Actor{}, "", "", ErrTooManyActiveUsers
	}
	// Durable first, as everywhere sessions are destroyed: if the delete does
	// not commit, nothing is forgotten in memory, the passcode is still good
	// for a retry, and no session is opened beside the ones a restart would
	// resurrect.
	if err := a.store.removeUser(actor.ID); err != nil {
		return identity.Actor{}, "", "", err
	}
	a.revokeUserLocked(actor.ID)
	if err := a.openSessionLocked(actor, revision, token, now); err != nil {
		return identity.Actor{}, "", "", err
	}
	// Single use. Spent only once the session exists, which is safe because
	// the lock is still held: no replay can run between the check above and
	// this delete.
	delete(a.otps, actor.ID)
	return actor, token, csrf, nil
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

// allowOTPForAddress charges the per-address budget and the resend cooldown.
//
// It is keyed on the address rather than the account ID so it can be charged
// before the lookup, for addresses that exist and addresses that do not alike.
// An address names at most one account, so for a registered one this is the
// per-account budget it always was.
func (a *SessionAuth) allowOTPForAddress(key string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	// Anyone can put an address in this map now, so it is swept rather than
	// trusted to stay small. An entry older than the cooldown decides nothing,
	// and the global request budget bounds how many younger ones can exist.
	for seen, last := range a.lastOTPRequest {
		if now.Sub(last) >= otpResendCooldown {
			delete(a.lastOTPRequest, seen)
		}
	}
	if last, ok := a.lastOTPRequest[key]; ok && now.Sub(last) < otpResendCooldown {
		return false
	}
	if !a.chargeLocked([]rateCharge{
		{key: "otp-user:" + key, window: otpRequestWindow, limit: otpRequestLimit},
		{key: "otp-user-hour:" + key, window: otpDailyWindow, limit: otpDailyLimit},
	}, now) {
		return false
	}
	a.lastOTPRequest[key] = now
	return true
}

// RevokeUser signs out every session belonging to an account. The CLI calls it
// when an account is removed; changing the sign-in address does the same
// through the account revision.
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
