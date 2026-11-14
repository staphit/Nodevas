package runs

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

// POST /api/runs/:id/offer (and /api/global-runs/:id/offer): the owner's
// answer, from the app, to what the built-in runner of a single-user hub
// asks before it starts a run (runnerwire/offerask.go). The hub relays it
// and the runner decides; a run on any other runner is approved on that
// runner's own PC, never here.

// maxOfferBody is room for RunOfferBody.
const maxOfferBody = 1 << 10

// Offer answer refusals, as the error body's code.
const (
	// CodeNotWaiting: the run is not waiting for an answer in the app.
	CodeNotWaiting = "not_waiting"
	// CodeApproveOnRunner: the run is approved on its runner's own PC.
	CodeApproveOnRunner = "approve_on_runner"
)

var errOfferAgent = locale.Errorf("agents cannot answer for a person whether a run starts", "agent 不能替人決定是否開始執行")

func (a *API) offer(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	// Starting code on someone's machine is that person's word, never an
	// agent's: an agent that could answer would approve its own runs.
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errOfferAgent, nil)
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	// The runner's owner only: it is their machine. Whoever else may see
	// the run (an administrator) finds no such run to answer for.
	if actor.ID == "" || r.OwnerID != actor.ID {
		notFound(c, "run")
		return
	}
	var body runnerwire.RunOfferBody
	if !httpx.DecodeJSONLimit(c, &body, maxOfferBody) {
		return
	}
	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	ctx := c.Request.Context()
	err := a.d.AnswerOffer(ctx, r, runs.OfferAnswer{
		AccountID: actor.ID, AccountName: name, Start: body.Start, AlwaysAllowShell: body.AlwaysAllowShell,
	})
	switch {
	case err == nil:
	case errors.Is(err, runs.ErrNotWaiting):
		httpx.Coded(c, http.StatusConflict, CodeNotWaiting, err, nil)
		return
	case errors.Is(err, runs.ErrApproveOnRunner):
		httpx.Coded(c, http.StatusConflict, CodeApproveOnRunner, err, nil)
		return
	case errors.Is(err, runners.ErrRunnerOffline):
		httpx.Coded(c, http.StatusServiceUnavailable, "RUNNER_OFFLINE", err, map[string]any{"blocked": runnerwire.RunnerBlockedOffline})
		return
	default:
		fail(c, err)
		return
	}
	updated, err := a.d.Store().Get(ctx, r.ID)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, a.forReader(c, updated.Run))
}
