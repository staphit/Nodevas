package store

import (
	"context"
	"errors"
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

// HistoryGit is the workspace's backend, pending changes and newest
// commits (at most 50).
func (s *Store) HistoryGit(ctx context.Context) (WorkspaceGit, error) {
	// TODO(green): implement.
	return WorkspaceGit{}, errors.New("not implemented")
}

// HistoryCommitPatch is one workspace commit's patch.
func (s *Store) HistoryCommitPatch(ctx context.Context, sha string) (WorkspaceCommitPatch, error) {
	// TODO(green): implement.
	return WorkspaceCommitPatch{}, errors.New("not implemented")
}
