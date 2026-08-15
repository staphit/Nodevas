// Workspace-wide explorer arrangement.
//
// The order projects appear in is a shared decision, not a per-browser
// preference: a team that puts the current book above the archived ones wants
// everyone opening that workspace to see the same tree. It lives once per
// workspace, in <workspace>/.vised/explorer.json, alongside the shared
// workflow vocabulary.
//
// The list is advisory. It may name a project that has since been deleted and
// omit one that was just created, because nothing rewrites it when projects
// move; the client sorts by it and appends whatever it did not cover.

package store

import (
	"encoding/json"
	"path/filepath"
	"sync"
)

// ExplorerFileName is the shared arrangement file inside the workspace's
// .vised directory.
const ExplorerFileName = "explorer.json"

// MaxExplorerOrderEntries bounds the stored arrangement. It is generous enough
// for every project and folder a workspace realistically holds, while keeping a
// malformed or malicious write from turning into an unbounded file.
const MaxExplorerOrderEntries = 2000

type explorerFile struct {
	ProjectOrder []string `json:"projectOrder"`
}

func WorkspaceExplorerPath(workspace string) string {
	return filepath.Join(workspace, DataDir, ExplorerFileName)
}

// LoadProjectOrder reads the shared arrangement. A missing or unreadable file
// means no one has reordered anything yet, which is an empty list rather than
// an error: the explorer still opens, just in its default order.
func LoadProjectOrder(workspace string) []string {
	if workspace == "" {
		return nil
	}
	data, err := ReadProjectFile(workspace, WorkspaceExplorerPath(workspace))
	if err != nil {
		return nil
	}
	var file explorerFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	if len(file.ProjectOrder) > MaxExplorerOrderEntries {
		file.ProjectOrder = file.ProjectOrder[:MaxExplorerOrderEntries]
	}
	return file.ProjectOrder
}

var workspaceExplorerMu sync.Mutex

// SaveProjectOrder replaces the shared arrangement.
func SaveProjectOrder(workspace string, order []string) error {
	workspaceExplorerMu.Lock()
	defer workspaceExplorerMu.Unlock()
	path := WorkspaceExplorerPath(workspace)
	if order == nil {
		order = []string{}
	}
	data, err := json.MarshalIndent(explorerFile{ProjectOrder: order}, "", "  ")
	if err != nil {
		return err
	}
	return WriteProjectFileAtomic(workspace, path, append(data, '\n'))
}
