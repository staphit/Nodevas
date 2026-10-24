package runs

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// Limits on POST /api/runs/pipelines/:id/review.
const (
	maxReviewBody    = 24 << 10
	maxReviewComment = 16 << 10
)

// review records a person's verdict on a pipeline waiting for one (W7):
// approve ends it approved, changes starts a revise round with the comment as
// the review. Like a merge approval, it is a person's decision about agents'
// work: an agent is refused, and so is anyone who may not see the pipeline.
func (a *API) review(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot review for a person"), nil)
		return
	}
	_, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	var body runnerwire.HumanReviewRequest
	if !httpx.DecodeJSONLimit(c, &body, maxReviewBody) {
		return
	}
	body.Verdict = strings.TrimSpace(body.Verdict)
	if body.Verdict != runnerwire.VerdictApprove && body.Verdict != runnerwire.VerdictChanges {
		invalid(c, "verdict must be %q or %q", runnerwire.VerdictApprove, runnerwire.VerdictChanges)
		return
	}
	if len(body.Comment) > maxReviewComment || !utf8.ValidString(body.Comment) {
		invalid(c, "comment must be valid text of at most %d bytes", maxReviewComment)
		return
	}
	p, err := a.d.Store().Pipeline(c.Request.Context(), c.Param("id"))
	if errors.Is(err, runs.ErrNotFound) || (err == nil && (p.Project != projectName ||
		!(actor.IsAdmin() || p.CreatedBy == actor.ID || p.OwnerID == actor.ID))) {
		notFound(c, "pipeline")
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	updated, err := a.d.HumanReview(c.Request.Context(), p.ID,
		runs.Reviewer{ID: actor.ID, Name: displayName(actor.ID, actor.Name)}, body.Verdict, body.Comment)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, updated.PipelineState)
}
