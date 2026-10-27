package runs

// The executor diff guard of a staged pipeline (agent roles, spec 2.7).
//
// RED PHASE STUB: declarations only, no behavior.

import "nodevas/internal/runnerwire"

// Guard checks an executor's changes against the Tester's files: a touched
// test file (by path, rename source or deletion) or a sensitive path (test
// infrastructure that could neuter the test command) is a violation.
func Guard(testFiles []string, executor []runnerwire.FileChange) runnerwire.GuardReport {
	return runnerwire.GuardReport{}
}

// NonTestFiles lists the Tester-changed paths that match none of globs; a
// Tester round that changes any fails.
func NonTestFiles(globs []string, changes []runnerwire.FileChange) []string { return nil }
