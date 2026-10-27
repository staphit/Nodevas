package remoteapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/remote"
	"strings"

	"github.com/gin-gonic/gin"
)

// Remote endpoints let a workspace back up project bundles to, and restore them
// from, off-box storage (plan.md P3). They stay off the write path: push builds
// the same .veproj archive the export button produces, and import goes through
// the same installArchive() the upload endpoint uses.

const maxRemoteBundleBytes = project.MaxProjectArchiveBytes

// getRemoteConfig returns the current backup configuration plus derived status.
// It never returns a host path to a remotely accessible client.
func (a *API) getRemoteConfig(c *gin.Context) {
	cfg := a.remotes.Load()
	folder := cfg.Folder
	if a.pm != nil && a.pm.IsRemote() {
		folder = ""
	}
	c.JSON(http.StatusOK, map[string]any{
		"kind":          cfg.Kind,
		"folder":        folder,
		"autoBackup":    cfg.AutoBackup,
		"intervalHours": cfg.IntervalHours,
		// The effective retention, not the raw stored number: the panel has to
		// tell the user what will actually happen, and the pruner clamps a
		// too-small setting up to its floor. A negative value is passed through
		// unchanged because it is a distinct choice ("never delete anything"),
		// and collapsing it to a count would make the panel hand back a number
		// that silently switches pruning on again.
		"retainBundles": effectiveRetain(cfg),
		"lastBackupAt":  a.remotes.LastBackupAt(),
		"gitUrl":        cfg.GitURL,
		"gitUser":       cfg.GitUser,
		// Whether a token is stored, never the token: as with the SMTP
		// password, the form only needs to say "saved".
		"hasGitToken": a.remotes.HasGitToken(cfg),
	})
}

// effectiveRetain is what the panel should display: the clamped retention
// count, or -1 when the operator has turned pruning off entirely.
func effectiveRetain(cfg remote.Config) int {
	if cfg.RetainBundles < 0 {
		return -1
	}
	return remote.RetainCount(cfg)
}

// putRemoteConfig sets the backup backend. A folder target is a host path, so
// it is refused once the server is reachable from the network — an account is
// permission to edit projects, not to write anywhere on the server's disk.
func (a *API) putRemoteConfig(c *gin.Context) {
	var body struct {
		Kind          string `json:"kind"`
		Folder        string `json:"folder"`
		AutoBackup    bool   `json:"autoBackup"`
		IntervalHours int    `json:"intervalHours"`
		RetainBundles int    `json:"retainBundles"`
		GitURL        string `json:"gitUrl"`
		GitUser       string `json:"gitUser"`
		// GitToken is write-only: empty keeps the stored token (when the URL
		// and user are unchanged), ClearGitToken forgets it.
		GitToken      string `json:"gitToken"`
		ClearGitToken bool   `json:"clearGitToken"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	cfg := remote.Config{
		Kind:          strings.TrimSpace(body.Kind),
		Folder:        strings.TrimSpace(body.Folder),
		AutoBackup:    body.AutoBackup,
		IntervalHours: body.IntervalHours,
		RetainBundles: body.RetainBundles,
	}
	token := remote.TokenChange{}
	switch cfg.Kind {
	case "":
		cfg = remote.Config{}
	case "folder":
		if cfg.Folder == "" {
			httpx.Err(c, http.StatusBadRequest, fmt.Errorf("folder is required"))
			return
		}
		if httpx.RefuseHostPath(c, a.pm) {
			return
		}
	case "git":
		// A URL and a token are not a host path, so a networked server may
		// use this backend; the address check is what keeps it from
		// reaching inside the network.
		cfg.Folder = ""
		cfg.GitURL = strings.TrimSpace(body.GitURL)
		cfg.GitUser = strings.TrimSpace(body.GitUser)
		token = remote.TokenChange{Set: strings.TrimSpace(body.GitToken), Clear: body.ClearGitToken}
		if err := a.remotes.CheckGitDestination(c.Request.Context(), cfg); err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
	default:
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("unknown remote backend %q", cfg.Kind))
		return
	}
	// Reject anything that cannot actually be built (bad folder path, etc.)
	// before it is saved.
	if _, err := a.remotes.BuildRemote(cfg); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if err := a.remotes.SaveWithToken(cfg, token); err != nil {
		if errors.Is(err, remote.ErrGitTokenRequired) {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.getRemoteConfig(c)
}

// getRemoteList returns the bundles the configured remote holds.
func (a *API) getRemoteList(c *gin.Context) {
	if a.remotes.Load().Kind == "git" {
		// Branches, not bundles: the per-project view is in the sync status.
		c.JSON(http.StatusOK, map[string]any{"backend": "git", "bundles": []remote.RemoteRef{}})
		return
	}
	backend, err := a.remotes.Store()
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if backend == nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("no remote is configured"))
		return
	}
	refs, err := backend.List(c.Request.Context())
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	if refs == nil {
		refs = []remote.RemoteRef{}
	}
	c.JSON(http.StatusOK, map[string]any{"backend": backend.Kind(), "bundles": refs})
}

// postRemotePush builds the export bundle for ?project= (the active project, a
// sub-project, or a whole folder subtree) and uploads it to the remote.
func (a *API) postRemotePush(c *gin.Context) {
	if a.remotes.Load().Kind == "git" {
		a.postGitPush(c)
		return
	}
	backend, err := a.remotes.Store()
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if backend == nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("no remote is configured"))
		return
	}
	name := strings.TrimSpace(c.Request.URL.Query().Get("project"))
	if name == "" && a.current != nil {
		name = a.current(c.Request)
	}
	scope, err := a.pm.ExportScope(name)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	ref, err := a.remotes.PushScope(c.Request.Context(), backend, scope)
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusCreated, map[string]any{"backend": backend.Kind(), "bundle": ref})
}

// postRemoteImport pulls a bundle by id and installs it as a new project. It
// reuses installArchive so the security-sensitive extraction has a single home.
func (a *API) postRemoteImport(c *gin.Context) {
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if a.remotes.Load().Kind == "git" {
		httpx.Err(c, http.StatusBadRequest, remote.ErrGitNoBundles)
		return
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("id is required"))
		return
	}
	backend, err := a.remotes.Store()
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if backend == nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("no remote is configured"))
		return
	}
	reader, err := backend.Pull(c.Request.Context(), id)
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxRemoteBundleBytes+1))
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	activeName, destination, err := a.pm.InstallArchive(data, body.Name, id)
	if err != nil {
		httpx.ErrWithStatus(c, err)
		return
	}
	c.JSON(http.StatusCreated, map[string]string{
		"active": activeName,
		"root":   a.visibleHostPath(destination),
	})
}

func (a *API) visibleHostPath(path string) string {
	if a.pm != nil && a.pm.IsRemote() {
		return ""
	}
	return path
}
