package runner

import (
	"context"

	"nodevas/internal/runnerwire"
)

// Read-only repo browsing for the hub's Explorer (runnerwire/repobrowse.go):
// the runner's owner may list, read and see git state of a registered repo,
// never anything outside it, never a secret, never a write.

// RepoBrowseOn is RepoBrowse with its default (on) applied.
func (c *Config) RepoBrowseOn() bool {
	// TODO(green): implement.
	return true
}

// BrowseRepo answers one repo_request for the runner whose owner is
// ownerID.
func BrowseRepo(ctx context.Context, cfg *Config, ownerID string, req runnerwire.RepoRequest) runnerwire.RepoDone {
	// TODO(green): implement.
	return runnerwire.RepoDone{ID: req.ID, Code: "unimplemented", Error: "repo browsing is not implemented"}
}
