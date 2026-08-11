package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"nodevas/internal/identity"
	"strings"
	"sync"
)

// Authenticator identifies the actor behind a request.
//
// It exists so that turning on real accounts is one implementation swap
// instead of a check added to each handler: the second form always misses one.
type Authenticator interface {
	// Authenticate returns the actor for this request. ErrUnauthenticated
	// means the request must be refused.
	Authenticate(r *http.Request) (identity.Actor, error)
	// NeedsCSRF reports whether unsafe methods must carry a CSRF token. A
	// loopback server with no credentials has nothing to forge against.
	NeedsCSRF() bool
	// Remote reports whether the server is reachable from other machines.
	// Endpoints that expose the host filesystem are refused when it is.
	Remote() bool
}

var (
	// ErrUnauthenticated is returned when a request carries no valid identity.
	ErrUnauthenticated = errors.New("authentication required")
	// ErrCSRF is returned when an unsafe request has no matching CSRF token.
	ErrCSRF = errors.New("missing or invalid CSRF token")
	// ErrAdminRequired is returned when a member attempts a server-wide
	// administrative operation.
	ErrAdminRequired = errors.New("administrator access required")
)

// LocalOnly is the authenticator for a server bound to loopback: the OS
// already decided who may connect, so every request is the local user.
type LocalOnly struct{}

func (LocalOnly) Authenticate(*http.Request) (identity.Actor, error) { return identity.Local, nil }
func (LocalOnly) NeedsCSRF() bool                                    { return false }
func (LocalOnly) Remote() bool                                       { return false }

type actorKey struct{}
type revalidatorKey struct{}

// ActorFrom returns the actor the auth middleware attached to the request.
func ActorFrom(r *http.Request) identity.Actor {
	if r != nil {
		if actor, ok := r.Context().Value(actorKey{}).(identity.Actor); ok {
			return actor
		}
	}
	return identity.Local
}

// RequireAdmin verifies that authentication middleware attached an
// administrator to the request. HTTP packages can use this after their normal
// authentication check without depending on the concrete authenticator.
func RequireAdmin(r *http.Request) error {
	if r == nil {
		return ErrAdminRequired
	}
	actor, attached := r.Context().Value(actorKey{}).(identity.Actor)
	if !attached || !actor.IsAdmin() {
		return ErrAdminRequired
	}
	return nil
}

// PublicPaths are the endpoints reachable without an identity: they are
// how a browser gets one.
var PublicPaths = map[string]bool{
	"/api/auth/login":  true,
	"/api/auth/status": true,
	// Asking for a one-time passcode is half of getting an identity, so it
	// cannot be behind having one. Its own throttles are what keep it from
	// being abused; see otp.go.
	"/api/auth/otp/request": true,
	// The Drive OAuth callback is a cross-site redirect from Google; the
	// SameSite=Strict Session cookie is not sent on it, so the request cannot
	// carry an identity. The single-use state token authenticates it instead.
	"/api/remote/drive/callback": true,
}

const (
	CSRFCookieName = "nodevas_csrf"
	CSRFHeaderName = "X-CSRF-Token"
)

// CheckCSRF does the double-submit check: the cookie is sent automatically by
// the browser, the header can only be set by same-origin script.
func CheckCSRF(r *http.Request) error {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return ErrCSRF
	}
	header := r.Header.Get(CSRFHeaderName)
	if header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
		return ErrCSRF
	}
	return nil
}

// RandomToken returns a URL-safe token with 256 bits of entropy.
func RandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Proxy-derived headers are trusted only when the immediate TCP peer belongs
// to an explicitly configured proxy network. A process-wide configuration is
// appropriate because one nodevas process owns one listener.
var trustedProxies struct {
	sync.RWMutex
	prefixes []netip.Prefix
}

var loopbackProxyPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

// TrustForwardedProto preserves the original toggle for embedders. Enabling
// it now trusts only loopback proxies; network proxies must be configured with
// SetTrustedProxyCIDRs.
func TrustForwardedProto(trust bool) {
	trustedProxies.Lock()
	defer trustedProxies.Unlock()
	trustedProxies.prefixes = nil
	if trust {
		trustedProxies.prefixes = append([]netip.Prefix(nil), loopbackProxyPrefixes...)
	}
}

// SetTrustedProxyCIDRs replaces the networks allowed to supply forwarding
// headers. Empty input disables proxy-header trust.
func SetTrustedProxyCIDRs(values []string) error {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			if address, addressErr := netip.ParseAddr(value); addressErr == nil {
				prefix = netip.PrefixFrom(address, address.BitLen())
			} else {
				return errors.New("invalid trusted proxy address " + value)
			}
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	trustedProxies.Lock()
	trustedProxies.prefixes = prefixes
	trustedProxies.Unlock()
	return nil
}

func remoteAddress(r *http.Request) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	value := strings.TrimSpace(r.RemoteAddr)
	if addressPort, err := netip.ParseAddrPort(value); err == nil {
		return addressPort.Addr().Unmap(), true
	}
	address, err := netip.ParseAddr(strings.Trim(value, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func isTrustedProxy(address netip.Addr) bool {
	trustedProxies.RLock()
	defer trustedProxies.RUnlock()
	for _, prefix := range trustedProxies.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// ClientIP returns the security identity of a request source. It walks a
// trusted X-Forwarded-For chain from right to left and stops at the first
// untrusted address, preventing clients from choosing their limiter bucket.
func ClientIP(r *http.Request) string {
	peer, ok := remoteAddress(r)
	if !ok {
		return "unknown"
	}
	client := peer
	if !isTrustedProxy(peer) {
		return client.String()
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0; index-- {
		value := strings.TrimSpace(forwarded[index])
		if value == "" {
			return peer.String()
		}
		candidate, err := netip.ParseAddr(value)
		if err != nil {
			// A trusted edge must overwrite, not append arbitrary malformed
			// values. Collapse to the proxy bucket instead of letting the client
			// choose a limiter identity from the remaining list.
			return peer.String()
		}
		client = candidate.Unmap()
		if !isTrustedProxy(client) {
			break
		}
	}
	return client.String()
}

// RequestIsSecure reports whether the browser reached this server over TLS,
// including through a terminating reverse proxy when one is trusted. Session
// cookies are marked Secure only then: a Secure cookie on plain HTTP is
// silently dropped, which would lock out a loopback user.
func RequestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer, ok := remoteAddress(r)
	if !ok || !isTrustedProxy(peer) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if strings.Contains(proto, ",") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

var (
	// ErrBadCredentials is deliberately the same for an unknown user and a
	// wrong password: the difference is not the caller's business.
	ErrBadCredentials = errors.New("invalid user name or password")
	// ErrTooManyLogins is returned while an account is throttled.
	ErrTooManyLogins = errors.New("too many failed sign-ins, try again later")
)

// WithActor returns a context carrying the actor, which ActorFrom reads back.
// The key stays unexported so nothing outside this package can forge one.
func WithActor(ctx context.Context, actor identity.Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// WithRevalidator attaches a live session check for long-lived transports.
func WithRevalidator(ctx context.Context, validate func() bool) context.Context {
	return context.WithValue(ctx, revalidatorKey{}, validate)
}

// StillAuthorized revalidates a long-lived request when authentication
// middleware supplied a checker. Ordinary request contexts remain valid.
func StillAuthorized(r *http.Request) bool {
	if r == nil {
		return false
	}
	validate, ok := r.Context().Value(revalidatorKey{}).(func() bool)
	return !ok || validate()
}
