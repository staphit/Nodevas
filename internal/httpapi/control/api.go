// Package control serves /api/control: Station's control center acting on
// everything under way in a project at once. Today that is one emergency
// stop, POST /api/control/pause-all, which does exactly what the viewer could
// do one by one (pause their autopilots, cancel their runs) and reports what
// it left alone. It grants nothing: each autopilot and run is checked the way
// its own route checks it.
package control

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	ap "nodevas/internal/autopilot"
	"nodevas/internal/httpapi/httpx"
	runsapi "nodevas/internal/httpapi/runs"
	"nodevas/internal/identity"
	"nodevas/internal/project"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

const maxBody = 1 << 10

// Target kinds.
const (
	KindAutopilot = "autopilot"
	KindRun       = "run"
)

// Why pause-all left something alone.
const (
	// SkipNotPermitted: someone else's autopilot or run, which the viewer
	// may not steer or cancel on their own either.
	SkipNotPermitted = "not_permitted"
	// SkipRunning: a run already running, left alone without cancelRunning.
	SkipRunning = "running"
	// SkipChanged: it moved on (ended, or was paused) while the request ran.
	SkipChanged = "changed"
	// SkipFailed: the pause or cancel itself failed.
	SkipFailed = "failed"
)

// PauseAllRequest is the body of POST /api/control/pause-all.
type PauseAllRequest struct {
	// CancelRunning cancels runs that already started too; without it only
	// queued and offered runs are withdrawn.
	CancelRunning bool `json:"cancelRunning"`
}

// Target is one autopilot or run pause-all acted on or skipped. A run the
// viewer may not see is reported by kind and reason only: the runs list
// hides it, and so does this.
type Target struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	// Status is what it was before pause-all touched it.
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// PauseAllResponse says what pause-all did.
type PauseAllResponse struct {
	Paused    []Target `json:"paused"`
	Cancelled []Target `json:"cancelled"`
	Skipped   []Target `json:"skipped"`
}

// API is the handler set. o may be nil on a server without autopilots.
type API struct {
	pm *project.ProjectManager
	d  *runs.Dispatcher
	o  *ap.Orchestrator
}

// New builds the handler set.
func New(pm *project.ProjectManager, d *runs.Dispatcher, o *ap.Orchestrator) *API {
	return &API{pm: pm, d: d, o: o}
}

// Register wires /api/control. It is not on the runner proxy's allowlist: an
// agent does not stop the project.
func (a *API) Register(api *gin.RouterGroup) {
	api.POST("/control/pause-all", a.pauseAll)
}

func canCancel(actor identity.Actor, r runs.Record) bool {
	return actor.IsAdmin() || r.CreatedBy == actor.ID || r.OwnerID == actor.ID
}

func canSteer(actor identity.Actor, createdBy string) bool {
	return actor.IsAdmin() || actor.ID == createdBy
}

// pauseAll pauses every running autopilot of the project the viewer may
// steer, then withdraws the queued and offered runs they may cancel (and,
// with cancelRunning, the running ones). Autopilots go first so none of
// them dispatches a task into the gap a cancelled run leaves.
func (a *API) pauseAll(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot pause the project"), nil)
		return
	}
	var body PauseAllRequest
	if !httpx.DecodeJSONLimit(c, &body, maxBody) {
		return
	}
	name := runsapi.ProjectName(a.pm, httpx.StoreFor(c.Request, a.pm))
	if name == "" {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("no project selected"), nil)
		return
	}
	ctx := c.Request.Context()
	out := PauseAllResponse{Paused: []Target{}, Cancelled: []Target{}, Skipped: []Target{}}
	if err := a.pauseAutopilots(ctx, actor, name, &out); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	if err := a.cancelRuns(ctx, actor, name, body.CancelRunning, &out); err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	audit.Annotate(ctx, audit.Annotation{Detail: auditDetail(body, out)})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (a *API) pauseAutopilots(ctx context.Context, actor identity.Actor, project string, out *PauseAllResponse) error {
	if a.o == nil {
		return nil
	}
	views, err := a.o.List(ctx, project, runnerwire.AutopilotRunning)
	if err != nil {
		return err
	}
	for _, view := range views {
		run := view.Run
		target := Target{Kind: KindAutopilot, ID: run.ID, NodeID: run.GoalNodeID, Status: run.Status}
		if !canSteer(actor, run.CreatedBy) {
			target.Reason = SkipNotPermitted
			out.Skipped = append(out.Skipped, target)
			continue
		}
		switch err := a.o.Pause(ctx, run.ID); {
		case err == nil:
			out.Paused = append(out.Paused, target)
		case errors.Is(err, ap.ErrConflict), errors.Is(err, ap.ErrNotFound):
			target.Reason = SkipChanged
			out.Skipped = append(out.Skipped, target)
		default:
			target.Reason = SkipFailed
			out.Skipped = append(out.Skipped, target)
		}
	}
	return nil
}

func (a *API) cancelRuns(ctx context.Context, actor identity.Actor, project string, cancelRunning bool, out *PauseAllResponse) error {
	// Everyone's active runs: the ones the viewer may not cancel are counted
	// as skipped, by kind and reason only.
	records, err := a.d.Store().List(ctx, runs.Filter{Project: project, Active: true})
	if err != nil {
		return err
	}
	who := actor.Name
	if who == "" {
		who = actor.ID
	}
	reason := "paused everything: cancelled by " + who
	for _, r := range records {
		if !canCancel(actor, r) {
			out.Skipped = append(out.Skipped, Target{Kind: KindRun, Reason: SkipNotPermitted})
			continue
		}
		target := Target{Kind: KindRun, ID: r.ID, NodeID: r.NodeID, Status: r.Status}
		if r.Status == runnerwire.RunRunning && !cancelRunning {
			target.Reason = SkipRunning
			out.Skipped = append(out.Skipped, target)
			continue
		}
		// A running run's pipeline goes with it, as POST /api/runs/:id/cancel
		// does. A pending one is withdrawn on its own: cancelling its whole
		// pipeline would stop a sibling reviewer that is already running.
		if r.Status == runnerwire.RunRunning && r.PipelineID != "" {
			if err := a.d.CancelPipeline(ctx, r.PipelineID, reason); err != nil {
				target.Reason = SkipFailed
				out.Skipped = append(out.Skipped, target)
				continue
			}
		}
		updated, err := a.d.Cancel(ctx, r.ID, reason)
		switch {
		case errors.Is(err, runs.ErrNotFound):
			target.Reason = SkipChanged
			out.Skipped = append(out.Skipped, target)
		case err != nil:
			target.Reason = SkipFailed
			out.Skipped = append(out.Skipped, target)
		case runnerwire.RunFinal(updated.Status) && updated.Status != runnerwire.RunCancelled:
			target.Reason = SkipChanged
			out.Skipped = append(out.Skipped, target)
		default:
			// Cancelled, or (running) asked to stop. A pending run accepted
			// in the instant between the list and the cancel is stopped too:
			// Dispatcher.Cancel acts on the status it finds.
			out.Cancelled = append(out.Cancelled, target)
		}
	}
	return nil
}

// maxAuditIDs caps the IDs one audit row lists; the counts are always whole.
const maxAuditIDs = 50

func ids(targets []Target) []string {
	out := []string{}
	for _, t := range targets {
		if t.ID == "" {
			continue
		}
		if len(out) == maxAuditIDs {
			break
		}
		out = append(out, t.Kind+":"+t.ID)
	}
	return out
}

func auditDetail(body PauseAllRequest, out PauseAllResponse) map[string]any {
	return map[string]any{
		"cancelRunning":    body.CancelRunning,
		"autopilotsPaused": len(out.Paused),
		"runsCancelled":    len(out.Cancelled),
		"skipped":          len(out.Skipped),
		"paused":           ids(out.Paused),
		"cancelled":        ids(out.Cancelled),
	}
}
