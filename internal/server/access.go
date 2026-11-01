package server

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/logging"
)

// errAccessRequired is all a refused caller learns: which check failed is the
// server log's business, not a probe's.
var errAccessRequired = locale.Errorf("cloudflare access required", "需要 Cloudflare Access")

// How often the log says the same thing about one caller. A grant is one line
// per identity and address per half hour: enough to see a new device or a new
// network, few enough that a browser left open all day is a handful of lines.
// A refusal, when Access is required, is a line every time, like the 403 it
// gets: how many came, and how fast, is what an alert on them counts. In
// audit mode nothing is refused, and a missing or broken Access setup would
// otherwise put a line beside every request a browser makes, so there it is
// one line per address and reason per minute.
const (
	accessGrantedEvery    = 30 * time.Minute
	accessUnverifiedEvery = time.Minute
	// maxAccessNotes caps each set of remembered callers. Anybody can make up
	// an address or a token, so the sets must not grow with them; past the
	// cap the oldest is forgotten, which costs at worst a repeated line.
	maxAccessNotes = 1024
)

// AccessMiddleware puts Cloudflare Access in front of every route.
//
// Required, a request without a valid Access token is refused with 403, so
// Nodevas stays closed even to a caller that reaches it around the tunnel.
// Not required (audit mode), a bad or missing token is only logged, which is
// how a deployment checks its Access setup before enforcing it.
//
// AccessMiddleware itself exempts every request straight from this machine
// (auth.DirectLoopback). The server installs accessGate instead: on a hub
// with accounts only the container healthcheck (/api/healthz) is exempt,
// and a local `nodevas mcp` passes through Access like any other client. A
// proxy on the same host that adds forwarding headers is checked like any
// other caller.
//
// A verified identity is recorded on the request for the audit trail. It
// signs nobody in: Nodevas sign-in stays its own second layer.
func AccessMiddleware(verifier *auth.AccessVerifier, required bool) gin.HandlerFunc {
	return accessMiddleware(verifier, required, time.Now)
}

// accessGate is AccessMiddleware as this server installs it, exempting what
// accessExempt exempts.
func (s *Server) accessGate() gin.HandlerFunc {
	return accessMiddlewareExempting(s.accessVerifier, s.accessRequired, s.accessExempt, time.Now)
}

// accessExempt reports a request Access does not apply to: one straight from
// this machine. On a hub with accounts that is only the health probe. A proxy
// on the same host that adds no forwarding header (nginx's defaults, ssh -L,
// socat) looks exactly like this machine, and the app behind it, metrics
// included, must stay closed to a caller that went around the edge; no
// person on a hub with accounts uses it from loopback without Access anyway.
func (s *Server) accessExempt(r *http.Request) bool {
	if !auth.DirectLoopback(r) {
		return false
	}
	if s.auth == nil || !s.auth.Remote() {
		return true
	}
	return r.URL.Path == healthzPath
}

// accessMiddleware is AccessMiddleware on a clock a test can move.
func accessMiddleware(verifier *auth.AccessVerifier, required bool, now func() time.Time) gin.HandlerFunc {
	return accessMiddlewareExempting(verifier, required, auth.DirectLoopback, now)
}

// accessMiddlewareExempting is accessMiddleware with the exemption given.
func accessMiddlewareExempting(verifier *auth.AccessVerifier, required bool, exempt func(*http.Request) bool, now func() time.Time) gin.HandlerFunc {
	if verifier == nil {
		return func(c *gin.Context) { c.Next() }
	}
	granted := newAccessNotes(accessGrantedEvery, now)
	unverified := newAccessNotes(accessUnverifiedEvery, now)
	return func(c *gin.Context) {
		r := c.Request
		if exempt(r) {
			c.Next()
			return
		}
		client := auth.ClientIP(r)
		claims, err := verifier.VerifyRequest(r)
		if err != nil {
			reason := accessDenialReason(err)
			if required {
				logAccessDenied(r, required, client, reason, err)
				httpx.Err(c, http.StatusForbidden, errAccessRequired)
				return
			}
			if unverified.due(client + "\x00" + reason) {
				logAccessDenied(r, required, client, reason, err)
			}
			c.Next()
			return
		}
		identity := claims.Email
		if identity == "" && claims.CommonName != "" {
			identity = "service:" + claims.CommonName
		}
		if identity != "" {
			c.Request = r.WithContext(auth.WithAccessEmail(r.Context(), identity))
		}
		if granted.due(identity + "\x00" + client) {
			logAccessGranted(r, claims, client)
		}
		c.Next()
	}
}

// accessDenialReason names a refused Access token for the log.
func accessDenialReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrAccessMissing):
		return "missing-jwt"
	case errors.Is(err, auth.ErrAccessAudience):
		return "bad-audience"
	case errors.Is(err, auth.ErrAccessExpired):
		return "expired-jwt"
	default:
		return "invalid-jwt"
	}
}

// logAccessGranted writes the line for a request Access let through. A
// person's token names their address; a service token (a runner's) names its
// client ID, which is an identifier and not the secret that goes with it.
func logAccessGranted(r *http.Request, claims auth.AccessClaims, client string) {
	attrs := []slog.Attr{
		logging.Event(logging.CategoryAuthentication, "auth.access.granted", logging.OutcomeSuccess),
		logging.UserEmail(claims.Email),
		logging.ClientIP(client),
		logging.Request(r.Context()),
	}
	if claims.Email == "" && claims.CommonName != "" {
		attrs = append(attrs, logging.User("", "service:"+claims.CommonName))
	}
	if claims.Country != "" {
		attrs = append(attrs, slog.String("nodevas.access.country", claims.Country))
	}
	slog.Default().LogAttrs(r.Context(), slog.LevelInfo, "auth.access.granted success", attrs...)
}

// logAccessDenied writes the line for a request without a valid Access
// token. The token itself is never logged, and neither is anything it
// claims: a token that failed verification vouches for nothing.
func logAccessDenied(r *http.Request, required bool, client, reason string, err error) {
	slog.Default().LogAttrs(r.Context(), slog.LevelWarn, "auth.access.denied failure",
		logging.Event(logging.CategoryAuthentication, "auth.access.denied", logging.OutcomeFailure),
		logging.EventReason(reason),
		logging.ClientIP(client),
		logging.Request(r.Context()),
		slog.String("url.path", r.URL.Path),
		slog.Bool("nodevas.access.required", required),
		// The code the deployment guides tell an operator to watch for before
		// switching Access to required.
		slog.String("event.code", "access.invalid"),
		logging.Err(err),
	)
}

// accessNotes remembers which callers the log mentioned recently, so the same
// line is written at most once a window per caller.
type accessNotes struct {
	every time.Duration
	now   func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time
}

func newAccessNotes(every time.Duration, now func() time.Time) *accessNotes {
	return &accessNotes{every: every, now: now, seen: map[string]time.Time{}}
}

// due reports whether key is due a line, and if it is, counts this one.
func (n *accessNotes) due(key string) bool {
	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	last, known := n.seen[key]
	if known && now.Sub(last) < n.every {
		return false
	}
	if !known && len(n.seen) >= maxAccessNotes {
		n.forgetLocked(now)
	}
	n.seen[key] = now
	return true
}

// forgetLocked drops every entry whose window has passed and, when the set is
// still full, the oldest of the rest.
func (n *accessNotes) forgetLocked(now time.Time) {
	oldestKey, oldest := "", time.Time{}
	for key, at := range n.seen {
		if now.Sub(at) >= n.every {
			delete(n.seen, key)
			continue
		}
		if oldestKey == "" || at.Before(oldest) {
			oldestKey, oldest = key, at
		}
	}
	if len(n.seen) >= maxAccessNotes {
		delete(n.seen, oldestKey)
	}
}
