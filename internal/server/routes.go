package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/fsbrowse"
	"nodevas/internal/httpapi/graph"
	"nodevas/internal/httpapi/node"
	"nodevas/internal/httpapi/project"
	"nodevas/internal/httpapi/remoteapi"
	"nodevas/internal/httpapi/system"
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
	router.Use(gin.Recovery(), s.secureHTTP, s.withAuth, s.withVisitorReadOnly, s.withAbuseLimits, s.withRequestProject, s.withAuditTrail)

	api := router.Group("/api")
	graph.New(s.pm, s.hub).Register(api)
	node.New(s.pm, s.hub).Register(api)
	project.New(s.pm).Register(api)
	fsbrowse.New(s.auth, s.openDir).Register(api)
	remoteapi.New(s.pm, s.remotes, s.auth).Register(api)
	sys := system.New(s.pm, s.notifier, s.auth)
	if s.mailer != nil {
		sys.UseMailer(s.mailer)
	}
	if s.audit != nil {
		sys.UseAudit(s.audit)
	}
	sys.Register(api)

	router.GET("/ws", s.hub.Handle)
	// Unmatched: JSON 404 under /api/, the SPA everywhere else.
	router.NoRoute(s.notFound)

	return router
}
