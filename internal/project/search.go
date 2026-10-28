// Cross-project search: the workspace-facing API, and the direct scan kept as
// the last-resort fallback.
//
// The matching itself lives in internal/searchindex, which keeps a durable
// index per project in the workspace database. This file is the adapter: it
// turns a
// ProjectInfo into a root path, turns index matches back into SearchResult,
// and forwards watcher events so a change updates one node's postings instead
// of throwing the project's index away.

package project

import (
	"context"
	"errors"
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
	// Score and Match come from the ranked search: Score is higher for a
	// better match; Match is "title", "phrase" or "terms".
	Score float64 `json:"score,omitempty"`
	Match string  `json:"match,omitempty"`
}

// searchSnippet cuts the part of a search document (the metadata line, then
// the file) around query. It is the ranked search's excerpt: whole
// characters, frontmatter left out. Cutting bytes here put half a Chinese
// character at the front of every snippet the palette showed.
func searchSnippet(content, query string) string {
	return searchindex.Excerpt(content, []string{query}, searchindex.ExcerptWidth)
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

func (si *searchIndex) rank(project ProjectInfo, query string, limit int) ([]SearchResult, error) {
	if si == nil {
		return nil, os.ErrInvalid
	}
	ranked, err := si.indexes().RankContext(context.Background(), project.Path, query, limit)
	if err != nil {
		return nil, err
	}
	tokens := searchindex.RankTokens(query)
	out := make([]SearchResult, 0, len(ranked))
	for _, match := range ranked {
		out = append(out, SearchResult{
			Project: project.Name,
			NodeID:  match.NodeID,
			Title:   match.Title,
			Snippet: searchindex.Excerpt(match.Text, tokens, searchindex.ExcerptWidth),
			Kind:    "node",
			Score:   match.Score,
			Match:   match.Match,
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

// UseDatabase points the search index at the workspace database, where it
// lives and survives a restart.
//
// Call it before the first search. Without it there is no index: every search
// falls back to the direct scan, which is correct but reads every file and
// ranks nothing — the path for an embedder that has no database.
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
	if err := pm.getSearchIndex().indexes().Prune(ctx, roots); err != nil && !errors.Is(err, searchindex.ErrNoIndex) {
		return err
	}
	return nil
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

// RankProjectNodes is the ranked search over one project: its best limit
// nodes for keywords or a whole sentence, each with the excerpt that matched.
// A project the index cannot open falls back to the direct scan, whose hits
// all contain the query as a phrase.
func (pm *ProjectManager) RankProjectNodes(info ProjectInfo, query string, limit int) ([]SearchResult, error) {
	results, err := pm.getSearchIndex().rank(info, query, limit)
	if err == nil {
		return results, nil
	}
	direct := SearchProjectNodesDirect(info, strings.ToLower(strings.TrimSpace(query)))
	if limit > 0 && len(direct) > limit {
		direct = direct[:limit]
	}
	for i := range direct {
		direct[i].Match = searchindex.MatchPhrase
	}
	return direct, nil
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
