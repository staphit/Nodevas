package gitrepo

import (
	"context"
	"errors"
)

// Read side for the Explorer's Git tab: the project's own commits and one
// commit's patch.

// MaxPatchBytes caps CommitPatch.
const MaxPatchBytes = 256 << 10

// RecentCommits is the repository's newest commits, at most limit.
func (r *Repo) RecentCommits(ctx context.Context, limit int) ([]Commit, error) {
	// TODO(green): implement.
	return nil, errors.New("not implemented")
}

// CommitPatch is commit sha's patch, cut at max bytes (truncated true).
func (r *Repo) CommitPatch(ctx context.Context, sha string, max int) (patch string, truncated bool, err error) {
	// TODO(green): implement.
	return "", false, errors.New("not implemented")
}
