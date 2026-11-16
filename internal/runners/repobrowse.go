package runners

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

// Read-only repo browsing relayed to a person's own runner
// (runnerwire/repobrowse.go). The hub only forwards; git and the disk are
// the runner's.

// Repo relay refusals.
var (
	ErrRepoNotFound   = locale.Errorf("runner or repo not found", "找不到 runner 或 repo")
	ErrRunnerOffline  = locale.Errorf("the runner is offline", "runner 離線中")
	ErrRepoBrowseOff  = locale.Errorf("repo browsing is off on this runner", "這個 runner 關閉了 repo 瀏覽")
	ErrRepoTimeout    = locale.Errorf("the runner did not answer in time", "runner 沒有及時回應")
	ErrRepoRunnerGone = locale.Errorf("the runner disconnected before it answered", "runner 在回應前就斷線了")
	// ErrRunnerOutdated: the runner is from before run workspaces
	// (runnerwire.CapRunWorkspace).
	ErrRunnerOutdated = locale.Errorf("update nodevas on the runner to see run workspaces", "請更新 runner 上的 nodevas 才能查看執行工作區")
	// ErrRunPreviewOff: run_preview on a runner that is not this hub's
	// built-in one, or whose hello lacks runnerwire.CapRunPreview.
	ErrRunPreviewOff = locale.Errorf("previews are off on this runner", "這個 runner 關閉了預覽")
)

// DefaultRepoTimeout bounds one browse call.
const DefaultRepoTimeout = 15 * time.Second

// RepoRelay carries repo browse calls to runners and their answers back.
type RepoRelay struct {
	reg *Registry
	// Timeout overrides DefaultRepoTimeout; tests shorten it.
	Timeout time.Duration

	mu    sync.Mutex
	calls map[string]*repoCall
}

type repoCall struct {
	runnerID string
	done     chan runnerwire.RepoDone
}

// NewRepoRelay is a relay over reg.
func NewRepoRelay(reg *Registry) *RepoRelay {
	return &RepoRelay{reg: reg, calls: map[string]*repoCall{}}
}

// Browse sends req to runnerID, which must be accountID's, online, able to
// browse and holding req.RepoID, and waits for its answer.
func (r *RepoRelay) Browse(ctx context.Context, accountID, runnerID string, req runnerwire.RepoRequest) (runnerwire.RepoDone, error) {
	if accountID == "" || runnerID == "" {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	runner, err := r.reg.Get(ctx, runnerID)
	if errors.Is(err, ErrNotFound) {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	if err != nil {
		return runnerwire.RepoDone{}, err
	}
	// Only the asker's own runner; an administrator's account is no
	// exception: the disk is its owner's.
	if runner.OwnerID != accountID {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	if !r.reg.Online(runnerID) {
		return runnerwire.RepoDone{}, ErrRunnerOffline
	}
	// Run-scoped ops go through BrowseRun only.
	if runnerwire.RunRepoOp(req.Op) {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	if runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapRepoBrowse) {
		return runnerwire.RepoDone{}, ErrRepoBrowseOff
	}
	if !slices.ContainsFunc(runner.Info.Repos, func(repo runnerwire.Repo) bool { return repo.ID == req.RepoID }) {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	return r.call(ctx, accountID, runnerID, req)
}

// BrowseRun is Browse for one run's worktree (runnerwire.RunRepoOp):
// req.RunID names the run, which the caller has checked is one of
// runnerID's runs in req.RepoID. The runner must know run workspaces, and,
// for anything but run_info, browse repos.
func (r *RepoRelay) BrowseRun(ctx context.Context, accountID, runnerID string, req runnerwire.RepoRequest) (runnerwire.RepoDone, error) {
	if accountID == "" || runnerID == "" || req.RunID == "" || !runnerwire.RunRepoOp(req.Op) {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	runner, err := r.reg.Get(ctx, runnerID)
	if errors.Is(err, ErrNotFound) {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	if err != nil {
		return runnerwire.RepoDone{}, err
	}
	// The run's own runner's owner only; administrators are no exception.
	if runner.OwnerID != accountID {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	// A preview needs this hub's built-in runner, as its welcome said, and
	// its say-so: a capability alone (a member's runner can claim anything)
	// is not enough, nor is being built in with the preview turned off.
	if req.Op == runnerwire.RepoOpRunPreview &&
		(!runner.BuiltIn || runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapRunPreview)) {
		return runnerwire.RepoDone{}, ErrRunPreviewOff
	}
	if !r.reg.Online(runnerID) {
		return runnerwire.RepoDone{}, ErrRunnerOffline
	}
	if runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapRunWorkspace) {
		return runnerwire.RepoDone{}, ErrRunnerOutdated
	}
	if req.Op != runnerwire.RepoOpRunInfo && !slices.Contains(runner.Info.Capabilities, runnerwire.CapRepoBrowse) {
		return runnerwire.RepoDone{}, ErrRepoBrowseOff
	}
	if !slices.ContainsFunc(runner.Info.Repos, func(repo runnerwire.Repo) bool { return repo.ID == req.RepoID }) {
		return runnerwire.RepoDone{}, ErrRepoNotFound
	}
	return r.call(ctx, accountID, runnerID, req)
}

// call sends req to runnerID and waits for its answer.
func (r *RepoRelay) call(ctx context.Context, accountID, runnerID string, req runnerwire.RepoRequest) (runnerwire.RepoDone, error) {
	id, err := newCallID()
	if err != nil {
		return runnerwire.RepoDone{}, err
	}
	req.ID, req.AccountID = id, accountID
	call := &repoCall{runnerID: runnerID, done: make(chan runnerwire.RepoDone, 1)}
	r.mu.Lock()
	if r.calls == nil {
		r.calls = map[string]*repoCall{}
	}
	r.calls[id] = call
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.calls, id)
		r.mu.Unlock()
	}()
	if err := r.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeRepoRequest, Repo: &req}); err != nil {
		if errors.Is(err, ErrOffline) {
			return runnerwire.RepoDone{}, ErrRunnerOffline
		}
		return runnerwire.RepoDone{}, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultRepoTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			return runnerwire.RepoDone{}, ctx.Err()
		case <-deadline.C:
			return runnerwire.RepoDone{}, ErrRepoTimeout
		case <-check.C:
			if !r.reg.Online(runnerID) {
				return runnerwire.RepoDone{}, ErrRepoRunnerGone
			}
		case done := <-call.done:
			return done, nil
		}
	}
}

// Deliver hands a runner's repo_done to the call waiting on it. An answer
// from any other runner than the one asked, or an oversized one, is
// dropped.
func (r *RepoRelay) Deliver(runnerID string, env runnerwire.Envelope) error {
	if env.RepoDone == nil || env.RepoDone.ID == "" {
		return nil
	}
	raw, err := json.Marshal(env.RepoDone)
	if err != nil || len(raw) > runnerwire.MaxRepoDoneBytes {
		return nil
	}
	r.mu.Lock()
	call := r.calls[env.RepoDone.ID]
	if call != nil && call.runnerID == runnerID {
		delete(r.calls, env.RepoDone.ID)
	} else {
		call = nil
	}
	r.mu.Unlock()
	if call == nil {
		return nil
	}
	select {
	case call.done <- *env.RepoDone:
	default:
	}
	return nil
}
