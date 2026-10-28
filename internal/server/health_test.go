package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nodevas/internal/identity"
	"nodevas/internal/realtime"
)

func getAs(handler http.Handler, path, peer string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = peer + ":40000"
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// The container's health probe asks /api/healthz: no sign-in, and a 503
// when the database does not answer, so a hub that lost its database is
// reported unhealthy instead of serving its sign-in page forever.
func TestHealthzNeedsNoSignInAndReportsTheDatabase(t *testing.T) {
	server, _, _, database, _ := accountServerPartsForTest(t)
	server.UseAgentsDB(database)
	handler := server.Handler()

	response := getAs(handler, "/api/healthz", "203.0.113.7", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("healthz = %d %s, want 200", response.Code, response.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["status"] != "ok" || body["database"] != "ok" {
		t.Fatalf("healthz body = %s (%v)", response.Body, err)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("healthz Cache-Control = %q, want no-store", cache)
	}
	head := httptest.NewRequest(http.MethodHead, "/api/healthz", nil)
	head.RemoteAddr = "203.0.113.7:40000"
	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, head)
	if headResponse.Code != http.StatusOK {
		t.Fatalf("HEAD healthz = %d, want 200", headResponse.Code)
	}
	probe := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	probe.RemoteAddr = "127.0.0.1:40000"
	if !QuietRequest(probe) {
		t.Fatal("the container's own health probe fills the request log")
	}
	if class, _ := ClassifyRoute(http.MethodGet, "/api/healthz"); class != ClassRead {
		t.Fatalf("healthz is the %s class, want read", class)
	}
}

func TestHealthzIs503WhenTheDatabaseIsDown(t *testing.T) {
	pm := projectManagerForTest(t)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	database := dbtestOpenForServer(t, pm.Workspace())
	server.UseAgentsDB(database)
	handler := server.Handler()
	if response := getAs(handler, "/api/healthz", "127.0.0.1", nil); response.Code != http.StatusOK {
		t.Fatalf("healthz before = %d %s, want 200", response.Code, response.Body)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	response := getAs(handler, "/api/healthz", "127.0.0.1", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz with the database closed = %d %s, want 503", response.Code, response.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["database"] != "down" {
		t.Fatalf("healthz body = %s (%v)", response.Body, err)
	}
}

// /api/metrics is for administrators, or for a scraper on the same machine
// (direct loopback, no forwarding headers).
func TestMetricsAreForAdminsOrLoopback(t *testing.T) {
	server, _, inbox, database, users := accountServerPartsForTest(t)
	server.UseAgentsDB(database)
	handler := server.Handler()

	if response := getAs(handler, "/api/metrics", "203.0.113.7", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous metrics = %d, want 401", response.Code)
	}
	if err := users.AddWithEmail(context.Background(), "bea", "correct-horse-battery", identity.RoleMember, "bea@example.test"); err != nil {
		t.Fatal(err)
	}
	member, _ := signIn(t, server, inbox, "bea@example.test")
	if response := getAs(handler, "/api/metrics", "203.0.113.7", member); response.Code != http.StatusForbidden {
		t.Fatalf("a member's metrics = %d %s, want 403", response.Code, response.Body)
	}
	admin, _ := signIn(t, server, inbox, testEmail) // ann, the first account, is an admin
	response := getAs(handler, "/api/metrics", "203.0.113.7", admin)
	if response.Code != http.StatusOK {
		t.Fatalf("an admin's metrics = %d %s, want 200", response.Code, response.Body)
	}
	var metrics struct {
		DB map[string]struct {
			Open    *int `json:"open"`
			MaxOpen *int `json:"max_open"`
		} `json:"db"`
		Limiter struct {
			RejectedByClass map[string]int64 `json:"rejected_by_class"`
		} `json:"limiter"`
		Runs map[string]int64 `json:"runs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &metrics); err != nil {
		t.Fatalf("metrics body %s: %v", response.Body, err)
	}
	for _, pool := range []string{"write", "read"} {
		if stats, ok := metrics.DB[pool]; !ok || stats.Open == nil || stats.MaxOpen == nil {
			t.Fatalf("metrics db = %s, want the %s pool's stats", response.Body, pool)
		}
	}
	if metrics.Limiter.RejectedByClass == nil || metrics.Runs == nil {
		t.Fatalf("metrics = %s, want limiter refusals by class and runs by state", response.Body)
	}

	// A scraper on the same machine needs no account; the same request
	// through a proxy does.
	if response := getAs(handler, "/api/metrics", "127.0.0.1", nil); response.Code != http.StatusOK {
		t.Fatalf("direct loopback metrics = %d %s, want 200", response.Code, response.Body)
	}
	proxied := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	proxied.RemoteAddr = "127.0.0.1:40000"
	proxied.Header.Set("X-Forwarded-For", "203.0.113.7")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, proxied)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("proxied loopback metrics = %d, want 401", recorder.Code)
	}
}
