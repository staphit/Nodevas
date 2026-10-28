package server

import (
	"net/http"
	"testing"
)

// The analytics reports load a whole window of run history each; they are
// the expensive class. Their settings are ordinary reads and writes, and
// search and repo browsing stay reads (abuse_test.go pins those).
func TestAnalyticsReportsAreTheExpensiveClass(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         RouteClass
	}{
		{http.MethodGet, "/api/analytics/insights", ClassExpensive},
		{http.MethodGet, "/api/analytics/overview", ClassExpensive},
		{http.MethodGet, "/api/analytics/scorecards", ClassExpensive},
		{http.MethodGet, "/api/analytics/tiers", ClassExpensive},
		{http.MethodGet, "/api/analytics/settings", ClassRead},
		{http.MethodGet, "/api/analytics/retention", ClassRead},
		{http.MethodGet, "/api/analytics/route-picks", ClassRead},
		{http.MethodPut, "/api/analytics/settings", ClassWrite},
		{http.MethodPost, "/api/analytics/suggestions/apply", ClassWrite},
		{http.MethodGet, "/api/search", ClassRead},
		{http.MethodGet, "/api/explorer/repos/runner-1/r1/tree", ClassRead},
	} {
		if got, _ := ClassifyRoute(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s is %s, want %s", tc.method, tc.path, got, tc.want)
		}
	}
}

// A verdict, a person's review vote, or anything else that settles a run
// changes the scorecards, so the cached ones are dropped.
func TestWritesThatChangeScorecardsInvalidateThem(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/api/verdicts", true},
		{http.MethodPost, "/api/runs/pipelines/p1/review", true},
		{http.MethodPost, "/api/runs/r1/merge", true},
		{http.MethodPost, "/api/runs", true},
		{http.MethodPut, "/api/analytics/settings", false},
		{http.MethodGet, "/api/runs/r1/verdicts", false},
		{http.MethodPut, "/api/graph", false},
	} {
		if got := changesScorecards(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s changes scorecards = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
