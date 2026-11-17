package runs

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// The agent workspace (runnerwire/workspace.go): one run's worktree, branch
// and what may be done with them. Looking into the worktree and acting on
// it are the runner's owner's alone: the disk is theirs, so an
// administrator is no exception, and an agent never is.

// WorkspaceWait is how long a merge or discard request waits for the
// runner's answer before it answers 202 with the request pending. A var so
// tests can shorten it.
var WorkspaceWait = 30 * time.Second

// workspaceInfoWait bounds the live look at the worktree that GET
// /workspace asks the runner for.
const workspaceInfoWait = 5 * time.Second

const maxWorkspaceBody = 4 << 10

// UseRepo relays the workspace's file browsing and live info to runners.
func (a *API) UseRepo(relay *runners.RepoRelay) { a.repo = relay }

// ownerOnly loads the run named in the path for its runner's owner, a
// person: anyone else who may see the run is refused, anyone who may not
// gets a 404.
func (a *API) ownerOnly(c *gin.Context) (runs.Record, identity.Actor, bool) {
	r, ok := a.visible(c)
	if !ok {
		return runs.Record{}, identity.Actor{}, false
	}
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("agents cannot change or browse run workspaces", "agent 不能瀏覽或變更執行工作區"), nil)
		return runs.Record{}, identity.Actor{}, false
	}
	if r.OwnerID != actor.ID {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("only the runner's owner can do this", "只有 runner 的擁有者可以這麼做"), nil)
		return runs.Record{}, identity.Actor{}, false
	}
	return r, actor, true
}

// runRepoRequest is a run-scoped repo request for r.
func runRepoRequest(r runs.Record, op, path string) runnerwire.RepoRequest {
	return runnerwire.RepoRequest{Op: op, Path: path, RepoID: r.RepoID, RunID: r.ID, NodeID: r.NodeID,
		Slug: r.BranchSlug, Branch: r.Branch, Base: r.Base}
}

// workspace is GET /api/runs/:id/workspace: the panel's gates for whoever
// may see the run and, for its runner's owner, a live look at the worktree.
// A runner that is away is never a server error: info stays null.
func (a *API) workspace(c *gin.Context) {
	r, ok := a.visible(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("agents cannot change or browse run workspaces", "agent 不能瀏覽或變更執行工作區"), nil)
		return
	}
	ctx := c.Request.Context()
	runner := a.d.WorkspaceRunnerOf(ctx, r)
	gates := runs.WorkspaceGate(r, runner)
	view := runnerwire.RunWorkspaceView{RunID: r.ID, Owner: r.OwnerID == actor.ID, RunnerOnline: runner.Online}
	if !view.Owner {
		// A pull request keeps its own approval, open to whoever may see
		// the run; everything else here is the owner's.
		view.Files.Blocked = runnerwire.WorkspaceFilesNotOwner
		if gates.MergeMode == runnerwire.WorkspaceMergeModePR {
			view.Merge.Mode = runnerwire.WorkspaceMergeModePR
		} else {
			view.Merge.Blocked = runnerwire.WorkspaceGateNotOwner
		}
		view.Discard.Blocked = runnerwire.WorkspaceGateNotOwner
		view.InfoError = runnerwire.WorkspaceInfoNotOwner
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, view)
		return
	}
	view.Merge = runnerwire.RunWorkspaceMergeGate{Mode: gates.MergeMode, Blocked: gates.MergeBlocked}
	view.Discard = runnerwire.RunWorkspaceDiscardGate{Allowed: gates.DiscardBlocked == "", Blocked: gates.DiscardBlocked}
	has := func(capability string) bool { return slices.Contains(runner.Caps, capability) }
	switch {
	case !runner.Online:
		view.InfoError = runnerwire.WorkspaceInfoOffline
	case !has(runnerwire.CapRunWorkspace):
		view.InfoError = runnerwire.WorkspaceInfoOutdated
	case a.repo != nil:
		infoCtx, cancel := context.WithTimeout(ctx, workspaceInfoWait)
		done, err := a.repo.BrowseRun(infoCtx, actor.ID, r.RunnerID, runRepoRequest(r, runnerwire.RepoOpRunInfo, ""))
		cancel()
		switch {
		case err == nil && done.Code == "" && done.RunInfo != nil:
			view.Info = done.RunInfo
		case errors.Is(err, runners.ErrRunnerOffline), errors.Is(err, runners.ErrRepoRunnerGone):
			view.InfoError = runnerwire.WorkspaceInfoOffline
		case errors.Is(err, runners.ErrRunnerOutdated):
			view.InfoError = runnerwire.WorkspaceInfoOutdated
		case errors.Is(err, runners.ErrRepoTimeout), errors.Is(err, context.DeadlineExceeded):
			view.InfoError = runnerwire.WorkspaceInfoTimeout
		}
	}
	switch {
	case !runner.Online:
		view.Files.Blocked = runnerwire.WorkspaceFilesOffline
	case !has(runnerwire.CapRunWorkspace):
		view.Files.Blocked = runnerwire.WorkspaceFilesOutdated
	case !has(runnerwire.CapRepoBrowse):
		view.Files.Blocked = runnerwire.WorkspaceFilesBrowseOff
	case view.Info != nil && !view.Info.Exists:
		view.Files.Blocked = runnerwire.WorkspaceFilesNoWorktree
	default:
		view.Files.Allowed = true
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, view)
}

// browseRun relays one read-only look into the run's worktree to its
// runner and answers with what it returned, or the refusal's status.
func (a *API) browseRun(c *gin.Context, op, path string, answer func(runnerwire.RepoDone) any) {
	r, actor, ok := a.ownerOnly(c)
	if !ok {
		return
	}
	if a.repo == nil {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("runner or repo not found", "找不到 runner 或 repo"), nil)
		return
	}
	if len(path) > runnerwire.MaxRepoPathBytes {
		httpx.Coded(c, http.StatusBadRequest, "BAD_PATH", locale.Errorf("the path is too long", "路徑太長"), nil)
		return
	}
	done, err := a.repo.BrowseRun(c.Request.Context(), actor.ID, r.RunnerID, runRepoRequest(r, op, path))
	switch {
	case errors.Is(err, runners.ErrRepoNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("runner or repo not found", "找不到 runner 或 repo"), nil)
		return
	case errors.Is(err, runners.ErrRunnerOffline), errors.Is(err, runners.ErrRepoRunnerGone):
		httpx.Coded(c, http.StatusServiceUnavailable, "RUNNER_OFFLINE", err, nil)
		return
	case errors.Is(err, runners.ErrRunnerOutdated):
		httpx.Coded(c, http.StatusPreconditionFailed, "RUNNER_OUTDATED", err, nil)
		return
	case errors.Is(err, runners.ErrRepoBrowseOff):
		httpx.Coded(c, http.StatusPreconditionFailed, "REPO_BROWSE_OFF", err, nil)
		return
	case errors.Is(err, runners.ErrRepoTimeout):
		httpx.Coded(c, http.StatusGatewayTimeout, "TIMEOUT", err, nil)
		return
	case err != nil:
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	if done.Code != "" {
		status, code := http.StatusBadGateway, "GIT_ERROR"
		switch done.Code {
		case runnerwire.RepoErrNotFound, runnerwire.RepoErrRefused:
			status, code = http.StatusNotFound, "NOT_FOUND"
		case runnerwire.RepoErrNoWorktree:
			status, code = http.StatusNotFound, "NO_WORKTREE"
		case runnerwire.RepoErrBadPath:
			status, code = http.StatusBadRequest, "BAD_PATH"
		case runnerwire.RepoErrHidden:
			status, code = http.StatusForbidden, "HIDDEN_FILE"
		case runnerwire.RepoErrOff:
			status, code = http.StatusPreconditionFailed, "REPO_BROWSE_OFF"
		case runnerwire.RepoErrBusy:
			status, code = http.StatusTooManyRequests, "BUSY"
			c.Header("Retry-After", "2")
		}
		msg := strings.TrimSpace(done.Error)
		if msg == "" {
			msg = done.Code
		}
		httpx.Coded(c, status, code, errors.New(msg), nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, answer(done))
}

func (a *API) workspaceTree(c *gin.Context) {
	a.browseRun(c, runnerwire.RepoOpRunList, c.Query("path"), func(d runnerwire.RepoDone) any {
		if d.Listing == nil {
			return runnerwire.RepoListing{Entries: []runnerwire.RepoEntry{}}
		}
		return d.Listing
	})
}

func (a *API) workspaceFile(c *gin.Context) {
	a.browseRun(c, runnerwire.RepoOpRunRead, c.Query("path"), func(d runnerwire.RepoDone) any {
		if d.File == nil {
			return runnerwire.RepoFile{}
		}
		return d.File
	})
}

func (a *API) workspaceStatus(c *gin.Context) {
	a.browseRun(c, runnerwire.RepoOpRunStatus, "", func(d runnerwire.RepoDone) any {
		if d.Status == nil {
			return runnerwire.RepoGitStatus{Entries: []runnerwire.RepoStatusEntry{}}
		}
		return d.Status
	})
}

// workspaceMerge is POST /api/runs/:id/workspace/merge: merge the run's
// branch into its base on the runner, at the run commit the owner saw.
func (a *API) workspaceMerge(c *gin.Context) {
	r, actor, ok := a.ownerOnly(c)
	if !ok {
		return
	}
	var body runnerwire.WorkspaceMergeBody
	if !httpx.DecodeJSONLimit(c, &body, maxWorkspaceBody) {
		return
	}
	body.HeadSHA = strings.TrimSpace(body.HeadSHA)
	if !validHeadSHA(body.HeadSHA) {
		invalid(c, "headSha must be the run commit you are merging", "headSha 須為你要合併的執行提交")
		return
	}
	a.workspaceAct(c, r, actor, runnerwire.WorkspaceMergeLocal, body.HeadSHA)
}

// workspaceDiscard is POST /api/runs/:id/workspace/discard: remove the run's
// worktree and branch, closing its PR.
func (a *API) workspaceDiscard(c *gin.Context) {
	r, actor, ok := a.ownerOnly(c)
	if !ok {
		return
	}
	if c.Request.ContentLength != 0 {
		var body struct{}
		if !httpx.DecodeJSONLimit(c, &body, maxWorkspaceBody) {
			return
		}
	}
	a.workspaceAct(c, r, actor, runnerwire.WorkspaceDiscard, "")
}

// workspaceAct asks for the action and waits a while for the runner's
// answer: 200 with the run once it answered (done or refused), 202 while
// the request still waits for it.
func (a *API) workspaceAct(c *gin.Context, r runs.Record, actor identity.Actor, action, headSHA string) {
	ctx := c.Request.Context()
	updated, err := a.d.WorkspaceAction(ctx, r.ID, runs.Approver{ID: actor.ID, Name: displayName(actor.ID, actor.Name)}, action, headSHA)
	if err != nil {
		workspaceFailed(c, err)
		return
	}
	status := http.StatusOK
	if pending := updated.WorkspaceAction; pending != nil && pending.Status == runnerwire.WorkspacePending {
		a.d.AwaitWorkspaceAction(ctx, pending.ID, WorkspaceWait)
		if latest, err := a.d.Store().Get(ctx, r.ID); err == nil {
			updated = latest
		}
		if updated.WorkspaceAction != nil && updated.WorkspaceAction.Status == runnerwire.WorkspacePending {
			status = http.StatusAccepted
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, a.forReader(c, updated.Run))
}

// previewAnswer is POST …/workspace/preview's answer: the page's address on
// the built-in runner's own preview listener, or, with no page, only the
// folder to open.
type previewAnswer struct {
	Web       bool       `json:"web"`
	URL       string     `json:"url,omitempty"`
	Folder    string     `json:"folder,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// Preview refusals with no workspace gate text of their own.
var (
	errPreviewOff = locale.Errorf("previews are off here: they need this computer's own runner, opened on this computer",
		"這裡無法預覽：需要這台電腦自己的 runner，並在這台電腦上開啟")
	errPreviewNoWorktree = locale.Errorf("the run's folder is gone", "這次執行的資料夾已經不在了")
	errPreviewBadAnswer  = locale.Errorf("the runner's preview answer is not usable", "runner 的預覽回應無法使用")
)

// previewBlocked answers a preview the run or its runner does not allow now:
// 409 with the code in "blocked".
func previewBlocked(c *gin.Context, code string) {
	var err error
	switch code {
	case runnerwire.RepoErrPreviewOff:
		err = errPreviewOff
	case runnerwire.RepoErrNoWorktree:
		err = errPreviewNoWorktree
	default:
		err = &runs.WorkspaceBlocked{Code: code}
	}
	httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, map[string]any{"blocked": code})
}

// previewHost is the loopback name the preview's address uses: the other
// one than the browser used for this app (127.0.0.1 for localhost,
// localhost for a loopback address), so the page never shares the app's
// cookies. ok is false for a request that did not come to a loopback name
// on this machine: the preview is only ever this computer's.
func previewHost(r *http.Request) (string, bool) {
	if !auth.ForwardedForThisMachine(r) {
		return "", false
	}
	seen := r.Host
	if forwarded := r.Header.Values("X-Forwarded-Host"); len(forwarded) > 0 {
		seen, _, _ = strings.Cut(forwarded[0], ",")
	}
	name := strings.TrimSpace(seen)
	if host, _, err := net.SplitHostPort(name); err == nil {
		name = host
	}
	name = strings.Trim(name, "[]")
	if strings.EqualFold(name, "localhost") {
		return "127.0.0.1", true
	}
	if addr, err := netip.ParseAddr(name); err == nil && addr.Unmap().IsLoopback() {
		return "localhost", true
	}
	return "", false
}

// previewURL is the page's address on the preview listener, or "" when the
// runner's answer is not one the hub hands on: a port, a 128-bit hex token
// and a plain relative path inside the run's folder.
func previewURL(host string, p runnerwire.RunPreview) string {
	if p.Port <= 0 || p.Port > 65535 || len(p.Token) != 32 || strings.Trim(p.Token, "0123456789abcdef") != "" {
		return ""
	}
	if p.Entry == "" || len(p.Entry) > runnerwire.MaxRepoPathBytes || !utf8.ValidString(p.Entry) ||
		strings.ContainsAny(p.Entry, "\x00\\:") {
		return ""
	}
	segs := strings.Split(p.Entry, "/")
	for i, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return ""
		}
		segs[i] = url.PathEscape(seg)
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(p.Port)) + "/p/" + p.Token + "/" + strings.Join(segs, "/")
}

// workspacePreview is POST /api/runs/:id/workspace/preview: a token for the
// finished run's page on its built-in runner's own loopback listener
// (runner/preview.go), so it opens in a browser tab on this computer without
// reaching this origin. The token goes to its owner's browser only: it is
// never logged or audited.
func (a *API) workspacePreview(c *gin.Context) {
	r, actor, ok := a.ownerOnly(c)
	if !ok {
		return
	}
	if c.Request.ContentLength != 0 {
		var body struct{}
		if !httpx.DecodeJSONLimit(c, &body, maxWorkspaceBody) {
			return
		}
	}
	switch {
	case !runnerwire.RunFinal(r.Status):
		previewBlocked(c, runnerwire.WorkspaceGateRunning)
		return
	case r.Workspace == runnerwire.RunWorkspaceMergedLocal:
		previewBlocked(c, runnerwire.WorkspaceGateMergedLocal)
		return
	case r.Workspace == runnerwire.RunWorkspaceDiscarded:
		previewBlocked(c, runnerwire.WorkspaceGateDiscarded)
		return
	}
	host, local := previewHost(c.Request)
	if !local || a.repo == nil {
		previewBlocked(c, runnerwire.RepoErrPreviewOff)
		return
	}
	done, err := a.repo.BrowseRun(c.Request.Context(), actor.ID, r.RunnerID, runRepoRequest(r, runnerwire.RepoOpRunPreview, ""))
	switch {
	case errors.Is(err, runners.ErrRepoNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("runner or repo not found", "找不到 runner 或 repo"), nil)
		return
	case errors.Is(err, runners.ErrRunPreviewOff), errors.Is(err, runners.ErrRepoBrowseOff):
		previewBlocked(c, runnerwire.RepoErrPreviewOff)
		return
	case errors.Is(err, runners.ErrRunnerOffline), errors.Is(err, runners.ErrRepoRunnerGone):
		previewBlocked(c, runnerwire.WorkspaceGateOffline)
		return
	case errors.Is(err, runners.ErrRunnerOutdated):
		previewBlocked(c, runnerwire.WorkspaceGateOutdated)
		return
	case errors.Is(err, runners.ErrRepoTimeout):
		httpx.Coded(c, http.StatusGatewayTimeout, "TIMEOUT", err, nil)
		return
	case err != nil:
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	switch done.Code {
	case "":
	case runnerwire.RepoErrNoWorktree:
		previewBlocked(c, runnerwire.RepoErrNoWorktree)
		return
	case runnerwire.RepoErrPreviewOff, runnerwire.RepoErrOff:
		previewBlocked(c, runnerwire.RepoErrPreviewOff)
		return
	case runnerwire.RepoErrBusy:
		c.Header("Retry-After", "2")
		httpx.Coded(c, http.StatusTooManyRequests, "BUSY", locale.Errorf("the runner is busy; try again shortly", "runner 忙碌中，請稍後再試"), nil)
		return
	case runnerwire.RepoErrNotFound, runnerwire.RepoErrRefused:
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("runner or repo not found", "找不到 runner 或 repo"), nil)
		return
	default:
		httpx.Coded(c, http.StatusBadGateway, "RUNNER_ERROR", errPreviewBadAnswer, nil)
		return
	}
	if done.Preview == nil {
		httpx.Coded(c, http.StatusBadGateway, "RUNNER_ERROR", errPreviewBadAnswer, nil)
		return
	}
	answer := previewAnswer{Web: done.Preview.Web, Folder: done.Preview.Path}
	if answer.Web {
		answer.URL = previewURL(host, *done.Preview)
		if answer.URL == "" {
			httpx.Coded(c, http.StatusBadGateway, "RUNNER_ERROR", errPreviewBadAnswer, nil)
			return
		}
		if at := done.Preview.ExpiresAt; !at.IsZero() {
			at = at.UTC()
			answer.ExpiresAt = &at
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, answer)
}

// workspaceFailed answers a refused workspace action: the gate's code in
// "blocked", with the status that says what to do about it.
func workspaceFailed(c *gin.Context, err error) {
	code := runs.BlockedCode(err)
	if code == "" && errors.Is(err, runs.ErrPrecondition) && strings.Contains(err.Error(), runs.MsgWorkspacePending) {
		code = runnerwire.WorkspaceGatePending
	}
	switch code {
	case "":
		fail(c, err)
	case runnerwire.WorkspaceGateOffline:
		httpx.Coded(c, http.StatusServiceUnavailable, "RUNNER_OFFLINE", err, map[string]any{"blocked": code})
	case runnerwire.WorkspaceGateOutdated:
		httpx.Coded(c, http.StatusPreconditionFailed, "RUNNER_OUTDATED", err, map[string]any{"blocked": code})
	default:
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, map[string]any{"blocked": code})
	}
}
