package server

import (
	"net/http"
	"testing"
	"time"
)

// Expanding sixty folders and typing a search in quick succession is one
// person's normal use: none of it may be refused.
func TestBrowsingAndSearchingFastIsNeverThrottled(t *testing.T) {
	g := newAbuseGuard()
	now := time.Now()
	paths := []string{
		"/api/explorer/repos/runner-1/r1/tree",
		"/api/explorer/repos/runner-1/r1/file",
		"/api/search",
	}
	for i := range 60 {
		path := paths[i%len(paths)]
		class, cost := ClassifyRoute(http.MethodGet, path)
		if ok, _ := g.AllowRoute(class, cost, "u-1", "127.0.0.1", now.Add(time.Duration(i)*20*time.Millisecond)); !ok {
			t.Fatalf("request %d (%s) was throttled", i+1, path)
		}
	}
}
