package auth

import (
	"context"
	"errors"
	"net/http"
	"nodevas/internal/identity"
	"strings"
	"sync"
	"time"
)

const (
	SessionCookieName = "nodevas_session"
	SessionTTL        = 12 * time.Hour
	// A browser tab left open all day should not be logged out mid-edit, so a
	// Session that is still being used is extended.
	sessionRenewAfter = 1 * time.Hour

	// MaxLoginBodyBytes is the recommended login-handler body cap. The auth
	// layer independently validates each decoded field as a second boundary.
	MaxLoginBodyBytes = 8 << 10

	loginUserWindow   = 5 * time.Minute
	LoginFailureLimit = 10 // retained name for compatibility; all attempts count
	loginIPWindow     = 1 * time.Minute
	loginIPLimit      = 20
	loginGlobalWindow = 1 * time.Minute
	loginGlobalLimit  = 60
	maxLoginBuckets   = 2048
	maxSessions       = 4096
)

type Session struct {
	actor    identity.Actor
	revision string
	created  time.Time
	expires  time.Time
}

type loginBucket struct {
	at      []time.Time
	touched time.Time
}

// SessionAuth is the authenticator for a server other machines can reach:
// accounts, Session cookies, and CSRF tokens.
//
// Sessions are answered from memory and mirrored into the workspace database,
// so a restart no longer signs everybody out. That used to be the deliberately
// safe direction — no session file could outlive a removed account — and the
// guarantee is now kept by Authenticate instead: every request re-reads the
// account's revision, and a session whose account has changed or gone is
// refused on the spot. Its map entry is removed only after the database delete
// commits; on failure it remains refused and the deletion is retried by later
// checks. Persisting a token buys nobody anything the account itself no longer
// authorises. The map and the table are keyed by the token's sha256, never by
// the token; see sessionKey.
type SessionAuth struct {
	users *UserStore
	store sessionStore

	mu       sync.Mutex
	sessions map[string]*Session
	attempts map[string]*loginBucket
	// One live passcode per account, keyed by account ID. See otp.go.
	otps map[string]*pendingOTP
	// maxActiveUsers caps how many distinct accounts may hold a session at
	// once; zero means no cap. See SetMaxActiveUsers.
	maxActiveUsers int
}

func NewSessionAuth(users *UserStore) *SessionAuth {
	store := sessionStore{}
	if users != nil {
		store.database = users.database
	}
	return &SessionAuth{
		users:    users,
		store:    store,
		sessions: store.load(time.Now()),
		attempts: map[string]*loginBucket{},
		otps:     map[string]*pendingOTP{},
	}
}

// ErrTooManyActiveUsers is returned when the operator's seat limit is already
// taken by other people. It is deliberately distinguishable from bad
// credentials: the person got both factors right, and telling them the server
// is full is not a fact an attacker did not already have.
var ErrTooManyActiveUsers = errors.New(
	"the maximum number of people are already signed in; ask one of them to sign out")

// ErrSessionPersistence means a destructive session transaction did not
// commit. Callers can distinguish infrastructure failure from rejected
// credentials without exposing the database error to a remote client.
var ErrSessionPersistence = errors.New("session persistence is unavailable")

// SetMaxActiveUsers caps how many distinct accounts may be signed in at once.
// Zero, the default, means no cap.
//
// The unit is accounts rather than sessions on purpose: one person with a
// phone and a laptop is one person, and a limit that counted tabs would lock
// them out of their own second device. A login beyond the cap is refused
// rather than allowed to evict somebody — evicting would let anyone who
// reaches the login form push the people already working off the server.
func (a *SessionAuth) SetMaxActiveUsers(limit int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if limit < 0 {
		limit = 0
	}
	a.maxActiveUsers = limit
}

// ActiveUsers reports how many distinct accounts hold a live session.
func (a *SessionAuth) ActiveUsers() int {
	now := time.Now()
	a.mu.Lock()
	// A persistence failure leaves the expired entries in the map so the
	// process and a restart cannot disagree. They still are not live users.
	_ = a.sweepSessionsLocked(now)
	count := len(a.activeUserIDsLocked(now))
	a.mu.Unlock()
	return count
}

// seatAvailableLocked reports whether this account may open a session under
// the seat limit. Somebody already signed in on another device is always
// allowed: the limit counts people, and their phone is not a second person.
func (a *SessionAuth) seatAvailableLocked(userID string, now time.Time) bool {
	if a.maxActiveUsers <= 0 {
		return true
	}
	// Visitors do not take seats. The limit exists to bound how many people can
	// have work open at once, and a visitor has none open — counting them would
	// let anyone with the shared PIN fill the server and lock the editors out,
	// which is the one thing a read-only credential must not be able to do.
	// maxSessions still bounds the total.
	if userID == VisitorID {
		return true
	}
	active := a.activeUserIDsLocked(now)
	if _, alreadyHere := active[userID]; alreadyHere {
		return true
	}
	return len(active) < a.maxActiveUsers
}

func (a *SessionAuth) activeUserIDsLocked(now time.Time) map[string]struct{} {
	ids := make(map[string]struct{}, len(a.sessions))
	for _, session := range a.sessions {
		if now.After(session.expires) {
			continue
		}
		ids[session.actor.ID] = struct{}{}
	}
	return ids
}

func (a *SessionAuth) NeedsCSRF() bool { return true }
func (a *SessionAuth) Remote() bool    { return true }

// CurrentActor returns live account authority, not a session snapshot.
func (a *SessionAuth) CurrentActor(ctx context.Context, id string) (identity.Actor, string, bool) {
	return a.users.ActorRevision(ctx, id)
}

func (a *SessionAuth) Authenticate(r *http.Request) (identity.Actor, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return identity.Actor{}, ErrUnauthenticated
	}
	key := sessionKey(cookie.Value)
	a.mu.Lock()
	found, ok := a.sessions[key]
	if !ok {
		a.mu.Unlock()
		return identity.Actor{}, ErrUnauthenticated
	}
	now := time.Now()
	if now.After(found.expires) {
		_ = a.removeSessionsLocked(key)
		a.mu.Unlock()
		return identity.Actor{}, ErrUnauthenticated
	}
	actor := found.actor
	revision := found.revision
	a.mu.Unlock()

	// A visitor session is pinned to no account, so there is no revision to
	// re-read. What it is pinned to instead is the credential still existing:
	// `nodevas visitor off` has to mean off now, not at the end of a
	// twelve-hour TTL, because the situation an operator turns it off in is one
	// where the link is somewhere they did not expect.
	//
	// The credential is not re-verified here, only its presence. Rotating the
	// PIN is not the same act as revoking access, and forcing every reader off
	// the page for it would make an operator hesitate over the safer of the two.
	if actor.ID == VisitorID {
		if !a.VisitorEnabled(r.Context()) {
			a.mu.Lock()
			if a.sessions[key] == found {
				_ = a.removeSessionsLocked(key)
			}
			a.mu.Unlock()
			return identity.Actor{}, ErrUnauthenticated
		}
		a.renew(key, found, now)
		return actor, nil
	}

	// The request's context, so a client that disconnects mid-request releases
	// the read slot this query is holding instead of pinning it until SQLite's
	// busy timeout gives up. A cancelled read reads as "no such revision" and
	// the request is refused, which is the direction an authentication check
	// must fail in anyway.
	currentRevision, exists := a.users.Revision(r.Context(), actor.ID)
	if !exists || currentRevision != revision {
		a.mu.Lock()
		if a.sessions[key] == found {
			_ = a.removeSessionsLocked(key)
		}
		a.mu.Unlock()
		return identity.Actor{}, ErrUnauthenticated
	}

	a.renew(key, found, now)
	return actor, nil
}

// renew pushes a session's expiry out, but only once it is old enough to be
// worth a write: a busy tab would otherwise touch the database on every
// request for a deadline that moves by an hour.
func (a *SessionAuth) renew(key string, session *Session, now time.Time) {
	a.mu.Lock()
	stale := a.sessions[key] == session &&
		time.Until(session.expires) < SessionTTL-sessionRenewAfter
	if stale {
		session.expires = now.Add(SessionTTL)
	}
	expires := session.expires
	a.mu.Unlock()
	if stale {
		a.store.touch(key, expires)
	}
}

// Login verifies credentials and opens a Session. The returned CSRF token is
// what the browser echoes back in the X-CSRF-Token header.
//
// The context is a parameter rather than r.Context() because there is no
// request here: this is the entry point for an embedder or a test that has
// credentials but no HTTP in front of them. Handlers want LoginRequest.
func (a *SessionAuth) Login(ctx context.Context, name, password string) (identity.Actor, string, string, error) {
	return a.login(ctx, name, password, "")
}

// LoginRequest applies source-IP limiting using only forwarding headers from
// configured trusted proxies. HTTP handlers should prefer it over Login.
func (a *SessionAuth) LoginRequest(r *http.Request, name, password string) (identity.Actor, string, string, error) {
	return a.login(requestContext(r), name, password, ClientIP(r))
}

func (a *SessionAuth) login(ctx context.Context, name, password, source string) (identity.Actor, string, string, error) {
	name = strings.TrimSpace(name)
	if !a.allowLogin(name, source) {
		return identity.Actor{}, "", "", ErrTooManyLogins
	}
	if !validLoginInput(name, password) {
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	actor, revision, ok := a.users.VerifyWithRevision(ctx, name, password)
	if !ok {
		// VerifyWithRevision cannot say why it said no, so ask the context.
		// Someone whose browser went away did not offer a wrong password, and
		// reporting it as one would write a sign-in failure into the audit
		// trail that nobody attempted. The throttle was already charged before
		// the hash ran and is not refunded here — that ordering is what keeps a
		// caller from spending the server's Argon2 budget for free, and undoing
		// it for cancelled requests would hand back exactly that.
		if err := contextFailure(ctx); err != nil {
			return identity.Actor{}, "", "", err
		}
		return identity.Actor{}, "", "", ErrBadCredentials
	}
	return a.openSession(actor, revision)
}

// requestContext is the request's context, or a background one when there is
// no request. Both OTP entry points accept a nil *http.Request, and a nil
// receiver would panic on r.Context() before any of the throttles ran.
func requestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}

// contextFailure reports the context's error, if any. It exists so the sign-in
// paths all phrase the same question the same way: "was this a refusal, or did
// the caller stop waiting?"
func contextFailure(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// openSession mints the cookie pair and records the session. Both sign-in
// paths end here, so the seat limit and the eviction rules cannot drift apart
// between them.
func (a *SessionAuth) openSession(actor identity.Actor, revision string) (identity.Actor, string, string, error) {
	token, err := RandomToken()
	if err != nil {
		return identity.Actor{}, "", "", err
	}
	csrf, err := RandomToken()
	if err != nil {
		return identity.Actor{}, "", "", err
	}
	a.mu.Lock()
	now := time.Now()
	if err := a.sweepSessionsLocked(now); err != nil {
		a.mu.Unlock()
		return identity.Actor{}, "", "", err
	}

	if !a.seatAvailableLocked(actor.ID, now) {
		a.mu.Unlock()
		return identity.Actor{}, "", "", ErrTooManyActiveUsers
	}

	for len(a.sessions) >= maxSessions {
		if err := a.evictOldestSessionLocked(); err != nil {
			a.mu.Unlock()
			return identity.Actor{}, "", "", err
		}
	}
	key := sessionKey(token)
	session := &Session{
		actor: actor, revision: revision, created: now, expires: now.Add(SessionTTL),
	}
	a.sessions[key] = session
	// Creation shares the same ordering lock as every destructive transaction.
	// If save ran after unlocking, a concurrent account revoke could commit its
	// DELETE before this INSERT and the delayed row would resurrect on restart.
	a.store.save(key, session)
	a.mu.Unlock()
	return actor, token, csrf, nil
}

// Logout removes persistence before the in-memory session. An error therefore
// has one meaning: the session remains live in both places and the caller may
// retry without a restart unexpectedly bringing it back.
func (a *SessionAuth) Logout(token string) error {
	key := sessionKey(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.removeSessionsLocked(key)
}

// allowLogin atomically charges global, source-IP, and account buckets before
// Argon2 starts. Rotating names cannot bypass the global/IP budgets, and every
// map and timestamp slice is bounded.
func (a *SessionAuth) allowLogin(name, source string) bool {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepAttemptsLocked(now)

	charges := []rateCharge{{key: "global", window: loginGlobalWindow, limit: loginGlobalLimit}}
	if source = normalizeSource(source); source != "" {
		charges = append(charges, rateCharge{key: "ip:" + source, window: loginIPWindow, limit: loginIPLimit})
	}
	if userNamePattern.MatchString(name) {
		charges = append(charges, rateCharge{key: "user:" + strings.ToLower(name), window: loginUserWindow, limit: LoginFailureLimit})
	}
	return a.chargeLocked(charges, now)
}

// rateCharge is one budget: how many events of a kind are allowed in a window.
type rateCharge struct {
	key    string
	window time.Duration
	limit  int
}

// chargeLocked is all-or-nothing: every budget is checked before any is spent,
// so a request that is going to be refused does not consume the budget of the
// requests that come after it.
func (a *SessionAuth) chargeLocked(charges []rateCharge, now time.Time) bool {
	for _, item := range charges {
		if len(a.recentAttemptsLocked(item.key, item.window, now)) >= item.limit {
			return false
		}
	}
	for _, item := range charges {
		bucket := a.attempts[item.key]
		if bucket == nil {
			a.makeBucketRoomLocked()
			bucket = &loginBucket{}
			a.attempts[item.key] = bucket
		}
		bucket.at = append(bucket.at, now)
		bucket.touched = now
	}
	return true
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" || len(source) > 64 {
		return ""
	}
	return source
}

func (a *SessionAuth) recentAttemptsLocked(key string, window time.Duration, now time.Time) []time.Time {
	bucket := a.attempts[key]
	if bucket == nil {
		return nil
	}
	cutoff := now.Add(-window)
	kept := bucket.at[:0:0]
	for _, at := range bucket.at {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	bucket.at = kept
	return kept
}

func (a *SessionAuth) sweepAttemptsLocked(now time.Time) {
	cutoff := now.Add(-loginUserWindow)
	for key, bucket := range a.attempts {
		if key != "global" && bucket.touched.Before(cutoff) {
			delete(a.attempts, key)
		}
	}
}

func (a *SessionAuth) makeBucketRoomLocked() {
	for len(a.attempts) >= maxLoginBuckets {
		oldestKey := ""
		var oldest time.Time
		for key, bucket := range a.attempts {
			if key == "global" {
				continue
			}
			if oldestKey == "" || bucket.touched.Before(oldest) {
				oldestKey, oldest = key, bucket.touched
			}
		}
		if oldestKey == "" {
			return
		}
		delete(a.attempts, oldestKey)
	}
}

// sweepSessionsLocked persists the expiry sweep before changing the live map.
// It must be called with a.mu held.
func (a *SessionAuth) sweepSessionsLocked(now time.Time) error {
	var gone []string
	for key, found := range a.sessions {
		if now.After(found.expires) {
			gone = append(gone, key)
		}
	}
	return a.removeSessionsLocked(gone...)
}

func (a *SessionAuth) evictOldestSessionLocked() error {
	oldestKey := ""
	var oldest time.Time
	for key, session := range a.sessions {
		if oldestKey == "" || session.created.Before(oldest) {
			oldestKey, oldest = key, session.created
		}
	}
	if oldestKey != "" {
		return a.removeSessionsLocked(oldestKey)
	}
	return nil
}

// removeSessionsLocked is the single consistency boundary for token-based
// revocation. Persistence commits first; only then does the running process
// forget the same keys. It must be called with a.mu held.
func (a *SessionAuth) removeSessionsLocked(keys ...string) error {
	if err := a.store.remove(keys...); err != nil {
		return err
	}
	for _, key := range keys {
		delete(a.sessions, key)
	}
	return nil
}
