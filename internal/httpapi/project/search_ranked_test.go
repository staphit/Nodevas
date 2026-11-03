package project

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	projectdomain "nodevas/internal/project"

	"github.com/gin-gonic/gin"
)

func runRanked(t *testing.T, api *API, query string) (int, []projectdomain.SearchResult) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/search?"+query, nil)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	api.GetSearch(context)
	var payload struct {
		Results []projectdomain.SearchResult `json:"results"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	return response.Code, payload.Results
}

func TestRankedSearchIsScopedScoredAndBounded(t *testing.T) {
	api := New(searchTestWorkspace(t, 30))
	q := "q=" + url.QueryEscape("which needle is the 中文 one?") + "&mode=ranked"

	code, results := runRanked(t, api, q+"&project=proj&limit=5")
	if code != http.StatusOK || len(results) != 5 {
		t.Fatalf("status %d, %d results", code, len(results))
	}
	for _, result := range results {
		if result.Project != "proj" || result.Kind != "node" || result.Match != "terms" ||
			result.Score <= 0 || result.Snippet == "" {
			t.Fatalf("result = %+v", result)
		}
	}
	if _, elsewhere := runRanked(t, api, q+"&project=nope"); len(elsewhere) != 0 {
		t.Fatalf("another project's filter returned %d results", len(elsewhere))
	}
	if _, defaults := runRanked(t, api, q); len(defaults) != 20 {
		t.Fatalf("default limit gave %d results", len(defaults))
	}
	for _, bad := range []string{"0", "101", "x"} {
		if code, _ := runRanked(t, api, q+"&limit="+bad); code != http.StatusBadRequest {
			t.Fatalf("limit=%s: status %d", bad, code)
		}
	}
	// A ranked search never lists a project row.
	if _, rows := runRanked(t, api, "q=proj&mode=ranked"); len(rows) > 0 && rows[0].Kind == "project" {
		t.Fatalf("project row in ranked results: %+v", rows[0])
	}
}
