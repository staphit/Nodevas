package system

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/httpapi/httpx"
)

// getAudit returns the recent database-backed trail.
// maxAuditFilterBytes mirrors what the audit package will accept. Kept in step
// with it deliberately: the point is to turn its refusal into a 400 here, not
// to have a second opinion about what is too long.
const (
	maxAuditFilterBytes      = 256
	maxAuditAcknowledgeBytes = 1 << 10
)

// getAuditHealth is a narrow operational signal, separate from the trail
// query. When audit_events cannot be written, Query may be unavailable too;
// the health state must still tell an administrator to reconcile structured
// fallback logs. A degraded audit trail does not make domain writes fail.
func (a *API) getAuditHealth(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if a.audit == nil {
		c.JSON(http.StatusServiceUnavailable, audit.Health{
			Status: audit.HealthUnconfigured, WriteStatus: audit.HealthUnconfigured,
		})
		return
	}
	health := a.audit.Health()
	status := http.StatusOK
	if health.Status != audit.HealthHealthy {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, health)
}

// postAuditHealthAcknowledge is the operator's explicit statement that every
// structured fallback through the supplied counter has been reconciled. The
// route is admin-only, and POST makes the server's normal CSRF and audit
// middleware apply. A compare-and-swap conflict prevents a concurrent fallback
// from being hidden by an acknowledgement of an older snapshot.
func (a *API) postAuditHealthAcknowledge(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if a.audit == nil {
		httpx.Coded(c, http.StatusServiceUnavailable, "audit_unconfigured",
			errors.New("audit store is not configured"), nil)
		return
	}
	var body struct {
		ExpectedFallbackEvents uint64 `json:"expectedFallbackEvents"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxAuditAcknowledgeBytes) {
		return
	}
	health, err := a.audit.AcknowledgeFallbacks(body.ExpectedFallbackEvents)
	if err == nil {
		c.JSON(http.StatusOK, health)
		return
	}
	switch {
	case errors.Is(err, audit.ErrWritesDegraded):
		httpx.Coded(c, http.StatusConflict, "audit_writes_degraded",
			errors.New("audit database writes must recover before reconciliation is acknowledged"),
			map[string]any{"health": health})
	case errors.Is(err, audit.ErrFallbackEventsChanged):
		httpx.Coded(c, http.StatusConflict, "audit_fallback_count_changed",
			errors.New("audit fallback count changed; refresh health and reconcile the new events"),
			map[string]any{"health": health})
	default:
		httpx.Coded(c, http.StatusServiceUnavailable, "audit_unavailable", err, nil)
	}
}

func (a *API) getAudit(c *gin.Context) {
	if a.audit == nil {
		c.JSON(http.StatusOK, map[string]any{"entries": []map[string]any{}})
		return
	}

	filter := audit.Filter{Limit: auditLimit(c)}
	// A project scope is what the existing UI asks for. `scope=server` is how
	// an admin reaches the events that belong to no project — sign-ins, for
	// one — which a project-scoped trail could never hold.
	if strings.TrimSpace(c.Query("scope")) == "server" {
		filter.ServerWide = true
	} else if st := httpx.StoreFor(c.Request, a.pm); st != nil {
		filter.Project = st.Root()
	}
	// The audit package refuses a filter value no caller would send, because a
	// truncated one silently answers a different question. That refusal is the
	// caller's mistake, not the server's, so it is a 400 here rather than the
	// 500 every other error from Query maps to.
	if actor := strings.TrimSpace(c.Query("actor")); actor != "" {
		if len(actor) > maxAuditFilterBytes {
			httpx.Err(c, http.StatusBadRequest, errors.New("actor filter is too long"))
			return
		}
		filter.ActorID = actor
	}
	if action := strings.TrimSpace(c.Query("action")); action != "" {
		if len(action) > maxAuditFilterBytes {
			httpx.Err(c, http.StatusBadRequest, errors.New("action filter is too long"))
			return
		}
		filter.ActionPrefix = action
	}

	events, err := a.audit.Query(c.Request.Context(), filter)
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}

	entries := make([]map[string]any, 0, len(events))
	for _, event := range events {
		entries = append(entries, map[string]any{
			"at":     event.At.Format(time.RFC3339),
			"actor":  event.ActorName,
			"id":     event.ActorID,
			"op":     event.Action,
			"target": event.Target,
			"ip":     event.ClientIP,
			"detail": event.Detail,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"entries": entries})
}

// auditLimit reads the page size. What counts as too large is decided in the
// audit package, so a zero here means "whatever that package's default is"
// rather than a second opinion.
func auditLimit(c *gin.Context) int {
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			return value
		}
	}
	return 0
}
