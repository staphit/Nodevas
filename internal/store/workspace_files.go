package store

import "errors"

// Workspace files for the Explorer's Files tab: the project root's files,
// read-only, with the store's own containment (no traversal, no symlink
// escape) and the secrets deny list hidden.

// Workspace walk limits.
const (
	MaxWorkspaceDepth     = 6
	MaxWorkspaceEntries   = 5000
	MaxWorkspaceFileBytes = 512 << 10
)

// Workspace file errors.
var (
	ErrBadPath    = errors.New("bad path")
	ErrHiddenFile = errors.New("this file is hidden because it may hold secrets")
)

// WorkspaceEntry is one file or directory under the project root.
type WorkspaceEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	NodeID string `json:"nodeId,omitempty"`
	Title  string `json:"title,omitempty"`
}

// WorkspaceListing is the project's files.
type WorkspaceListing struct {
	Entries   []WorkspaceEntry `json:"entries"`
	Hidden    int              `json:"hidden"`
	Truncated bool             `json:"truncated"`
}

// WorkspaceFile is one file's content.
type WorkspaceFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated"`
	NodeID    string `json:"nodeId,omitempty"`
	URL       string `json:"url,omitempty"`
}

// WorkspaceFiles walks the project root.
func (s *Store) WorkspaceFiles() (WorkspaceListing, error) {
	// TODO(green): implement.
	return WorkspaceListing{}, errors.New("not implemented")
}

// ReadWorkspaceFile reads one file under the project root.
func (s *Store) ReadWorkspaceFile(rel string) (WorkspaceFile, error) {
	// TODO(green): implement.
	return WorkspaceFile{}, errors.New("not implemented")
}
