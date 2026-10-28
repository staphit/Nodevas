package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/db"
	"nodevas/internal/httpapi/httpx"
)

// Liveness and metrics for an operator: GET /api/healthz for the
// container's health probe, and GET /api/metrics for an administrator or a
// scraper on the same machine.

const (
	healthzPath = "/api/healthz"
	metricsPath = "/api/metrics"

	// healthzTimeout bounds the database ping; the probe itself gives up
	// after five seconds (Dockerfile).
	healthzTimeout = 2 * time.Second
	// metricsTimeout bounds the one query metrics runs.
	metricsTimeout = 3 * time.Second
)

// publicHealthPath reports a route withAuth lets through without a
// session: the health probe always, metrics from a direct loopback peer
// (the handler checks the rest).
func publicHealthPath(r *http.Request) bool {
	switch r.URL.Path {
	case healthzPath:
		return true
	case metricsPath:
		return auth.DirectLoopback(r)
	}
	return false
}

// useDatabase remembers the workspace database for healthz and metrics.
func (s *Server) useDatabase(database *db.DB) {
	if database != nil && s.database == nil {
		s.database = database
	}
}

func (s *Server) registerHealth(api *gin.RouterGroup) {
	api.GET("/healthz", s.healthz)
	api.HEAD("/healthz", s.healthz)
	api.GET("/metrics", s.metrics)
}

// healthz answers 200 while the read pool reaches the database, and 503
// when it does not. It names no error: it is public.
func (s *Server) healthz(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.database == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "database": "none"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), healthzTimeout)
	defer cancel()
	if err := s.database.Reader().PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "database": "down"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "database": "ok"})
}

// poolStats is sql.DBStats as metrics reports it.
type poolStats struct {
	MaxOpen           int     `json:"max_open"`
	Open              int     `json:"open"`
	InUse             int     `json:"in_use"`
	Idle              int     `json:"idle"`
	WaitCount         int64   `json:"wait_count"`
	WaitDurationMS    float64 `json:"wait_duration_ms"`
	MaxIdleClosed     int64   `json:"max_idle_closed"`
	MaxIdleTimeClosed int64   `json:"max_idle_time_closed"`
	MaxLifetimeClosed int64   `json:"max_lifetime_closed"`
}

func statsOf(pool *sql.DB) poolStats {
	st := pool.Stats()
	return poolStats{
		MaxOpen: st.MaxOpenConnections, Open: st.OpenConnections, InUse: st.InUse, Idle: st.Idle,
		WaitCount: st.WaitCount, WaitDurationMS: float64(st.WaitDuration) / float64(time.Millisecond),
		MaxIdleClosed: st.MaxIdleClosed, MaxIdleTimeClosed: st.MaxIdleTimeClosed, MaxLifetimeClosed: st.MaxLifetimeClosed,
	}
}

// connectionCounter is what the realtime hub reports about its sockets, when
// it does.
type connectionCounter interface {
	ConnectionCount() int
}

// metrics is GET /api/metrics: database pools, realtime connections,
// rate-limiter refusals by class, runs by state, and the audit queue.
func (s *Server) metrics(c *gin.Context) {
	r := c.Request
	if !auth.DirectLoopback(r) {
		actor := auth.ActorFrom(r)
		if !actor.IsAdmin() || actor.IsAgent() {
			httpx.Err(c, http.StatusForbidden, errors.New("metrics are for administrators"))
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	out := gin.H{"goroutines": runtime.NumGoroutine()}

	pools := map[string]poolStats{}
	runsByState := map[string]int64{}
	if s.database != nil {
		for name, pool := range map[string]*sql.DB{
			"write": s.database.Writer(), "read": s.database.Reader(), "search": s.database.Search(),
		} {
			if pool != nil {
				pools[name] = statsOf(pool)
			}
		}
		if err := s.runsByState(r.Context(), runsByState); err != nil {
			out["runs_error"] = "unavailable"
		}
	}
	out["db"] = pools
	out["runs"] = runsByState

	if counter, ok := any(s.hub).(connectionCounter); ok && s.hub != nil {
		out["realtime"] = gin.H{"connections": counter.ConnectionCount()}
	}
	if s.auditQueue != nil {
		out["audit_queue"] = s.auditQueue.Stats()
	}
	if s.abuse != nil {
		out["limiter"] = s.abuse.Stats()
	} else {
		out["limiter"] = AbuseStats{RejectedByClass: map[string]int64{}}
	}
	c.JSON(http.StatusOK, out)
}

// runsByState counts runs by status.
func (s *Server) runsByState(ctx context.Context, into map[string]int64) error {
	ctx, cancel := context.WithTimeout(ctx, metricsTimeout)
	defer cancel()
	rows, err := s.database.QueryContext(ctx, `SELECT status, COUNT(*) FROM runs GROUP BY status`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return err
		}
		into[status] = n
	}
	return rows.Err()
}
