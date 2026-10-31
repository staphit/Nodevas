// HTTP handler for cross-project search. Routes: routes.go (routeProjects).

package project

import (
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"nodevas/internal/searchindex"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/locale"
)

// GetSearch searches project names, node metadata, Markdown, and subpages
// across the whole workspace. Results stay bounded for interactive use.
func (a *API) GetSearch(c *gin.Context) {
	query := strings.TrimSpace(c.Request.URL.Query().Get("q"))
	if len(query) < 2 {
		c.JSON(http.StatusOK, map[string]any{"results": []project.SearchResult{}})
		return
	}
	if len(query) > 256 {
		httpx.Err(c, http.StatusBadRequest, locale.Errorf("search query is too long", "搜尋字串太長"))
		return
	}
	if c.Request.URL.Query().Get("mode") == "ranked" {
		a.getRankedSearch(c, query)
		return
	}
	projects, err := a.pm.List()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	normalized := strings.ToLower(query)
	results := make([]project.SearchResult, 0, 32)
	for _, info := range projects {
		if len(results) >= 100 {
			break
		}
		if !info.IsFolder && !a.readable(c.Request, info.Path) {
			continue
		}
		if strings.Contains(strings.ToLower(info.Label+" "+info.Name), normalized) {
			results = append(results, project.SearchResult{
				Project: info.Name,
				Title:   info.Label,
				// Search is not admin-only, so a remote member would learn the
				// server's on-disk layout from this snippet; the catalog blanks
				// the same field through the same gate.
				Snippet: a.visibleHostPath(info.Path),
				Kind:    "project",
			})
		}
		nodeResults, searchErr := a.pm.SearchProjectNodes(info, normalized)
		if searchErr != nil {
			nodeResults = project.SearchProjectNodesDirect(info, normalized)
		}
		for _, nodeResult := range nodeResults {
			if len(results) >= 100 {
				break
			}
			results = append(results, nodeResult)
		}
	}
	c.JSON(http.StatusOK, map[string]any{"results": results})
}

// Ranked-search result bounds.
const (
	defaultRankedLimit = 20
	maxRankedLimit     = 100
)

// getRankedSearch is GET /api/search?mode=ranked: nodes only, best first, for
// a query of keywords or a whole question. An exact title ranks first, then
// the whole query as a phrase, then shared terms by BM25. ?project= keeps it
// to one project; ?limit= caps the results (default 20, max 100).
func (a *API) getRankedSearch(c *gin.Context, query string) {
	limit := defaultRankedLimit
	if raw := strings.TrimSpace(c.Request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxRankedLimit {
			httpx.Err(c, http.StatusBadRequest,
				locale.Errorf("limit must be a whole number between 1 and %d", "limit 須為 1 到 %d 的整數", maxRankedLimit))
			return
		}
		limit = parsed
	}
	only := strings.TrimSpace(c.Request.URL.Query().Get("project"))
	projects, err := a.pm.List()
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	results := make([]project.SearchResult, 0, limit)
	for _, info := range projects {
		if info.IsFolder || (only != "" && info.Name != only) || !a.readable(c.Request, info.Path) {
			continue
		}
		ranked, err := a.pm.RankProjectNodes(info, query, limit)
		if err != nil {
			continue
		}
		results = append(results, ranked...)
	}
	// Across projects: tier first, then score. Scores share the workspace's
	// corpus statistics, so this is approximate but never puts a phrase hit
	// below a terms hit.
	sort.SliceStable(results, func(i, j int) bool {
		ti, tj := rankedTier(results[i].Match), rankedTier(results[j].Match)
		if ti != tj {
			return ti < tj
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	c.JSON(http.StatusOK, map[string]any{"results": results})
}

func rankedTier(match string) int {
	switch match {
	case searchindex.MatchTitle:
		return 0
	case searchindex.MatchPhrase:
		return 1
	}
	return 2
}
