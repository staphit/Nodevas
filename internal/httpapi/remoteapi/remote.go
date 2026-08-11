package remoteapi

import (
	"fmt"
	"io"
	"net/http"
	"nodevas/internal/auth"
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
// It never returns a secret: OAuth credentials and tokens live encrypted in the
// app secrets directory.
func (a *API) getRemoteConfig(c *gin.Context) {
	cfg := a.remotes.Load()
	folder := cfg.Folder
	driveFolderID := cfg.DriveFolderID
	if a.pm != nil && a.pm.IsRemote() {
		folder = ""
		if a.remotes.DriveOwnedByAnother(auth.ActorFrom(c.Request).ID) {
			driveFolderID = ""
		}
	}
	driveAvailable, _ := a.remotes.DriveCredentialStatus()
	c.JSON(http.StatusOK, map[string]any{
		"kind":          cfg.Kind,
		"folder":        folder,
		"driveFolderId": driveFolderID,
		"autoBackup":    cfg.AutoBackup,
		"intervalHours": cfg.IntervalHours,
		// The effective retention, not the raw stored number: the panel has to
		// tell the user what will actually happen, and the pruner clamps a
		// too-small setting up to its floor. A negative value is passed through
		// unchanged because it is a distinct choice ("never delete anything"),
		// and collapsing it to a count would make the panel hand back a number
		// that silently switches pruning on again.
		"retainBundles":  effectiveRetain(cfg),
		"driveAvailable": driveAvailable,
		"driveConnected": a.remotes.DriveTokens().Connected(),
		"driveNeedsReauth": a.remotes.DriveTokens().Connected() &&
			!a.remotes.DriveTokens().BrowseReady(),
		"lastBackupAt": a.remotes.LastBackupAt(),
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

// requireDriveOwner prevents one administrator from using another
// administrator's personal Google account. Unbound tokens are intentionally
// unusable until an administrator reconnects them.
func (a *API) requireDriveOwner(c *gin.Context) {
	if a.ensureDriveOwner(c) {
		c.Next()
	}
}

func (a *API) ensureDriveOwner(c *gin.Context) bool {
	if a.pm == nil || !a.pm.IsRemote() {
		return true
	}
	tok, err := a.remotes.DriveTokens().Load()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return false
	}
	actorID := auth.ActorFrom(c.Request).ID
	if tok.RefreshToken == "" {
		return true
	}
	if tok.RefreshToken != "" && tok.OwnerID != "" && tok.OwnerID == actorID {
		return true
	}
	httpx.Err(c, http.StatusForbidden,
		fmt.Errorf("Google Drive is not connected by this account; reconnect it first"))
	return false
}

// requireConfiguredRemoteOwner applies Drive ownership to generic backup
// operations when the configured backend is Drive. Folder remotes are local
// desktop-only and have no personal cloud identity.
func (a *API) requireConfiguredRemoteOwner(c *gin.Context) {
	if a.remotes.Load().Kind == "drive" {
		a.requireDriveOwner(c)
		return
	}
	c.Next()
}

// putRemoteConfig sets the backup backend. A folder target is a host path, so
// it is refused once the server is reachable from the network — an account is
// permission to edit projects, not to write anywhere on the server's disk.
func (a *API) putRemoteConfig(c *gin.Context) {
	var body struct {
		Kind          string `json:"kind"`
		Folder        string `json:"folder"`
		DriveFolderID string `json:"driveFolderId"`
		AutoBackup    bool   `json:"autoBackup"`
		IntervalHours int    `json:"intervalHours"`
		RetainBundles int    `json:"retainBundles"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	cfg := remote.Config{
		Kind:          strings.TrimSpace(body.Kind),
		Folder:        strings.TrimSpace(body.Folder),
		DriveFolderID: strings.TrimSpace(body.DriveFolderID),
		AutoBackup:    body.AutoBackup,
		IntervalHours: body.IntervalHours,
		RetainBundles: body.RetainBundles,
	}
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
	case "drive":
		if _, err := a.remotes.DriveClientCreds(); err != nil {
			httpx.Err(c, http.StatusBadRequest, err)
			return
		}
	default:
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("unknown remote backend %q", cfg.Kind))
		return
	}
	if (a.remotes.Load().Kind == "drive" || cfg.Kind == "drive") && !a.ensureDriveOwner(c) {
		return
	}
	// Reject anything that cannot actually be built (bad folder path, etc.)
	// before it is saved.
	if _, err := a.remotes.BuildRemote(cfg); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if err := a.remotes.Save(cfg); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	a.getRemoteConfig(c)
}

// getRemoteList returns the bundles the configured remote holds.
func (a *API) getRemoteList(c *gin.Context) {
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
	backend, err := a.remotes.Store()
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if backend == nil {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("no remote is configured"))
		return
	}
	scope, err := a.pm.ExportScope(strings.TrimSpace(c.Request.URL.Query().Get("project")))
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

// getDriveFolders lists folders visible to the connected Drive account. It is
// a picker aid only: project bytes still travel through .veproj snapshots and
// the local workspace remains the single writer.
func (a *API) getDriveFolders(c *gin.Context) {
	if _, err := a.remotes.DriveClientCreds(); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if !a.remotes.DriveTokens().BrowseReady() {
		httpx.Err(c, http.StatusPreconditionFailed,
			fmt.Errorf("Google Drive 權限已變更，請重新連線以瀏覽資料夾"))
		return
	}
	parent := strings.TrimSpace(c.Request.URL.Query().Get("parent"))
	if parent == "" {
		parent = "root"
	}
	folders, err := a.remotes.ListDriveFolders(c.Request.Context(), parent)
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	if folders == nil {
		folders = []remote.DriveFolderRef{}
	}
	c.JSON(http.StatusOK, map[string]any{
		"parent":  parent,
		"folders": folders,
	})
}

// getDriveBundles lists Nodevas snapshots directly inside a selected Drive
// folder. It does not mutate the backup config while the picker is browsing.
func (a *API) getDriveBundles(c *gin.Context) {
	if _, err := a.remotes.DriveClientCreds(); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if !a.remotes.DriveTokens().BrowseReady() {
		httpx.Err(c, http.StatusPreconditionFailed,
			fmt.Errorf("Google Drive 權限已變更，請重新連線以瀏覽檔案"))
		return
	}
	parent := strings.TrimSpace(c.Request.URL.Query().Get("parent"))
	if parent == "" {
		parent = "root"
	}
	bundles, err := a.remotes.ListDriveBundles(c.Request.Context(), parent)
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	if bundles == nil {
		bundles = []remote.RemoteRef{}
	}
	c.JSON(http.StatusOK, map[string]any{"parent": parent, "bundles": bundles})
}

// postDriveConnect downloads one user-selected Nodevas bundle, installs it in
// the current local workspace, and records the selected Drive folder as the
// future backup target. The project editor remains local-file based; Drive is
// the authenticated snapshot source/sink rather than a fake POSIX disk.
func (a *API) postDriveConnect(c *gin.Context) {
	var body struct {
		FolderID string `json:"folderId"`
		BundleID string `json:"bundleId"`
		Name     string `json:"name"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	folderID := strings.TrimSpace(body.FolderID)
	if folderID == "" {
		folderID = "root"
	}
	bundleID := strings.TrimSpace(body.BundleID)
	if bundleID == "" {
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("bundleId is required"))
		return
	}
	if _, err := a.remotes.DriveClientCreds(); err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	if !a.remotes.DriveTokens().BrowseReady() {
		httpx.Err(c, http.StatusPreconditionFailed,
			fmt.Errorf("Google Drive 權限已變更，請重新連線後再匯入"))
		return
	}
	bundles, err := a.remotes.ListDriveBundles(c.Request.Context(), folderID)
	if err != nil {
		httpx.Err(c, http.StatusBadGateway, err)
		return
	}
	var selected remote.RemoteRef
	for _, bundle := range bundles {
		if bundle.ID == bundleID {
			selected = bundle
			break
		}
	}
	if selected.ID == "" {
		httpx.Err(c, http.StatusNotFound, fmt.Errorf("所選 Drive bundle 不在指定資料夾內"))
		return
	}
	backend, err := a.remotes.BuildRemote(remote.Config{
		Kind:          "drive",
		DriveFolderID: folderID,
	})
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	reader, err := backend.Pull(c.Request.Context(), selected.ID)
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
	activeName, destination, err := a.pm.InstallArchive(data, body.Name, selected.Name)
	if err != nil {
		httpx.ErrWithStatus(c, err)
		return
	}
	cfg := a.remotes.Load()
	cfg.Kind = "drive"
	cfg.Folder = ""
	cfg.DriveFolderID = ""
	if folderID != "root" {
		cfg.DriveFolderID = folderID
	}
	if err := a.remotes.Save(cfg); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, map[string]any{
		"active":   activeName,
		"root":     a.visibleHostPath(destination),
		"folderId": folderID,
		"bundle":   selected,
	})
}

func (a *API) visibleHostPath(path string) string {
	if a.pm != nil && a.pm.IsRemote() {
		return ""
	}
	return path
}

// actorDirectory is how the consent flow re-checks an account. Not every
// authenticator can answer — a loopback build has nothing to look up — and the
// domain treats a missing directory as a refusal where one matters.
func (a *API) actorDirectory() remote.ActorDirectory {
	directory, _ := a.auth.(remote.ActorDirectory)
	return directory
}

// driveAuthStatus is the HTTP spelling of a consent failure. The classes come
// from the domain; only their status codes are decided here.
func driveAuthStatus(err error) int {
	switch remote.DriveFaultOf(err) {
	case remote.DriveFaultRequest:
		return http.StatusBadRequest
	case remote.DriveFaultForbidden:
		return http.StatusForbidden
	case remote.DriveFaultConflict:
		return http.StatusConflict
	case remote.DriveFaultUpstream:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// getDriveAuth starts the OAuth consent flow by redirecting the browser to
// Google.
func (a *API) getDriveAuth(c *gin.Context) {
	consent, err := a.remotes.BeginDriveAuth(c.Request.Context(), remote.DriveAuthRequest{
		Actors:  a.actorDirectory(),
		ActorID: auth.ActorFrom(c.Request).ID,
		Source:  c.Request.URL.Query().Get("source"),
		Host:    c.Request.Host,
		Secure:  auth.RequestIsSecure(c.Request),
	})
	if err != nil {
		httpx.Err(c, driveAuthStatus(err), err)
		return
	}
	c.Redirect(http.StatusFound, consent)
}

// getDriveCallback finishes the flow. It is a public path because a cross-site
// redirect from Google does not carry the SameSite=Strict session cookie; the
// state token is what authenticates the exchange, so nothing here may read an
// identity off the request.
func (a *API) getDriveCallback(c *gin.Context) {
	query := c.Request.URL.Query()
	destination, err := a.remotes.CompleteDriveAuth(c.Request.Context(), remote.DriveCallbackRequest{
		Actors: a.actorDirectory(),
		Error:  query.Get("error"),
		Code:   query.Get("code"),
		State:  query.Get("state"),
		Source: query.Get("source"),
		Host:   c.Request.Host,
		Secure: auth.RequestIsSecure(c.Request),
	})
	if err != nil {
		httpx.Err(c, driveAuthStatus(err), err)
		return
	}
	c.Redirect(http.StatusFound, destination)
}

// postDriveDisconnect forgets the stored Drive token.
func (a *API) postDriveDisconnect(c *gin.Context) {
	if err := a.remotes.DisconnectDrive(auth.ActorFrom(c.Request).ID); err != nil {
		httpx.Err(c, driveAuthStatus(err), err)
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
