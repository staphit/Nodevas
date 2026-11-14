package runners

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// Runner settings from the hub (runnerwire/settings.go): the runner's owner
// changes how many runs it takes at once from the web. The hub only relays;
// the runner checks who asks, its own kill switch and its own ceiling, and
// writes its own config file.

// Settings relay refusals, beside the repo relay's ErrRunnerOffline,
// ErrRepoTimeout and ErrRepoRunnerGone.
var (
	// ErrSettingsOff: the runner's owner turned remoteSettings off.
	ErrSettingsOff = locale.Errorf("the runner's owner allows changing this only on that machine (remoteSettings)",
		"runner 的擁有者只允許在那台電腦上變更（remoteSettings）")
	// ErrSettingsOutdated: the runner is from before remote settings.
	ErrSettingsOutdated = locale.Errorf("update nodevas on the runner to change its settings from here",
		"請更新 runner 上的 nodevas 才能在這裡變更它的設定")
	// ErrSettingsLocalOnly: what an agent may do, when to ask and the PR
	// options are changed from the app only for this computer's built-in
	// runner; a team member's runner is set up on that PC.
	ErrSettingsLocalOnly = locale.Errorf("change this on the runner's own computer",
		"請在 runner 所在的電腦上變更這項設定")
)

// LocalOnly reports whether req asks for more than maxConcurrent: settings
// only the built-in runner takes from the app (runnerwire.SettingsRequest).
func LocalOnly(req runnerwire.SettingsRequest) bool {
	return len(req.Permission) > 0 || req.AskEveryRun != nil || len(req.ShellWithoutAsking) > 0 ||
		req.AutoPR != nil || req.PRDraft != nil || req.Redetect
}

// DefaultSettingsTimeout bounds one settings change, and
// DefaultSettingsRedetectTimeout one that looks for the agent CLIs again.
// Vars so tests of the whole server, which cannot reach the relay's Timeout,
// can shorten them.
var (
	DefaultSettingsTimeout         = runnerwire.SettingsTimeoutSeconds * time.Second
	DefaultSettingsRedetectTimeout = runnerwire.SettingsRedetectTimeoutSeconds * time.Second
)

// SettingsRelay carries settings requests to runners and their answers back.
type SettingsRelay struct {
	reg *Registry
	// Timeout overrides DefaultSettingsTimeout; tests shorten it.
	Timeout time.Duration

	mu    sync.Mutex
	calls map[string]*settingsCall
}

type settingsCall struct {
	runnerID string
	done     chan runnerwire.SettingsDone
}

// NewSettingsRelay is a relay over reg.
func NewSettingsRelay(reg *Registry) *SettingsRelay {
	return &SettingsRelay{reg: reg, calls: map[string]*settingsCall{}}
}

// SettingsBlocked says why runner's settings cannot be changed from the hub
// now: ErrRunnerOffline, ErrSettingsOff (it knows remote settings but its
// owner turned them off, or allows no ceiling), ErrSettingsOutdated, or nil.
// Remote settings came with global agents: a runner that takes global runs
// and does not say remote-settings has the setting turned off; one from
// before them (a phase-1 runner knows run workspaces, not this) is outdated.
func (r *SettingsRelay) SettingsBlocked(runner runnerwire.Runner) error {
	if !r.reg.Online(runner.ID) {
		return ErrRunnerOffline
	}
	return settingsBlocked(runner.Info)
}

// Settable is RunnerLoad.Settable: "" when a runner's settings can be
// changed from the hub now, else runnerwire.RunnerBlockedOffline,
// RunnerBlockedOutdated or RunnerBlockedSettingsOff.
func Settable(online bool, info *runnerwire.Info) string {
	if !online {
		return runnerwire.RunnerBlockedOffline
	}
	switch settingsBlocked(info) {
	case nil:
		return ""
	case ErrSettingsOff:
		return runnerwire.RunnerBlockedSettingsOff
	default:
		return runnerwire.RunnerBlockedOutdated
	}
}

func settingsBlocked(info *runnerwire.Info) error {
	switch {
	case info != nil && slices.Contains(info.Capabilities, runnerwire.CapRemoteSettings):
		if info.MaxConcurrentCeiling <= 0 {
			return ErrSettingsOff
		}
		return nil
	case info != nil && slices.Contains(info.Capabilities, runnerwire.CapGlobalRuns):
		return ErrSettingsOff
	default:
		return ErrSettingsOutdated
	}
}

// LocalBlocked says why the built-in runner's own settings (LocalOnly)
// cannot be changed from the hub now: ErrSettingsLocalOnly for any runner
// but the built-in one, ErrRunnerOffline, or nil.
func (r *SettingsRelay) LocalBlocked(runner runnerwire.Runner) error {
	if !runner.BuiltIn {
		return ErrSettingsLocalOnly
	}
	if !r.reg.Online(runner.ID) {
		return ErrRunnerOffline
	}
	return nil
}

// Request sends req to runnerID, which must be req.AccountID's, online and
// taking remote settings (for maxConcurrent) or the built-in runner (for
// anything else), and waits for its answer. The runner sends its new hello
// before the answer, so once Request returns the registry holds it.
func (r *SettingsRelay) Request(ctx context.Context, runnerID string, req runnerwire.SettingsRequest) (runnerwire.SettingsDone, error) {
	if req.AccountID == "" || runnerID == "" {
		return runnerwire.SettingsDone{}, ErrNotFound
	}
	runner, err := r.reg.Get(ctx, runnerID)
	if err != nil {
		return runnerwire.SettingsDone{}, err
	}
	// Only the runner's owner; an administrator's account is no exception:
	// the machine is its owner's.
	if runner.OwnerID != req.AccountID {
		return runnerwire.SettingsDone{}, ErrNotFound
	}
	if LocalOnly(req) {
		if err := r.LocalBlocked(runner); err != nil {
			return runnerwire.SettingsDone{}, err
		}
	}
	if req.MaxConcurrent != nil {
		if err := r.SettingsBlocked(runner); err != nil {
			return runnerwire.SettingsDone{}, err
		}
	} else if !LocalOnly(req) {
		return runnerwire.SettingsDone{}, ErrInvalid
	}

	id, err := newCallID()
	if err != nil {
		return runnerwire.SettingsDone{}, err
	}
	req.ID = id
	call := &settingsCall{runnerID: runnerID, done: make(chan runnerwire.SettingsDone, 1)}
	r.mu.Lock()
	if r.calls == nil {
		r.calls = map[string]*settingsCall{}
	}
	r.calls[id] = call
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.calls, id)
		r.mu.Unlock()
	}()
	if err := r.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeSettingsRequest, Settings: &req}); err != nil {
		if errors.Is(err, ErrOffline) {
			return runnerwire.SettingsDone{}, ErrRunnerOffline
		}
		return runnerwire.SettingsDone{}, err
	}
	timeout := r.Timeout
	switch {
	case timeout > 0:
	case req.Redetect:
		timeout = DefaultSettingsRedetectTimeout
	default:
		timeout = DefaultSettingsTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	check := time.NewTicker(time.Second)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			return runnerwire.SettingsDone{}, ctx.Err()
		case <-deadline.C:
			return runnerwire.SettingsDone{}, ErrRepoTimeout
		case <-check.C:
			if !r.reg.Online(runnerID) {
				return runnerwire.SettingsDone{}, ErrRepoRunnerGone
			}
		case done := <-call.done:
			return done, nil
		}
	}
}

// Deliver hands a runner's settings_done to the request waiting on it. An
// answer from any other runner than the one asked, or one nobody waits for
// any more, is dropped.
func (r *SettingsRelay) Deliver(runnerID string, env runnerwire.Envelope) error {
	if env.SettingsDone == nil || env.SettingsDone.ID == "" {
		return nil
	}
	r.mu.Lock()
	call := r.calls[env.SettingsDone.ID]
	if call != nil && call.runnerID == runnerID {
		delete(r.calls, env.SettingsDone.ID)
	} else {
		call = nil
	}
	r.mu.Unlock()
	if call == nil {
		return nil
	}
	done := *env.SettingsDone
	if len(done.Error) > runnerwire.MaxSettingsError {
		done.Error = strings.ToValidUTF8(done.Error[:runnerwire.MaxSettingsError], "")
	}
	select {
	case call.done <- done:
	default:
	}
	return nil
}
