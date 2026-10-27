package runners

import (
	"context"
	"errors"
	"time"

	"nodevas/internal/runnerwire"
)

// Read-only repo browsing relayed to a person's own runner
// (runnerwire/repobrowse.go). The hub only forwards; git and the disk are
// the runner's.

// Repo relay refusals.
var (
	ErrRepoNotFound   = errors.New("runner or repo not found")
	ErrRunnerOffline  = errors.New("the runner is offline")
	ErrRepoBrowseOff  = errors.New("repo browsing is off on this runner")
	ErrRepoTimeout    = errors.New("the runner did not answer in time")
	ErrRepoRunnerGone = errors.New("the runner disconnected before it answered")
)

// DefaultRepoTimeout bounds one browse call.
const DefaultRepoTimeout = 15 * time.Second

// RepoRelay carries repo browse calls to runners and their answers back.
type RepoRelay struct {
	reg *Registry
	// Timeout overrides DefaultRepoTimeout; tests shorten it.
	Timeout time.Duration
}

// NewRepoRelay is a relay over reg.
func NewRepoRelay(reg *Registry) *RepoRelay { return &RepoRelay{reg: reg} }

// Browse sends req to runnerID, which must be accountID's, online, able to
// browse and holding req.RepoID, and waits for its answer.
func (r *RepoRelay) Browse(ctx context.Context, accountID, runnerID string, req runnerwire.RepoRequest) (runnerwire.RepoDone, error) {
	// TODO(green): implement.
	return runnerwire.RepoDone{}, errors.New("not implemented")
}

// Deliver hands a runner's repo_done to the call waiting on it. An answer
// from any other runner than the one asked, or an oversized one, is
// dropped.
func (r *RepoRelay) Deliver(runnerID string, env runnerwire.Envelope) error {
	// TODO(green): implement.
	return nil
}
