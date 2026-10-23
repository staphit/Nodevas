// Step-up: re-proving, with a fresh mailed passcode, that the person behind a
// signed-in browser session is still the account holder (W6).
//
// A runner may auto-accept remote dispatches only from a dispatcher who
// stepped up recently (runnerwire.Job.SteppedUp), so a stolen session cookie
// alone cannot run agents on the owner's machine: the thief would also need
// the owner's mailbox, now, within the window.
//
// It is a Nodevas-side passcode and independent of Cloudflare Access: Access
// decides who reaches the server at all, step-up decides whether this session
// may vouch for remote work. A step-up belongs to one session: it is stored on
// the session row so a restart keeps it, and a new sign-in starts without one.
// Agents cannot step up; only a person at a browser can.

package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"nodevas/internal/runnerwire"
)

// DefaultStepUpWindow is how long a step-up lasts unless the server says
// otherwise (serve --step-up-minutes).
const DefaultStepUpWindow = runnerwire.StepUpWindowSeconds * time.Second

// MaxStepUpWindow bounds the setting: a step-up that lasts all day is a
// second session cookie, not a re-proof.
const MaxStepUpWindow = 12 * time.Hour

var (
	// ErrStepUpAgent refuses a step-up from an agent: the point is a person
	// reading their mailbox.
	ErrStepUpAgent = errors.New("only a person at a browser can step up")
	// ErrStepUpNoEmail is an account with no address to send a passcode to.
	ErrStepUpNoEmail = errors.New("this account has no email address to send a step-up passcode to")
	// ErrStepUpCode is a wrong, expired or missing step-up passcode.
	ErrStepUpCode = errors.New("that step-up passcode is wrong or has expired; request a new one")
)

// SetStepUpWindow sets how long a step-up lasts. Zero or less restores the
// default; anything over MaxStepUpWindow is cut to it.
func (a *SessionAuth) SetStepUpWindow(window time.Duration) {
	if window <= 0 {
		window = DefaultStepUpWindow
	}
	if window > MaxStepUpWindow {
		window = MaxStepUpWindow
	}
	a.mu.Lock()
	a.stepUpWindow = window
	a.mu.Unlock()
}

// StepUpWindow is how long a step-up lasts.
func (a *SessionAuth) StepUpWindow() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.windowLocked()
}

func (a *SessionAuth) windowLocked() time.Duration {
	if a.stepUpWindow <= 0 {
		return DefaultStepUpWindow
	}
	return a.stepUpWindow
}

func (a *SessionAuth) clock() time.Time {
	if a.now == nil {
		return time.Now()
	}
	return a.now()
}

// sessionLocked finds the live session behind a request's cookie. It must be
// called with a.mu held.
func (a *SessionAuth) sessionLocked(r *http.Request) (string, *Session, bool) {
	if r == nil {
		return "", nil, false
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", nil, false
	}
	key := sessionKey(cookie.Value)
	session, ok := a.sessions[key]
	if !ok || time.Now().After(session.expires) {
		return "", nil, false
	}
	return key, session, true
}

// RequestStepUp issues a step-up passcode for the request's session, to the
// address registered on its account. The same budgets as a sign-in passcode
// apply, charged per source and per address, so step-up cannot be used to
// fill the account holder's inbox any faster than the sign-in form can.
//
// The returned Challenge is the caller's to deliver and forget.
func (a *SessionAuth) RequestStepUp(r *http.Request) (Challenge, error) {
	if ActorFrom(r).IsAgent() {
		return Challenge{}, ErrStepUpAgent
	}
	a.mu.Lock()
	key, session, ok := a.sessionLocked(r)
	var actorID string
	if ok {
		actorID = session.actor.ID
	}
	a.mu.Unlock()
	if !ok {
		return Challenge{}, ErrUnauthenticated
	}

	ctx := requestContext(r)
	users, err := a.users.accounts(ctx, accountByID, 1, actorID)
	if err != nil || len(users) == 0 {
		if err := contextFailure(ctx); err != nil {
			return Challenge{}, err
		}
		return Challenge{}, ErrUnauthenticated
	}
	account := users[0]
	if account.Email == "" {
		return Challenge{}, ErrStepUpNoEmail
	}
	// The same budgets as a sign-in passcode, in buckets of their own: a
	// person who has just signed in must not be told to wait out the sign-in
	// passcode's resend cooldown before they can step up.
	if !a.allowStepUpRequest(ClientIP(r)) || !a.allowOTPForAddress("stepup:"+emailKey(account.Email)) {
		return Challenge{}, ErrTooManyOTPRequests
	}

	code, err := newOTP()
	if err != nil {
		return Challenge{}, err
	}
	expires := a.clock().Add(OTPTTL)
	a.mu.Lock()
	// The session may have gone while the account was read.
	if _, still := a.sessions[key]; !still {
		a.mu.Unlock()
		return Challenge{}, ErrUnauthenticated
	}
	// One live step-up passcode per session; the last one requested wins.
	a.stepUps[key] = &pendingOTP{digest: digestOTP(code), expires: expires, revision: userRevision(account)}
	a.mu.Unlock()
	return Challenge{Code: code, Email: account.Email, Expires: expires, Actor: account.Name}, nil
}

// allowStepUpRequest charges the global and per-source passcode budgets, at
// the sign-in request's limits, in step-up's own buckets.
func (a *SessionAuth) allowStepUpRequest(source string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepAttemptsLocked(now)
	charges := []rateCharge{{key: "stepup-global", window: otpRequestWindow, limit: loginGlobalLimit}}
	if source = normalizeSource(source); source != "" {
		charges = append(charges, rateCharge{key: "stepup-ip:" + source, window: otpRequestWindow, limit: otpRequestLimit})
	}
	return a.chargeLocked(charges, now)
}

// VerifyStepUp checks a step-up passcode and, when it is right, marks the
// request's session stepped up from now for the window. It returns when the
// step-up ends.
//
// Every attempt is charged to the sign-in budgets (per source, and per account
// as "stepup:<id>") before anything is compared, and a passcode dies after
// the same number of wrong guesses as a sign-in passcode.
func (a *SessionAuth) VerifyStepUp(r *http.Request, code string) (time.Time, error) {
	if ActorFrom(r).IsAgent() {
		return time.Time{}, ErrStepUpAgent
	}
	a.mu.Lock()
	key, session, ok := a.sessionLocked(r)
	var actorID string
	if ok {
		actorID = session.actor.ID
	}
	a.mu.Unlock()
	if !ok {
		return time.Time{}, ErrUnauthenticated
	}
	if !a.chargeLogin("stepup:"+actorID, ClientIP(r)) {
		return time.Time{}, ErrTooManyLogins
	}
	if len(code) == 0 || len(code) > MaxOTPBytes {
		return time.Time{}, ErrStepUpCode
	}
	revision, exists := a.users.Revision(requestContext(r), actorID)

	now := a.clock()
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := a.stepUps[key]
	if !exists || pending == nil || now.After(pending.expires) || pending.revision != revision ||
		a.sessions[key] != session {
		delete(a.stepUps, key)
		return time.Time{}, ErrStepUpCode
	}
	offered := digestOTP(code)
	if subtle.ConstantTimeCompare(pending.digest[:], offered[:]) != 1 {
		pending.attempts++
		if pending.attempts >= maxOTPAttempts {
			delete(a.stepUps, key)
		}
		return time.Time{}, ErrStepUpCode
	}
	delete(a.stepUps, key)
	session.steppedUp = now
	a.store.stepUp(key, now)
	return now.Add(a.windowLocked()), nil
}

// StepUpState reports whether the request's session is stepped up and until
// when. An agent, or a request without a live session, is never stepped up.
func (a *SessionAuth) StepUpState(r *http.Request) (bool, time.Time) {
	at, ok := a.SteppedUpAt(r)
	if !ok {
		return false, time.Time{}
	}
	return true, at.Add(a.StepUpWindow())
}

// SteppedUpAt is when the request's session stepped up, if that step-up is
// still within the window. Run creation records it (runs.stepped_up_at) so
// offers can vouch for the dispatcher while it lasts.
func (a *SessionAuth) SteppedUpAt(r *http.Request) (time.Time, bool) {
	if ActorFrom(r).IsAgent() {
		return time.Time{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, session, ok := a.sessionLocked(r)
	if !ok || session.steppedUp.IsZero() {
		return time.Time{}, false
	}
	if !a.clock().Before(session.steppedUp.Add(a.windowLocked())) {
		return time.Time{}, false
	}
	return session.steppedUp, true
}
