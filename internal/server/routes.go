package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	agentsapi "nodevas/internal/httpapi/agents"
	autopilotapi "nodevas/internal/httpapi/autopilot"
	controlapi "nodevas/internal/httpapi/control"
	"nodevas/internal/httpapi/fsbrowse"
	"nodevas/internal/httpapi/graph"
	membersapi "nodevas/internal/httpapi/members"
	"nodevas/internal/httpapi/node"
	"nodevas/internal/httpapi/project"
	"nodevas/internal/httpapi/remoteapi"
	runnersapi "nodevas/internal/httpapi/runners"
	runsapi "nodevas/internal/httpapi/runs"
	stationapi "nodevas/internal/httpapi/station"
	"nodevas/internal/httpapi/system"
	usageapi "nodevas/internal/httpapi/usage"
	"nodevas/internal/usage"
)

// This file is the whole HTTP surface in one place. Each handler package owns
// its own URLs and registers them here, so a package can be read on its own
// and the router stays a list of who serves what.

// Handler builds the router and the middleware every request passes through.
// Call it after UseAccounts and UseListenAddress.
func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// No implicit redirect for a trailing slash or a "fixable" path: a URL
	// either names a route or it does not. Request logging stays explicit, but
	// recovery is mandatory: malformed user input must never crash the process.
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.Use(recoverPanics(), s.secureHTTP, AccessMiddleware(s.accessVerifier, s.accessRequired), s.preAuthAbuseLimits, s.withAuth, s.withAbuseLimits, s.withRequestProject, s.withProjectAccess, s.withRoleGuard, s.withAuditTrail, s.withScorecardInvalidation)

	api := router.Group("/api")
	graph.New(s.pm, s.hub).Register(api)
	node.New(s.pm, s.hub).Register(api)
	// With accounts, each one has its own current project; it is remembered
	// in memory when there is no database to keep it in.
	if s.users != nil && s.current == nil {
		s.current = newCurrentProjects(nil)
	}
	var currentName func(*http.Request) string
	projectAPI := project.New(s.pm)
	if s.perAccountProjects() {
		hooks := currentProjectHooks{s}
		projectAPI.UseCurrent(hooks)
		currentName = func(r *http.Request) string {
			name, _ := hooks.Current(r)
			return name
		}
	}
	if s.members != nil {
		projectAPI.UseMembers(projectAccess{s}, membershipHooks{s.members})
		var find membersapi.FindAccount
		if s.users != nil {
			find = s.users.FindAccount
		}
		membersapi.New(s.pm, s.members, find).UseCurrent(currentName).Register(api)
	}
	projectAPI.Register(api)
	fsbrowse.New(s.auth, s.openDir).Register(api)
	remoteapi.New(s.pm, s.remotes).UseCurrent(currentName).Register(api)
	if s.agents != nil {
		agentsAPI := agentsapi.New(s.pm, s.agents)
		agentsAPI.UseRunCheck(func(r *http.Request) bool {
			_, proxied := runnerProxiedFrom(r.Context())
			return proxied
		})
		agentsAPI.UseRunOf(func(r *http.Request) string { return proxiedRunIDFrom(r.Context()) })
		if s.runs != nil {
			agentsAPI.UseRunUsage(s.runs.Store().SetUsageFromSession)
		}
		if s.usage != nil {
			agentsAPI.UseUsage(s.usage, func(projectKey string) {
				if s.hub == nil {
					return
				}
				if projectKey == "" {
					s.hub.Broadcast(usage.ChangedEvent, "")
					return
				}
				s.hub.BroadcastRoom(projectKey, usage.ChangedEvent, "")
			})
		}
		agentsAPI.Register(api)
	}
	if s.usage != nil {
		usageapi.New(s.pm, s.usage).Register(api)
	}
	if s.runners != nil {
		runnersapi.New(s.runners, s.runs).WithLLM(s.llm).WithRepo(s.repo).WithLinks(s.runnerLinkOptions()).Register(api)
	}
	if s.runs != nil {
		runsAPI := runsapi.New(s.pm, s.runners, s.runs)
		s.configureStepUp(runsAPI)
		runsAPI.UseAutoRoute(s.autoRouteRequest)
		if s.agents != nil {
			runsAPI.UseAsks(s.agents)
		}
		runsAPI.Register(api)
		// Agent analytics (pure SQL and rules) and the Explorer.
		s.analyticsAPI().Register(api)
		s.explorerAPI().Register(api)
		if s.autopilot != nil {
			autopilotAPI := autopilotapi.New(s.pm, s.autopilot)
			s.configureAutopilotStepUp(autopilotAPI)
			autopilotAPI.Register(api)
		}
		// Station's control center: pause everything the viewer may.
		controlapi.New(s.pm, s.runs, s.autopilot).Register(api)
	} else {
		s.configureStepUp(nil)
	}
	if s.pm != nil {
		// Station's building: every readable project at once. It checks each
		// project itself, so it is exempt from the per-request project gate,
		// and it is not on the runner proxy's allowlist.
		stationapi.New(s.pm, stationapi.NewSources(s.pm, s.runs, s.autopilot, s.agents), s.projectReadable).Register(api)
	}
	sys := system.New(s.pm, s.notifier, s.auth)
	if s.mailer != nil {
		sys.UseMailer(s.mailer)
	}
	if s.audit != nil {
		sys.UseAudit(s.audit)
	}
	if s.users != nil {
		sys.UseUsers(s.users)
	}
	if s.abuse != nil {
		sys.UseLimiter(func() any {
			return s.abuse.Stats()
		})
	}
	sys.Register(api)
	s.registerHealth(api)
	if s.copilot != nil {
		s.copilot.Register(api)
	}

	router.GET("/ws", s.hub.Handle)
	// Unmatched: JSON 404 under /api/, the SPA everywhere else.
	router.NoRoute(s.notFound)

	// Kept so the copilot's tool bridge can serve its calls through this same
	// router, middleware and all, without a socket.
	s.router.Store(&builtRouter{handler: router})
	return router
}
