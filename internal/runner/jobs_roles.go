package runner

// Role jobs on the runner side (agent roles, spec section 4): judge CHECK
// lines, the reviewer's tester-files acknowledgement, judge verdicts, the
// draft PR a Tester run opens, and the role-aware `nodevas mcp` arguments.

import (
	"regexp"
	"slices"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// checkLine is "CHECK <id>: pass|fail[ — note]", markdown emphasis allowed.
var checkLine = regexp.MustCompile("(?i)^[\\s*_`>#-]*check[\\s*_`]+([A-Za-z0-9_-]{1,16})[\\s*_`]*[:：][\\s*_`]*(pass|fail)\\b[\\s*_`]*(?:[-—–:]+\\s*(.*))?$")

// parseChecks reads "CHECK <id>: pass|fail — note" lines from a judge's final
// message, at most runnerwire.MaxCheckResults, notes capped.
func parseChecks(text string) []runnerwire.CheckResult {
	var out []runnerwire.CheckResult
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		m := checkLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		note := strings.TrimSpace(strings.Trim(strings.TrimSpace(m[3]), "*_`"))
		out = append(out, runnerwire.CheckResult{
			ID:   m[1],
			Pass: strings.EqualFold(m[2], "pass"),
			Note: adapter.Truncate(note, runnerwire.MaxCheckNote),
		})
		if len(out) >= runnerwire.MaxCheckResults {
			break
		}
	}
	return out
}

// guardAckLine is the reviewer's acknowledgement of the Executor's changes
// to the Tester's files, on a line of its own.
var guardAckLine = regexp.MustCompile("(?i)^[\\s*_`>#-]*tests[\\s*_`]*[:：][\\s*_`]*approve-changes[\\s*_`.]*$")

// parseGuardAck reports whether a review's final message carries the line
// "TESTS: approve-changes", acknowledging the executor's changes to the
// Tester's files.
func parseGuardAck(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if guardAckLine.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

var judgeVerdictLine = regexp.MustCompile("(?i)^[\\s*_`>#-]*verdict[\\s*_`]*[:：][\\s*_`]*(pass|fail)[\\s*_`.]*$")

// parseJudgeVerdict is a judge's "VERDICT: pass|fail" on the final message's
// last non-empty line, "" otherwise.
func parseJudgeVerdict(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if m := judgeVerdictLine.FindStringSubmatch(strings.TrimSpace(lines[i])); m != nil {
			return strings.ToLower(m[1])
		}
		return ""
	}
	return ""
}

// prDraft reports whether the PR a run opens is a draft: the owner's
// setting, a failed run, or a Tester run (its tests are red by design).
func prDraft(cfg *Config, job runnerwire.Job, result runnerwire.RunResult) bool {
	return cfg.PRDraft || result.Status != runnerwire.RunSucceeded || job.Kind == runnerwire.JobTest
}

// toolName is what a tool name looks like; anything else (a flag, a space)
// never reaches the sidecar's command line.
var toolName = regexp.MustCompile(`^[a-z][a-z_]{0,63}$`)

// roleArgs are the role flags of a role job's sidecar: --role, and --tools
// with the hub's allowlist (names only) plus relayTools, the tools the run's
// permission relay needs whatever the role lists (adapter.RelayMCPTools).
// nil for a legacy job, whose run toolset has them already.
func roleArgs(job runnerwire.Job, relayTools ...string) []string {
	if job.Role == "" || !engine.ValidRoleID(job.Role) {
		return nil
	}
	var tools []string
	for _, tool := range append(slices.Clone(job.Tools), relayTools...) {
		if toolName.MatchString(tool) && !slices.Contains(tools, tool) {
			tools = append(tools, tool)
		}
	}
	args := []string{"--role", job.Role}
	if len(tools) > 0 {
		args = append(args, "--tools", strings.Join(tools, ","))
	}
	return args
}

// roleAgentClass is the write class a role job's sidecar declares: the
// role's rank for a built-in (orchestrator for the planner and QA), worker
// otherwise. The hub caps it at the run's role whatever is declared.
func roleAgentClass(job runnerwire.Job) string {
	if job.Role != "" && engine.RoleClass(nil, job.Role) == identity.AgentOrchestrator {
		return string(identity.AgentOrchestrator)
	}
	return string(identity.AgentWorker)
}
