package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
)

type RouteClass string

const (
	ClassPublicAuth RouteClass = "public-auth"
	ClassRead       RouteClass = "read"
	ClassWrite      RouteClass = "write"
	ClassExpensive  RouteClass = "expensive"
	ClassHeavy      RouteClass = "heavy"
	ClassWSUpgrade  RouteClass = "ws-upgrade"
)

// Default limiter rates and bursts
const (
	defaultPreAuthGlobalRate  = 200.0
	defaultPreAuthGlobalBurst = 400.0
	defaultPreAuthIPRate      = 40.0
	defaultPreAuthIPBurst     = 80.0

	defaultReadGlobalRate  = 300.0
	defaultReadGlobalBurst = 600.0
	defaultReadIPRate      = 60.0
	defaultReadIPBurst     = 120.0
	defaultReadActorRate   = 40.0
	defaultReadActorBurst  = 80.0

	defaultWriteGlobalRate  = 150.0
	defaultWriteGlobalBurst = 300.0
	defaultWriteIPRate      = 40.0
	defaultWriteIPBurst     = 80.0
	defaultWriteActorRate   = 30.0
	defaultWriteActorBurst  = 60.0

	defaultExpensiveGlobalRate  = 20.0
	defaultExpensiveGlobalBurst = 40.0
	defaultExpensiveIPRate      = 8.0
	defaultExpensiveIPBurst     = 24.0
	defaultExpensiveActorRate   = 4.0
	defaultExpensiveActorBurst  = 16.0

	defaultHeavyGlobalRate  = 10.0
	defaultHeavyGlobalBurst = 30.0
	defaultHeavyIPRate      = 6.0
	defaultHeavyIPBurst     = 18.0
	defaultHeavyActorRate   = 4.0
	defaultHeavyActorBurst  = 16.0

	defaultWSUpgradeGlobalRate  = 40.0
	defaultWSUpgradeGlobalBurst = 80.0
	defaultWSUpgradeIPRate      = 10.0
	defaultWSUpgradeIPBurst     = 20.0
	defaultWSUpgradeActorRate   = 5.0
	defaultWSUpgradeActorBurst  = 10.0

	abuseEntryTTL   = 10 * time.Minute
	maxAbuseEntries = 4096
)

type abuseEntry struct {
	tokens float64
	last   time.Time
	seen   time.Time
}

type bucketCheck struct {
	key   string
	rate  float64
	burst float64
	cost  float64
}

type tokenBucketStore struct {
	mu         sync.Mutex
	entries    map[string]*abuseEntry
	maxEntries int
	ttl        time.Duration
}

func newTokenBucketStore(maxEntries int, ttl time.Duration) *tokenBucketStore {
	if maxEntries <= 0 {
		maxEntries = maxAbuseEntries
	}
	if ttl <= 0 {
		ttl = abuseEntryTTL
	}
	return &tokenBucketStore{
		entries:    make(map[string]*abuseEntry),
		maxEntries: maxEntries,
		ttl:        ttl,
	}
}

func isPinnedBucket(key string) bool {
	return strings.HasSuffix(key, ":global") || strings.HasPrefix(key, "preauth:global") || strings.HasPrefix(key, "global:")
}

// allowMulti evaluates all bucket checks atomically.
// Tokens are only deducted from all buckets if every check passes.
// If any check fails, no tokens are consumed and the maximum retry duration is returned.
func (s *tokenBucketStore) allowMulti(checks []bucketCheck, now time.Time) (bool, int) {
	if len(checks) == 0 {
		return true, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Memory bounding & cleanup strategy
	if len(s.entries) >= s.maxEntries {
		for k, entry := range s.entries {
			if !isPinnedBucket(k) && now.Sub(entry.seen) > s.ttl {
				delete(s.entries, k)
			}
		}
		// If still full after TTL cleanup, evict oldest non-pinned entries (LRU by seen timestamp)
		if len(s.entries) >= s.maxEntries {
			type kv struct {
				k    string
				seen time.Time
			}
			evictable := make([]kv, 0, len(s.entries))
			for k, entry := range s.entries {
				if !isPinnedBucket(k) {
					evictable = append(evictable, kv{k: k, seen: entry.seen})
				}
			}
			// Sort oldest seen first
			for i := 0; i < len(evictable)-1; i++ {
				for j := i + 1; j < len(evictable); j++ {
					if evictable[j].seen.Before(evictable[i].seen) {
						evictable[i], evictable[j] = evictable[j], evictable[i]
					}
				}
			}
			toRemove := s.maxEntries / 5
			if toRemove < 1 {
				toRemove = 1
			}
			for i := 0; i < len(evictable) && i < toRemove; i++ {
				delete(s.entries, evictable[i].k)
			}
		}
	}

	// 2. First pass: check token availability across all dimensions without mutation
	type evaluatedBucket struct {
		key       string
		entry     *abuseEntry
		newTokens float64
		cost      float64
	}
	evaluated := make([]evaluatedBucket, 0, len(checks))
	maxRetry := 0
	allAllowed := true

	for _, check := range checks {
		cost := check.cost
		if cost <= 0 {
			cost = 1.0
		}

		entry := s.entries[check.key]
		var curTokens float64
		var lastTime time.Time

		if entry == nil {
			if len(s.entries) >= s.maxEntries && !isPinnedBucket(check.key) {
				return false, 1
			}
			curTokens = check.burst
			lastTime = now
		} else {
			curTokens = entry.tokens
			lastTime = entry.last
		}

		elapsed := now.Sub(lastTime).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		curTokens += elapsed * check.rate
		if curTokens > check.burst {
			curTokens = check.burst
		}

		if curTokens < cost {
			missing := cost - curTokens
			retrySec := int(math.Ceil(missing / check.rate))
			if retrySec < 1 {
				retrySec = 1
			}
			if retrySec > maxRetry {
				maxRetry = retrySec
			}
			allAllowed = false
		} else {
			evaluated = append(evaluated, evaluatedBucket{
				key:       check.key,
				entry:     entry,
				newTokens: curTokens - cost,
				cost:      cost,
			})
		}
	}

	// 3. If any dimension failed, abort atomically without deducting tokens
	if !allAllowed {
		return false, maxRetry
	}

	// 4. Commit: all checks passed, apply token deductions and update seen/last
	for _, eb := range evaluated {
		entry := eb.entry
		if entry == nil {
			entry = &abuseEntry{
				tokens: eb.newTokens,
				last:   now,
				seen:   now,
			}
			s.entries[eb.key] = entry
		} else {
			entry.tokens = eb.newTokens
			entry.last = now
			entry.seen = now
		}
	}

	return true, 0
}

func (s *tokenBucketStore) allow(key string, now time.Time, rate, burst, cost float64) (bool, int) {
	return s.allowMulti([]bucketCheck{{key: key, rate: rate, burst: burst, cost: cost}}, now)
}

func (s *tokenBucketStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// AbuseGuardOptions configures custom rates, bursts, and concurrency bounds.
type AbuseGuardOptions struct {
	PreAuthGlobalRate    float64
	PreAuthGlobalBurst   float64
	PreAuthIPRate        float64
	PreAuthIPBurst       float64
	ReadGlobalRate       float64
	ReadGlobalBurst      float64
	ReadIPRate           float64
	ReadIPBurst          float64
	ReadActorRate        float64
	ReadActorBurst       float64
	WriteGlobalRate      float64
	WriteGlobalBurst     float64
	WriteIPRate          float64
	WriteIPBurst         float64
	WriteActorRate       float64
	WriteActorBurst      float64
	ExpensiveGlobalRate  float64
	ExpensiveGlobalBurst float64
	ExpensiveIPRate      float64
	ExpensiveIPBurst     float64
	ExpensiveActorRate   float64
	ExpensiveActorBurst  float64
	HeavyGlobalRate      float64
	HeavyGlobalBurst     float64
	HeavyIPRate          float64
	HeavyIPBurst         float64
	HeavyActorRate       float64
	HeavyActorBurst      float64
	WSUpgradeGlobalRate  float64
	WSUpgradeGlobalBurst float64
	WSUpgradeIPRate      float64
	WSUpgradeIPBurst     float64
	WSUpgradeActorRate   float64
	WSUpgradeActorBurst  float64
	MaxConcurrentHeavy   int
	MaxConcurrentDOCX    int
	MaxConcurrentRemote  int
}

type abuseGuard struct {
	store *tokenBucketStore

	sem       chan struct{} // general heavy / expensive
	docxSem   chan struct{} // DOCX heavy
	remoteSem chan struct{} // remote sync heavy

	preAuthGlobalRate    float64
	preAuthGlobalBurst   float64
	preAuthIPRate        float64
	preAuthIPBurst       float64
	readGlobalRate       float64
	readGlobalBurst      float64
	readIPRate           float64
	readIPBurst          float64
	readActorRate        float64
	readActorBurst       float64
	writeGlobalRate      float64
	writeGlobalBurst     float64
	writeIPRate          float64
	writeIPBurst         float64
	writeActorRate       float64
	writeActorBurst      float64
	expensiveGlobalRate  float64
	expensiveGlobalBurst float64
	expensiveIPRate      float64
	expensiveIPBurst     float64
	expensiveActorRate   float64
	expensiveActorBurst  float64
	heavyGlobalRate      float64
	heavyGlobalBurst     float64
	heavyIPRate          float64
	heavyIPBurst         float64
	heavyActorRate       float64
	heavyActorBurst      float64
	wsUpgradeGlobalRate  float64
	wsUpgradeGlobalBurst float64
	wsUpgradeIPRate      float64
	wsUpgradeIPBurst     float64
	wsUpgradeActorRate   float64
	wsUpgradeActorBurst  float64

	statsMu         sync.Mutex
	rejectedByClass map[RouteClass]int64
	rejectedTotal   int64

	logMu     sync.Mutex
	logTokens float64
	logLast   time.Time
}

func newAbuseGuard() *abuseGuard {
	return newAbuseGuardWithOptions(AbuseGuardOptions{})
}

func newAbuseGuardWithOptions(opts AbuseGuardOptions) *abuseGuard {
	parallel := opts.MaxConcurrentHeavy
	if parallel <= 0 {
		parallel = runtime.GOMAXPROCS(0)
		if parallel < 2 {
			parallel = 2
		}
		if parallel > 8 {
			parallel = 8
		}
	}
	docxParallel := opts.MaxConcurrentDOCX
	if docxParallel <= 0 {
		docxParallel = 2
	}
	remoteParallel := opts.MaxConcurrentRemote
	if remoteParallel <= 0 {
		remoteParallel = 2
	}

	setFloat := func(val, def float64) float64 {
		if val > 0 {
			return val
		}
		return def
	}

	return &abuseGuard{
		store:     newTokenBucketStore(maxAbuseEntries, abuseEntryTTL),
		sem:       make(chan struct{}, parallel),
		docxSem:   make(chan struct{}, docxParallel),
		remoteSem: make(chan struct{}, remoteParallel),

		preAuthGlobalRate:  setFloat(opts.PreAuthGlobalRate, defaultPreAuthGlobalRate),
		preAuthGlobalBurst: setFloat(opts.PreAuthGlobalBurst, defaultPreAuthGlobalBurst),
		preAuthIPRate:      setFloat(opts.PreAuthIPRate, defaultPreAuthIPRate),
		preAuthIPBurst:     setFloat(opts.PreAuthIPBurst, defaultPreAuthIPBurst),

		readGlobalRate:  setFloat(opts.ReadGlobalRate, defaultReadGlobalRate),
		readGlobalBurst: setFloat(opts.ReadGlobalBurst, defaultReadGlobalBurst),
		readIPRate:      setFloat(opts.ReadIPRate, defaultReadIPRate),
		readIPBurst:     setFloat(opts.ReadIPBurst, defaultReadIPBurst),
		readActorRate:   setFloat(opts.ReadActorRate, defaultReadActorRate),
		readActorBurst:  setFloat(opts.ReadActorBurst, defaultReadActorBurst),

		writeGlobalRate:  setFloat(opts.WriteGlobalRate, defaultWriteGlobalRate),
		writeGlobalBurst: setFloat(opts.WriteGlobalBurst, defaultWriteGlobalBurst),
		writeIPRate:      setFloat(opts.WriteIPRate, defaultWriteIPRate),
		writeIPBurst:     setFloat(opts.WriteIPBurst, defaultWriteIPBurst),
		writeActorRate:   setFloat(opts.WriteActorRate, defaultWriteActorRate),
		writeActorBurst:  setFloat(opts.WriteActorBurst, defaultWriteActorBurst),

		expensiveGlobalRate:  setFloat(opts.ExpensiveGlobalRate, defaultExpensiveGlobalRate),
		expensiveGlobalBurst: setFloat(opts.ExpensiveGlobalBurst, defaultExpensiveGlobalBurst),
		expensiveIPRate:      setFloat(opts.ExpensiveIPRate, defaultExpensiveIPRate),
		expensiveIPBurst:     setFloat(opts.ExpensiveIPBurst, defaultExpensiveIPBurst),
		expensiveActorRate:   setFloat(opts.ExpensiveActorRate, defaultExpensiveActorRate),
		expensiveActorBurst:  setFloat(opts.ExpensiveActorBurst, defaultExpensiveActorBurst),

		heavyGlobalRate:  setFloat(opts.HeavyGlobalRate, defaultHeavyGlobalRate),
		heavyGlobalBurst: setFloat(opts.HeavyGlobalBurst, defaultHeavyGlobalBurst),
		heavyIPRate:      setFloat(opts.HeavyIPRate, defaultHeavyIPRate),
		heavyIPBurst:     setFloat(opts.HeavyIPBurst, defaultHeavyIPBurst),
		heavyActorRate:   setFloat(opts.HeavyActorRate, defaultHeavyActorRate),
		heavyActorBurst:  setFloat(opts.HeavyActorBurst, defaultHeavyActorBurst),

		wsUpgradeGlobalRate:  setFloat(opts.WSUpgradeGlobalRate, defaultWSUpgradeGlobalRate),
		wsUpgradeGlobalBurst: setFloat(opts.WSUpgradeGlobalBurst, defaultWSUpgradeGlobalBurst),
		wsUpgradeIPRate:      setFloat(opts.WSUpgradeIPRate, defaultWSUpgradeIPRate),
		wsUpgradeIPBurst:     setFloat(opts.WSUpgradeIPBurst, defaultWSUpgradeIPBurst),
		wsUpgradeActorRate:   setFloat(opts.WSUpgradeActorRate, defaultWSUpgradeActorRate),
		wsUpgradeActorBurst:  setFloat(opts.WSUpgradeActorBurst, defaultWSUpgradeActorBurst),

		rejectedByClass: make(map[RouteClass]int64),
		logTokens:       20.0,
		logLast:         time.Now(),
	}
}

// ClassifyRoute categorizes a request into a route class with an associated cost.
// Any unknown /api/* route falls back to a safe default class (ClassWrite if unsafe, ClassRead if safe).
func ClassifyRoute(method, path string) (RouteClass, float64) {
	if path == "/ws" {
		return ClassWSUpgrade, 1.0
	}
	if path == "/api/auth/login" || path == "/api/auth/otp/request" ||
		path == "/api/auth/status" || path == "/api/remote/drive/callback" {
		return ClassPublicAuth, 1.0
	}
	if isHeavyPath(path) {
		return ClassHeavy, 2.0
	}
	if isExpensivePath(path) {
		return ClassExpensive, 2.0
	}
	if isUnsafeMethod(method) {
		return ClassWrite, 1.0
	}
	return ClassRead, 1.0
}

func isHeavyPath(path string) bool {
	if path == "/api/export" || path == "/api/projects/export" ||
		path == "/api/projects/import" || path == "/api/projects/import-path" {
		return true
	}
	if nodePageImportPath(path) {
		return true
	}
	if path == "/api/remote/push" || path == "/api/remote/import" || path == "/api/remote/sync/flush" {
		return true
	}
	return false
}

func isExpensivePath(path string) bool {
	if path == "/api/search" || path == "/api/validate" || path == "/api/dsl/check" ||
		path == "/api/history/version" || path == "/api/history/restore" ||
		path == "/api/trash/restore" || path == "/api/notify/test" ||
		path == "/api/audit" || path == "/api/audit/health" {
		return true
	}
	return strings.HasPrefix(path, "/api/remote/") &&
		path != "/api/remote/config" &&
		path != "/api/remote/sync/status" &&
		path != "/api/remote/drive/credentials" &&
		path != "/api/remote/drive/callback" &&
		path != "/api/remote/drive/auth"
}

func expensivePath(path string) bool {
	return isHeavyPath(path) || isExpensivePath(path)
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

func remoteHeavyPath(path string) bool {
	return path == "/api/remote/push" ||
		path == "/api/remote/import" ||
		path == "/api/remote/sync/flush"
}

func (g *abuseGuard) recordRejection(class RouteClass) {
	g.statsMu.Lock()
	defer g.statsMu.Unlock()
	g.rejectedTotal++
	g.rejectedByClass[class]++
}

func (g *abuseGuard) AllowPreAuth(ip string, now time.Time) (bool, int) {
	checks := []bucketCheck{
		{key: "preauth:global", rate: g.preAuthGlobalRate, burst: g.preAuthGlobalBurst, cost: 1.0},
	}
	if ip != "" {
		checks = append(checks, bucketCheck{
			key:   "preauth:ip:" + ip,
			rate:  g.preAuthIPRate,
			burst: g.preAuthIPBurst,
			cost:  1.0,
		})
	}
	ok, retry := g.store.allowMulti(checks, now)
	if !ok {
		g.recordRejection(ClassPublicAuth)
		return false, retry
	}
	return true, 0
}

func (g *abuseGuard) AllowRoute(class RouteClass, cost float64, actorID, ip string, now time.Time) (bool, int) {
	if class == ClassPublicAuth {
		return true, 0
	}

	var gRate, gBurst, ipRate, ipBurst, actRate, actBurst float64
	switch class {
	case ClassRead:
		gRate, gBurst = g.readGlobalRate, g.readGlobalBurst
		ipRate, ipBurst = g.readIPRate, g.readIPBurst
		actRate, actBurst = g.readActorRate, g.readActorBurst
	case ClassWrite:
		gRate, gBurst = g.writeGlobalRate, g.writeGlobalBurst
		ipRate, ipBurst = g.writeIPRate, g.writeIPBurst
		actRate, actBurst = g.writeActorRate, g.writeActorBurst
	case ClassExpensive:
		gRate, gBurst = g.expensiveGlobalRate, g.expensiveGlobalBurst
		ipRate, ipBurst = g.expensiveIPRate, g.expensiveIPBurst
		actRate, actBurst = g.expensiveActorRate, g.expensiveActorBurst
	case ClassHeavy:
		gRate, gBurst = g.heavyGlobalRate, g.heavyGlobalBurst
		ipRate, ipBurst = g.heavyIPRate, g.heavyIPBurst
		actRate, actBurst = g.heavyActorRate, g.heavyActorBurst
	case ClassWSUpgrade:
		gRate, gBurst = g.wsUpgradeGlobalRate, g.wsUpgradeGlobalBurst
		ipRate, ipBurst = g.wsUpgradeIPRate, g.wsUpgradeIPBurst
		actRate, actBurst = g.wsUpgradeActorRate, g.wsUpgradeActorBurst
	default:
		gRate, gBurst = g.readGlobalRate, g.readGlobalBurst
		ipRate, ipBurst = g.readIPRate, g.readIPBurst
		actRate, actBurst = g.readActorRate, g.readActorBurst
	}

	checks := []bucketCheck{
		{key: string(class) + ":global", rate: gRate, burst: gBurst, cost: cost},
	}
	if ip != "" {
		checks = append(checks, bucketCheck{
			key:   string(class) + ":ip:" + ip,
			rate:  ipRate,
			burst: ipBurst,
			cost:  cost,
		})
	}
	if actorID != "" {
		checks = append(checks, bucketCheck{
			key:   string(class) + ":actor:" + actorID,
			rate:  actRate,
			burst: actBurst,
			cost:  cost,
		})
	}

	ok, retry := g.store.allowMulti(checks, now)
	if !ok {
		g.recordRejection(class)
		return false, retry
	}
	return true, 0
}

func (g *abuseGuard) allow(key string, now time.Time, rate, burst float64) bool {
	ok, _ := g.store.allow(key, now, rate, burst, 1.0)
	return ok
}

func routeTemplateOrPath(c *gin.Context) string {
	if fp := c.FullPath(); fp != "" {
		return fp
	}
	return normalizePath(c.Request.URL.Path)
}

func normalizePath(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if isDynamicParam(part) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func isDynamicParam(s string) bool {
	if _, err := strconv.Atoi(s); err == nil {
		return true
	}
	if len(s) >= 8 && (strings.Contains(s, "-") || strings.Contains(s, "_")) {
		return true
	}
	return false
}

func (g *abuseGuard) logRateLimit(ctx context.Context, class RouteClass, path, role, ip string, retryAfter int) {
	g.logMu.Lock()
	now := time.Now()
	elapsed := now.Sub(g.logLast).Seconds()
	if elapsed > 0 {
		g.logTokens += elapsed * 10.0
		if g.logTokens > 20.0 {
			g.logTokens = 20.0
		}
		g.logLast = now
	}
	allowLog := g.logTokens >= 1.0
	if allowLog {
		g.logTokens -= 1.0
	}
	g.logMu.Unlock()

	if allowLog {
		slog.WarnContext(ctx, "rate limit exceeded",
			slog.String("abuse.class", string(class)),
			slog.String("http.path", path),
			slog.String("actor.role", role),
			slog.String("client.ip", ip),
			slog.Int("retry_after", retryAfter),
		)
	}
}

// preAuthAbuseLimits is installed before authentication to reject volumetric unauthenticated
// traffic on /api/* and /ws without invoking session checks. Static assets are untouched.
func (s *Server) preAuthAbuseLimits(c *gin.Context) {
	if s.abuse == nil {
		c.Next()
		return
	}
	path := c.Request.URL.Path
	if !strings.HasPrefix(path, "/api/") && path != "/ws" {
		c.Next()
		return
	}

	ip := auth.ClientIP(c.Request)
	now := time.Now()
	if ok, retrySec := s.abuse.AllowPreAuth(ip, now); !ok {
		s.abuse.logRateLimit(c.Request.Context(), ClassPublicAuth, routeTemplateOrPath(c), "pre-auth", ip, retrySec)
		c.Header("Retry-After", strconv.Itoa(retrySec))
		httpx.Err(c, http.StatusTooManyRequests, errors.New("too many requests"))
		c.Abort()
		return
	}
	c.Next()
}

// withAbuseLimits bounds operations by route class, cost, actor, IP, and global buckets.
// It also enforces concurrency gates for heavy/expensive operations.
func (s *Server) withAbuseLimits(c *gin.Context) {
	if s.abuse == nil {
		c.Next()
		return
	}
	path := c.Request.URL.Path
	method := c.Request.Method
	if !strings.HasPrefix(path, "/api/") && path != "/ws" {
		c.Next()
		return
	}

	class, cost := ClassifyRoute(method, path)
	if class == ClassPublicAuth {
		c.Next()
		return
	}

	actor := auth.ActorFrom(c.Request)
	actorID := actor.ID
	if actorID == "" {
		actorID = "anonymous"
	}
	ip := auth.ClientIP(c.Request)
	now := time.Now()

	if ok, retrySec := s.abuse.AllowRoute(class, cost, actorID, ip, now); !ok {
		s.abuse.logRateLimit(c.Request.Context(), class, routeTemplateOrPath(c), string(actor.Role), ip, retrySec)
		c.Header("Retry-After", strconv.Itoa(retrySec))
		httpx.Err(c, http.StatusTooManyRequests, errors.New("too many requests"))
		c.Abort()
		return
	}

	if class != ClassHeavy && class != ClassExpensive {
		c.Next()
		return
	}

	// Concurrency semaphore gate
	var sem chan struct{}
	if docxHeavyPath(path) {
		sem = s.abuse.docxSem
	} else if remoteHeavyPath(path) {
		sem = s.abuse.remoteSem
	} else {
		sem = s.abuse.sem
	}

	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
		c.Next()
	case <-c.Request.Context().Done():
		return
	default:
		s.abuse.logRateLimit(c.Request.Context(), class, routeTemplateOrPath(c), string(actor.Role), ip, 1)
		c.Header("Retry-After", "1")
		httpx.Err(c, http.StatusTooManyRequests, errors.New("server is busy"))
		c.Abort()
	}
}

// AbuseStats reports limiter health and metrics for administrators.
type AbuseStats struct {
	ActiveBuckets   int                       `json:"active_buckets"`
	MaxEntries      int                       `json:"max_entries"`
	RejectedTotal   int64                     `json:"rejected_total"`
	RejectedByClass map[string]int64          `json:"rejected_by_class"`
	Semaphores      map[string]map[string]int `json:"semaphores"`
}

func (g *abuseGuard) Stats() AbuseStats {
	g.statsMu.Lock()
	byClass := make(map[string]int64, len(g.rejectedByClass))
	for k, v := range g.rejectedByClass {
		byClass[string(k)] = v
	}
	total := g.rejectedTotal
	g.statsMu.Unlock()

	return AbuseStats{
		ActiveBuckets:   g.store.count(),
		MaxEntries:      g.store.maxEntries,
		RejectedTotal:   total,
		RejectedByClass: byClass,
		Semaphores: map[string]map[string]int{
			"heavy": {
				"in_use":   len(g.sem),
				"capacity": cap(g.sem),
			},
			"docx": {
				"in_use":   len(g.docxSem),
				"capacity": cap(g.docxSem),
			},
			"remote": {
				"in_use":   len(g.remoteSem),
				"capacity": cap(g.remoteSem),
			},
		},
	}
}
