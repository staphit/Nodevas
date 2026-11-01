package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kept worktrees: a run whose commit left agent config out keeps its
// worktree, since that config lives nowhere else (dropWorktree). Each one is
// recorded in <dataDir>/kept-worktrees.json, so `nodevas runner status` can
// show them and `nodevas runner prune-kept` can remove, when a person asks,
// the ones that hold nothing else. Nothing removes them on its own.

const (
	keptFile = "kept-worktrees.json"
	// maxKept bounds the record; past it the oldest entries are forgotten
	// (their worktrees stay, only unlisted).
	maxKept = 1000
	// maxKeptReasonPaths is how many paths a skip reason names.
	maxKeptReasonPaths = 5
)

// keptMu serialises this process's reads and writes of the record.
var keptMu sync.Mutex

// KeptWorktree is a run's worktree the runner kept because its commit left
// agent config out.
type KeptWorktree struct {
	RunID string `json:"runId"`
	// Repo is the repository the worktree belongs to.
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	// Head is the worktree's HEAD when it was kept.
	Head string `json:"head,omitempty"`
	// GitFile and GitDirs are Worktree's: where its .git pointed before any
	// agent ran there.
	GitFile string `json:"gitFile"`
	GitDirs string `json:"gitDirs"`
	// Withheld is the agent config the commit left out.
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

func keptPath(dataDir string) string { return filepath.Join(dataDir, keptFile) }

func loadKept(dataDir string) ([]KeptWorktree, error) {
	raw, err := os.ReadFile(keptPath(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []KeptWorktree
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", keptFile, err)
	}
	return entries, nil
}

// RecordKept notes a worktree kept because its commit left the withheld
// agent config out. A worktree already recorded is recorded afresh.
func RecordKept(ctx context.Context, dataDir, repoPath string, wt Worktree, withheld []string) error {
	head, _ := revParse(ctx, wt.Path, "HEAD")
	entry := KeptWorktree{RunID: filepath.Base(wt.Path), Repo: repoPath, Path: wt.Path, Branch: wt.Branch, Head: head,
		GitFile: wt.GitFile, GitDirs: wt.GitDirs, Withheld: slices.Clone(withheld),
		KeptAt: time.Now().UTC().Format(time.RFC3339)}
	keptMu.Lock()
	defer keptMu.Unlock()
	entries, err := loadKept(dataDir)
	if err != nil {
		return err
	}
	entries = slices.DeleteFunc(entries, func(e KeptWorktree) bool { return sameRepoPath(e.Path, wt.Path) })
	entries = append(entries, entry)
	if over := len(entries) - maxKept; over > 0 {
		entries = entries[over:]
	}
	return writeFileAtomic(keptPath(dataDir), entries)
}

// KeptWorktrees lists the recorded kept worktrees still on disk, oldest
// first.
func KeptWorktrees(dataDir string) ([]KeptWorktree, error) {
	keptMu.Lock()
	entries, err := loadKept(dataDir)
	keptMu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []KeptWorktree
	for _, e := range entries {
		if _, err := os.Lstat(e.Path); err == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// PruneKept removes the recorded kept worktrees that hold nothing but the
// agent config their commit left out: under this runner's worktrees
// directory, their .git still pointing where it did, their commits on a
// branch (or HEAD where it was), and no uncommitted change, tracked or
// untracked, beyond the withheld config. Their branches stay. With dryRun
// nothing is removed; Removed is what would be. Records of worktrees already
// gone are dropped.
func PruneKept(ctx context.Context, dataDir string, dryRun bool) (PruneReport, error) {
	keptMu.Lock()
	defer keptMu.Unlock()
	entries, err := loadKept(dataDir)
	if err != nil {
		return PruneReport{}, err
	}
	var report PruneReport
	var drop []string
	for _, e := range entries {
		if _, err := os.Lstat(e.Path); errors.Is(err, fs.ErrNotExist) {
			drop = append(drop, e.Path)
			continue
		}
		if reason := keptUnsafe(ctx, dataDir, e); reason != "" {
			report.Skipped = append(report.Skipped, KeptSkip{KeptWorktree: e, Reason: reason})
			continue
		}
		if dryRun {
			report.Removed = append(report.Removed, e)
			continue
		}
		if err := removeWorktree(ctx, e.Repo, Worktree{Path: e.Path, Branch: e.Branch}, false); err != nil {
			report.Skipped = append(report.Skipped, KeptSkip{KeptWorktree: e, Reason: "could not remove it: " + err.Error()})
			continue
		}
		report.Removed = append(report.Removed, e)
		drop = append(drop, e.Path)
	}
	if dryRun || len(drop) == 0 {
		return report, nil
	}
	// Read again: a running runner may have kept another since.
	entries, err = loadKept(dataDir)
	if err != nil {
		return report, err
	}
	entries = slices.DeleteFunc(entries, func(e KeptWorktree) bool {
		return slices.ContainsFunc(drop, func(p string) bool { return sameRepoPath(p, e.Path) })
	})
	return report, writeFileAtomic(keptPath(dataDir), entries)
}

// keptUnsafe is why a kept worktree must not be removed, or "".
func keptUnsafe(ctx context.Context, dataDir string, e KeptWorktree) string {
	rel, err := filepath.Rel(filepath.Join(dataDir, "worktrees"), e.Path)
	if err != nil || rel != e.RunID || !validRunID.MatchString(rel) {
		return "it is not in this runner's worktrees directory"
	}
	info, err := os.Lstat(e.Path)
	if err != nil || !info.IsDir() || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return "it is not a plain directory"
	}
	if err := checkGitDir(ctx, Worktree{Path: e.Path, GitFile: e.GitFile, GitDirs: e.GitDirs}); err != nil {
		return "its .git no longer points where the run left it"
	}
	// Commits on a branch outlive the worktree; on a detached HEAD only one
	// still where the run left it.
	if _, code, stderr, err := gitExit(ctx, e.Path, "symbolic-ref", "-q", "HEAD"); err != nil || code > 1 {
		return "cannot read its HEAD: " + firstLine(stderr)
	} else if code == 1 {
		head, err := revParse(ctx, e.Path, "HEAD")
		if err != nil || head != e.Head {
			return "its detached HEAD moved since the run; commits there would be lost"
		}
	}
	out, err := git(ctx, e.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=all", "--no-renames")
	if err != nil {
		return "cannot read its changes: " + firstLine(err.Error())
	}
	var extra []string
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 4 {
			continue
		}
		if path := rec[3:]; !slices.Contains(e.Withheld, path) {
			extra = append(extra, path)
		}
	}
	if len(extra) > 0 {
		list := extra
		if len(list) > maxKeptReasonPaths {
			list = append(list[:maxKeptReasonPaths:maxKeptReasonPaths], fmt.Sprintf("and %d more", len(extra)-maxKeptReasonPaths))
		}
		return "it has uncommitted changes beyond the withheld agent config: " + strings.Join(list, ", ")
	}
	return ""
}

// TerminalText is a string made safe for one line of a terminal: agent- or
// hub-chosen text (a withheld file's name) cannot add lines or send escape
// sequences.
func TerminalText(s string) string { return terminalText(s) }
