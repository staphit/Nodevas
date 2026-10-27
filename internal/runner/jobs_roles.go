package runner

// Role jobs on the runner side (agent roles, spec section 4): judge CHECK
// lines, the reviewer's tester-files acknowledgement, judge verdicts, and the
// draft PR a Tester run opens.
//
// RED PHASE STUB: declarations only, no behavior.

import "nodevas/internal/runnerwire"

// parseChecks reads "CHECK <id>: pass|fail — note" lines from a judge's final
// message, at most runnerwire.MaxCheckResults, notes capped.
func parseChecks(text string) []runnerwire.CheckResult { return nil }

// parseGuardAck reports whether a review's final message carries the line
// "TESTS: approve-changes", acknowledging the executor's changes to the
// Tester's files.
func parseGuardAck(text string) bool { return false }

// parseJudgeVerdict is a judge's "VERDICT: pass|fail" on the final message's
// last non-empty line, "" otherwise.
func parseJudgeVerdict(text string) string { return "" }

// prDraft reports whether the PR a run opens is a draft: the owner's
// setting, a failed run, or a Tester run (its tests are red by design).
func prDraft(cfg *Config, job runnerwire.Job, result runnerwire.RunResult) bool { return false }
