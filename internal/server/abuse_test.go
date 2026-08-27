package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/realtime"
)

func TestNodePageImportPathOnlyMatchesImport(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"/api/nodes/node-1/pages/import":  true,
		"/api/nodes/node-1/pages":         false,
		"/api/nodes/node-1/pages/page-1":  false,
		"/api/nodes//pages/import":        false,
		"/api/nodes/node-1/pages/import/": false,
		"/api/nodes/node-1/files/import":  false,
	}
	for path, want := range tests {
		path, want := path, want
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if got := nodePageImportPath(path); got != want {
				t.Fatalf("nodePageImportPath(%q) = %v, want %v", path, got, want)
			}
		})
	}
}

func TestOrdinaryPageRoutesAreNotExpensive(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/nodes/node-1/pages",
		"/api/nodes/node-1/pages/page-1",
	} {
		if expensivePath(path) {
			t.Fatalf("ordinary page route %q must not use the expensive-work limiter", path)
		}
		if docxHeavyPath(path) {
			t.Fatalf("ordinary page route %q must not use the DOCX semaphore", path)
		}
	}

	if !expensivePath("/api/nodes/node-1/pages/import") ||
		!docxHeavyPath("/api/nodes/node-1/pages/import") {
		t.Fatal("page import must retain expensive-work and DOCX limits")
	}
}

func TestTokenBucketRefillBurstAndCost(t *testing.T) {
	t.Parallel()

	store := newTokenBucketStore(100, 10*time.Minute)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rate := 10.0
	burst := 20.0

	// 1. Initial burst capacity
	for i := 0; i < 20; i++ {
		ok, _ := store.allow("test-key", start, rate, burst, 1.0)
		if !ok {
			t.Fatalf("initial request %d should have been allowed within burst", i)
		}
	}
	// 2. 21st request immediately after should fail
	ok, retrySec := store.allow("test-key", start, rate, burst, 1.0)
	if ok {
		t.Fatal("request beyond burst should have been rejected")
	}
	if retrySec < 1 {
		t.Fatalf("retrySec = %d, want >= 1", retrySec)
	}

	// 3. Time advance by 0.5s -> refills 5 tokens
	mid := start.Add(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		ok, _ := store.allow("test-key", mid, rate, burst, 1.0)
		if !ok {
			t.Fatalf("refilled request %d should have been allowed", i)
		}
	}
	ok, _ = store.allow("test-key", mid, rate, burst, 1.0)
	if ok {
		t.Fatal("request after consuming refilled tokens should be rejected")
	}

	// 4. Time advance by 10s -> refills to burst ceiling (20 tokens max)
	later := mid.Add(10 * time.Second)
	// Deduct cost = 4.0
	for i := 0; i < 5; i++ {
		ok, _ := store.allow("test-key", later, rate, burst, 4.0)
		if !ok {
			t.Fatalf("cost 4 request %d should have been allowed", i)
		}
	}
	// 20 tokens consumed by 5 * 4. Next cost 4 request should fail
	ok, retrySec = store.allow("test-key", later, rate, burst, 4.0)
	if ok {
		t.Fatal("request exceeding burst capacity with cost 4 should be rejected")
	}
	if retrySec < 1 {
		t.Fatalf("retrySec = %d, want >= 1", retrySec)
	}

	// 5. Backward clock skew protection
	past := later.Add(-5 * time.Second)
	ok, _ = store.allow("test-key", past, rate, burst, 1.0)
	if ok {
		t.Fatal("backward clock skew should not generate tokens")
	}
}

func TestTokenBucketStoreMemoryBoundingAndTTL(t *testing.T) {
	t.Parallel()

	maxEntries := 50
	ttl := 100 * time.Millisecond
	store := newTokenBucketStore(maxEntries, ttl)
	now := time.Now()

	// Fill store up to maxEntries
	for i := 0; i < maxEntries; i++ {
		store.allow("key-"+strconv.Itoa(i), now, 10, 10, 1.0)
	}
	if store.count() > maxEntries {
		t.Fatalf("store count %d exceeds max %d", store.count(), maxEntries)
	}

	// Insert more keys before TTL; eviction policy should keep entries bounded
	for i := maxEntries; i < maxEntries+100; i++ {
		store.allow("key-"+strconv.Itoa(i), now, 10, 10, 1.0)
		if store.count() > maxEntries {
			t.Fatalf("store count %d exceeded max %d during flood", store.count(), maxEntries)
		}
	}

	// Wait for TTL expiration
	later := now.Add(200 * time.Millisecond)
	store.allow("new-key", later, 10, 10, 1.0)
	if store.count() > maxEntries {
		t.Fatalf("store count %d exceeded max %d after TTL sweep", store.count(), maxEntries)
	}
}

func TestRouteClassificationCompleteness(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		method    string
		path      string
		wantClass RouteClass
		wantCost  float64
	}{
		{http.MethodGet, "/ws", ClassWSUpgrade, 1.0},
		{http.MethodPost, "/api/auth/login", ClassPublicAuth, 1.0},
		{http.MethodPost, "/api/auth/otp/request", ClassPublicAuth, 1.0},
		{http.MethodGet, "/api/auth/status", ClassPublicAuth, 1.0},
		{http.MethodGet, "/api/remote/drive/callback", ClassPublicAuth, 1.0},

		// Heavy routes
		{http.MethodPost, "/api/export", ClassHeavy, 2.0},
		{http.MethodGet, "/api/projects/export", ClassHeavy, 2.0},
		{http.MethodPost, "/api/projects/import", ClassHeavy, 2.0},
		{http.MethodPost, "/api/projects/import-path", ClassHeavy, 2.0},
		{http.MethodPost, "/api/nodes/node-1/pages/import", ClassHeavy, 2.0},
		{http.MethodPost, "/api/remote/push", ClassHeavy, 2.0},
		{http.MethodPost, "/api/remote/import", ClassHeavy, 2.0},
		{http.MethodPost, "/api/remote/sync/flush", ClassHeavy, 2.0},

		// Expensive routes
		{http.MethodGet, "/api/search", ClassExpensive, 2.0},
		{http.MethodGet, "/api/validate", ClassExpensive, 2.0},
		{http.MethodPost, "/api/dsl/check", ClassExpensive, 2.0},
		{http.MethodGet, "/api/history/version", ClassExpensive, 2.0},
		{http.MethodPost, "/api/history/restore", ClassExpensive, 2.0},
		{http.MethodPost, "/api/trash/restore", ClassExpensive, 2.0},
		{http.MethodPost, "/api/notify/test", ClassExpensive, 2.0},
		{http.MethodGet, "/api/audit", ClassExpensive, 2.0},
		{http.MethodGet, "/api/audit/health", ClassExpensive, 2.0},
		{http.MethodGet, "/api/remote/list", ClassExpensive, 2.0},
		{http.MethodGet, "/api/remote/drive/folders", ClassExpensive, 2.0},

		// Regular writes
		{http.MethodPut, "/api/graph", ClassWrite, 1.0},
		{http.MethodPost, "/api/graph/ops", ClassWrite, 1.0},
		{http.MethodPost, "/api/nodes", ClassWrite, 1.0},
		{http.MethodPut, "/api/nodes/n1", ClassWrite, 1.0},
		{http.MethodDelete, "/api/nodes/n1", ClassWrite, 1.0},
		{http.MethodPost, "/api/nodes/n1/duplicate", ClassWrite, 1.0},

		// Regular reads
		{http.MethodGet, "/api/graph", ClassRead, 1.0},
		{http.MethodGet, "/api/state", ClassRead, 1.0},
		{http.MethodGet, "/api/ready", ClassRead, 1.0},
		{http.MethodGet, "/api/nodes/n1", ClassRead, 1.0},
		{http.MethodGet, "/api/nodes/n1/pages", ClassRead, 1.0},
		{http.MethodGet, "/api/nodes/n1/pages/p1", ClassRead, 1.0},
		{http.MethodGet, "/api/projects", ClassRead, 1.0},
		{http.MethodGet, "/api/claims", ClassRead, 1.0},

		// Safe defaults for unknown future /api/ routes
		{http.MethodGet, "/api/future/feature", ClassRead, 1.0},
		{http.MethodPost, "/api/future/feature", ClassWrite, 1.0},
	}

	for _, tc := range testCases {
		gotClass, gotCost := ClassifyRoute(tc.method, tc.path)
		if gotClass != tc.wantClass || gotCost != tc.wantCost {
			t.Errorf("ClassifyRoute(%s, %q) = (%v, %v), want (%v, %v)",
				tc.method, tc.path, gotClass, gotCost, tc.wantClass, tc.wantCost)
		}
	}
}

func TestPreAuthLimitsFloodRejection(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		PreAuthGlobalRate:  1000,
		PreAuthGlobalBurst: 1000,
		PreAuthIPRate:      5,
		PreAuthIPBurst:     5,
	})

	srv := &Server{abuse: guard}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(srv.preAuthAbuseLimits)
	router.GET("/api/graph", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	router.GET("/index.html", func(c *gin.Context) {
		c.String(http.StatusOK, "spa")
	})

	// 5 requests from 192.0.2.1 should succeed
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d returned %d, want 200", i, w.Code)
		}
	}

	// 6th request from 192.0.2.1 must be 429
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("request beyond limit returned %d, want 429", w.Code)
	}
	if retry := w.Header().Get("Retry-After"); retry == "" {
		t.Fatal("expected Retry-After header on 429")
	}

	// SPA route /index.html from the same throttled IP should NOT be affected
	spaW := httptest.NewRecorder()
	spaReq := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	spaReq.RemoteAddr = "192.0.2.1:1234"
	router.ServeHTTP(spaW, spaReq)
	if spaW.Code != http.StatusOK {
		t.Fatalf("SPA route returned %d, want 200", spaW.Code)
	}
}

func TestMultiDimensionalLimiting(t *testing.T) {
	t.Parallel()

	now := time.Now()

	t.Run("ActorExhaustionConstrainsAcrossIPs", func(t *testing.T) {
		guard := newAbuseGuardWithOptions(AbuseGuardOptions{
			ReadGlobalRate:  1000,
			ReadGlobalBurst: 1000,
			ReadIPRate:      1000,
			ReadIPBurst:     1000,
			ReadActorRate:   3,
			ReadActorBurst:  3,
		})

		// Actor "alice" from IP1 consumes 3 tokens
		for i := 0; i < 3; i++ {
			ok, _ := guard.AllowRoute(ClassRead, 1.0, "alice", "192.0.2.1", now)
			if !ok {
				t.Fatalf("request %d for alice should have succeeded", i)
			}
		}

		// Actor "alice" from IP2 is blocked by actor quota
		ok, _ := guard.AllowRoute(ClassRead, 1.0, "alice", "198.51.100.1", now)
		if ok {
			t.Fatal("actor alice should be rate limited across different IPs")
		}

		// Different actor "bob" from IP2 is allowed
		ok, _ = guard.AllowRoute(ClassRead, 1.0, "bob", "198.51.100.1", now)
		if !ok {
			t.Fatal("actor bob should be allowed")
		}
	})

	t.Run("IPExhaustionConstrainsAcrossActors", func(t *testing.T) {
		guard := newAbuseGuardWithOptions(AbuseGuardOptions{
			ReadGlobalRate:  1000,
			ReadGlobalBurst: 1000,
			ReadIPRate:      3,
			ReadIPBurst:     3,
			ReadActorRate:   1000,
			ReadActorBurst:  1000,
		})

		// 3 distinct accounts from same IP
		for i := 0; i < 3; i++ {
			ok, _ := guard.AllowRoute(ClassRead, 1.0, "user-"+strconv.Itoa(i), "192.0.2.1", now)
			if !ok {
				t.Fatalf("request %d should have succeeded", i)
			}
		}

		// 4th distinct account from same IP is blocked by IP quota
		ok, _ := guard.AllowRoute(ClassRead, 1.0, "user-99", "192.0.2.1", now)
		if ok {
			t.Fatal("IP quota should block new accounts from same IP")
		}
	})

	t.Run("GlobalExhaustionConstrainsAll", func(t *testing.T) {
		guard := newAbuseGuardWithOptions(AbuseGuardOptions{
			ReadGlobalRate:  3,
			ReadGlobalBurst: 3,
			ReadIPRate:      1000,
			ReadIPBurst:     1000,
			ReadActorRate:   1000,
			ReadActorBurst:  1000,
		})

		for i := 0; i < 3; i++ {
			ok, _ := guard.AllowRoute(ClassRead, 1.0, "user-"+strconv.Itoa(i), "192.0.2."+strconv.Itoa(i), now)
			if !ok {
				t.Fatalf("request %d should have succeeded", i)
			}
		}

		ok, _ := guard.AllowRoute(ClassRead, 1.0, "new-user", "198.51.100.1", now)
		if ok {
			t.Fatal("global quota should block further requests across actors and IPs")
		}
	})
}

func TestSemaphoreConcurrencyGate(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		HeavyGlobalRate:     100,
		HeavyGlobalBurst:    100,
		HeavyIPRate:         100,
		HeavyIPBurst:        100,
		HeavyActorRate:      100,
		HeavyActorBurst:     100,
		MaxConcurrentHeavy:  2,
		MaxConcurrentDOCX:   1,
		MaxConcurrentRemote: 1,
	})

	srv := &Server{abuse: guard}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), identity.Local))
		c.Next()
	})
	router.Use(srv.withAbuseLimits)

	block := make(chan struct{})
	done := make(chan struct{})
	router.POST("/api/export", func(c *gin.Context) {
		close(done)
		<-block
		c.String(http.StatusOK, "export complete")
	})

	// Start 1st DOCX export in background (holds slot)
	go func() {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/export", nil)
		router.ServeHTTP(w, req)
	}()

	<-done

	// 2nd DOCX export should fail-fast with 429 "server is busy"
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/export", nil)
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent DOCX export returned %d, want 429", w2.Code)
	}

	var body map[string]string
	_ = json.Unmarshal(w2.Body.Bytes(), &body)
	if body["error"] != "server is busy" {
		t.Fatalf("error body = %v, want 'server is busy'", body)
	}

	// Release block
	close(block)
}

func TestAbuseStatsReporting(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate:  1,
		ReadGlobalBurst: 1,
	})

	now := time.Now()
	guard.AllowRoute(ClassRead, 1.0, "u1", "127.0.0.1", now)
	guard.AllowRoute(ClassRead, 1.0, "u1", "127.0.0.1", now) // rejected

	stats := guard.Stats()
	if stats.RejectedTotal < 1 {
		t.Fatalf("stats.RejectedTotal = %d, want >= 1", stats.RejectedTotal)
	}
	if stats.RejectedByClass[string(ClassRead)] < 1 {
		t.Fatalf("stats.RejectedByClass[read] = %d, want >= 1", stats.RejectedByClass[string(ClassRead)])
	}
}

func TestContextCancellationReleasesSemaphore(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		MaxConcurrentDOCX: 1,
	})
	srv := &Server{abuse: guard}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), identity.Local))
		c.Next()
	})
	router.Use(srv.withAbuseLimits)

	entered := make(chan struct{})
	router.POST("/api/export", func(c *gin.Context) {
		close(entered)
		<-c.Request.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/export", nil).WithContext(ctx)
		router.ServeHTTP(w, req)
	}()

	<-entered
	cancel() // Cancel request context
	wg.Wait()

	// Verify semaphore slot was freed and next request can acquire it
	slotAcquired := false
	router.POST("/api/export/next", func(c *gin.Context) {
		slotAcquired = true
		c.String(http.StatusOK, "ok")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/export", nil)
	// We can test acquiring the docx semaphore
	select {
	case guard.docxSem <- struct{}{}:
		<-guard.docxSem
		slotAcquired = true
	default:
		slotAcquired = false
	}
	if !slotAcquired {
		t.Fatal("docx semaphore was not released after context cancellation")
	}
	_ = w
	_ = req
}

func TestGlobalStarvationRegressionAttackerCannotStarveLegitimateUser(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate:  100,
		ReadGlobalBurst: 100,
		ReadIPRate:      2,
		ReadIPBurst:     2,
		ReadActorRate:   100,
		ReadActorBurst:  100,
	})

	now := time.Now()
	attackerIP := "198.51.100.1"
	legitimateIP := "203.0.113.1"

	// Attacker consumes allowed 2 requests
	for i := 0; i < 2; i++ {
		ok, _ := guard.AllowRoute(ClassRead, 1.0, "attacker", attackerIP, now)
		if !ok {
			t.Fatalf("attacker request %d should have been allowed under burst", i)
		}
	}

	// Attacker floods 50 rejected requests
	for i := 0; i < 50; i++ {
		ok, _ := guard.AllowRoute(ClassRead, 1.0, "attacker", attackerIP, now)
		if ok {
			t.Fatalf("attacker flooded request %d should have been blocked", i)
		}
	}

	// Legitimate user arrives from a different IP.
	// Since atomic allowMulti protects global tokens from being deducted on rejected IP checks,
	// legitimate user's quota MUST NOT be starved!
	for i := 0; i < 2; i++ {
		ok, _ := guard.AllowRoute(ClassRead, 1.0, "alice", legitimateIP, now)
		if !ok {
			t.Fatalf("legitimate user request %d was starved by attacker's rejected requests!", i)
		}
	}
}

func TestTokenBucketStoreLRUEvictionAndPinnedGlobalBuckets(t *testing.T) {
	t.Parallel()

	maxEntries := 10
	store := newTokenBucketStore(maxEntries, 10*time.Minute)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Insert pinned global entries
	pinnedKeys := []string{"read:global", "write:global", "preauth:global"}
	for _, pk := range pinnedKeys {
		ok, _ := store.allow(pk, t0, 10, 10, 1.0)
		if !ok {
			t.Fatalf("pinned key %s insertion failed", pk)
		}
	}

	// Insert 20 ephemeral non-pinned keys with sequential seen times
	for i := 1; i <= 20; i++ {
		key := "ip-" + strconv.Itoa(i)
		tSeen := t0.Add(time.Duration(i) * time.Second)
		store.allow(key, tSeen, 10, 10, 1.0)
		if store.count() > maxEntries {
			t.Fatalf("store count %d exceeded max %d during flood", store.count(), maxEntries)
		}
	}

	// Verify all pinned global keys are STILL present and never evicted
	store.mu.Lock()
	for _, pk := range pinnedKeys {
		if store.entries[pk] == nil {
			t.Fatalf("pinned global key %q was illegally evicted!", pk)
		}
	}
	// Verify oldest ephemeral key (e.g. ip-1) was evicted, while newest key (ip-20) remains
	if store.entries["ip-1"] != nil {
		t.Fatal("oldest ephemeral key ip-1 was expected to be evicted under LRU")
	}
	if store.entries["ip-20"] == nil {
		t.Fatal("newest ephemeral key ip-20 should be present in store")
	}
	store.mu.Unlock()
}

func TestAllRegisteredRouterRoutesClassifiedAndBounded(t *testing.T) {
	t.Parallel()

	pm := projectManagerForTest(t)
	hub := realtime.NewHub()
	srv := serverForTest(t, pm, hub, nil)
	handler := srv.Handler()

	engine, ok := handler.(*gin.Engine)
	if !ok {
		t.Fatal("handler is not a *gin.Engine")
	}

	routes := engine.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes registered on engine")
	}

	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api") && route.Path != "/ws" {
			continue
		}
		class, cost := ClassifyRoute(route.Method, route.Path)
		if class == "" {
			t.Errorf("route %s %s classified to empty class", route.Method, route.Path)
		}
		if cost < 1.0 {
			t.Errorf("route %s %s assigned cost %f < 1.0", route.Method, route.Path, cost)
		}
	}
}

type mockActorAuth struct{}

func (m mockActorAuth) Authenticate(r *http.Request) (identity.Actor, error) {
	if actor := auth.ActorFrom(r); actor.ID != "" {
		return actor, nil
	}
	return identity.Local, nil
}
func (m mockActorAuth) NeedsCSRF() bool { return false }
func (m mockActorAuth) Remote() bool    { return false }

func TestAuthenticationRoleAbuseIntegration(t *testing.T) {
	t.Parallel()

	pm := projectManagerForTest(t)
	hub := realtime.NewHub()
	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate:   100,
		ReadGlobalBurst:  100,
		ReadIPRate:       100,
		ReadIPBurst:      100,
		ReadActorRate:    3,
		ReadActorBurst:   3,
		WriteGlobalRate:  100,
		WriteGlobalBurst: 100,
		WriteIPRate:      100,
		WriteIPBurst:     100,
		WriteActorRate:   3,
		WriteActorBurst:  3,
	})

	srv := serverForTest(t, pm, hub, nil)
	srv.abuse = guard
	srv.auth = mockActorAuth{}
	handler := srv.Handler()

	t.Run("VisitorCannotPerformWrite", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(`{"title":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		// Simulate visitor context
		req = req.WithContext(auth.WithActor(req.Context(), identity.Actor{ID: "visitor-1", Name: "visitor", Role: identity.RoleVisitor}))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("visitor write returned %d, want 403", rec.Code)
		}
	})

	t.Run("ActorRateLimitTracksDistinctRoles", func(t *testing.T) {
		// User1 makes 3 read requests (allowed)
		for i := 0; i < 3; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
			req.RemoteAddr = "192.168.1.10:1234"
			req = req.WithContext(auth.WithActor(req.Context(), identity.Actor{ID: "editor-1", Name: "editor1", Role: identity.RoleMember}))
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("editor-1 request %d returned %d, want 200", i, rec.Code)
			}
		}

		// User1 4th read request is rate-limited
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		req.RemoteAddr = "192.168.1.10:1234"
		req = req.WithContext(auth.WithActor(req.Context(), identity.Actor{ID: "editor-1", Name: "editor1", Role: identity.RoleMember}))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("editor-1 4th request returned %d, want 429", rec.Code)
		}

		// User2 (editor-2) from different IP is not affected by User1's actor limit
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		req2.RemoteAddr = "192.168.1.20:1234"
		req2 = req2.WithContext(auth.WithActor(req2.Context(), identity.Actor{ID: "editor-2", Name: "editor2", Role: identity.RoleMember}))
		handler.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("editor-2 request returned %d, want 200", rec2.Code)
		}
	})
}

func TestWebSocketMessageRateLimitFloodAnd1008(t *testing.T) {
	t.Parallel()

	pm := projectManagerForTest(t)
	hub := realtime.NewHub()
	srv := serverForTest(t, pm, hub, nil)
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.CloseNow()

	// Read hello message
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}

	// Flood messages rapidly beyond wsMessageBurst (burst is 20)
	for i := 0; i < 50; i++ {
		msg := map[string]any{"type": "presence", "nodeId": "node-1", "editing": false}
		data, _ := json.Marshal(msg)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			break
		}
	}

	// Server should close the connection with StatusPolicyViolation (1008)
	closedWith1008 := false
	var lastErr error
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			lastErr = err
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
				closedWith1008 = true
			}
			break
		}
	}
	if !closedWith1008 {
		t.Fatalf("expected close code 1008 (StatusPolicyViolation), got err: %v (close status: %v)", lastErr, websocket.CloseStatus(lastErr))
	}
}

func TestAbuseLimiterConcurrentStressAndGoroutineSafety(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate:  10000,
		ReadGlobalBurst: 10000,
		ReadIPRate:      1000,
		ReadIPBurst:     1000,
		ReadActorRate:   1000,
		ReadActorBurst:  1000,
	})

	initialGoroutines := runtime.NumGoroutine()
	var wg sync.WaitGroup
	numWorkers := 30
	opsPerWorker := 100

	start := time.Now()
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		workerID := strconv.Itoa(w)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				guard.AllowRoute(ClassRead, 1.0, "user-"+workerID, "192.168.1."+workerID, time.Now())
				guard.AllowPreAuth("192.168.1."+workerID, time.Now())
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	stats := guard.Stats()
	if stats.ActiveBuckets == 0 {
		t.Fatal("expected active buckets in limiter stats")
	}

	// Goroutine leak check
	time.Sleep(50 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+20 {
		t.Fatalf("potential goroutine leak: before=%d, after=%d", initialGoroutines, finalGoroutines)
	}

	t.Logf("Stress test completed %d operations across %d workers in %v (active buckets: %d)",
		numWorkers*opsPerWorker*2, numWorkers, elapsed, stats.ActiveBuckets)
}

func TestAbuseLimiterHighLoadMemoryAndGoroutineMetrics(t *testing.T) {
	t.Parallel()

	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate:  100000,
		ReadGlobalBurst: 100000,
		ReadIPRate:      10000,
		ReadIPBurst:     10000,
		ReadActorRate:   10000,
		ReadActorBurst:  10000,
	})

	runtime.GC()
	var mStart runtime.MemStats
	runtime.ReadMemStats(&mStart)
	gStart := runtime.NumGoroutine()

	numWorkers := 50
	opsPerWorker := 2000
	totalOps := numWorkers * opsPerWorker

	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		wID := strconv.Itoa(w)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				guard.AllowRoute(ClassRead, 1.0, "user-"+wID, "10.0.0."+wID, time.Now())
			}
		}()
	}
	wg.Wait()
	duration := time.Since(start)

	var mEnd runtime.MemStats
	runtime.ReadMemStats(&mEnd)
	gEnd := runtime.NumGoroutine()

	t.Logf("High Load Metrics: %d ops in %v (%.2f ops/sec), Alloc delta: %d KB, Goroutines start=%d end=%d",
		totalOps, duration, float64(totalOps)/duration.Seconds(), (mEnd.Alloc-mStart.Alloc)/1024, gStart, gEnd)
}

func BenchmarkAbuseLimiterAllowRouteParallel(b *testing.B) {
	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate:  1000000,
		ReadGlobalBurst: 1000000,
		ReadIPRate:      1000000,
		ReadIPBurst:     1000000,
		ReadActorRate:   1000000,
		ReadActorBurst:  1000000,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		now := time.Now()
		for pb.Next() {
			guard.AllowRoute(ClassRead, 1.0, "actor-bench", "127.0.0.1", now)
		}
	})
}
