// HTTP handler for cross-project search. Routes: routes.go (routeProjects).

package project

import (
	"fmt"
	"net/http"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/project"
	"strings"

	"github.com/gin-gonic/gin"
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
		httpx.Err(c, http.StatusBadRequest, fmt.Errorf("search query is too long"))
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
