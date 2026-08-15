package server

import (
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
)

const (
	expensiveRate  = 4.0
	expensiveBurst = 8.0
	writeRate      = 30.0
	writeBurst     = 60.0
	abuseEntryTTL  = 10 * time.Minute
	maxAbuseActors = 2048
)

type abuseEntry struct {
	tokens float64
	last   time.Time
	seen   time.Time
}

type abuseGuard struct {
	sem     chan struct{}
	docxSem chan struct{}
	mu      sync.Mutex
	byActor map[string]*abuseEntry
}

func newAbuseGuard() *abuseGuard {
	parallel := runtime.GOMAXPROCS(0)
	if parallel < 2 {
		parallel = 2
	}
	if parallel > 8 {
		parallel = 8
	}
	return &abuseGuard{
		sem:     make(chan struct{}, parallel),
		docxSem: make(chan struct{}, 2),
		byActor: make(map[string]*abuseEntry),
	}
}

func expensivePath(path string) bool {
	if path == "/api/search" || path == "/api/export" ||
		path == "/api/projects/export" || path == "/api/projects/import" {
		return true
	}
	if nodePageImportPath(path) {
		return true
	}
	return strings.HasPrefix(path, "/api/remote/") &&
		path != "/api/remote/config" &&
		path != "/api/remote/drive/credentials" &&
		path != "/api/remote/drive/callback" &&
		path != "/api/remote/drive/auth"
}

func nodePageImportPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/api/nodes/")
	if !ok {
		return false
	}
	nodeID, suffix, ok := strings.Cut(rest, "/")
	return ok && nodeID != "" && suffix == "pages/import"
}

func docxHeavyPath(path string) bool {
	return path == "/api/export" || nodePageImportPath(path)
}

func (g *abuseGuard) allow(key string, now time.Time, rate, burst float64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.byActor) >= maxAbuseActors {
		for actor, entry := range g.byActor {
			if now.Sub(entry.seen) > abuseEntryTTL {
				delete(g.byActor, actor)
			}
		}
	}
	entry := g.byActor[key]
	if entry == nil {
		if len(g.byActor) >= maxAbuseActors {
			return false
		}
		entry = &abuseEntry{tokens: burst, last: now}
		g.byActor[key] = entry
	}
	entry.tokens += now.Sub(entry.last).Seconds() * rate
	if entry.tokens > burst {
		entry.tokens = burst
	}
	entry.last = now
	entry.seen = now
	if entry.tokens < 1 {
		return false
	}
	entry.tokens--
	return true
}

// withAbuseLimits bounds expensive authenticated work independently of body
// size. It protects CPU, temporary disk and cloud quotas from one account
// issuing many concurrent requests.
func (s *Server) withAbuseLimits(c *gin.Context) {
	if s.abuse == nil {
		c.Next()
		return
	}
	actor := auth.ActorFrom(c.Request)
	now := time.Now()
	if strings.HasPrefix(c.Request.URL.Path, "/api/") && isUnsafeMethod(c.Request.Method) &&
		!strings.HasPrefix(c.Request.URL.Path, "/api/auth/") &&
		!s.abuse.allow("write:"+actor.ID, now, writeRate, writeBurst) {
		c.Header("Retry-After", "1")
		httpx.Err(c, http.StatusTooManyRequests, errors.New("too many write requests"))
		return
	}
	if !expensivePath(c.Request.URL.Path) {
		c.Next()
		return
	}
	if !s.abuse.allow("expensive:"+actor.ID, now, expensiveRate, expensiveBurst) {
		c.Header("Retry-After", "1")
		httpx.Err(c, http.StatusTooManyRequests, errors.New("too many expensive requests"))
		return
	}
	semaphore := s.abuse.sem
	if docxHeavyPath(c.Request.URL.Path) {
		semaphore = s.abuse.docxSem
	}
	select {
	case semaphore <- struct{}{}:
		defer func() { <-semaphore }()
		c.Next()
	default:
		c.Header("Retry-After", "1")
		httpx.Err(c, http.StatusTooManyRequests, errors.New("server is busy"))
	}
}
