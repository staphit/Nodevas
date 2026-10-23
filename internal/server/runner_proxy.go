// The runner's MCP proxy: an agent running on a runner reaches the board
// through /api/runner/proxy/<rest>, authenticated by the runner's token, and
// is served /api/<rest> as the runner's owner marked as an agent — for the
// routes the MCP sidecar uses and nothing else.

package server

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// proxyRoute is one method and path pattern the proxy forwards. "*" in a
// pattern stands for exactly one non-empty path segment.
type proxyRoute struct {
	method, pattern string
}

// runnerProxyRoutes is the allowlist: what `nodevas mcp` (internal/mcp)
// actually calls, and only that. TestRunnerProxyAllowsEveryMCPRoute reads the
// MCP client's source and fails when it starts using a route missing here;
// TestRunnerProxyRefusesEverythingElse pins what must never be on it.
//
// A runner token is a credential sitting on a laptop. What it opens is the
// board an agent works on — read, claim, report, talk — not the runners or
// runs that would let it dispatch more work, not accounts, not settings.
var runnerProxyRoutes = []proxyRoute{
	{http.MethodGet, "/api/auth/status"},
	{http.MethodGet, "/api/projects"},
	{http.MethodGet, "/api/ready"},
	{http.MethodGet, "/api/graph"},
	{http.MethodGet, "/api/documents"},
	{http.MethodGet, "/api/search"},
	{http.MethodGet, "/api/validate"},
	{http.MethodGet, "/api/nodes/*"},
	{http.MethodGet, "/api/nodes/*/context"},
	{http.MethodPost, "/api/nodes"},
	{http.MethodPut, "/api/nodes/*"},
	{http.MethodPost, "/api/nodes/*/claim"},
	{http.MethodPost, "/api/nodes/*/release"},
	{http.MethodPost, "/api/nodes/*/status"},
	{http.MethodPost, "/api/graph/ops"},
	{http.MethodGet, "/api/agents"},
	{http.MethodPost, "/api/agents/heartbeat"},
	{http.MethodPost, "/api/agents/leave"},
	{http.MethodPost, "/api/agents/messages"},
	// save_attachment; what a held message carries stays out of reach of an
	// agent until a person approves it (agents.Viewer.mayRead).
	{http.MethodGet, "/api/agents/messages/*/files/*"},
	{http.MethodPost, "/api/agents/inbox"},
	{http.MethodPost, "/api/agents/asks"},
	// Deliberately absent: holding a session's inbound mail and approving or
	// rejecting held messages are a person's decisions about agents.
}

// runnerProxyAllows reports whether the proxy forwards method on target, an
// /api/ path. A path that is not already clean (dot segments, doubled or
// trailing slashes) is refused rather than normalised: the router must see
// exactly the path that was checked.
func runnerProxyAllows(method, target string) bool {
	if target != path.Clean(target) || !strings.HasPrefix(target, "/api/") {
		return false
	}
	segments := strings.Split(target, "/")
	for _, route := range runnerProxyRoutes {
		if route.method == method && matchSegments(strings.Split(route.pattern, "/"), segments) {
			return true
		}
	}
	return false
}

func matchSegments(pattern, segments []string) bool {
	if len(pattern) != len(segments) {
		return false
	}
	for i, want := range pattern {
		if want == "*" {
			if segments[i] == "" {
				return false
			}
			continue
		}
		if want != segments[i] {
			return false
		}
	}
	return true
}

// runnerProxyKey marks a request the proxy re-dispatched. The key is
// unexported and the value is set only in serveRunnerProxy, after a runner
// token was checked; like auth.WithInProcessAgent, a context value cannot
// arrive from the network.
type runnerProxyKey struct{}

type runnerProxied struct {
	actor identity.Actor
	// still reports whether the runner's token is still good, for long-polls
	// that outlive a revoke.
	still func() bool
}

func runnerProxiedFrom(ctx context.Context) (runnerProxied, bool) {
	proxied, ok := ctx.Value(runnerProxyKey{}).(runnerProxied)
	return proxied, ok
}

// runnerProxyActor is who a proxied request acts as: the runner's owner as
// the server knows them now (their current role, not the one they had when
// the runner was made), marked as the agent class the request declares.
// Only worker and orchestrator exist for agents; nothing else is accepted.
func (s *Server) runnerProxyActor(r *http.Request, runner runnerwire.Runner) (identity.Actor, error) {
	class := identity.AgentClass(strings.TrimSpace(r.Header.Get(auth.AgentRoleHeaderName)))
	switch class {
	case identity.AgentHuman:
		class = identity.AgentWorker
	case identity.AgentWorker, identity.AgentOrchestrator:
	default:
		return identity.Actor{}, auth.ErrUnknownAgentRole
	}
	var actor identity.Actor
	switch accounts, ok := s.auth.(*auth.SessionAuth); {
	case ok:
		current, _, found := accounts.CurrentActor(r.Context(), runner.OwnerID)
		if !found {
			// The owner's account is gone; so is everything it lent out.
			return identity.Actor{}, runners.ErrUnauthenticated
		}
		actor = current
	case runner.OwnerID == identity.Local.ID:
		actor = identity.Local
	default:
		actor = identity.Actor{ID: runner.OwnerID, Name: runner.OwnerName}
	}
	actor.Agent = class
	return actor, nil
}

// serveRunnerProxy serves an /api/runner/proxy/ request that withRunnerAuth
// has authenticated: it checks the target against the allowlist, then runs
// the request again through the server's whole handler as /api/<rest>, so
// abuse limits, project scoping, node permissions and the audit trail apply
// exactly as they do to anyone else.
func (s *Server) serveRunnerProxy(c *gin.Context, runner runnerwire.Runner) {
	r := c.Request
	target := "/api/" + strings.TrimPrefix(r.URL.Path, runnerwire.ProxyPathPrefix)
	if !runnerProxyAllows(r.Method, target) {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			errors.New("this route is not available to agents on a runner"), nil)
		return
	}
	actor, err := s.runnerProxyActor(r, runner)
	if err != nil {
		httpx.Err(c, http.StatusUnauthorized, err)
		return
	}
	handler := s.BuiltHandler()
	if handler == nil {
		httpx.Err(c, http.StatusServiceUnavailable, errors.New("server is starting"))
		return
	}
	token := runners.BearerToken(r)
	proxied := runnerProxied{actor: actor, still: func() bool {
		_, err := s.runners.Authenticate(context.Background(), token)
		return err == nil
	}}
	inner := r.Clone(context.WithValue(r.Context(), runnerProxyKey{}, proxied))
	inner.URL.Path = target
	inner.URL.RawPath = ""
	inner.RequestURI = inner.URL.RequestURI()
	// The token did its job here; the inner request carries the marker
	// instead, and nothing past this point should see a credential.
	inner.Header.Del("Authorization")
	inner.Header.Del("Cookie")
	inner.Header.Del(auth.AgentRoleHeaderName)
	handler.ServeHTTP(c.Writer, inner)
	c.Abort()
}
