package runs

// The executor diff guard of a staged pipeline (agent roles, spec 2.7). The
// hub computes it from the changes the runner reported after the agent
// exited, so the agent cannot shape it.

import (
	"slices"

	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// Guard checks an executor's changes against the Tester's files: a touched
// test file (by path, rename source or deletion) or a sensitive path (test
// infrastructure that could neuter the test command) is a violation.
func Guard(testFiles []string, executor []runnerwire.FileChange) runnerwire.GuardReport {
	report := runnerwire.GuardReport{TestFiles: slices.Clone(testFiles)}
	protected := make(map[string]bool, len(testFiles))
	for _, p := range testFiles {
		protected[p] = true
	}
	add := func(list *[]string, p string) {
		if !slices.Contains(*list, p) {
			*list = append(*list, p)
		}
	}
	for _, change := range executor {
		for _, p := range []string{change.Path, change.OldPath} {
			if p == "" {
				continue
			}
			if protected[p] {
				add(&report.Touched, p)
			}
			if SensitivePath(p) {
				add(&report.Sensitive, p)
			}
		}
	}
	slices.Sort(report.Touched)
	slices.Sort(report.Sensitive)
	return report
}

// executorTestFiles lists the paths the Executor changed that match the test
// globs but are not the Tester's files.
func executorTestFiles(globs, testFiles []string, executor []runnerwire.FileChange) []string {
	if len(globs) == 0 {
		globs = engine.DefaultTestGlobs
	}
	var out []string
	for _, change := range executor {
		for _, p := range []string{change.Path, change.OldPath} {
			if p != "" && !slices.Contains(testFiles, p) && engine.MatchAnyGlob(globs, p) && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// NonTestFiles lists the Tester-changed paths that match none of globs; a
// Tester round that changes any fails. A rename's source counts too.
func NonTestFiles(globs []string, changes []runnerwire.FileChange) []string {
	if len(globs) == 0 {
		globs = engine.DefaultTestGlobs
	}
	var out []string
	for _, change := range changes {
		for _, p := range []string{change.Path, change.OldPath} {
			if p != "" && !engine.MatchAnyGlob(globs, p) && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

// changedPaths lists every path (and rename source) in changes, sorted.
func changedPaths(changes []runnerwire.FileChange) []string {
	var out []string
	for _, change := range changes {
		for _, p := range []string{change.Path, change.OldPath} {
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}
