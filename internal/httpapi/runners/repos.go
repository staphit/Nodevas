package runners

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
)

// Repos of the built-in runner from the web (runnerwire/repoadmin.go): on a
// single-user hub the owner adds a folder as a repo, makes a folder a git
// project first, removes a repo or sets its test command, all from the app.
//
//	POST   /api/runners/:id/repos/inspect     {path}            → RepoInspect
//	POST   /api/runners/:id/repos             {path, initPlan?} → {repo, runner}
//	DELETE /api/runners/:id/repos/:repoId                       → {runner}
//	PUT    /api/runners/:id/repos/:repoId/test {command, timeoutMinutes} → {runner}
//
// The owner only, a person only, and the built-in runner only: any other
// runner's repos are added on its own PC (409, blocked add_on_runner). The
// hub relays and the runner decides, re-checking every folder against its
// own guard; the hub refuses its own data folders before asking.

// Audit rows of repo changes the runner answered (or did not answer in
// time: it may have applied them). Inspecting a folder changes nothing and
// is not recorded.
const (
	ActionRepoAdd         = "runner.repo.add"
	ActionRepoInit        = "runner.repo.init"
	ActionRepoRemove      = "runner.repo.remove"
	ActionRepoTestCommand = "runner.repo.test_command"
)

// BlockedAddOnRunner is the blocked reason when a runner's repos are not
// changed from here: they are added on that runner's own PC.
const BlockedAddOnRunner = "add_on_runner"

// Body caps: room for a path (maxRepoPath) or a test command
// (runnerwire.MaxTestCommandBytes) as JSON, where a backslash takes two
// bytes, and the rest of the request.
const (
	maxRepoBody     = 4 << 10
	maxRepoTestBody = 2*runnerwire.MaxTestCommandBytes + 1<<10
	maxRepoPath     = 1024
	maxRepoID       = 256
)

// RepoAdminOptions wires repo admin into the API.
type RepoAdminOptions struct {
	Relay *runners.RepoAdminRelay
	// Reserved reports whether a folder is the hub's own data (its
	// workspace or its catalog root, or inside one), which is never added
	// as a repo; nil reserves nothing.
	Reserved func(path string) bool
}

// WithRepoAdmin relays the built-in runner's repo changes through opts. It
// returns a for chaining.
func (a *API) WithRepoAdmin(opts RepoAdminOptions) *API {
	a.repoAdmin = &opts
	return a
}

func (a *API) registerRepoAdmin(api *gin.RouterGroup) {
	if a.repoAdmin == nil || a.repoAdmin.Relay == nil {
		return
	}
	api.POST("/runners/:id/repos/inspect", a.inspectRepo)
	api.POST("/runners/:id/repos", a.addRepo)
	api.DELETE("/runners/:id/repos/:repoId", a.removeRepo)
	api.PUT("/runners/:id/repos/:repoId/test", a.setRepoTest)
}

var (
	errRepoAgent = locale.Errorf("agents cannot change a runner's repos", "agent 不能變更 runner 的 repo")
	errRepoPath  = locale.Errorf("path must be the full path of a folder on this computer", "path 必須是這台電腦上某個資料夾的完整路徑")
	errRepoID    = locale.Errorf("repoId is not a repo id", "repoId 不是 repo 代號")
	errRepoTest  = locale.Errorf("command must be one line of at most %d bytes, and timeoutMinutes from 0 to %d",
		"command 必須是最多 %d 位元組的單行文字，timeoutMinutes 必須介於 0 到 %d",
		runnerwire.MaxTestCommandBytes, runnerwire.MaxTestTimeoutMinutes)
	errRepoNodevas = locale.Errorf("Nodevas keeps its own data in this folder; pick a project folder",
		"這裡是 Nodevas 自己的資料，請選專案資料夾")
)

// repoRunner is the runner named in the path, if it is a person's own and
// its repos are changed from here; otherwise it has answered.
func (a *API) repoRunner(c *gin.Context) (runnerwire.Runner, identity.Actor, bool) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errRepoAgent, nil)
		return runnerwire.Runner{}, actor, false
	}
	runner, err := a.reg.Get(c.Request.Context(), c.Param("id"))
	// Somebody else's runner is not there at all, administrators included:
	// the disk is its owner's.
	if errors.Is(err, runners.ErrNotFound) || (err == nil && (actor.ID == "" || runner.OwnerID != actor.ID)) {
		fail(c, runners.ErrNotFound)
		return runnerwire.Runner{}, actor, false
	}
	if err != nil {
		fail(c, err)
		return runnerwire.Runner{}, actor, false
	}
	if blocked := a.repoAdmin.Relay.Blocked(runner); blocked != nil {
		repoBlocked(c, blocked)
		return runnerwire.Runner{}, actor, false
	}
	return runner, actor, true
}

// repoBlocked answers a change the hub cannot relay now.
func repoBlocked(c *gin.Context, err error) {
	switch {
	case errors.Is(err, runners.ErrRepoAdminOff):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, map[string]any{"blocked": BlockedAddOnRunner})
	case errors.Is(err, runners.ErrRunnerOffline), errors.Is(err, runners.ErrRepoRunnerGone):
		httpx.Coded(c, http.StatusServiceUnavailable, "RUNNER_OFFLINE", err, map[string]any{"blocked": runnerwire.RunnerBlockedOffline})
	case errors.Is(err, runners.ErrRepoTimeout):
		httpx.Coded(c, http.StatusGatewayTimeout, "TIMEOUT", err, nil)
	default:
		fail(c, err)
	}
}

// cleanRepoPath is the folder a request names, or false: a full path of at
// most maxRepoPath bytes of printable text. The runner resolves and checks
// it; this keeps junk off the socket.
func cleanRepoPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > maxRepoPath || !utf8.ValidString(path) || !filepath.IsAbs(path) {
		return "", false
	}
	for _, r := range path {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return filepath.Clean(path), true
}

// validRepoID accepts a repo id as a hello carries one.
func validRepoID(id string) bool {
	return id != "" && len(id) <= maxRepoID && utf8.ValidString(id) && !strings.ContainsFunc(id, unicode.IsControl)
}

// reserved reports whether path is the hub's own data.
func (a *API) reserved(path string) bool {
	return a.repoAdmin.Reserved != nil && a.repoAdmin.Reserved(path)
}

// repoErrors are the runner's refusals in both languages, by code, and the
// status each answers with.
var repoErrors = map[string]struct {
	status int
	said   error
}{
	runnerwire.RepoAdminErrRefused: {http.StatusForbidden, locale.Errorf("only the runner's owner can change its repos", "只有 runner 的擁有者可以變更它的 repo")},
	runnerwire.RepoAdminErrSensitive: {http.StatusBadRequest, locale.Errorf("agents must never work in this folder; pick a project folder",
		"不能讓 agent 在這個資料夾工作，請選專案資料夾")},
	runnerwire.RepoAdminErrNotFound:   {http.StatusBadRequest, locale.Errorf("that folder or repo does not exist", "找不到那個資料夾或 repo")},
	runnerwire.RepoAdminErrNotDir:     {http.StatusBadRequest, locale.Errorf("that is not a folder", "那不是資料夾")},
	runnerwire.RepoAdminErrNotRepo:    {http.StatusConflict, locale.Errorf("this folder isn't a Git project yet", "這個資料夾還不是 Git 專案")},
	runnerwire.RepoAdminErrAlready:    {http.StatusConflict, locale.Errorf("this repo is already added", "這個 repo 已經新增過了")},
	runnerwire.RepoAdminErrTooMany:    {http.StatusConflict, locale.Errorf("this runner has as many repos as it can hold", "這個 runner 的 repo 已達上限")},
	runnerwire.RepoAdminErrTooBig:     {http.StatusConflict, locale.Errorf("too many files to save a first version here; set it up with a Git tool, then add it", "檔案太多，無法在這裡建立第一個版本；請先用 Git 工具設定好再新增")},
	runnerwire.RepoAdminErrGitMissing: {http.StatusConflict, locale.Errorf("Git isn't installed on this computer; install Git, then try again", "這台電腦沒有安裝 Git，請先安裝 Git 再試一次")},
	runnerwire.RepoAdminErrGit:        {http.StatusConflict, locale.Errorf("Git could not set up the folder", "Git 無法設定這個資料夾")},
	runnerwire.RepoAdminErrPlanChanged: {http.StatusConflict, locale.Errorf("the folder changed while you were deciding; check it again",
		"你在確認時資料夾內容變了，請重新檢查")},
	runnerwire.RepoAdminErrBusy:       {http.StatusConflict, locale.Errorf("the runner is busy with another repo change; try again", "runner 正在處理另一個 repo 變更，請再試一次")},
	runnerwire.RepoAdminErrBadRequest: {http.StatusBadRequest, locale.Errorf("the runner could not read the request", "runner 無法讀取這個請求")},
}

// repoDoneFailed answers a runner's refusal: the error body's code is the
// runner's own code, for the web to word, with what the runner found.
func repoDoneFailed(c *gin.Context, done runnerwire.RepoAdminDone) {
	if done.Code == runnerwire.RepoAdminErrOff {
		repoBlocked(c, runners.ErrRepoAdminOff)
		return
	}
	extras := map[string]any{}
	if done.Inspect != nil {
		extras["inspect"] = done.Inspect
		if done.Inspect.Refused != "" {
			extras["refused"] = done.Inspect.Refused
		}
	}
	if done.Repo != nil {
		extras["repo"] = done.Repo
	}
	known, ok := repoErrors[done.Code]
	if !ok {
		// A code this hub does not know: the runner's own prose says why.
		httpx.Coded(c, http.StatusConflict, done.Code,
			locale.Errorf("the runner did not apply the change: %s", "runner 沒有套用變更：%s", done.Error), extras)
		return
	}
	httpx.Coded(c, known.status, done.Code, known.said, extras)
}

// inspectRepo is POST /api/runners/:id/repos/inspect: what adding a folder
// would do. It changes nothing.
func (a *API) inspectRepo(c *gin.Context) {
	runner, actor, ok := a.repoRunner(c)
	if !ok {
		return
	}
	var body runnerwire.RepoInspectBody
	if !httpx.DecodeJSONLimit(c, &body, maxRepoBody) {
		return
	}
	path, ok := cleanRepoPath(body.Path)
	if !ok {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errRepoPath, nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	if a.reserved(path) {
		c.JSON(http.StatusOK, runnerwire.RepoInspect{Path: path, State: runnerwire.RepoStateRefused, Refused: runnerwire.RepoRefusedNodevas})
		return
	}
	done, err := a.repoAdmin.Relay.Request(c.Request.Context(), runner.ID, runnerwire.RepoAdminRequest{
		AccountID: actor.ID, Op: runnerwire.RepoAdminInspect, Path: path,
	})
	if err != nil {
		repoBlocked(c, err)
		return
	}
	if done.Code != "" {
		repoDoneFailed(c, done)
		return
	}
	if done.Inspect == nil {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", locale.Errorf("the runner did not say what it found", "runner 沒有回報檢查結果"), nil)
		return
	}
	// A folder inside a Git project adds the whole project: the hub's own
	// data must not be in that one either.
	if top := done.Inspect.Top; top != "" && done.Inspect.State != runnerwire.RepoStateRefused && a.reserved(top) {
		c.JSON(http.StatusOK, runnerwire.RepoInspect{Path: path, Top: top, State: runnerwire.RepoStateRefused, Refused: runnerwire.RepoRefusedNodevas})
		return
	}
	c.JSON(http.StatusOK, done.Inspect)
}

// addRepo is POST /api/runners/:id/repos: add a git project, or, with the
// init plan the person confirmed, make the folder one first.
func (a *API) addRepo(c *gin.Context) {
	runner, actor, ok := a.repoRunner(c)
	if !ok {
		return
	}
	var body runnerwire.RepoAddBody
	if !httpx.DecodeJSONLimit(c, &body, maxRepoBody) {
		return
	}
	path, ok := cleanRepoPath(body.Path)
	plan := strings.TrimSpace(body.InitPlan)
	if !ok || len(plan) > maxRepoID || !utf8.ValidString(plan) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errRepoPath, nil)
		return
	}
	if a.reserved(path) {
		httpx.Coded(c, http.StatusBadRequest, runnerwire.RepoAdminErrSensitive, errRepoNodevas,
			map[string]any{"refused": runnerwire.RepoRefusedNodevas})
		return
	}
	op, action := runnerwire.RepoAdminAdd, ActionRepoAdd
	if plan != "" {
		op, action = runnerwire.RepoAdminInitAdd, ActionRepoInit
	}
	ctx := c.Request.Context()
	done, err := a.repoAdmin.Relay.Request(ctx, runner.ID, runnerwire.RepoAdminRequest{
		AccountID: actor.ID, Op: op, Path: path, InitPlan: plan,
	})
	if err != nil {
		if errors.Is(err, runners.ErrRepoTimeout) {
			a.auditRepo(c, action, runner.ID, map[string]any{"path": path, "code": "timeout"})
		}
		repoBlocked(c, err)
		return
	}
	detail := map[string]any{"path": path, "code": done.Code}
	if done.Repo != nil {
		detail["repo"], detail["name"] = done.Repo.ID, done.Repo.Name
	}
	a.auditRepo(c, action, runner.ID, detail)
	if done.Code != "" {
		repoDoneFailed(c, done)
		return
	}
	if done.Repo == nil {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", locale.Errorf("the runner did not say which repo it added", "runner 沒有回報新增的 repo"), nil)
		return
	}
	// The runner said hello with its new repos before it answered, so the
	// registry holds them, and a run can pick the repo at once.
	updated, err := a.reg.Get(ctx, runner.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, runnerwire.RepoAddResponse{Repo: *done.Repo, Runner: updated})
}

// removeRepo is DELETE /api/runners/:id/repos/:repoId: agents can no
// longer work in the repo; its files are not touched.
func (a *API) removeRepo(c *gin.Context) {
	runner, actor, ok := a.repoRunner(c)
	if !ok {
		return
	}
	repoID := c.Param("repoId")
	if !validRepoID(repoID) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errRepoID, nil)
		return
	}
	detail := map[string]any{"repo": repoID, "name": repoName(runner, repoID)}
	a.relayRepoChange(c, runner, ActionRepoRemove, detail, runnerwire.RepoAdminRequest{
		AccountID: actor.ID, Op: runnerwire.RepoAdminRemove, RepoID: repoID,
	})
}

// setRepoTest is PUT /api/runners/:id/repos/:repoId/test: the command the
// runner runs, on this computer, to check what agents wrote ("" clears it).
func (a *API) setRepoTest(c *gin.Context) {
	runner, actor, ok := a.repoRunner(c)
	if !ok {
		return
	}
	repoID := c.Param("repoId")
	if !validRepoID(repoID) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errRepoID, nil)
		return
	}
	var body runnerwire.RepoTestBody
	if !httpx.DecodeJSONLimit(c, &body, maxRepoTestBody) {
		return
	}
	command := strings.TrimSpace(body.Command)
	if len(command) > runnerwire.MaxTestCommandBytes || !utf8.ValidString(command) || strings.ContainsFunc(command, unicode.IsControl) ||
		body.TimeoutMinutes < 0 || body.TimeoutMinutes > runnerwire.MaxTestTimeoutMinutes {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errRepoTest, nil)
		return
	}
	// The command is what will run on agents' code, so the trail keeps it.
	detail := map[string]any{"repo": repoID, "name": repoName(runner, repoID), "command": command, "timeoutMinutes": body.TimeoutMinutes}
	a.relayRepoChange(c, runner, ActionRepoTestCommand, detail, runnerwire.RepoAdminRequest{
		AccountID: actor.ID, Op: runnerwire.RepoAdminSetTest, RepoID: repoID,
		TestCommand: command, TestTimeoutMinutes: body.TimeoutMinutes,
	})
}

// relayRepoChange sends a change to one of runner's repos, records it, and
// answers with the runner after its new hello.
func (a *API) relayRepoChange(c *gin.Context, runner runnerwire.Runner, action string, detail map[string]any, req runnerwire.RepoAdminRequest) {
	ctx := c.Request.Context()
	done, err := a.repoAdmin.Relay.Request(ctx, runner.ID, req)
	if err != nil {
		if errors.Is(err, runners.ErrRepoTimeout) {
			detail["code"] = "timeout"
			a.auditRepo(c, action, runner.ID, detail)
		}
		repoBlocked(c, err)
		return
	}
	detail["code"] = done.Code
	a.auditRepo(c, action, runner.ID, detail)
	if done.Code != "" {
		repoDoneFailed(c, done)
		return
	}
	updated, err := a.reg.Get(ctx, runner.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RepoRunnerResponse{Runner: updated})
}

// repoName is the name runner's last hello gives repoID, or "".
func repoName(runner runnerwire.Runner, repoID string) string {
	if runner.Info == nil {
		return ""
	}
	for _, repo := range runner.Info.Repos {
		if repo.ID == repoID {
			return repo.Name
		}
	}
	return ""
}

// auditRepo records a repo change on the runner. The request middleware
// adds its own row for the write.
func (a *API) auditRepo(c *gin.Context, action, runnerID string, detail map[string]any) {
	if a.link == nil || a.link.Audit == nil {
		return
	}
	detail["runner"] = runnerID
	a.link.Audit(c.Request, action, auth.ActorFrom(c.Request), runnerID, detail)
}
