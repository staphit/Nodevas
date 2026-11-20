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

// ContestPickWait bounds how long POST /api/contests/:id/pick waits for
// initiated workspace actions (such as local discard checks) to answer before
// returning 202 with their status. A var so tests can adjust it.
var ContestPickWait = time.Second

func canSeeContest(actor identity.Actor, c runs.ContestRecord) bool {
	return actor.IsAdmin() || c.CreatedBy == actor.ID || c.OwnerID == actor.ID
}

func contestNotFound(c *gin.Context) {
	httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("contest not found", "找不到比稿"), nil)
}

// visibleContest loads contest :id if the actor may see it. Global contests
// work with no active project; node contests enforce project scope directly.
func (a *API) visibleContest(c *gin.Context) (runs.ContestRecord, bool) {
	contest, err := a.d.Store().GetContest(c.Request.Context(), c.Param("id"))
	if errors.Is(err, runs.ErrNotFound) || (err == nil && !canSeeContest(auth.ActorFrom(c.Request), contest)) {
		contestNotFound(c)
		return runs.ContestRecord{}, false
	}
	if err != nil {
		fail(c, err)
		return runs.ContestRecord{}, false
	}
	if !contest.Global {
		targetProject := httpx.RequestProjectName(c.Request)
		if targetProject == "" {
			targetProject = ProjectName(a.pm, httpx.StoreFor(c.Request, a.pm))
		}
		if targetProject == "" || targetProject != contest.Project {
			contestNotFound(c)
			return runs.ContestRecord{}, false
		}
	}
	return contest, true
}

// creatorOnlyContest loads contest :id and requires the caller to be its
// creator (admins are refused 403 PERMISSION_DENIED; outsiders get 404).
func (a *API) creatorOnlyContest(c *gin.Context) (runs.ContestRecord, identity.Actor, bool) {
	contest, ok := a.visibleContest(c)
	if !ok {
		return runs.ContestRecord{}, identity.Actor{}, false
	}
	actor := auth.ActorFrom(c.Request)
	if contest.CreatedBy != actor.ID {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("only the contest creator can do this", "只有比稿建立者可以這麼做"), nil)
		return runs.ContestRecord{}, identity.Actor{}, false
	}
	return contest, actor, true
}

func contestStartFailed(c *gin.Context, err error) {
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
}

type createContestPayload struct {
	runnerwire.CreateContestRequest
	Mode      string                  `json:"mode,omitempty"`
	Pipeline  *runnerwire.Pipeline    `json:"pipeline,omitempty"`
	Fallbacks []runnerwire.AgentModel `json:"fallbacks,omitempty"`
}

type createGlobalContestPayload struct {
	runnerwire.CreateGlobalContestRequest
	Mode      string                  `json:"mode,omitempty"`
	Pipeline  *runnerwire.Pipeline    `json:"pipeline,omitempty"`
	Fallbacks []runnerwire.AgentModel `json:"fallbacks,omitempty"`
}

func (a *API) createContest(c *gin.Context) {
	if a.d == nil {
		httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
		return
	}
	actor := auth.ActorFrom(c.Request)
	var body createContestPayload
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	st, projectName, ok := a.requestProject(c)
	if !ok {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	res, err := a.d.StartContest(c.Request.Context(), runs.StartContestRequest{
		Body:        body.CreateContestRequest,
		Project:     projectName,
		ActorID:     actor.ID,
		ActorName:   actor.Name,
		SteppedUpAt: steppedUpAt,
		Mode:        body.Mode,
		Pipeline:    body.Pipeline,
		Fallbacks:   body.Fallbacks,
		LoadNode: func() (runs.NodeInfo, bool, error) {
			return LoadNodeInfo(st, body.NodeID)
		},
	})
	if err != nil {
		contestStartFailed(c, err)
		return
	}
	out := runnerwire.ContestResponse{
		Contest: res.Contest.Contest,
		Runs:    make([]runnerwire.Run, 0, len(res.Runs)),
	}
	for _, r := range res.Runs {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, out)
}

func (a *API) createGlobalContest(c *gin.Context) {
	if a.d == nil {
		httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
		return
	}
	actor := auth.ActorFrom(c.Request)
	var body createGlobalContestPayload
	if !httpx.DecodeJSONLimit(c, &body, maxGlobalBody) {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	res, err := a.d.StartGlobalContest(c.Request.Context(), runs.StartGlobalContestRequest{
		Body:        body.CreateGlobalContestRequest,
		ActorID:     actor.ID,
		ActorName:   actor.Name,
		SteppedUpAt: steppedUpAt,
		Mode:        body.Mode,
		Pipeline:    body.Pipeline,
		Fallbacks:   body.Fallbacks,
	})
	if err != nil {
		contestStartFailed(c, err)
		return
	}
	out := runnerwire.ContestResponse{
		Contest: res.Contest.Contest,
		Runs:    make([]runnerwire.Run, 0, len(res.Runs)),
	}
	for _, r := range res.Runs {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, out)
}

func (a *API) getContest(c *gin.Context) {
	if a.d == nil {
		httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
		return
	}
	contest, ok := a.visibleContest(c)
	if !ok {
		return
	}
	detail, err := a.d.GetContestDetail(c.Request.Context(), contest.ID)
	if err != nil {
		fail(c, err)
		return
	}
	cmp := detail.Compare
	out := runnerwire.ContestResponse{
		Contest: detail.Contest.Contest,
		Runs:    make([]runnerwire.Run, 0, len(detail.Runs)),
		Compare: &cmp,
	}
	for _, r := range detail.Runs {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) judgeContest(c *gin.Context) {
	if a.d == nil {
		httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
		return
	}
	contest, actor, ok := a.creatorOnlyContest(c)
	if !ok {
		return
	}
	var body runnerwire.JudgeContestRequest
	if !httpx.DecodeJSONLimit(c, &body, 4<<10) {
		return
	}
	steppedUpAt := ""
	if a.steppedUpAt != nil {
		if at, ok := a.steppedUpAt(c.Request); ok {
			steppedUpAt = at.UTC().Format(time.RFC3339Nano)
		}
	}
	updated, _, err := a.d.JudgeContest(c.Request.Context(), runs.JudgeContestRequest{
		ContestID:   contest.ID,
		Body:        body,
		ActorID:     actor.ID,
		ActorName:   actor.Name,
		SteppedUpAt: steppedUpAt,
	})
	if err != nil {
		var refused *runs.StartError
		if errors.As(err, &refused) {
			contestStartFailed(c, err)
			return
		}
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, gin.H{"contest": updated.Contest})
}

func (a *API) pickContest(c *gin.Context) {
	if a.d == nil {
		httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
		return
	}
	contest, actor, ok := a.creatorOnlyContest(c)
	if !ok {
		return
	}
	var body runnerwire.PickRequest
	if !httpx.DecodeJSONLimit(c, &body, 4<<10) {
		return
	}
	res, err := a.d.PickContest(c.Request.Context(), runs.PickContestRequest{
		ContestID: contest.ID,
		Body:      body,
		ActorID:   actor.ID,
		ActorName: actor.Name,
		Wait:      ContestPickWait,
	})
	if err != nil {
		var refused *runs.StartError
		if errors.As(err, &refused) {
			contestStartFailed(c, err)
			return
		}
		fail(c, err)
		return
	}
	actions := res.Actions
	if actions == nil {
		actions = []runnerwire.PickActionResult{}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, runnerwire.PickResponse{
		Contest: res.Contest.Contest,
		Actions: actions,
	})
}

func (a *API) cancelContest(c *gin.Context) {
	if a.d == nil {
		httpx.Coded(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", locale.Errorf("not implemented", "尚未實作"), nil)
		return
	}
	contest, ok := a.visibleContest(c)
	if !ok {
		return
	}
	if c.Request.ContentLength != 0 {
		var body struct{}
		if !httpx.DecodeJSONLimit(c, &body, 1<<10) {
			return
		}
	}
	actor := auth.ActorFrom(c.Request)
	res, err := a.d.CancelContest(c.Request.Context(), contest.ID, actor.ID, actor.Name)
	if err != nil {
		fail(c, err)
		return
	}
	out := runnerwire.ContestResponse{
		Contest: res.Contest.Contest,
		Runs:    make([]runnerwire.Run, 0, len(res.Runs)),
	}
	for _, r := range res.Runs {
		out.Runs = append(out.Runs, a.forReader(c, r.Run))
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}
