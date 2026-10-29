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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/searchindex"
	"nodevas/internal/store"
)

// Workspace-wide project search.

type SearchResult struct {
	Project string `json:"project"`
	NodeID  string `json:"nodeId,omitempty"`
	PageID  string `json:"pageId,omitempty"`
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
// It is an incremental update, not a drop: only the touched node is re-read,
// in the background, so the watcher never waits on the index.
func (si *searchIndex) invalidate(projectPath, nodeID string) {
	if si == nil {
		return
	}
	si.indexes().NotifyNode(projectPath, nodeID)
}

// changed is the store hook: path was written, renamed or removed by the
// server itself, which the watcher never reports.
func (si *searchIndex) changed(projectPath, path string) {
	if si == nil {
		return
	}
	si.indexes().Notify(projectPath, path)
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
			PageID:  match.PageID,
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
			PageID:  match.PageID,
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
//
// A hub opens its workspace, and so its last project, before the database is
// up, and the index that opening asked to warm had nowhere to go. So this
// warms every open project again.
func (pm *ProjectManager) UseDatabase(database *db.DB) {
	pm.mu.Lock()
	index := &searchIndex{manager: searchindex.NewManagerWithDB(database)}
	pm.searchIndex = index
	pm.searchLive.Store(index)
	pm.mu.Unlock()
	for _, st := range pm.cachedStores() {
		if pm.Watching(st.Root()) {
			index.indexes().Warm(st.Root())
		}
	}
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
		pm.searchLive.Store(pm.searchIndex)
	}
	return pm.searchIndex
}

// coldSearchWait is how long a search waits for a project's first index
// build before it answers from searchWhileIndexing instead. A var so tests
// can pin it.
var coldSearchWait = 50 * time.Millisecond

func (pm *ProjectManager) SearchProjectNodes(project ProjectInfo, normalized string) ([]SearchResult, error) {
	index := pm.getSearchIndex()
	if index.indexes().Warming(project.Path, coldSearchWait) {
		return pm.searchWhileIndexing(project, normalized), nil
	}
	return index.search(project, normalized)
}

// AwaitSearchIndex reports whether the index of the project at projectPath is
// built within the given time, starting the build if nothing is on it. A
// workspace without a database has no index and reports true: its searches
// scan the files.
func (pm *ProjectManager) AwaitSearchIndex(projectPath string, within time.Duration) bool {
	return !pm.getSearchIndex().indexes().Warming(projectPath, within)
}

type searchManifestPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func readSearchManifestPages(root, pageDir string) []searchManifestPage {
	data, err := store.ReadProjectFileLimit(root, filepath.Join(pageDir, "pages.json"), searchPageContentLimit)
	if err != nil {
		return nil
	}
	var manifest struct {
		Pages []searchManifestPage `json:"pages"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	return manifest.Pages
}

// searchWhileIndexing answers a search of a project whose first index build
// is still running, which takes seconds for a large one: it matches what the
// cached graph holds (titles, kinds, tags, assignees) and reads no file. Body
// text becomes searchable once the build is in. Not the direct scan: that
// reads every node's files and pages, which on a project of a few nodes with
// a thousand pages is seconds per search, competing with the build itself.
func (pm *ProjectManager) searchWhileIndexing(project ProjectInfo, normalized string) []SearchResult {
	graph, _, err := pm.cachedStore(project.Path).LoadGraph()
	if err != nil {
		return SearchProjectNodesDirect(project, normalized)
	}
	userNames := make(map[string]string, len(graph.Users))
	for _, user := range graph.Users {
		userNames[user.ID] = user.Name
	}
	pageDirs := store.ListNodePayloadDirs(project.Path, ".pages")
	results := make([]SearchResult, 0)
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		metadata := nodeSearchMetadata(node, userNames)
		if strings.Contains(strings.ToLower(metadata), normalized) {
			results = append(results, SearchResult{
				Project: project.Name,
				NodeID:  node.ID,
				Title:   node.Title,
				Snippet: searchSnippet(metadata, normalized),
				Kind:    "node",
			})
			continue
		}
		pageDir, ok := pageDirs[node.ID]
		if !ok {
			continue
		}
		for _, page := range readSearchManifestPages(project.Path, pageDir) {
			title := strings.TrimSpace(page.Title)
			if page.ID != "" && title != "" && strings.Contains(strings.ToLower(title), normalized) {
				haystack := metadata + "\n" + title
				results = append(results, SearchResult{
					Project: project.Name,
					NodeID:  node.ID,
					PageID:  page.ID,
					Title:   node.Title,
					Snippet: searchSnippet(haystack, normalized),
					Kind:    "node",
				})
				break
			}
		}
	}
	return results
}

// nodeSearchMetadata is the line of a node's graph fields a search matches
// besides its files.
func nodeSearchMetadata(node *engine.Node, userNames map[string]string) string {
	return strings.Join([]string{
		node.ID, node.Title, node.Kind, node.Priority,
		userNames[node.Assignee], strings.Join(node.Tags, " "), node.Requires,
	}, " ")
}

// RankProjectNodes is the ranked search over one project: its best limit
// nodes for keywords or a whole sentence, each with the excerpt that matched.
// A project the index cannot open falls back to the direct scan, whose hits
// all contain the query as a phrase.
//
// While the project's first index build runs, it answers from
// searchWhileIndexing: hits whose title holds the query first.
func (pm *ProjectManager) RankProjectNodes(info ProjectInfo, query string, limit int) ([]SearchResult, error) {
	index := pm.getSearchIndex()
	if index.indexes().Warming(info.Path, coldSearchWait) {
		normalized := strings.ToLower(strings.TrimSpace(query))
		results := pm.searchWhileIndexing(info, normalized)
		for i := range results {
			results[i].Match = searchindex.MatchPhrase
			if strings.Contains(strings.ToLower(results[i].Title), normalized) {
				results[i].Match = searchindex.MatchTitle
			}
		}
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Match == searchindex.MatchTitle && results[j].Match != searchindex.MatchTitle
		})
		if limit > 0 && len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}
	results, err := index.rank(info, query, limit)
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

// noteSearchChange is what every Store this manager hands out reports its
// writes to. A Store calls it with its own lock held, possibly while this
// manager holds pm.mu, so it reads the index through searchLive and takes no
// lock of the manager's.
func (pm *ProjectManager) noteSearchChange(projectPath, path string) {
	pm.searchLive.Load().changed(projectPath, path)
}

// warmSearch starts building a project's index in the background, so the
// first search after it opens does not pay for the build.
func (pm *ProjectManager) warmSearch(projectPath string) {
	pm.getSearchIndex().indexes().Warm(projectPath)
}

// closeSearchIndex waits for the index's background work to finish, so
// nothing is still reading a project's files once the manager is closed.
func (pm *ProjectManager) closeSearchIndex() {
	_ = pm.getSearchIndex().indexes().Close()
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
		metadata := nodeSearchMetadata(node, userNames)
		content := readSearchTextDirect(project.Path, store.NodeDocPath(project.Path, node.ID))
		mainHaystack := metadata + "\n" + content
		if strings.Contains(strings.ToLower(mainHaystack), normalized) {
			results = append(results, SearchResult{
				Project: project.Name,
				NodeID:  node.ID,
				Title:   node.Title,
				Snippet: searchSnippet(mainHaystack, normalized),
				Kind:    "node",
			})
			continue
		}
		var pageContent strings.Builder
		matchedPageID := ""
		matchedPageHaystack := ""
		pageDir := store.NodePagesPath(project.Path, node.ID)
		if entries, err := store.ReadProjectDir(project.Path, pageDir); err == nil {
			manifestPages := readSearchManifestPages(project.Path, pageDir)
			orderByID := make(map[string]int, len(manifestPages))
			titlesByID := make(map[string]string, len(manifestPages))
			for idx, p := range manifestPages {
				if p.ID != "" {
					orderByID[p.ID] = idx
					titlesByID[p.ID] = strings.TrimSpace(p.Title)
				}
			}
			textEntries := make([]os.DirEntry, 0, len(entries))
			for _, entry := range entries {
				if entry.IsDir() || entry.Name() == "pages.json" {
					continue
				}
				format, ok := store.PageFormatFromFilename(entry.Name())
				if !ok || !store.PageFormatIsText(format) {
					continue
				}
				textEntries = append(textEntries, entry)
			}
			if len(orderByID) > 0 {
				sort.SliceStable(textEntries, func(i, j int) bool {
					idI := strings.TrimSuffix(textEntries[i].Name(), filepath.Ext(textEntries[i].Name()))
					idJ := strings.TrimSuffix(textEntries[j].Name(), filepath.Ext(textEntries[j].Name()))
					oi, okI := orderByID[idI]
					oj, okJ := orderByID[idJ]
					if okI != okJ {
						return okI
					}
					if okI && okJ {
						return oi < oj
					}
					return false
				})
			}
			for _, entry := range textEntries {
				data, err := store.ReadProjectFileLimit(project.Path, filepath.Join(pageDir, entry.Name()), int64(searchPageContentLimit))
				if err != nil {
					continue
				}
				remaining := searchPageContentLimit - pageContent.Len()
				if len(data) > remaining {
					data = data[:remaining]
				}
				pageID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
				pageText := string(data)
				if pageTitle := titlesByID[pageID]; pageTitle != "" && !strings.Contains(strings.ToLower(pageText), strings.ToLower(pageTitle)) {
					pageText = pageTitle + "\n" + pageText
				}
				if matchedPageID == "" {
					candidate := metadata + "\n" + pageText
					if strings.Contains(strings.ToLower(candidate), normalized) {
						matchedPageID = pageID
						matchedPageHaystack = candidate
					}
				}
				pageContent.WriteByte('\n')
				pageContent.WriteString(pageText)
				if pageContent.Len() >= searchPageContentLimit {
					break
				}
			}
		}
		if matchedPageID != "" {
			results = append(results, SearchResult{
				Project: project.Name,
				NodeID:  node.ID,
				PageID:  matchedPageID,
				Title:   node.Title,
				Snippet: searchSnippet(matchedPageHaystack, normalized),
				Kind:    "node",
			})
			continue
		}
		haystack := mainHaystack + pageContent.String()
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
