package remoteapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/remote"
)

// The git backend's endpoints (internal/remote/git.go): back up one project's
// branch, test the connection, and keep local history on a side branch.

// gitProjectName is ?project=, or the caller's current project, or the
// active one.
func (a *API) gitProjectName(c *gin.Context) (string, bool) {
	name := strings.TrimSpace(c.Request.URL.Query().Get("project"))
	if name == "" && a.current != nil {
		name = a.current(c.Request)
	}
	if name == "" {
		active, err := a.pm.ActiveProject()
		if err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return "", false
		}
		name = active.Name
	}
	return name, true
}

func gitErrorStatus(err error) int {
	switch {
	case errors.Is(err, remote.ErrSyncConflict):
		return http.StatusConflict
	case errors.Is(err, remote.ErrNoGitHistory):
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

// postGitPush is "back up now" for one project on the git backend. The reply
// keeps the bundle shape the folder backend's push has, so callers that only
// show a name work unchanged.
func (a *API) postGitPush(c *gin.Context) {
	name, ok := a.gitProjectName(c)
	if !ok {
		return
	}
	status, err := a.remotes.PushGitProject(c.Request.Context(), name)
	if err != nil {
		httpx.Err(c, gitErrorStatus(err), err)
		return
	}
	ref := remote.RemoteRef{ID: "refs/heads/" + status.Branch, Name: status.Branch}
	if status.LastPush != nil {
		ref.Modified = status.LastPush.At
	}
	c.JSON(http.StatusCreated, map[string]any{"backend": "git", "bundle": ref, "project": status})
}

// postGitTest runs ls-remote against the saved remote with the saved token.
// The form saves before it tests, as the mail settings do, so what is tested
// is what the backups will use.
func (a *API) postGitTest(c *gin.Context) {
	count, err := a.remotes.TestGitConnection(c.Request.Context())
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "branches": count})
}

// postGitKeepLocal pushes a diverged project's history to a new side
// branch, leaving its remote branch as it is.
func (a *API) postGitKeepLocal(c *gin.Context) {
	name, ok := a.gitProjectName(c)
	if !ok {
		return
	}
	branch, err := a.remotes.KeepLocalGit(c.Request.Context(), name)
	if err != nil {
		httpx.Err(c, gitErrorStatus(err), err)
		return
	}
	c.JSON(http.StatusCreated, map[string]any{"branch": branch})
}
