// Cross-project search: the workspace-facing API, and the direct scan kept as
// the last-resort fallback.
//
// The matching itself lives in internal/searchindex, which holds a durable
// inverted index per project. This file is the adapter: it turns a
// ProjectInfo into a root path, turns index matches back into SearchResult,
// and forwards watcher events so a change updates one node's postings instead
// of throwing the project's index away.

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/searchindex"
	"nodevas/internal/store"
)

// Workspace-wide project search.

type SearchResult struct {
	Project string `json:"project"`
	NodeID  string `json:"nodeId,omitempty"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Kind    string `json:"kind"`
}

func searchSnippet(content, query string) string {
	compact := strings.Join(strings.Fields(content), " ")
	lower := strings.ToLower(compact)
	index := strings.Index(lower, query)
	if index < 0 {
		if len(compact) > 140 {
			return compact[:140] + "…"
		}
		return compact
	}
	start := index - 50
	if start < 0 {
		start = 0
	}
	end := index + len(query) + 90
	if end > len(compact) {
		end = len(compact)
	}
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(compact) {
		suffix = "…"
	}
	return prefix + compact[start:end] + suffix
}

// searchPageContentLimit bounds what the direct scan reads from one file. The
// index mirrors it (see internal/searchindex), so the fallback and the index
// agree on which part of a file is searchable.
const searchPageContentLimit = 2 << 20

// searchIndex is the project package's handle on the shared index cache. The
// type name is fixed by the ProjectManager field it fills; all it does is
// adapt ProjectInfo to a root path.
type searchIndex struct {
	once    sync.Once
	manager *searchindex.Manager
}

func newSearchIndex() *searchIndex {
	return &searchIndex{manager: searchindex.NewManager()}
}

func (si *searchIndex) indexes() *searchindex.Manager {
	si.once.Do(func() {
		if si.manager == nil {
			si.manager = searchindex.NewManager()
		}
	})
	return si.manager
}

// invalidate is the watcher hook. nodeID is empty when graph.yaml changed.
// It is an incremental update, not a drop: only the touched node is re-read.
func (si *searchIndex) invalidate(projectPath, nodeID string) {
	if si == nil {
		return
	}
	si.indexes().Update(projectPath, nodeID)
}

func (si *searchIndex) search(project ProjectInfo, normalized string) ([]SearchResult, error) {
	if si == nil {
		return nil, os.ErrInvalid
	}
	matches, err := si.indexes().Search(project.Path, normalized)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(matches))
	for _, match := range matches {
		out = append(out, SearchResult{
			Project: project.Name,
			NodeID:  match.NodeID,
			Title:   match.Title,
			Snippet: searchSnippet(match.Text, normalized),
			Kind:    "node",
		})
	}
	return out, nil
}

func readSearchTextDirect(root, path string) string {
	data, err := store.ReadProjectFileLimit(root, path, searchPageContentLimit)
	if err != nil {
		return ""
	}
	return string(data)
}

// UseDatabase points the search index at the workspace database, so the index
// survives a restart instead of being rebuilt by the first search after one.
//
// Call it before the first search. Without it the manager keeps its own
// private in-memory index, which is correct but forgets everything on exit —
// that is the fallback for an embedder that has no database.
func (pm *ProjectManager) UseDatabase(database *db.DB) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.searchIndex = &searchIndex{manager: searchindex.NewManagerWithDB(database)}
}

// PruneSearchIndex drops index rows for projects that are no longer in the
// workspace.
//
// The index used to be a file inside each project and disappeared with it. It
// is rows in a shared database now, so a project deleted from disk leaves its
// rows behind with nothing to notice — this is what notices.
func (pm *ProjectManager) PruneSearchIndex(ctx context.Context) error {
	projects, err := pm.List()
	if err != nil {
		return err
	}
	roots := make([]string, 0, len(projects))
	for _, project := range projects {
		if !project.IsFolder {
			roots = append(roots, project.Path)
		}
	}
	return pm.getSearchIndex().indexes().Prune(ctx, roots)
}

func (pm *ProjectManager) getSearchIndex() *searchIndex {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.searchIndex == nil {
		pm.searchIndex = newSearchIndex()
	}
	return pm.searchIndex
}

func (pm *ProjectManager) SearchProjectNodes(project ProjectInfo, normalized string) ([]SearchResult, error) {
	return pm.getSearchIndex().search(project, normalized)
}

func (pm *ProjectManager) invalidateSearch(projectPath, nodeID string) {
	pm.getSearchIndex().invalidate(projectPath, nodeID)
}

// FlushSearchIndex writes every pending index to disk. Useful at shutdown and
// in tests; skipping it only costs the next start a rebuild.
func (pm *ProjectManager) FlushSearchIndex() {
	pm.getSearchIndex().indexes().Flush()
}

// SearchProjectNodesDirect re-reads the project and scans it. It is the
// fallback for a project the index cannot handle at all, so that a broken
// index degrades search to "slow" rather than "gone".
func SearchProjectNodesDirect(project ProjectInfo, normalized string) []SearchResult {
	graphData, err := store.ReadProjectFile(project.Path, filepath.Join(project.Path, "graph.yaml"))
	if err != nil {
		return nil
	}
	graph, err := engine.ParseGraph(graphData)
	if err != nil {
		return nil
	}
	userNames := make(map[string]string, len(graph.Users))
	for _, user := range graph.Users {
		userNames[user.ID] = user.Name
	}
	results := make([]SearchResult, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		metadata := strings.Join([]string{
			node.ID, node.Title, node.Kind, node.Priority,
			userNames[node.Assignee], strings.Join(node.Tags, " "), node.Requires,
		}, " ")
		content := readSearchTextDirect(project.Path, store.NodeDocPath(project.Path, node.ID))
		var pageContent strings.Builder
		pageDir := store.NodePagesPath(project.Path, node.ID)
		if entries, err := store.ReadProjectDir(project.Path, pageDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				format, ok := store.PageFormatFromFilename(entry.Name())
				if !ok || !store.PageFormatIsText(format) {
					continue
				}
				data, err := store.ReadProjectFileLimit(project.Path, filepath.Join(pageDir, entry.Name()), int64(searchPageContentLimit))
				if err != nil {
					continue
				}
				remaining := searchPageContentLimit - pageContent.Len()
				if len(data) > remaining {
					data = data[:remaining]
				}
				pageContent.WriteByte('\n')
				pageContent.Write(data)
				if pageContent.Len() >= searchPageContentLimit {
					break
				}
			}
		}
		haystack := metadata + "\n" + content + pageContent.String()
		if strings.Contains(strings.ToLower(haystack), normalized) {
			results = append(results, SearchResult{
				Project: project.Name,
				NodeID:  node.ID,
				Title:   node.Title,
				Snippet: searchSnippet(haystack, normalized),
				Kind:    "node",
			})
		}
	}
	return results
}
