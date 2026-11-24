package runs

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// followUpBody is POST /api/global-runs/:id/follow-up's body: the wire request
// plus simple, the Simple-mode flag POST /api/global-runs also takes. With it
// the hub adds its fixed plain-words instruction (runs.SimpleInstruction).
type followUpBody struct {
	runnerwire.FollowUpRequest
	Simple bool `json:"simple,omitempty"`
}

// groupFollowUpBody is POST /api/global-runs/groups/:id/follow-up's body,
// with simple as in followUpBody.
type groupFollowUpBody struct {
	runnerwire.GroupFollowUpRequest
	Simple bool `json:"simple,omitempty"`
}

// POST /api/global-runs/:id/follow-up: continues a global run in the same
// worktree folder and branch (or queues a reply while a background run works).
func (a *API) followUp(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	var body followUpBody
	if !httpx.DecodeJSONLimit(c, &body, maxGlobalBody) {
		return
	}
	actor := auth.ActorFrom(c.Request)
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	record, err := a.d.FollowUp(c.Request.Context(), runs.FollowUpRequest{
		PriorID:     r.ID,
		Body:        body.FollowUpRequest,
		Actor:       actor,
		ActorID:     actor.ID,
		ActorName:   actor.Name,
		IsAgent:     actor.IsAgent(),
		SteppedUpAt: steppedUpAt,
		Simple:      body.Simple,
	})
	if err != nil {
		followUpFailed(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, a.forReader(c, record.Run))
}

func followUpFailed(c *gin.Context, err error) {
	var refused *runs.StartError
	if errors.As(err, &refused) {
		if refused.Reason == "PERMISSION_DENIED" {
			httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", refused, nil)
			return
		}
		if refused.Reason == runs.CodeContinued && refused.LatestID() != "" {
			httpx.Coded(c, http.StatusConflict, runs.CodeContinued, refused,
				map[string]any{"latestId": refused.LatestID()})
			return
		}
	}
	startFailed(c, err)
}

// GET /api/global-runs/:id/thread: returns every non-withdrawn turn of a global
// run's work chain, oldest first (at most 50).
func (a *API) thread(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	records, err := a.d.Store().Thread(c.Request.Context(), r)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.RunThreadResponse{Runs: make([]runnerwire.Run, 0, len(records))}
	for _, rec := range records {
		out.Runs = append(out.Runs, a.forReader(c, rec.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// POST /api/runs/:id/end and POST /api/global-runs/:id/end: asks the runner to
// gracefully quit a running interactive session and publish/keep its work (§7.1).
func (a *API) endRun(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	if c.Request.ContentLength > 0 {
		var body struct{}
		if !httpx.DecodeJSONLimit(c, &body, 1<<10) {
			return
		}
	}
	actor := auth.ActorFrom(c.Request)
	updated, err := a.d.End(c.Request.Context(), r.ID, actor)
	switch {
	case err == nil:
	case errors.Is(err, runs.ErrEndForbidden):
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", err, nil)
		return
	case errors.Is(err, runs.ErrEndOutdated):
		httpx.Coded(c, http.StatusPreconditionFailed, "RUNNER_OUTDATED", err,
			map[string]any{"blocked": runnerwire.RunnerBlockedOutdated})
		return
	default:
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, a.forReader(c, updated.Run))
}
