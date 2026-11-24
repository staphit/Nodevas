package runs

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

func canSeeGroup(actor identity.Actor, g runs.GroupRecord) bool {
	return actor.IsAdmin() || g.CreatedBy == actor.ID || g.OwnerID == actor.ID
}

func (a *API) visibleGroup(c *gin.Context) (runs.GroupRecord, bool) {
	g, err := a.d.Store().GetGroup(c.Request.Context(), c.Param("id"))
	if errors.Is(err, runs.ErrNotFound) || (err == nil && !canSeeGroup(auth.ActorFrom(c.Request), g)) {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("group not found", "找不到群組"), nil)
		return runs.GroupRecord{}, false
	}
	if err != nil {
		fail(c, err)
		return runs.GroupRecord{}, false
	}
	return g, true
}

func (a *API) createGlobalGroup(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	actor := auth.ActorFrom(c.Request)
	var body runnerwire.CreateGlobalGroupRequest
	if !httpx.DecodeJSONLimit(c, &body, maxGlobalBody) {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	res, err := a.d.StartGlobalGroup(c.Request.Context(), runs.GlobalGroupStartRequest{
		Body: body, ActorID: actor.ID, ActorName: actor.Name, SteppedUpAt: steppedUpAt,
	})
	if err != nil {
		var refused *runs.StartError
		if errors.As(err, &refused) {
			if refused.Code == runs.StartInvalid && refused.Reason != "" {
				var extras map[string]any
				if refused.Field != "" {
					extras = map[string]any{"field": refused.Field}
				}
				httpx.Coded(c, http.StatusBadRequest, refused.Reason, refused, extras)
				return
			}
			if refused.Code == runs.StartBudget {
				extras := map[string]any{
					"exceeded": refused.Budget.Exceeded,
					"room":     runs.BudgetRoom(refused.Budget),
				}
				httpx.Coded(c, http.StatusTooManyRequests, CodeBudgetExceeded, refused, extras)
				return
			}
		}
		startFailed(c, err)
		return
	}
	out := runnerwire.GlobalGroupResponse{
		Group: res.Group.RunGroup,
		Runs:  make([]runnerwire.Run, 0, len(res.Runs)),
	}
	for _, r := range res.Runs {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, out)
}

func (a *API) getGlobalGroup(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	g, ok := a.visibleGroup(c)
	if !ok {
		return
	}
	records, err := a.d.Store().GroupRuns(c.Request.Context(), g.ID)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.GlobalGroupResponse{
		Group: g.RunGroup,
		Runs:  make([]runnerwire.Run, 0, len(records)),
	}
	for _, r := range records {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) cancelGlobalGroup(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	g, ok := a.visibleGroup(c)
	if !ok {
		return
	}
	actor := auth.ActorFrom(c.Request)
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	records, err := a.d.CancelGroup(c.Request.Context(), g.ID, name)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.CancelAllResponse{
		Cancelled: len(records),
		Runs:      make([]runnerwire.Run, 0, len(records)),
	}
	for _, r := range records {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) resumeGlobalGroup(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	g, ok := a.visibleGroup(c)
	if !ok {
		return
	}
	resumed, err := a.d.ResumeGroup(c.Request.Context(), g.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"group": resumed.RunGroup})
}

func (a *API) followUpGlobalGroup(c *gin.Context) {
	if !a.takesRuns(c) {
		return
	}
	g, ok := a.visibleGroup(c)
	if !ok {
		return
	}
	var body groupFollowUpBody
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
	res, err := a.d.FollowUpGroup(c.Request.Context(), runs.GroupFollowUpRequest{
		GroupID:     g.ID,
		Body:        body.GroupFollowUpRequest,
		Actor:       actor,
		ActorID:     actor.ID,
		ActorName:   actor.Name,
		IsAgent:     actor.IsAgent(),
		SteppedUpAt: steppedUpAt,
		Simple:      body.Simple,
	})
	if err != nil {
		var refused *runs.StartError
		if errors.As(err, &refused) && refused.Code == runs.StartNotFound && refused.What == "group" {
			httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("group not found", "找不到群組"), nil)
			return
		}
		followUpFailed(c, err)
		return
	}
	out := runnerwire.GroupFollowUpResponse{
		Started: make([]runnerwire.Run, 0, len(res.Started)),
		Skipped: res.Skipped,
	}
	if out.Skipped == nil {
		out.Skipped = []runnerwire.GroupFollowUpSkipped{}
	}
	for _, r := range res.Started {
		out.Started = append(out.Started, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}
