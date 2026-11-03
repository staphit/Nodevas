package explorer

import "github.com/gin-gonic/gin"

// Register wires /api/explorer/*. Every route is project-scoped (the
// project gate applies) and a person's only.
func (a *API) Register(api *gin.RouterGroup) {
	g := api.Group("/explorer")
	g.GET("/workspace/files", a.workspaceFiles)
	g.GET("/workspace/file", a.workspaceFile)
	g.GET("/workspace/git", a.workspaceGit)
	g.GET("/workspace/git/commit", a.workspaceCommit)
	g.GET("/repos", a.repos)
	g.GET("/repos/:runner/:repo/tree", a.repoTree)
	g.GET("/repos/:runner/:repo/file", a.repoFile)
	g.GET("/repos/:runner/:repo/git/status", a.repoStatus)
	g.GET("/repos/:runner/:repo/git/log", a.repoLog)
	g.GET("/repos/:runner/:repo/git/diff", a.repoDiff)
	g.GET("/agents", a.agentsList)
	g.GET("/sessions/:id/actions", a.sessionActions)
}
