package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"nodevas/internal/gitrepo"
)

// The workspace's history as the Explorer's Git tab shows it.

// WorkspacePending is one changed path not yet in history.
type WorkspacePending struct {
	Path string `json:"path"`
	Mark string `json:"mark"`
}

// WorkspaceCommit is one history commit.
type WorkspaceCommit struct {
	SHA     string `json:"sha"`
	At      string `json:"at"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// WorkspaceGit is GET /api/explorer/workspace/git.
type WorkspaceGit struct {
	Backend string             `json:"backend"`
	Branch  string             `json:"branch"`
	Pending []WorkspacePending `json:"pending"`
	Commits []WorkspaceCommit  `json:"commits"`
}

// WorkspaceCommitPatch is one commit and its (capped) patch.
type WorkspaceCommitPatch struct {
	WorkspaceCommit
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

// Workspace git limits.
const (
	MaxWorkspaceCommits = 50
	maxWorkspacePending = 500
)

// ErrNoGitHistory: the workspace keeps history as file copies.
var ErrNoGitHistory = errors.New("this workspace keeps history as file copies, not git")

// ErrCommitNotFound: the workspace history has no such commit.
var ErrCommitNotFound = errors.New("no such commit in the workspace history")

// historyRepo is the git history's repository, or ErrNoGitHistory.
func (s *Store) historyRepo(ctx context.Context) (*gitrepo.Repo, error) {
	g, ok := s.history().(*gitHistory)
	if !ok {
		return nil, ErrNoGitHistory
	}
	return g.ready(ctx)
}

// HistoryGit is the workspace's backend, pending changes and newest
// commits (at most 50).
func (s *Store) HistoryGit(ctx context.Context) (WorkspaceGit, error) {
	out := WorkspaceGit{Backend: string(HistoryFiles), Pending: []WorkspacePending{}, Commits: []WorkspaceCommit{}}
	repo, err := s.historyRepo(ctx)
	if errors.Is(err, ErrNoGitHistory) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Backend = string(HistoryGit)
	if raw, err := repo.Run(ctx, nil, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		out.Branch = strings.TrimSpace(string(raw))
	}
	commits, err := repo.RecentCommits(ctx, MaxWorkspaceCommits)
	if err != nil {
		return out, err
	}
	for _, c := range commits {
		out.Commits = append(out.Commits, WorkspaceCommit{SHA: c.SHA, At: c.At.UTC().Format(time.RFC3339),
			Author: c.Author, Subject: c.Subject})
	}
	raw, err := repo.Run(ctx, nil, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=normal")
	if err != nil {
		return out, err
	}
	for _, rec := range strings.Split(string(raw), "\x00") {
		if len(rec) < 4 || len(out.Pending) >= maxWorkspacePending {
			continue
		}
		xy, rel := rec[:2], rec[3:]
		if strings.HasPrefix(rel, DataDir+"/") || secretRel(rel) {
			continue
		}
		out.Pending = append(out.Pending, WorkspacePending{Path: rel, Mark: pendingMark(xy)})
	}
	return out, nil
}

func pendingMark(xy string) string {
	if xy == "??" {
		return "A"
	}
	for _, c := range xy {
		switch c {
		case 'A':
			return "A"
		case 'D':
			return "D"
		case 'R', 'C':
			return "R"
		}
	}
	return "M"
}

// HistoryCommitPatch is one workspace commit's patch.
func (s *Store) HistoryCommitPatch(ctx context.Context, sha string) (WorkspaceCommitPatch, error) {
	if !gitrepo.ValidSHA(sha) {
		return WorkspaceCommitPatch{}, fmt.Errorf("%w: a commit is its full id", ErrBadPath)
	}
	repo, err := s.historyRepo(ctx)
	if err != nil {
		return WorkspaceCommitPatch{}, err
	}
	raw, err := repo.Run(ctx, nil, "log", "-n1", "--no-show-signature", "--format=%H%x1f%aI%x1f%an%x1f%s", sha, "--")
	if err != nil {
		return WorkspaceCommitPatch{}, ErrCommitNotFound
	}
	parts := strings.SplitN(strings.TrimSpace(string(raw)), "\x1f", 4)
	if len(parts) != 4 {
		return WorkspaceCommitPatch{}, ErrCommitNotFound
	}
	patch, truncated, err := repo.CommitPatch(ctx, sha, gitrepo.MaxPatchBytes)
	if err != nil {
		return WorkspaceCommitPatch{}, err
	}
	return WorkspaceCommitPatch{WorkspaceCommit: WorkspaceCommit{SHA: parts[0], At: parts[1], Author: parts[2], Subject: parts[3]},
		Patch: patch, Truncated: truncated}, nil
}

// dropSecretPatchSections removes the sections of a patch that touch a
// file that may hold secrets.
func dropSecretPatchSections(patch string) string { return patch }
