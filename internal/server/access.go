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
)

// errAccessRequired is all a refused caller learns: which check failed is the
// server log's business, not a probe's.
var errAccessRequired = errors.New("cloudflare access required")

// accessWarnEvery spaces audit-mode warnings so a misconfigured edge cannot
// flood the log with one line per request.
const accessWarnEvery = time.Minute

// AccessMiddleware puts Cloudflare Access in front of every route.
//
// Required, a request without a valid Access token is refused with 403, so
// Nodevas stays closed even to a caller that reaches it around the tunnel.
// Not required (audit mode), a bad or missing token is only logged, which is
// how a deployment checks its Access setup before enforcing it.
//
// A request straight from this machine is exempt: the container healthcheck
// and a local `nodevas mcp` never pass through Access. A proxy on the same
// host adds forwarding headers, and its requests are checked like any other.
//
// A verified identity is recorded on the request for the audit trail. It
// signs nobody in: Nodevas sign-in stays its own second layer.
func AccessMiddleware(verifier *auth.AccessVerifier, required bool) gin.HandlerFunc {
	if verifier == nil {
		return func(c *gin.Context) { c.Next() }
	}
	var (
		mu       sync.Mutex
		lastWarn time.Time
	)
	warn := func(r *http.Request, err error) {
		mu.Lock()
		if time.Since(lastWarn) < accessWarnEvery {
			mu.Unlock()
			return
		}
		lastWarn = time.Now()
		mu.Unlock()
		slog.Warn("cloudflare access check failed",
			slog.String("event.code", "access.invalid"),
			slog.Bool("access.required", required),
			slog.String("url.path", r.URL.Path),
			slog.String("client.ip", auth.ClientIP(r)),
			slog.String("error", err.Error()),
		)
	}
	return func(c *gin.Context) {
		r := c.Request
		if auth.DirectLoopback(r) {
			c.Next()
			return
		}
		claims, err := verifier.VerifyRequest(r)
		if err != nil {
			warn(r, err)
			if required {
				httpx.Err(c, http.StatusForbidden, errAccessRequired)
				return
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
		c.Next()
	}
}
