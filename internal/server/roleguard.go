// The role guard: an agent acting in a role (identity.Actor.HasRole) may call
// only the routes its role's capabilities cover. Default deny. The table is
// derived from the runner proxy's route list (spec 2.3); the store then
// checks each write against the node it touches (store/roleperm.go).

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/store"
)

// roleCapOps is the capability POST /api/graph/ops needs: whichever its ops
// need, which the store checks one op at a time.
const roleCapOps = "ops"

// roleRoute is one route a role may call and the capability it needs.
type roleRoute struct {
	method, pattern, capability string
}

// roleRoutes is the runner proxy's allowlist (runnerProxyRoutes) with the
// capability each route needs. TestRoleGuardCoversEveryMCPRoute keeps the two
// in step and pins the table to testdata/role_routes.json.
var roleRoutes = []roleRoute{
	{http.MethodGet, "/api/auth/status", string(engine.CapRead)},
	{http.MethodGet, "/api/projects", string(engine.CapRead)},
	{http.MethodGet, "/api/ready", string(engine.CapRead)},
	{http.MethodGet, "/api/graph", string(engine.CapRead)},
	{http.MethodGet, "/api/documents", string(engine.CapRead)},
	{http.MethodGet, "/api/search", string(engine.CapRead)},
	{http.MethodGet, "/api/validate", string(engine.CapRead)},
	{http.MethodGet, "/api/nodes/*", string(engine.CapRead)},
	{http.MethodGet, "/api/nodes/*/context", string(engine.CapRead)},
	{http.MethodPost, "/api/nodes", string(engine.CapCreate)},
	{http.MethodPut, "/api/nodes/*", string(engine.CapBody)},
	{http.MethodPost, "/api/nodes/*/claim", string(engine.CapClaim)},
	{http.MethodPost, "/api/nodes/*/release", string(engine.CapClaim)},
	{http.MethodPost, "/api/nodes/*/status", string(engine.CapStatus)},
	{http.MethodPost, "/api/nodes/*/acceptance", string(engine.CapAcceptance)},
	{http.MethodPost, "/api/graph/ops", roleCapOps},
	{http.MethodPost, "/api/graph/subgraph", string(engine.CapCreate)},
	{http.MethodGet, "/api/agents", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/heartbeat", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/leave", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/messages", string(engine.CapMessage)},
	{http.MethodGet, "/api/agents/messages/*/files/*", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/inbox", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/asks", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/sessions/*/changes", string(engine.CapMessage)},
	{http.MethodPost, "/api/agents/sessions/*/usage", string(engine.CapMessage)},
	{http.MethodPost, "/api/verdicts", string(engine.CapVerdict)},
}

// roleRouteCapability is the capability (engine.Capability, or "ops" for
// POST /api/graph/ops, whose ops the store checks one by one) a role needs to
// call method on target. ok is false for a route no role may call. A path
// that is not already clean is refused, as the runner proxy refuses it.
func roleRouteCapability(method, target string) (capability string, ok bool) {
	if target != path.Clean(target) || !strings.HasPrefix(target, "/api/") {
		return "", false
	}
	segments := strings.Split(target, "/")
	for _, route := range roleRoutes {
		if route.method == method && matchSegments(strings.Split(route.pattern, "/"), segments) {
			return route.capability, true
		}
	}
	return "", false
}

// withRoleGuard refuses a roled agent a route its role does not cover, and
// a role the project does not define. It also lowers the actor's class to
// the role's rank (a custom role's rank is known only to the project), so a
// role never raises what the request declared. Legacy actors pass untouched.
func (s *Server) withRoleGuard(c *gin.Context) {
	r := c.Request
	actor := auth.ActorFrom(r)
	if !actor.HasRole() || !strings.HasPrefix(r.URL.Path, "/api/") {
		c.Next()
		return
	}
	capability, ok := roleRouteCapability(r.Method, r.URL.Path)
	if !ok {
		httpx.Coded(c, http.StatusForbidden, store.CodeRoleDenied,
			locale.Errorf("role %s may not call %s %s", "角色 %s 不能呼叫 %s %s", actor.AgentRole, r.Method, r.URL.Path), map[string]any{"role": actor.AgentRole})
		return
	}
	st := httpx.StoreFor(r, s.pm)
	// A run works its own project: the node it was dispatched on, and the
	// role it got, are that project's. Another project's node that happens
	// to share the id is not its node.
	// A run whose project no longer resolves cannot have its node anywhere:
	// it is refused, never waved through.
	if proxied, ok := runnerProxiedFrom(r.Context()); ok && proxied.runProject != "" && s.pm != nil &&
		httpx.ProjectScopedPath(r.URL.Path) {
		own, err := s.pm.StoreFor(proxied.runProject)
		if err != nil || st == nil {
			httpx.Coded(c, http.StatusForbidden, store.CodeRoleDenied,
				locale.Errorf("the run's project %q no longer exists", "這次執行的專案「%s」已不存在", proxied.runProject), map[string]any{"role": actor.AgentRole})
			return
		}
		if own.Root() != st.Root() {
			httpx.Coded(c, http.StatusForbidden, store.CodeRoleDenied,
				locale.Errorf("role %s works only in its run's project %q", "角色 %s 只能在其執行的專案「%s」中工作", actor.AgentRole, proxied.runProject), map[string]any{"role": actor.AgentRole})
			return
		}
	}
	var g *engine.Graph
	if st != nil {
		loaded, _, err := st.LoadGraph()
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
		g = loaded
	}
	role, found := g.RoleOf(actor.AgentRole)
	if !found {
		httpx.Coded(c, http.StatusForbidden, store.CodeUnknownRole,
			locale.Errorf("unknown agent role %q: this project does not define it", "不明的 agent 角色「%s」：這個專案沒有定義它", actor.AgentRole), map[string]any{"role": actor.AgentRole})
		return
	}
	// The executor's body writes (its scene or event) and graph ops are
	// decided by the store, which sees the node.
	bodyByStore := capability == string(engine.CapBody) && role.Slot() == engine.RoleExecutor
	if capability != roleCapOps && !bodyByStore && !role.HasCap(engine.Capability(capability)) && !runPermissionAsk(c) {
		httpx.Coded(c, http.StatusForbidden, store.CodeRoleDenied,
			locale.Errorf("role %s may not call %s %s (needs %s)", "角色 %s 不能呼叫 %s %s（需要 %s）", role.ID, r.Method, r.URL.Path, capability),
			map[string]any{"role": role.ID})
		return
	}
	if lowered := identity.LowerClass(actor.Agent, role.Rank); lowered != actor.Agent {
		actor.Agent = lowered
		c.Request = r.WithContext(auth.WithActor(r.Context(), actor))
	}
	c.Next()
}

// maxPeekedAskBody is the most of an ask's body runPermissionAsk reads: the
// asks API's own limit.
const maxPeekedAskBody = 64 << 10

// runPermissionAsk reports whether c is a run relaying one of its agent CLI's
// permission prompts: POST /api/agents/asks of kind permission, through its
// runner's proxy from one of that runner's runs. Any role may: the ask only
// puts the run's own tool call to a person and grants nothing by itself (the
// hub refuses aboutMsgId on it, so it forwards no verified approval),
// whereas refusing it denies every prompt without asking anyone. The body is
// put back exactly as it came, and its kind read as the asks API reads it.
func runPermissionAsk(c *gin.Context) bool {
	r := c.Request
	if r.Method != http.MethodPost || r.URL.Path != "/api/agents/asks" || r.Body == nil {
		return false
	}
	if proxied, ok := runnerProxiedFrom(r.Context()); !ok || proxied.runID == "" {
		return false
	}
	body := r.Body
	raw, err := io.ReadAll(io.LimitReader(body, maxPeekedAskBody+1))
	c.Request.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), body), body}
	if err != nil || len(raw) > maxPeekedAskBody {
		return false
	}
	var ask struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &ask) != nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(ask.Kind)) == agents.AskKindPermission
}

// errRunEnded refuses a request from a run that has finished.
var errRunEnded = locale.Errorf("this run has ended; its agent can no longer act on the board", "這次執行已結束；它的 agent 不能再操作看板")
