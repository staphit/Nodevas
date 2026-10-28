package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
)

// queueRouter serves GET /api/state behind withAbuseLimits, signed in as
// actor, with read buckets of the given rate and burst per address and per
// actor.
func queueRouter(authn auth.Authenticator, actor identity.Actor, rate, burst float64) *gin.Engine {
	guard := newAbuseGuardWithOptions(AbuseGuardOptions{
		ReadGlobalRate: 1000, ReadGlobalBurst: 1000,
		ReadIPRate: rate, ReadIPBurst: burst,
		ReadActorRate: rate, ReadActorBurst: burst,
	})
	srv := &Server{abuse: guard, auth: authn}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	router.Use(srv.withAbuseLimits)
	router.GET("/api/state", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

func serveRead(router *gin.Engine, ctx context.Context) int {
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil).WithContext(ctx)
	request.RemoteAddr = "127.0.0.1:50000"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code
}

// Switching projects eight times in a second, in two tabs, sends a few
// hundred reads at once: a burst past the bucket is someone clicking, so the
// reads wait their turn instead of failing, on a standalone hub (address
// bucket) and on a hub with accounts (actor bucket) alike.
func TestRapidReadsPastTheBurstQueueInsteadOfFailing(t *testing.T) {
	t.Parallel()

	member := identity.Actor{ID: "member-1", Name: "member", Role: identity.RoleMember}
	cases := map[string]*gin.Engine{
		"standalone": queueRouter(auth.LocalOnly{}, identity.Local, 50, 10),
		"accounts":   queueRouter(auth.NewSessionAuth(nil), member, 50, 10),
	}
	for name, router := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			const reads = 40 // 30 past the burst: 0.6 s of refill at 50/s
			codes := make([]int, reads)
			var wg sync.WaitGroup
			for i := range reads {
				wg.Add(1)
				go func() {
					defer wg.Done()
					codes[i] = serveRead(router, context.Background())
				}()
			}
			wg.Wait()
			for i, code := range codes {
				if code != http.StatusOK {
					t.Fatalf("read %d of a fast burst returned %d, want 200 after a wait", i+1, code)
				}
			}
		})
	}
}

// The queue is bounded: a read that would wait past it is refused at once,
// with a Retry-After, rather than held.
func TestReadQueueIsBounded(t *testing.T) {
	t.Parallel()

	member := identity.Actor{ID: "member-1", Name: "member", Role: identity.RoleMember}
	router := queueRouter(auth.NewSessionAuth(nil), member, 0.001, 1)
	if code := serveRead(router, context.Background()); code != http.StatusOK {
		t.Fatalf("first read returned %d, want 200", code)
	}
	started := time.Now()
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("read past the queue returned %d, want 429", response.Code)
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("a refused read has no Retry-After")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a read past the queue was held %v before its 429", elapsed)
	}
}

// A queued read whose client gives up (the tab switched project again) gives
// its place back: the reads after it do not wait for it.
func TestAbandonedQueuedReadGivesItsPlaceBack(t *testing.T) {
	t.Parallel()

	member := identity.Actor{ID: "member-1", Name: "member", Role: identity.RoleMember}
	router := queueRouter(auth.NewSessionAuth(nil), member, 2, 1)
	if code := serveRead(router, context.Background()); code != http.StatusOK {
		t.Fatalf("first read returned %d, want 200", code)
	}
	// Due in 0.5 s; its client leaves after 50 ms.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if code := serveRead(router, ctx); code == http.StatusTooManyRequests {
		t.Fatal("a queued read was refused instead of waiting")
	}
	// Without the place given back this one waits about 0.95 s; with it, 0.45.
	started := time.Now()
	if code := serveRead(router, context.Background()); code != http.StatusOK {
		t.Fatalf("read after an abandoned one returned %d, want 200", code)
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("read after an abandoned one waited %v, want about 450ms", elapsed)
	}
}
