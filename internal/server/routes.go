package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	agentsapi "nodevas/internal/httpapi/agents"
	autopilotapi "nodevas/internal/httpapi/autopilot"
	"nodevas/internal/httpapi/fsbrowse"
	"nodevas/internal/httpapi/graph"
	membersapi "nodevas/internal/httpapi/members"
	"nodevas/internal/httpapi/node"
	"nodevas/internal/httpapi/project"
	"nodevas/internal/httpapi/remoteapi"
	runnersapi "nodevas/internal/httpapi/runners"
	runsapi "nodevas/internal/httpapi/runs"
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
	router.Use(gin.Recovery(), s.secureHTTP, AccessMiddleware(s.accessVerifier, s.accessRequired), s.preAuthAbuseLimits, s.withAuth, s.withAbuseLimits, s.withRequestProject, s.withProjectAccess, s.withAuditTrail)

	api := router.Group("/api")
	graph.New(s.pm, s.hub).Register(api)
	node.New(s.pm, s.hub).Register(api)
	projectAPI := project.New(s.pm)
	if s.members != nil {
		projectAPI.UseMembers(projectAccess{s}, membershipHooks{s.members})
		var find membersapi.FindAccount
		if s.users != nil {
			find = s.users.FindAccount
		}
		membersapi.New(s.pm, s.members, find).Register(api)
	}
	projectAPI.Register(api)
	fsbrowse.New(s.auth, s.openDir).Register(api)
	remoteapi.New(s.pm, s.remotes).Register(api)
	if s.agents != nil {
		agentsAPI := agentsapi.New(s.pm, s.agents)
		agentsAPI.UseRunCheck(func(r *http.Request) bool {
			_, proxied := runnerProxiedFrom(r.Context())
			return proxied
		})
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
		runnersapi.New(s.runners, s.runs).WithLLM(s.llm).WithLinks(s.runnerLinkOptions()).Register(api)
	}
	if s.runs != nil {
		runsAPI := runsapi.New(s.pm, s.runners, s.runs)
		s.configureStepUp(runsAPI)
		runsAPI.Register(api)
		if s.autopilot != nil {
			autopilotAPI := autopilotapi.New(s.pm, s.autopilot)
			s.configureAutopilotStepUp(autopilotAPI)
			autopilotAPI.Register(api)
		}
	} else {
		s.configureStepUp(nil)
	}
	sys := system.New(s.pm, s.notifier, s.auth)
	if s.mailer != nil {
		sys.UseMailer(s.mailer)
	}
	if s.audit != nil {
		sys.UseAudit(s.audit)
	}
	if s.abuse != nil {
		sys.UseLimiter(func() any {
			return s.abuse.Stats()
		})
	}
	sys.Register(api)
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
