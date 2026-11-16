package runners

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// Runner settings from the web (runnerwire/settings.go): a runner's owner
// changes how many runs it takes at once. The machine is its owner's, so an
// administrator is no exception, and an agent never is; the runner checks
// all of it again, with its own ceiling.
//
// On a single-user hub the built-in runner's own settings are changed here
// too ("this computer"): what each agent may do, whether to ask before every
// run, which full-access agents' automatic runs start without asking, the PR
// options, and a fresh look for the agent CLIs. Any other runner refuses
// them (runnerwire.SettingsErrLocalOnly); it is set up on its own PC.

// ActionSettingsUpdate is the audit row of a settings change the runner
// answered (or did not answer in time: it may have applied it).
const ActionSettingsUpdate = "runner.settings.update"

// maxSettingsBody is room for a permission level and a shell choice per
// agent kind (runnerwire.MaxAgents each) besides the rest.
const maxSettingsBody = 4 << 10

// WithSettings relays runner settings changes through relay. It returns a
// for chaining.
func (a *API) WithSettings(relay *runners.SettingsRelay) *API {
	a.settings = relay
	return a
}

func (a *API) registerSettings(api *gin.RouterGroup) {
	if a.settings == nil {
		return
	}
	api.PUT("/runners/:id/settings", a.putSettings)
}

var (
	errSettingsAgent = locale.Errorf("agents cannot change runner settings", "agent 不能變更 runner 設定")
	errSettingsOwner = locale.Errorf("only the runner's owner can change its settings", "只有 runner 的擁有者可以變更它的設定")
)

// settingsOutOfRange answers a limit past what the runner allows from here.
func settingsOutOfRange(c *gin.Context, ceiling int) {
	httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT",
		locale.Errorf("maxConcurrent must be from 1 to %d on this runner", "這個 runner 的 maxConcurrent 必須介於 1 到 %d", ceiling),
		map[string]any{"max": ceiling})
}

// settingsInvalid answers a local setting the runner could not take: an
// agent kind it does not list, or a level that is not one.
func settingsInvalid(c *gin.Context, err error) {
	httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
}

// agentPathRefused answers an agent path that was not taken, with why
// (runnerwire.AgentPathRefused*) for the app to say in plain words.
func agentPathRefused(c *gin.Context, refused string, err error) {
	httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, map[string]any{"field": "agentPath", "refused": refused})
}

// settingsRequest is body as the runner is asked it. An agent path, set or
// cleared, changes which agents are here, so the runner looks for them again
// before it answers.
func settingsRequest(accountID string, body runnerwire.RunnerSettingsBody) runnerwire.SettingsRequest {
	return runnerwire.SettingsRequest{
		AccountID: accountID, MaxConcurrent: body.MaxConcurrent,
		Permission: body.Permission, AskEveryRun: body.AskEveryRun, ShellWithoutAsking: body.ShellWithoutAsking,
		AutoPR: body.AutoPR, PRDraft: body.PRDraft, AgentPath: body.AgentPath, PermissionRelay: body.PermissionRelay,
		Redetect: body.Redetect || len(body.AgentPath) > 0,
	}
}

// agentPathInvalid is an agent path the hub refuses before asking the
// runner: answered with field agentPath and refused invalid, as the runner's
// own refusals are.
type agentPathInvalid struct{ error }

func (e agentPathInvalid) Unwrap() error { return e.error }

// checkLocalSettings checks the built-in runner's own settings in body
// against the runner's last hello: an agent kind must be one it lists, a
// permission level read, edit or full (custom is shown, never set), and an
// agent path one agent's, of at most runnerwire.MaxAgentPathBytes with no
// NUL. The runner checks the file itself.
func checkLocalSettings(runner runnerwire.Runner, body runnerwire.RunnerSettingsBody) error {
	if len(body.Permission) > runnerwire.MaxAgents || len(body.ShellWithoutAsking) > runnerwire.MaxAgents {
		return locale.Errorf("too many agents in one change", "一次變更的 agent 太多")
	}
	listed := func(kind string) bool {
		return runner.Info != nil && slices.ContainsFunc(runner.Info.Agents, func(agent runnerwire.Agent) bool { return agent.Kind == kind })
	}
	if len(body.AgentPath) > 1 {
		return agentPathInvalid{locale.Errorf("add one agent's program at a time", "一次只能加入一個 agent 的程式")}
	}
	for kind, path := range body.AgentPath {
		switch {
		case !listed(kind):
			return agentPathInvalid{locale.Errorf("%q is not an agent this runner knows", "「%s」不是這個 runner 知道的 agent", kind)}
		case len(path) > runnerwire.MaxAgentPathBytes:
			return agentPathInvalid{locale.Errorf("the program's path is over %d bytes", "程式的路徑超過 %d 位元組", runnerwire.MaxAgentPathBytes)}
		case strings.ContainsRune(path, 0):
			return agentPathInvalid{locale.Errorf("the program's path is not a path", "程式的路徑不是有效的路徑")}
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(body.Permission)) {
		if !listed(kind) {
			return locale.Errorf("%q is not an agent this runner knows", "「%s」不是這個 runner 知道的 agent", kind)
		}
		switch body.Permission[kind] {
		case runnerwire.PermRead, runnerwire.PermEdit, runnerwire.PermFull:
		default:
			return locale.Errorf("permission for %s must be read, edit or full", "%s 的權限必須是 read、edit 或 full", kind)
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(body.ShellWithoutAsking)) {
		if !listed(kind) {
			return locale.Errorf("%q is not an agent this runner knows", "「%s」不是這個 runner 知道的 agent", kind)
		}
	}
	return nil
}

// putSettings is PUT /api/runners/:id/settings.
func (a *API) putSettings(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errSettingsAgent, nil)
		return
	}
	ctx := c.Request.Context()
	runner, err := a.reg.Get(ctx, c.Param("id"))
	// Somebody else's runner is not there at all: a member probing ids
	// learns nothing, and an administrator does not change another
	// person's machine.
	if errors.Is(err, runners.ErrNotFound) || (err == nil && (actor.ID == "" || runner.OwnerID != actor.ID)) {
		fail(c, runners.ErrNotFound)
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	var body runnerwire.RunnerSettingsBody
	if !httpx.DecodeJSONLimit(c, &body, maxSettingsBody) {
		return
	}
	ceiling := 0
	if runner.Info != nil {
		ceiling = min(runner.Info.MaxConcurrentCeiling, runnerwire.MaxConcurrentLimit)
	}
	req := settingsRequest(actor.ID, body)
	local := runners.LocalOnly(req)
	if local {
		if blocked := a.settings.LocalBlocked(runner); blocked != nil {
			settingsBlocked(c, blocked)
			return
		}
		if err := checkLocalSettings(runner, body); err != nil {
			if errors.As(err, new(agentPathInvalid)) {
				agentPathRefused(c, runnerwire.AgentPathRefusedInvalid, err)
				return
			}
			settingsInvalid(c, err)
			return
		}
	}
	from, to := runs.Capacity(runner.Info), 0
	switch {
	case body.MaxConcurrent != nil:
		if blocked := a.settings.SettingsBlocked(runner); blocked != nil {
			settingsBlocked(c, blocked)
			return
		}
		if *body.MaxConcurrent < 1 || *body.MaxConcurrent > ceiling {
			settingsOutOfRange(c, ceiling)
			return
		}
		to = *body.MaxConcurrent
	case !local:
		// Nothing asked: the one field every runner takes is missing.
		settingsOutOfRange(c, ceiling)
		return
	}

	done, err := a.settings.Request(ctx, runner.ID, req)
	if err != nil {
		if errors.Is(err, runners.ErrRepoTimeout) {
			// It may have been applied all the same.
			a.auditSettings(c, runner.ID, body, from, to, to, false, "timeout")
			httpx.Coded(c, http.StatusGatewayTimeout, "TIMEOUT", err, nil)
			return
		}
		settingsBlocked(c, err)
		return
	}
	a.auditSettings(c, runner.ID, body, from, to, done.MaxConcurrent, done.Persisted, done.Code)
	switch done.Code {
	case "":
	case runnerwire.SettingsErrRefused:
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errSettingsOwner, nil)
		return
	case runnerwire.SettingsErrOff:
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", runners.ErrSettingsOff,
			map[string]any{"blocked": runnerwire.RunnerBlockedSettingsOff})
		return
	case runnerwire.SettingsErrRange:
		settingsOutOfRange(c, ceiling)
		return
	case runnerwire.SettingsErrLocalOnly:
		settingsBlocked(c, runners.ErrSettingsLocalOnly)
		return
	case runnerwire.SettingsErrInvalid:
		err := locale.Errorf("the runner did not take the change: %s", "runner 沒有接受變更：%s", done.Error)
		if done.Refused != "" && len(body.AgentPath) > 0 {
			agentPathRefused(c, done.Refused, err)
			return
		}
		settingsInvalid(c, err)
		return
	default:
		// A code this hub does not know: the runner's own prose says why.
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION",
			locale.Errorf("the runner did not apply the change: %s", "runner 沒有套用變更：%s", done.Error), nil)
		return
	}
	// The runner said hello with its new settings before it answered, so
	// the registry holds them now.
	updated, err := a.reg.Get(ctx, runner.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.RunnerSettingsResponse{Runner: updated, Persisted: done.Persisted})
}

// settingsBlocked answers a change the hub cannot relay now.
func settingsBlocked(c *gin.Context, err error) {
	switch {
	case errors.Is(err, runners.ErrRunnerOffline), errors.Is(err, runners.ErrRepoRunnerGone):
		httpx.Coded(c, http.StatusServiceUnavailable, "RUNNER_OFFLINE", err, map[string]any{"blocked": runnerwire.RunnerBlockedOffline})
	case errors.Is(err, runners.ErrSettingsOff):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, map[string]any{"blocked": runnerwire.RunnerBlockedSettingsOff})
	case errors.Is(err, runners.ErrSettingsLocalOnly):
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", err, map[string]any{"blocked": runnerwire.SettingsErrLocalOnly})
	case errors.Is(err, runners.ErrSettingsOutdated):
		httpx.Coded(c, http.StatusPreconditionFailed, "RUNNER_OUTDATED", err, map[string]any{"blocked": runnerwire.RunnerBlockedOutdated})
	default:
		fail(c, err)
	}
}

// auditSettings records what the owner asked for and what the runner did:
// for maxConcurrent the limit before, the one asked for, and the one in
// effect after (as asked when the runner did not say); the built-in
// runner's own settings as asked. The request middleware adds its own row
// for the PUT.
func (a *API) auditSettings(c *gin.Context, runnerID string, body runnerwire.RunnerSettingsBody, from, asked, to int, persisted bool, code string) {
	if a.link == nil || a.link.Audit == nil {
		return
	}
	detail := map[string]any{
		"runner":    runnerID,
		"persisted": persisted,
		"code":      code,
	}
	if body.MaxConcurrent != nil {
		detail["maxConcurrent"] = map[string]any{"from": from, "asked": asked, "to": to}
	}
	if len(body.Permission) > 0 {
		detail["permission"] = body.Permission
	}
	if body.AskEveryRun != nil {
		detail["askEveryRun"] = *body.AskEveryRun
	}
	if len(body.ShellWithoutAsking) > 0 {
		detail["shellWithoutAsking"] = body.ShellWithoutAsking
	}
	if body.AutoPR != nil {
		detail["autoPR"] = *body.AutoPR
	}
	if body.PRDraft != nil {
		detail["prDraft"] = *body.PRDraft
	}
	if body.PermissionRelay != nil {
		detail["permissionRelay"] = *body.PermissionRelay
	}
	if len(body.AgentPath) > 0 {
		// Which agent and whether it was set; the path stays on the PC.
		change := map[string]string{}
		for kind, path := range body.AgentPath {
			change[kind] = "set"
			if path == "" {
				change[kind] = "cleared"
			}
		}
		detail["agentPath"] = change
	}
	if body.Redetect || len(body.AgentPath) > 0 {
		detail["redetect"] = true
	}
	a.link.Audit(c.Request, ActionSettingsUpdate, auth.ActorFrom(c.Request), runnerID, detail)
}
