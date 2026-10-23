package adapter

import (
	"regexp"

	"nodevas/internal/runnerwire"
)

// Failure classification (W5): a failed run's error text (the CLI's own
// result or error line, then the tail of its stderr) sorted into the
// runnerwire.Failure* kinds, so the hub can tell "another model may work"
// from "the task itself failed".
//
// Each adapter has one table. Rules are tried in order and the first match
// wins, so the specific comes before the general: a 429 that says the usage
// limit is reached is a quota, not a rate limit. Nothing matching is
// runnerwire.FailureTask: when in doubt, do not fall back.

// failureRule is one row of a table: a pattern and the kind it means.
type failureRule struct {
	kind string
	re   *regexp.Regexp
}

func rule(kind, pattern string) failureRule {
	return failureRule{kind: kind, re: regexp.MustCompile("(?i)" + pattern)}
}

// http429 is a 429 that is an HTTP status, not a 429 in an ID or a count.
const http429 = `(api error|http|status|code)\W{0,3}429\b|\b429 too many requests`

// shellMissing is the shell's word for a CLI (or its node runtime) that is
// not there, on Unix and on Windows (a .cmd shim whose node is gone).
var shellMissing = []failureRule{
	rule(runnerwire.FailureAgentAbsent, `command not found|is not recognized as an internal or external command`),
}

func classify(rules []failureRule, text string) string {
	for _, r := range rules {
		if r.re.MatchString(text) {
			return r.kind
		}
	}
	return runnerwire.FailureTask
}

// claudeFailures reads Claude Code's result text ("API Error: 429 …", "Claude
// AI usage limit reached|…", "Credit balance is too low", "Invalid API key ·
// Please run /login", "There's an issue with the selected model …").
var claudeFailures = append([]failureRule{
	rule(runnerwire.FailureAgentAbsent, `please run /login|invalid api key|not logged in|not authenticated|`+
		`oauth token has expired|authentication_error|\b401\b.{0,40}unauthori[sz]ed`),
	rule(runnerwire.FailureModel, `issue with the selected model|not_found_error.{0,80}model|model.{0,80}not_found_error|`+
		`model.{0,60}(does not exist|not found|not available|not supported)|invalid model`),
	rule(runnerwire.FailureQuota, `usage limit|limit reached|hit your limit|credit balance|out of credits|`+
		`billing|insufficient_quota|quota`),
	rule(runnerwire.FailureRateLimit, `rate.?limit|overloaded|`+http429+`|\b529\b`),
}, shellMissing...)

// codexFailures reads codex's error and turn.failed lines ("The 'x' model is
// not supported when using Codex with a ChatGPT account. (HTTP 400)",
// "exceeded retry limit, last status: 429 Too Many Requests", "You've hit
// your usage limit") and its stderr ("Not logged in … codex login").
var codexFailures = append([]failureRule{
	rule(runnerwire.FailureAgentAbsent, `codex login|not logged in|not signed in|not authenticated|`+
		`token_expired|refresh token|\b401\b.{0,40}unauthori[sz]ed`),
	rule(runnerwire.FailureModel, `not supported when using codex|model_not_found|unknown model|unsupported model|`+
		`model.{0,60}(does not exist|not found|not supported|not available)|does not have access to (the )?model`),
	rule(runnerwire.FailureQuota, `usage limit|usage_limit_reached|hit your limit|insufficient_quota|quota|credits|billing`),
	rule(runnerwire.FailureRateLimit, `rate.?limit|too many requests|`+http429+`|overloaded|server_is_overloaded`),
}, shellMissing...)

// geminiFailures reads the Gemini CLI's error lines and result ("Quota
// exceeded for quota metric …", RESOURCE_EXHAUSTED, "models/x is not found").
// Like the adapter itself, written from the CLI's documentation.
var geminiFailures = append([]failureRule{
	rule(runnerwire.FailureAgentAbsent, `please set an auth method|auth method|not authenticated|login required|`+
		`failed to login|gemini_api_key|unauthenticated|\b401\b`),
	rule(runnerwire.FailureModel, `model.{0,60}(is not found|not found|not supported|does not exist|not available)|`+
		`not found for api version`),
	rule(runnerwire.FailureQuota, `quota`),
	rule(runnerwire.FailureRateLimit, `resource_exhausted|rate.?limit|too many requests|`+http429+`|overloaded|\b503\b`),
}, shellMissing...)

// Classify sorts a failed Claude Code run.
func (Claude) Classify(text string) string { return classify(claudeFailures, text) }

// Classify sorts a failed Codex run.
func (Codex) Classify(text string) string { return classify(codexFailures, text) }

// Classify sorts a failed Gemini CLI run.
func (Gemini) Classify(text string) string { return classify(geminiFailures, text) }
