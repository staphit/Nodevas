// Where the authenticator factor is asked for: the second step of a sign-in,
// step-up, and the account's own enrollment.
//
// Sign-in. For an account with an authenticator, the right mailed passcode no
// longer opens a session. It spends the passcode and opens a pending sign-in
// instead — a short-lived ticket held only in this process's memory — which an
// authenticator or recovery code then completes. Only completing it ends the
// account's other sessions, exactly as a one-factor sign-in does, so a person
// who has the mailbox and not the phone can neither get in nor sign the owner
// out.
//
// Step-up. An account with an authenticator steps up with it, and no mail is
// sent: the point of the second factor is that the mailbox alone no longer
// vouches for runner auto-accept.
//
// Guessing. A six-digit code within one step either side is three chances in
// a million per guess, so wrong codes are budgeted per account — a short
// window and a daily one — in addition to the per-request throttles every
// sign-in path already charges, and a pending sign-in dies after a few wrong
// codes. The budgets live in memory and a restart forgets them; a restart
// also forgets every pending sign-in, which costs the attacker a fresh mailed
// passcode each time.

package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"nodevas/internal/identity"
)

const (
	// SecondFactorTOTP and SecondFactorRecovery name what completed a sign-in
	// or a step-up; StepUpMethodEmail is the mailed-passcode step-up.
	SecondFactorTOTP     = "totp"
	SecondFactorRecovery = "recovery"
	StepUpMethodEmail    = "email"

	// SignInTicketTTL is how long a pending sign-in waits for its
	// authenticator code: long enough to find the phone, as long as the
	// passcode that opened it.
	SignInTicketTTL = OTPTTL
	// maxSecondFactorAttempts wrong codes burn a pending sign-in.
	maxSecondFactorAttempts = 5
	maxPendingSignIns       = 1024

	// Wrong-code budgets per account. Twenty a day is a 0.006% chance a day of
	// guessing a code; five in a quarter hour keeps a typo-prone person from
	// locking themselves out for the day in one sitting.
	totpFailureWindow = 15 * time.Minute
	totpFailureLimit  = 5
	totpDailyWindow   = 24 * time.Hour
	totpDailyLimit    = 20
)

var (
	// ErrSecondFactorRequired is LoginWithOTP's answer when the passcode was
	// right but the account also has an authenticator: the sign-in is not
	// finished. SignInWithOTP returns the pending ticket instead.
	ErrSecondFactorRequired = errors.New("this account also needs a code from its authenticator app")
	// ErrSignInExpired is a pending sign-in that is unknown, expired, used up
	// or overtaken by an account change. The way on is a new passcode.
	ErrSignInExpired = errors.New("this sign-in has expired; start again with a new email passcode")
	// ErrStepUpRequired guards enrollment: turning a second factor on is done
	// from a session that recently re-proved who it is.
	ErrStepUpRequired = errors.New("verify it's you first (step-up), then try again")
	// ErrPersonOnly refuses an agent: only a person at a browser changes
	// their own sign-in factors.
	ErrPersonOnly = errors.New("only a person at a browser can change two-factor authentication")
)

// SecondFactorProof says what a second factor was proved with.
type SecondFactorProof struct {
	// Method is SecondFactorTOTP or SecondFactorRecovery.
	Method string
	// RecoveryCodesLeft is set when a recovery code was spent.
	RecoveryCodesLeft int
}

// PendingSignIn is a sign-in waiting for its authenticator code. The ticket
// is the caller's to hand to the browser (as an HttpOnly cookie) and forget;
// only its digest is kept.
type PendingSignIn struct {
	Ticket  string
	Expires time.Time
}

// SignInResult is a sign-in's outcome: a session (Token, CSRF), or, for an
// account with an authenticator, a pending sign-in to complete.
type SignInResult struct {
	Actor   identity.Actor
	Token   string
	CSRF    string
	Pending *PendingSignIn
	// Proof is what finished a two-factor sign-in; zero for one factor.
	Proof SecondFactorProof
	// Refusal is set beside the error of a refused step, for the server's log.
	Refusal SignInRefusal
}

// SignInRefusal is the server log's account of a refused sign-in step. The
// client never sees it: most refusals give one error on purpose, so a caller
// cannot tell an unknown address from a wrong passcode, but the operator
// reading the log should be able to, and should see whose account it was.
type SignInRefusal struct {
	// Reason is one of the Refused names below, or "" when the error already
	// says it all (a throttle, a full server, a failed database).
	Reason string
	// Account is the account the attempt named, and Email its registered
	// address when the step read it. Both are zero when it named none, and
	// the handler then names what was typed, through TypedAddress.
	Account identity.Actor
	Email   string
}

// Why a sign-in step was refused, in the server log's words.
const (
	RefusedBadCredentials  = "bad-credentials"   // a wrong passcode or code
	RefusedUnknownAccount  = "unknown-account"   // the address names no account
	RefusedExpiredCode     = "expired-code"      // no live passcode or pending sign-in to check it against
	RefusedTooManyAttempts = "too-many-attempts" // a wrong guess that used up the passcode or the pending sign-in
	RefusedServerFault     = "error"             // refused as the others are, though what failed was the server
)

type pendingSignIn struct {
	actor    identity.Actor
	revision string
	expires  time.Time
	attempts int
}

// TOTPEnrollment is what an authenticator app enrolls from. The secret is in
// it twice — raw for typing, inside the URI for the QR code — and it is
// returned once, to the session that asked.
type TOTPEnrollment struct {
	Secret  string
	URI     string
	Account string
}

// LoginWithOTP completes a one-factor sign-in. For an account with an
// authenticator it returns ErrSecondFactorRequired and opens nothing; the
// HTTP handler uses SignInWithOTP, which also returns the pending ticket.
func (a *SessionAuth) LoginWithOTP(r *http.Request, email, otp string) (identity.Actor, string, string, error) {
	result, err := a.SignInWithOTP(r, email, otp)
	if err != nil {
		return identity.Actor{}, "", "", err
	}
	if result.Pending != nil {
		return identity.Actor{}, "", "", ErrSecondFactorRequired
	}
	return result.Actor, result.Token, result.CSRF, nil
}

// CompleteSignIn finishes a pending sign-in with an authenticator code or a
// recovery code. On success it does what a one-factor sign-in does: ends the
// account's other sessions and opens this one.
func (a *SessionAuth) CompleteSignIn(r *http.Request, ticket, code string) (SignInResult, error) {
	source := ""
	if r != nil {
		source = ClientIP(r)
	}
	ctx := requestContext(r)
	// The global and per-source budgets, charged before the ticket is even
	// looked at, like every other sign-in door.
	if !a.chargeLogin("", source) {
		return SignInResult{}, ErrTooManyLogins
	}
	expired := SignInResult{Refusal: SignInRefusal{Reason: RefusedExpiredCode}}
	if ticket == "" || len(ticket) > 128 {
		return expired, ErrSignInExpired
	}
	key := sessionKey(ticket)
	now := time.Now()
	a.mu.Lock()
	pending := a.pendingSignIns[key]
	if pending == nil || now.After(pending.expires) {
		delete(a.pendingSignIns, key)
		a.mu.Unlock()
		return expired, ErrSignInExpired
	}
	actor := pending.actor
	a.mu.Unlock()
	// From here a refusal is a refusal for this account.
	refused := func(reason string) SignInResult {
		return SignInResult{Refusal: SignInRefusal{Reason: reason, Account: actor}}
	}

	proof, err := a.proveSecondFactor(ctx, actor.ID, code)
	if err != nil {
		reason := ""
		if errors.Is(err, ErrTOTPCode) {
			reason = RefusedBadCredentials
			a.mu.Lock()
			pending.attempts++
			if pending.attempts >= maxSecondFactorAttempts {
				delete(a.pendingSignIns, key)
				reason = RefusedTooManyAttempts
			}
			a.mu.Unlock()
		}
		return refused(reason), err
	}
	revision, exists := a.users.Revision(ctx, actor.ID)
	token, csrf, err := newSessionTokens()
	if err != nil {
		return refused(""), err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// The ticket may have been used by a racing request, and the account may
	// have changed — address, role, removal — since the passcode was right.
	if a.pendingSignIns[key] != pending || !exists || revision != pending.revision {
		delete(a.pendingSignIns, key)
		return refused(RefusedExpiredCode), ErrSignInExpired
	}
	if err := a.finishSignInLocked(actor, revision, token, time.Now()); err != nil {
		return refused(""), err
	}
	delete(a.pendingSignIns, key)
	return SignInResult{Actor: actor, Token: token, CSRF: csrf, Proof: proof}, nil
}

// finishSignInLocked is the common end of every web sign-in: the seat check,
// the revocation of the account's other sessions (durable first), and the new
// session. It must be called with a.mu held.
func (a *SessionAuth) finishSignInLocked(actor identity.Actor, revision, token string, now time.Time) error {
	if err := a.sweepSessionsLocked(now); err != nil {
		return err
	}
	// The seat limit is checked before anything is spent or revoked, while the
	// account's own sessions still count as its seat. Checking after the
	// revocation would let a full server sign the person out of their other
	// devices and then refuse them this one.
	if !a.seatAvailableLocked(actor.ID, now) {
		return ErrTooManyActiveUsers
	}
	// Durable first, as everywhere sessions are destroyed: if the delete does
	// not commit, nothing is forgotten in memory and no session is opened
	// beside the ones a restart would resurrect.
	if err := a.store.removeUser(actor.ID); err != nil {
		return err
	}
	a.revokeUserLocked(actor.ID)
	return a.openSessionLocked(actor, revision, token, now)
}

// openPendingSignInLocked records a pending sign-in and returns its ticket.
// It must be called with a.mu held.
func (a *SessionAuth) openPendingSignInLocked(actor identity.Actor, revision, ticket string, now time.Time) *PendingSignIn {
	for key, pending := range a.pendingSignIns {
		if now.After(pending.expires) {
			delete(a.pendingSignIns, key)
		}
	}
	for len(a.pendingSignIns) >= maxPendingSignIns {
		oldestKey := ""
		var oldest time.Time
		for key, pending := range a.pendingSignIns {
			if oldestKey == "" || pending.expires.Before(oldest) {
				oldestKey, oldest = key, pending.expires
			}
		}
		delete(a.pendingSignIns, oldestKey)
	}
	expires := now.Add(SignInTicketTTL)
	a.pendingSignIns[sessionKey(ticket)] = &pendingSignIn{actor: actor, revision: revision, expires: expires}
	return &PendingSignIn{Ticket: ticket, Expires: expires}
}

// proveSecondFactor checks an authenticator code (six digits) or a recovery
// code (anything else) for an account with an authenticator, under the
// account's wrong-code budgets. A wrong code is ErrTOTPCode; a spent budget is
// ErrTooManyLogins.
func (a *SessionAuth) proveSecondFactor(ctx context.Context, userID, code string) (SecondFactorProof, error) {
	now := a.clock()
	a.mu.Lock()
	lockedOut := a.secondFactorLockedOutLocked(userID, now)
	a.mu.Unlock()
	if lockedOut {
		return SecondFactorProof{}, ErrTooManyLogins
	}
	fail := func() (SecondFactorProof, error) {
		a.mu.Lock()
		a.recordSecondFactorFailureLocked(userID, now)
		a.mu.Unlock()
		return SecondFactorProof{}, ErrTOTPCode
	}
	if len(code) == 0 || len(code) > MaxOTPBytes {
		return fail()
	}
	if normalizeTOTP(code) != "" {
		ok, err := a.users.verifyTOTPCode(ctx, userID, code, now)
		if err != nil {
			return SecondFactorProof{}, err
		}
		if !ok {
			return fail()
		}
		return SecondFactorProof{Method: SecondFactorTOTP}, nil
	}
	left, ok, err := a.users.useRecoveryCode(ctx, userID, code)
	if err != nil {
		return SecondFactorProof{}, err
	}
	if !ok {
		return fail()
	}
	return SecondFactorProof{Method: SecondFactorRecovery, RecoveryCodesLeft: left}, nil
}

func (a *SessionAuth) secondFactorLockedOutLocked(userID string, now time.Time) bool {
	failures := a.totpFailures[userID]
	kept := failures[:0]
	recent := 0
	for _, at := range failures {
		if now.Sub(at) < totpDailyWindow {
			kept = append(kept, at)
			if now.Sub(at) < totpFailureWindow {
				recent++
			}
		}
	}
	if len(kept) == 0 {
		delete(a.totpFailures, userID)
	} else {
		a.totpFailures[userID] = kept
	}
	return recent >= totpFailureLimit || len(kept) >= totpDailyLimit
}

func (a *SessionAuth) recordSecondFactorFailureLocked(userID string, now time.Time) {
	// Only accounts with an authenticator reach here, and only after a right
	// passcode or from a live session, so the map is bounded by those
	// accounts; entries older than a day are dropped as they are read.
	failures := a.totpFailures[userID]
	if len(failures) >= totpDailyLimit {
		failures = failures[1:]
	}
	a.totpFailures[userID] = append(failures, now)
}

// personSession resolves the request to a person's live session and its
// account row.
func (a *SessionAuth) personSession(r *http.Request) (string, *Session, UserRecord, error) {
	if ActorFrom(r).IsAgent() {
		return "", nil, UserRecord{}, ErrPersonOnly
	}
	a.mu.Lock()
	key, session, ok := a.sessionLocked(r)
	a.mu.Unlock()
	if !ok {
		return "", nil, UserRecord{}, ErrUnauthenticated
	}
	ctx := requestContext(r)
	users, err := a.users.accounts(ctx, accountByID, 1, session.actor.ID)
	if err != nil || len(users) == 0 {
		if err := contextFailure(ctx); err != nil {
			return "", nil, UserRecord{}, err
		}
		return "", nil, UserRecord{}, ErrUnauthenticated
	}
	return key, session, users[0], nil
}

// StepUpMethod says how the request's session steps up: SecondFactorTOTP for
// an account with an authenticator, StepUpMethodEmail otherwise.
func (a *SessionAuth) StepUpMethod(r *http.Request) (string, error) {
	_, _, account, err := a.personSession(r)
	if err != nil {
		return "", err
	}
	enabled, err := a.users.TOTPEnabled(requestContext(r), account.ID)
	if err != nil {
		return "", err
	}
	if enabled {
		return SecondFactorTOTP, nil
	}
	return StepUpMethodEmail, nil
}

// TOTPStatus is the request's own account's authenticator state.
func (a *SessionAuth) TOTPStatus(r *http.Request) (TOTPState, error) {
	_, _, account, err := a.personSession(r)
	if err != nil {
		return TOTPState{}, err
	}
	return a.users.TOTPStatus(requestContext(r), account.ID)
}

// requireStepUp refuses enrollment from a session that has not recently
// stepped up.
func (a *SessionAuth) requireStepUp(r *http.Request) error {
	if _, ok := a.SteppedUpAt(r); !ok {
		return ErrStepUpRequired
	}
	return nil
}

// BeginTOTP starts (or restarts) enrollment for the request's account and
// returns the secret for the app. It needs a current step-up, and an account
// that does not already have an authenticator on.
func (a *SessionAuth) BeginTOTP(r *http.Request) (TOTPEnrollment, error) {
	_, _, account, err := a.personSession(r)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if err := a.requireStepUp(r); err != nil {
		return TOTPEnrollment{}, err
	}
	secret, err := a.users.beginTOTP(requestContext(r), account.ID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	label := account.Email
	if label == "" {
		label = account.Name
	}
	return TOTPEnrollment{Secret: encodeTOTPSecret(secret), URI: totpURI(label, secret), Account: label}, nil
}

// ConfirmTOTP turns the begun enrollment on with a code from the app and
// returns the recovery codes, once. It needs a current step-up.
//
// Every other session the account had is signed out: they were opened with
// the mailbox alone, and turning the second factor on is exactly the moment
// somebody who only had the mailbox should lose what it got them.
func (a *SessionAuth) ConfirmTOTP(r *http.Request, code string) ([]string, error) {
	key, _, account, err := a.personSession(r)
	if err != nil {
		return nil, err
	}
	if err := a.requireStepUp(r); err != nil {
		return nil, err
	}
	if !a.chargeLogin("totp-enroll:"+account.ID, ClientIP(r)) {
		return nil, ErrTooManyLogins
	}
	if len(code) == 0 || len(code) > MaxOTPBytes {
		return nil, ErrTOTPCode
	}
	codes, err := a.users.confirmTOTP(requestContext(r), account.ID, code, a.clock())
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var others []string
	for other, session := range a.sessions {
		if other != key && session.actor.ID == account.ID {
			others = append(others, other)
		}
	}
	if err := a.removeSessionsLocked(others...); err != nil {
		// The factor is on and its recovery codes exist only in this answer,
		// so failing here would lose them. The sessions stay until signed
		// out or expired; the operator hears about it.
		log.Printf("two-factor enabled for %s but its other sessions could not be signed out: %v", account.Name, err)
	}
	return codes, nil
}

// DisableTOTP turns the request's account's authenticator off. It takes a
// current authenticator code, or a recovery code — the way back for somebody
// who lost the phone but kept the printout and wants to enroll a new one.
func (a *SessionAuth) DisableTOTP(r *http.Request, code string) (SecondFactorProof, error) {
	_, _, account, err := a.personSession(r)
	if err != nil {
		return SecondFactorProof{}, err
	}
	if !a.chargeLogin("totp-change:"+account.ID, ClientIP(r)) {
		return SecondFactorProof{}, ErrTooManyLogins
	}
	ctx := requestContext(r)
	enabled, err := a.users.TOTPEnabled(ctx, account.ID)
	if err != nil {
		return SecondFactorProof{}, err
	}
	if !enabled {
		return SecondFactorProof{}, ErrTOTPNotEnabled
	}
	proof, err := a.proveSecondFactor(ctx, account.ID, code)
	if err != nil {
		return SecondFactorProof{}, err
	}
	if _, err := a.users.disableTOTP(ctx, account.ID); err != nil {
		return SecondFactorProof{}, err
	}
	return proof, nil
}

// RegenerateRecoveryCodes replaces the account's recovery codes. It takes a
// current authenticator code, not a recovery code: spending the last of the
// old set to mint a new one would make the set endless for whoever holds it.
func (a *SessionAuth) RegenerateRecoveryCodes(r *http.Request, code string) ([]string, error) {
	_, _, account, err := a.personSession(r)
	if err != nil {
		return nil, err
	}
	if !a.chargeLogin("totp-change:"+account.ID, ClientIP(r)) {
		return nil, ErrTooManyLogins
	}
	ctx := requestContext(r)
	enabled, err := a.users.TOTPEnabled(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrTOTPNotEnabled
	}
	if normalizeTOTP(code) == "" {
		return nil, ErrTOTPCode
	}
	if _, err := a.proveSecondFactor(ctx, account.ID, code); err != nil {
		return nil, err
	}
	return a.users.regenerateRecoveryCodes(ctx, account.ID)
}

// AccountEmail is the address registered for an account, or "" when it has
// none or cannot be read. It is for notices about the account's own security
// settings, sent to the same inbox its passcodes go to.
func (a *SessionAuth) AccountEmail(ctx context.Context, actorID string) string {
	if a == nil || a.users == nil || actorID == "" {
		return ""
	}
	users, err := a.users.accounts(ctx, accountByID, 1, actorID)
	if err != nil || len(users) == 0 {
		return ""
	}
	return users[0].Email
}
