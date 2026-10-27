package runner

// Verify jobs (agent roles, spec section 4): no agent, no MCP, no model. The
// runner runs the repository's own test command, configured here and never
// sent by the hub, in a throwaway worktree at the PR head, and reports the
// exit code. Red passes when the tests fail; green when they pass.
//
// RED PHASE STUB: declarations only, no behavior.

import (
	"context"
	"errors"
	"time"

	"nodevas/internal/runnerwire"
)

// DefaultTestTimeout bounds a test command without its own timeout.
const DefaultTestTimeout = 20 * time.Minute

// VerifyRequest is one verify run, built from this runner's own config and
// the job's phase and head only.
type VerifyRequest struct {
	// RepoPath is the owner's checkout; WorkRoot where the throwaway
	// worktree goes.
	RepoPath, WorkRoot string
	Phase              string
	// Head is the commit the job names; PRHead what the PR's head is now.
	// They must agree.
	Head, PRHead string
	// Command is the repo's test command from runner.json, argv-style.
	Command []string
	Timeout time.Duration
}

var errVerifyNotImplemented = errors.New("verify: not implemented")

// RunVerify runs req's command at req.Head and reports the result.
func RunVerify(ctx context.Context, req VerifyRequest) (runnerwire.VerifyResult, error) {
	return runnerwire.VerifyResult{}, errVerifyNotImplemented
}

// verifyRequestFor builds a verify request for job from this runner's config.
// Nothing the job carries but its phase and head is used. reason is why the
// job cannot run ("" when it can).
func verifyRequestFor(cfg *Config, job runnerwire.Job, repo runnerwire.Repo, prHead, workRoot string) (VerifyRequest, string) {
	return VerifyRequest{}, "not implemented"
}

// SetRepoTestCommand sets (or, with command "", clears) a repo's test command
// and timeout: `nodevas runner repo set <id|name> --test-command "..."`.
// The command is split argv-style; no shell unless the owner writes one.
func (c *Config) SetRepoTestCommand(arg, command string, timeoutMinutes int) (runnerwire.Repo, error) {
	return runnerwire.Repo{}, errVerifyNotImplemented
}

// RepoTestCommand is a repo's test command and timeout, as loaded at start.
func (c *Config) RepoTestCommand(repoID string) ([]string, time.Duration) { return nil, 0 }

// SetAllowVerify is `runner autoaccept verify on|off`.
func (c *Config) SetAllowVerify(on bool) {}
