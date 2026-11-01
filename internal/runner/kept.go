package runner

import "context"

// KeptWorktree is a run's worktree the runner kept because its commit left
// agent config out.
type KeptWorktree struct {
	RunID    string   `json:"runId"`
	Repo     string   `json:"repo"`
	Path     string   `json:"path"`
	Branch   string   `json:"branch,omitempty"`
	Head     string   `json:"head,omitempty"`
	GitFile  string   `json:"gitFile"`
	GitDirs  string   `json:"gitDirs"`
	Withheld []string `json:"withheld"`
	KeptAt   string   `json:"keptAt"`
}

// KeptSkip is a kept worktree PruneKept left alone, and why.
type KeptSkip struct {
	KeptWorktree
	Reason string
}

// PruneReport is what PruneKept removed (or, in a dry run, would remove)
// and what it left alone.
type PruneReport struct {
	Removed []KeptWorktree
	Skipped []KeptSkip
}

// KeptWorktrees lists the kept worktrees still on disk.
func KeptWorktrees(dataDir string) ([]KeptWorktree, error) { return nil, nil }

// RecordKept notes a worktree kept for withheld agent config.
func RecordKept(ctx context.Context, dataDir, repoPath string, wt Worktree, withheld []string) error {
	return nil
}

// PruneKept removes the kept worktrees that are safe to remove.
func PruneKept(ctx context.Context, dataDir string, dryRun bool) (PruneReport, error) {
	return PruneReport{}, nil
}
