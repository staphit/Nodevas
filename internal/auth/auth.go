package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"strings"
	"sync"

	"nodevas/internal/locale"
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
	ErrUnauthenticated = locale.Errorf("authentication required", "需要登入")
	// ErrCSRF is returned when an unsafe request has no matching CSRF token.
	ErrCSRF = locale.Errorf("missing or invalid CSRF token", "缺少 CSRF 權杖或權杖無效")
	// ErrAdminRequired is returned when a member attempts a server-wide
	// administrative operation.
	ErrAdminRequired = locale.Errorf("administrator access required", "需要管理員權限")
	// ErrUnknownAgentRole is returned when a request declares an agent role
	// this server does not know. It surfaces as a 401 via withAuth, so the
	// message must tell the client what to send instead.
	ErrUnknownAgentRole = locale.Errorf(
		"unknown %s: send \"worker\" or \"orchestrator\", or omit the header for a human session",
		"不明的 %s：請送出 \"worker\" 或 \"orchestrator\"，人的工作階段則不要帶這個標頭", AgentRoleHeaderName)
)

// AgentRoleHeaderName declares which class of agent a request acts as. It is
// honored only by LocalOnly: on loopback the OS account is the trust boundary,
// so a header the local user's own tools set is as trustworthy as the request.
const AgentRoleHeaderName = "X-Nodevas-Agent-Role"

// RoleHeaderName declares the agent role (engine roles: planner, qa, tester,
// executor, reviewer, researcher, custom-role-*) an external loopback session
// acts in. Like AgentRoleHeaderName it is honored only by LocalOnly; a role
// never raises the declared class.
const RoleHeaderName = "X-Nodevas-Role"

// ErrInvalidRole is returned for a RoleHeaderName value that is not a
// well-formed role id.
var ErrInvalidRole = locale.Errorf("invalid %s: send a built-in role or custom-role-<id>",
	"%s 無效：請送出內建角色或 custom-role-<id>", RoleHeaderName)

// ErrRelayedRequest refuses, on a server without accounts, a request a proxy
// on this machine relayed for somebody else.
var ErrRelayedRequest = locale.Errorf(
	"this server has no accounts and answers only this machine; it was reached through a proxy for another one (serve it with accounts to share it)",
	"這個伺服器沒有帳號，只回應本機；這個請求是經由代理從別台機器來的（要分享請啟用帳號）")

// LocalOnly is the authenticator for a server bound to loopback: the OS
// already decided who may connect, so every request is the local user.
//
// That holds only for a request this machine sent. A tunnel, a reverse proxy
// or the Vite dev server opened to the LAN connects from loopback too, and
// says in its forwarding headers whom it relays; a request whose headers
// name anybody but this machine is refused (ForwardedForThisMachine).
type LocalOnly struct{}

func (LocalOnly) Authenticate(r *http.Request) (identity.Actor, error) {
	if r == nil {
		return identity.Local, nil
	}
	if !ForwardedForThisMachine(r) {
		return identity.Actor{}, ErrRelayedRequest
	}
	class := identity.AgentClass(r.Header.Get(AgentRoleHeaderName))
	switch class {
	case identity.AgentHuman, identity.AgentWorker, identity.AgentOrchestrator:
	default:
		return identity.Actor{}, ErrUnknownAgentRole
	}
	agentRole, present := r.Header[http.CanonicalHeaderKey(RoleHeaderName)]
	if !present {
		if class == identity.AgentHuman {
			return identity.Local, nil
		}
		actor := identity.Local
		actor.Agent = class
		return actor, nil
	}
	// An external session in a role: the role is syntax-checked here and
	// resolved against the project by the role guard (UNKNOWN_ROLE there).
	// A role alone acts as a worker, and a built-in role's rank can only
	// lower the declared class.
	if len(agentRole) != 1 || !engine.ValidRoleID(agentRole[0]) {
		return identity.Actor{}, ErrInvalidRole
	}
	if class == identity.AgentHuman {
		class = identity.AgentWorker
	}
	if rank := engine.RoleClass(nil, agentRole[0]); rank != "" {
		class = identity.LowerClass(class, rank)
	}
	actor := identity.Local
	actor.Agent = class
	actor.AgentRole = agentRole[0]
	return actor, nil
}
func (LocalOnly) NeedsCSRF() bool { return false }
func (LocalOnly) Remote() bool    { return false }

type actorKey struct{}
type revalidatorKey struct{}

// ActorFrom returns the actor the auth middleware attached to the request.
func ActorFrom(r *http.Request) identity.Actor {
	if actor, ok := AttachedActor(r); ok {
		return actor
	}
	return identity.Local
}

// AttachedActor is the actor the auth middleware attached, and false when it
// attached none: a public route (sign-in, a device-link redeem) has nobody
// signed in, and must not be taken for the local administrator that
// ActorFrom falls back to.
func AttachedActor(r *http.Request) (identity.Actor, bool) {
	if r == nil {
		return identity.Actor{}, false
	}
	actor, ok := r.Context().Value(actorKey{}).(identity.Actor)
	return actor, ok
}

// RequireAdmin verifies that authentication middleware attached an
// administrator to the request, and that a person sent it. Server-wide
// settings are a person's to change: an agent acting for an administrator,
// through the copilot or a runner, is refused, as account administration
// and the runner access token already refuse it. HTTP packages can use this
// after their normal authentication check without depending on the concrete
// authenticator.
func RequireAdmin(r *http.Request) error {
	actor, attached := AttachedActor(r)
	if !attached || !actor.IsAdmin() || actor.IsAgent() {
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
	// The second step of a two-factor sign-in: the browser has a pending
	// sign-in ticket (an HttpOnly cookie), not an identity yet.
	"/api/auth/login/totp": true,
}

// SignInPaths are the public endpoints that accept a credential from a
// stranger. They share one body cap and one throttle class, so a new door
// cannot quietly arrive with weaker treatment than the old ones.
var SignInPaths = map[string]bool{
	"/api/auth/login":       true,
	"/api/auth/otp/request": true,
	"/api/auth/login/totp":  true,
}

const (
	CSRFCookieName = "nodevas_csrf"
	CSRFHeaderName = "X-CSRF-Token"

	// hostCookiePrefix names a cookie the browser accepts only Secure, for
	// Path=/ and without a Domain, so a sibling subdomain cannot set one.
	hostCookiePrefix = "__Host-"
)

// SessionCookieNameFor is the session cookie's name in a response to r:
// __Host- prefixed when the browser reached the server over TLS, the plain
// name otherwise (a browser refuses a __Host- cookie on plain HTTP).
func SessionCookieNameFor(r *http.Request) string {
	return cookieNameFor(r, SessionCookieName)
}

// CSRFCookieNameFor is the CSRF cookie's name in a response to r, chosen as
// SessionCookieNameFor chooses.
func CSRFCookieNameFor(r *http.Request) string {
	return cookieNameFor(r, CSRFCookieName)
}

func cookieNameFor(r *http.Request, name string) string {
	if r != nil && RequestIsSecure(r) {
		return hostCookiePrefix + name
	}
	return name
}

// SessionToken is the request's session cookie, and false when it carries
// none, or more than one: a sibling subdomain can set a cookie of the plain
// name for the parent domain, which the browser sends beside, or before,
// the server's own, and serving either would put the person in an account
// that is not theirs. Over TLS only the __Host- cookie counts.
func SessionToken(r *http.Request) (string, bool) {
	return cookieValue(r, SessionCookieName)
}

// cookieValue reads the one cookie called name: the __Host- prefixed one when
// it is there, and the plain one only on a request that is not over TLS. An
// in-process request replays a TLS browser's Cookie header without TLS of
// its own, hence a __Host- cookie is taken whatever the transport.
func cookieValue(r *http.Request, name string) (string, bool) {
	if r == nil {
		return "", false
	}
	if host := r.CookiesNamed(hostCookiePrefix + name); len(host) > 0 {
		if len(host) > 1 || host[0].Value == "" {
			return "", false
		}
		return host[0].Value, true
	}
	if RequestIsSecure(r) {
		return "", false
	}
	plain := r.CookiesNamed(name)
	if len(plain) != 1 || plain[0].Value == "" {
		return "", false
	}
	return plain[0].Value, true
}

// CheckCSRF does the double-submit check: the cookie is sent automatically by
// the browser, the header can only be set by same-origin script.
func CheckCSRF(r *http.Request) error {
	cookie, ok := cookieValue(r, CSRFCookieName)
	if !ok {
		return ErrCSRF
	}
	header := r.Header.Get(CSRFHeaderName)
	if header == "" || subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
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

// PeerIsTrustedProxy reports r's immediate peer and whether it is a proxy
// trusted to set forwarding headers. An unparseable peer is "" and untrusted.
func PeerIsTrustedProxy(r *http.Request) (peer string, trusted bool) {
	address, ok := remoteAddress(r)
	if !ok {
		return "", false
	}
	return address.String(), isTrustedProxy(address)
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

// SourceKey is the throttle identity of a client address. An IPv6 address
// stands for its /64: one host or one subscriber holds the whole of it, so
// stepping through it must not buy a fresh budget each time. An IPv4 address,
// or anything that is not an address, is returned as given.
func SourceKey(address string) string {
	address = strings.TrimSpace(address)
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	parsed = parsed.Unmap()
	if !parsed.Is6() {
		return parsed.String()
	}
	prefix, err := parsed.WithZone("").Prefix(64)
	if err != nil {
		return address
	}
	return prefix.String()
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
	ErrBadCredentials = locale.Errorf("invalid user name or password", "使用者名稱或密碼錯誤")
	// ErrTooManyLogins is returned while an account is throttled.
	ErrTooManyLogins = locale.Errorf("too many failed sign-ins, try again later", "登入失敗次數太多，請稍後再試")
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
