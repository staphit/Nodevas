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

// Repo admin from the hub (runnerwire/repoadmin.go): on a single-user hub
// the owner adds, removes and sets up the built-in runner's repos from the
// app. The hub only relays, like settings; the runner checks who asks, that
// it is the built-in runner, and every folder against its own guard, and
// writes its own config file. A team member's runner adds repos on that PC.

// ErrRepoAdminOff: the runner is not this computer's built-in runner (or is
// from before repo admin): repos are added on the runner's own computer.
var ErrRepoAdminOff = locale.Errorf("add repos on the runner's own computer (in Nodevas Runner there)",
	"請在 runner 所在的電腦上新增 repo（在那台電腦的 Nodevas Runner 裡）")

// DefaultRepoAdminTimeout bounds one repo admin op, and
// DefaultRepoAdminInitTimeout an init_add, which saves a whole folder as the
// first version. Vars so tests of the whole server, which cannot reach the
// relay's Timeout, can shorten them.
var (
	DefaultRepoAdminTimeout     = runnerwire.RepoAdminTimeoutSeconds * time.Second
	DefaultRepoAdminInitTimeout = runnerwire.RepoAdminInitTimeoutSeconds * time.Second
)

// Limits on what a repo_admin_done may carry back, besides the socket's.
const (
	maxInitIgnored = 64
)

// RepoAdminRelay carries repo admin requests to runners and their answers
// back.
type RepoAdminRelay struct {
	reg *Registry
	// Timeout overrides DefaultRepoAdminTimeout; tests shorten it.
	Timeout time.Duration

	mu    sync.Mutex
	calls map[string]*repoAdminCall
}

type repoAdminCall struct {
	runnerID string
	done     chan runnerwire.RepoAdminDone
}

// NewRepoAdminRelay is a relay over reg.
func NewRepoAdminRelay(reg *Registry) *RepoAdminRelay {
	return &RepoAdminRelay{reg: reg, calls: map[string]*repoAdminCall{}}
}

// Blocked says why runner's repos cannot be changed from the hub now:
// ErrRepoAdminOff (not the built-in runner, or one that does not take repo
// admin), ErrRunnerOffline, or nil.
func (r *RepoAdminRelay) Blocked(runner runnerwire.Runner) error {
	if !runner.BuiltIn || runner.Info == nil || !slices.Contains(runner.Info.Capabilities, runnerwire.CapRepoAdmin) {
		return ErrRepoAdminOff
	}
	if !r.reg.Online(runner.ID) {
		return ErrRunnerOffline
	}
	return nil
}

// Request sends req to runnerID, which must be req.AccountID's, online and
// the built-in runner, and waits for its answer. The runner says hello with
// its new repos before it answers, so once Request returns the registry
// holds them.
func (r *RepoAdminRelay) Request(ctx context.Context, runnerID string, req runnerwire.RepoAdminRequest) (runnerwire.RepoAdminDone, error) {
	if req.AccountID == "" || runnerID == "" {
		return runnerwire.RepoAdminDone{}, ErrNotFound
	}
	runner, err := r.reg.Get(ctx, runnerID)
	if err != nil {
		return runnerwire.RepoAdminDone{}, err
	}
	// Only the runner's owner; an administrator's account is no exception:
	// the disk is its owner's.
	if runner.OwnerID != req.AccountID {
		return runnerwire.RepoAdminDone{}, ErrNotFound
	}
	if err := r.Blocked(runner); err != nil {
		return runnerwire.RepoAdminDone{}, err
	}

	id, err := newCallID()
	if err != nil {
		return runnerwire.RepoAdminDone{}, err
	}
	req.ID = id
	call := &repoAdminCall{runnerID: runnerID, done: make(chan runnerwire.RepoAdminDone, 1)}
	r.mu.Lock()
	if r.calls == nil {
		r.calls = map[string]*repoAdminCall{}
	}
	r.calls[id] = call
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.calls, id)
		r.mu.Unlock()
	}()
	if err := r.reg.Send(runnerID, runnerwire.Envelope{Type: runnerwire.TypeRepoAdmin, RepoAdmin: &req}); err != nil {
		if errors.Is(err, ErrOffline) {
			return runnerwire.RepoAdminDone{}, ErrRunnerOffline
		}
		return runnerwire.RepoAdminDone{}, err
	}
	timeout := r.Timeout
	switch {
	case timeout > 0:
	case req.Op == runnerwire.RepoAdminInitAdd:
		timeout = DefaultRepoAdminInitTimeout
	default:
		timeout = DefaultRepoAdminTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	check := time.NewTicker(time.Second)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			return runnerwire.RepoAdminDone{}, ctx.Err()
		case <-deadline.C:
			return runnerwire.RepoAdminDone{}, ErrRepoTimeout
		case <-check.C:
			if !r.reg.Online(runnerID) {
				return runnerwire.RepoAdminDone{}, ErrRepoRunnerGone
			}
		case done := <-call.done:
			return done, nil
		}
	}
}

// Deliver hands a runner's repo_admin_done to the request waiting on it. An
// answer from any other runner than the one asked, or one nobody waits for
// any more, is dropped.
func (r *RepoAdminRelay) Deliver(runnerID string, env runnerwire.Envelope) error {
	if env.RepoAdminDone == nil || env.RepoAdminDone.ID == "" {
		return nil
	}
	r.mu.Lock()
	call := r.calls[env.RepoAdminDone.ID]
	if call != nil && call.runnerID == runnerID {
		delete(r.calls, env.RepoAdminDone.ID)
	} else {
		call = nil
	}
	r.mu.Unlock()
	if call == nil {
		return nil
	}
	select {
	case call.done <- clampRepoAdminDone(*env.RepoAdminDone):
	default:
	}
	return nil
}

// clampRepoAdminDone keeps an answer within what the web shows: the runner
// keeps to these limits, and the hub holds it to them.
func clampRepoAdminDone(done runnerwire.RepoAdminDone) runnerwire.RepoAdminDone {
	done.Code = clip(done.Code, maxShortField)
	done.Error = clip(done.Error, runnerwire.MaxRepoAdminError)
	if done.Repo != nil {
		repo := clampRepo(*done.Repo)
		done.Repo = &repo
	}
	if done.Inspect != nil {
		inspect := *done.Inspect
		inspect.Path = clip(inspect.Path, maxPathField)
		inspect.Top = clip(inspect.Top, maxPathField)
		inspect.State = clip(inspect.State, maxShortField)
		inspect.Refused = clip(inspect.Refused, maxShortField)
		if inspect.Repo != nil {
			repo := clampRepo(*inspect.Repo)
			inspect.Repo = &repo
		}
		if inspect.Init != nil {
			plan := *inspect.Init
			plan.Hash = clip(plan.Hash, maxShortField)
			ignored := make([]string, 0, min(len(plan.Ignored), maxInitIgnored))
			for _, pattern := range plan.Ignored {
				if len(ignored) == maxInitIgnored {
					break
				}
				ignored = append(ignored, clip(pattern, maxShortField))
			}
			plan.Ignored = ignored
			inspect.Init = &plan
		}
		done.Inspect = &inspect
	}
	return done
}

func clampRepo(repo runnerwire.Repo) runnerwire.Repo {
	repo.ID = clip(repo.ID, maxShortField)
	repo.Name = clip(repo.Name, maxShortField)
	repo.Path = clip(repo.Path, maxPathField)
	repo.Remote = clip(repo.Remote, maxPathField)
	return repo
}

// clip cuts s to at most n bytes of valid UTF-8.
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
