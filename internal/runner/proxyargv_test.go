package runner

import (
	"regexp"
	"testing"

	"nodevas/internal/runnerwire"
)

// proxySecretOnArgv matches a run's proxy URL: /r/ and its secret.
var proxySecretOnArgv = regexp.MustCompile(`/r/[0-9a-f]{32}`)

// A run's proxy secret is never on a command line, which every user on the
// machine can read (/proc/<pid>/cmdline): not on the `nodevas mcp` sidecar's,
// whose arguments Claude takes from its MCP config, and not on Codex's own,
// which carries the sidecar's arguments in -c.
func TestRunProxySecretIsNotOnACommandLine(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-argv", "ok:"))
	env.hub.wait("run-argv", runnerwire.TypeFinish)
	if mcp := env.recorded(t, "run-argv", "mcp.json"); proxySecretOnArgv.MatchString(mcp) {
		t.Errorf("the sidecar's command line in Claude's MCP config carries the proxy secret: %s", mcp)
	}

	codex := testJob("run-argv-codex", "codex-ok:")
	codex.Agent, codex.Model = "codex", "default"
	env.hub.offer(codex)
	env.hub.wait("run-argv-codex", runnerwire.TypeFinish)
	if args := env.recorded(t, "run-argv-codex", "args"); proxySecretOnArgv.MatchString(args) {
		t.Errorf("codex's command line carries the proxy secret: %s", args)
	}
}
