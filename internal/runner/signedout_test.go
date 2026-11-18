package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runnerwire"
)

// claudeAuthStatus is the command a look asks Claude Code whether it is
// signed in with. It reads the stored login and the environment only:
// nothing goes to the model.
const claudeAuthStatus = "/bin/claude auth status --json"

// What `claude auth status --json` printed on Claude Code 2.1.327, signed in
// (an API key in the environment) and not, cut to the fields read.
const (
	claudeSignedIn  = `{"loggedIn": true, "authMethod": "api_key", "apiProvider": "firstParty"}`
	claudeSignedOut = `{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty", "loginExpiresAt": null}`
)

// A Claude Code that says it is not signed in is there, but not ready: the
// app must not offer it. Anything else it says (signed in, an older CLI
// without the command, no answer) changes nothing.
func TestDetectClaudeSignedOut(t *testing.T) {
	cmds := &fakeCommands{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
		outputs: map[string]string{
			"/bin/claude --version": "2.1.327 (Claude Code)\n",
			"/bin/codex --version":  "codex-cli 0.149.1\n",
			claudeAuthStatus:        claudeSignedOut + "\n",
		},
		// It exits 1 when signed out, as the real one does.
		errs: map[string]error{claudeAuthStatus: errors.New("exit status 1")},
	}
	claude := agentNamed(t, Detect(context.Background(), cmds, &Config{}), "claude")
	if claude.Installed || !claude.SignedOut || claude.Version != "2.1.327 (Claude Code)" ||
		!strings.Contains(claude.Error, "not signed in") {
		t.Fatalf("signed-out claude = %+v", claude)
	}
	if cmds.ran("/bin/codex login status") != 0 {
		// Codex says "Not logged in" while an API key or its own provider
		// would run it fine: not a signal to trust.
		t.Fatalf("codex was asked about its login: %q", cmds.calls)
	}

	cmds.set(claudeAuthStatus, claudeSignedIn)
	cmds.errs = nil
	if claude := agentNamed(t, Detect(context.Background(), cmds, &Config{}), "claude"); !claude.Installed || claude.SignedOut || claude.Error != "" {
		t.Fatalf("signed-in claude = %+v", claude)
	}

	// An older Claude Code has no such command: it is not read as signed out.
	cmds.set(claudeAuthStatus, "")
	cmds.errs = map[string]error{claudeAuthStatus: errors.New("exit status 1: error: unknown option '--json'")}
	if claude := agentNamed(t, Detect(context.Background(), cmds, &Config{}), "claude"); !claude.Installed || claude.SignedOut {
		t.Fatalf("claude without auth status = %+v", claude)
	}

	// No answer in time: as before.
	cmds.errs = nil
	release := cmds.hang(claudeAuthStatus)
	defer release()
	p := newProber(cmds)
	p.versionTimeout = 50 * time.Millisecond
	agents, _ := p.look(context.Background(), &Config{})
	if claude := agentNamed(t, agents, "claude"); !claude.Installed || claude.SignedOut {
		t.Fatalf("claude that did not answer = %+v", claude)
	}
}

// A run that fails because Claude Code is signed out says so with a code,
// beside the CLI's own words, and the runner looks again at once, so the
// app stops offering it without waiting for the next look.
func TestSignedOutRunIsCoded(t *testing.T) {
	var cmds *fakeCommands
	env := startJobRunner(t, func(c *Client) {
		cmds = c.Commands.(*fakeCommands)
		cmds.outputs[claudeAuthStatus] = claudeSignedIn
	})
	if claude := agentNamed(t, env.hub.hello().Hello.Agents, "claude"); !claude.Installed {
		t.Fatalf("hello: %+v", claude)
	}
	// Signed out since the last look.
	cmds.set(claudeAuthStatus, claudeSignedOut)
	env.hub.offer(testJob("run-login", "login-fail:"))
	result := env.hub.wait("run-login", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureAgentAbsent ||
		result.FailureCode != runnerwire.FailureSignedOut || !strings.Contains(result.Error, "/login") {
		t.Fatalf("result = %+v", result)
	}
	hello := env.hub.hello()
	if claude := agentNamed(t, hello.Hello.Agents, "claude"); claude.Installed || !claude.SignedOut {
		t.Fatalf("hello after the run: %+v", claude)
	}

	// Offered it anyway (a hub that had not seen the hello yet): declined
	// with the code that says why.
	env.hub.offer(testJob("run-again", "ok:"))
	if decline := env.hub.wait("run-again", runnerwire.TypeDecline); decline.Code != runnerwire.DeclineAgentSignedOut {
		t.Fatalf("decline = %+v", decline)
	}

	// A task that failed on its own carries no code.
	cmds.set(claudeAuthStatus, claudeSignedIn)
	env.client.detect.again(context.Background(), 5*time.Second)
	env.hub.offer(testJob("run-fail", "fail:"))
	if result := env.hub.wait("run-fail", runnerwire.TypeFinish).Result; result.FailureCode != "" {
		t.Fatalf("task failure = %+v", result)
	}
}

// Once Claude Code is signed out, the runner keeps asking it (only `claude
// auth status`, which costs nothing) whether it is signed in again, far more
// often than its full looks. The moment it is, a full look tells the hub, so
// the person who signed in can start the task again without waiting minutes
// for the next look or knowing to press Check again.
func TestSignInIsNoticedSoon(t *testing.T) {
	var cmds *fakeCommands
	env := startJobRunner(t, func(c *Client) {
		cmds = c.Commands.(*fakeCommands)
		cmds.outputs[claudeAuthStatus] = claudeSignedIn
		c.RedetectInterval = time.Hour
		c.signInRecheck = 10 * time.Millisecond
	})
	if claude := agentNamed(t, env.hub.hello().Hello.Agents, "claude"); !claude.Installed {
		t.Fatalf("hello: %+v", claude)
	}
	// Signed in: nothing is asked between looks.
	asked := cmds.ran(claudeAuthStatus)
	time.Sleep(100 * time.Millisecond)
	if n := cmds.ran(claudeAuthStatus); n != asked {
		t.Fatalf("signed-in claude asked about its login %d times between looks", n-asked)
	}

	cmds.set(claudeAuthStatus, claudeSignedOut)
	env.hub.offer(testJob("run-login", "login-fail:"))
	env.hub.wait("run-login", runnerwire.TypeFinish)
	if claude := agentNamed(t, env.hub.hello().Hello.Agents, "claude"); claude.Installed || !claude.SignedOut {
		t.Fatalf("hello after the run: %+v", claude)
	}

	// Still signed out: only its login is asked, no full look (no
	// --version, which every look runs for every CLI).
	versions := cmds.ran("/bin/codex --version")
	asked = cmds.ran(claudeAuthStatus)
	eventually(t, "a few checks of the login", func() bool { return cmds.ran(claudeAuthStatus) >= asked+3 })
	if n := cmds.ran("/bin/codex --version"); n != versions {
		t.Fatalf("a signed-out claude started %d full looks", n-versions)
	}

	// Signed in again: the hub hears it without anyone asking.
	cmds.set(claudeAuthStatus, claudeSignedIn)
	if claude := agentNamed(t, env.hub.hello().Hello.Agents, "claude"); !claude.Installed || claude.SignedOut {
		t.Fatalf("hello after signing in: %+v", claude)
	}
}

// A CLI gone from PATH is agent_unavailable too, but no sign-in fixes it.
func TestMissingAgentIsNotSignedOut(t *testing.T) {
	c := &Client{Config: &Config{}, Commands: &fakeCommands{paths: map[string]string{}}}
	j := newJobs(context.Background(), c)
	r := &run{job: testJob("run-x", "ok:")}
	result, _ := j.runAgent(context.Background(), r, Worktree{Path: t.TempDir()}, r.job)
	if result.FailureKind != runnerwire.FailureAgentAbsent || result.FailureCode != "" {
		t.Fatalf("result = %+v", result)
	}
}
