package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

// The analytics page asks for three reports at once; on a host with fewer
// expensive slots than that they queue briefly instead of failing.
func TestAnalyticsReportsWaitForASlot(t *testing.T) {
	t.Parallel()
	guard := newAbuseGuardWithOptions(AbuseGuardOptions{MaxConcurrentHeavy: 1})
	srv := &Server{abuse: guard}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(srv.withAbuseLimits)
	router.GET("/api/analytics/scorecards", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/api/validate", func(c *gin.Context) { c.Status(http.StatusOK) })

	guard.sem <- struct{}{} // another expensive request holds the only slot
	if code := serveCode(router, "/api/validate"); code != http.StatusTooManyRequests {
		t.Fatalf("another expensive route with no slot got %d, want an immediate 429", code)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		<-guard.sem
	}()
	if code := serveCode(router, "/api/analytics/scorecards"); code != http.StatusOK {
		t.Fatalf("a report waiting for a slot got %d, want 200", code)
	}
}

func serveCode(router http.Handler, path string) int {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "198.51.100.30:40000"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code
}
