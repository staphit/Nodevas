package runs

// GET /api/runs/:id/terminal: a browser's view of an interactive run (W2.5).
//
// The socket takes a session like /ws and goes through the same Host, Origin
// and Sec-Fetch-Site checks as any state-changing request, since a typing
// viewer's frames reach a terminal on someone's machine. Who may watch is who
// may see the run; who may type is narrower: the person who dispatched it and
// the runner's owner. An administrator watching someone else's run, and any
// agent, never types.
//
// Frames, server → browser:
//
//	binary  terminal output; the first one on attach is the scrollback
//	text    runnerwire.TermControl {"type":"status",...} or {"type":"readonly"}
//
// Frames, browser → server (ignored from a read-only viewer):
//
//	binary  keystrokes, at most runnerwire.MaxTermChunk per frame
//	text    {"type":"resize","cols":C,"rows":R}

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/safego"
)

// Terminal socket timings and input limits. Variables so tests can shorten
// them.
var (
	// termWriteTimeout bounds one frame to a slow browser.
	termWriteTimeout = 10 * time.Second
	// termRecheck is how often a viewer who sends nothing has their session
	// checked again.
	termRecheck = time.Minute
	// termInputRate and termInputBurst bound a typing viewer's input, in
	// bytes per second. Past them the socket is simply read more slowly.
	termInputRate  = float64(64 << 10)
	termInputBurst = float64(64 << 10)
)

// termResizeCost is what a resize counts against the input bucket.
const termResizeCost = 64

// validMode accepts the run modes POST /api/runs takes.
func validMode(mode string) bool { return runs.ValidMode(mode) }

// modeSupported reports whether a runner's hello lets it run a mode.
func modeSupported(info *runnerwire.Info, mode string) bool { return runs.ModeSupported(info, mode) }

// mayType is who may type into a run's terminal: whoever dispatched it and the
// runner's owner, never an agent.
func mayType(actor identity.Actor, r runs.Record) bool {
	return !actor.IsAgent() && actor.ID != "" && (actor.ID == r.CreatedBy || actor.ID == r.OwnerID)
}

// bucket is a token bucket that waits rather than refuses: input over the
// rate is delayed, so a paste arrives whole, just not all at once.
type bucket struct {
	rate, burst, tokens float64
	at                  time.Time
}

func newBucket(rate, burst float64) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, at: time.Now()}
}

func (b *bucket) wait(ctx context.Context, n int) error {
	need := min(float64(n), b.burst)
	now := time.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.at).Seconds()*b.rate)
	b.at = now
	if b.tokens < need {
		delay := time.Duration((need - b.tokens) / b.rate * float64(time.Second))
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
		b.tokens = need
		b.at = time.Now()
	}
	b.tokens -= need
	return nil
}

func (a *API) terminal(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot open run terminals"), nil)
		return
	}
	r, ok := a.visible(c)
	if !ok {
		return
	}
	if r.Mode != runnerwire.ModeInteractive {
		httpx.Coded(c, http.StatusConflict, "FAILED_PRECONDITION", errors.New("this run is not interactive"), nil)
		return
	}
	canType := mayType(actor, r)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	viewer, err := a.d.TermAttach(ctx, r, canType)
	switch {
	case errors.Is(err, runs.ErrTermBusy):
		httpx.Coded(c, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", err, nil)
		return
	case err != nil && !errors.Is(err, runs.ErrTermGone):
		fail(c, err)
		return
	}
	// Same rule as /ws: the library refuses an Origin that is not this Host.
	conn, acceptErr := websocket.Accept(c.Writer, c.Request, nil)
	if acceptErr != nil {
		if viewer != nil {
			viewer.Detach()
		}
		slog.Warn("run terminal: accept", slog.String("run", r.ID), slog.Any("error", acceptErr))
		return
	}
	if viewer == nil {
		// Ended, and its output is no longer kept: say how it ended and go.
		raw, _ := json.Marshal(runnerwire.TermControl{Type: "status", Status: r.Status})
		writeCtx, done := context.WithTimeout(ctx, termWriteTimeout)
		_ = conn.Write(writeCtx, websocket.MessageText, raw)
		done()
		_ = conn.Close(websocket.StatusNormalClosure, "the run ended; its terminal output is no longer kept")
		return
	}
	defer viewer.Detach()
	conn.SetReadLimit(runnerwire.MaxTermChunk)

	name := actor.Name
	if name == "" {
		name = actor.ID
	}
	audit := func(action string, detail map[string]any) {
		a.d.Audit(runs.AuditEntry{
			Project: r.Project, ActorID: actor.ID, ActorName: name,
			Action: action, Target: r.ID, Detail: detail,
		})
	}
	audit("run.terminal.attach", map[string]any{"canType": canType})
	var typed int64
	defer func() { audit("run.terminal.detach", map[string]any{"canType": canType, "inputBytes": typed}) }()

	safego.Go("terminal writer", func() { a.termWriter(ctx, cancel, conn, viewer) })
	safego.Go("terminal recheck", func() { termRecheckLoop(ctx, c.Request, conn) })

	input := newBucket(termInputRate, termInputBurst)
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			_ = conn.CloseNow()
			return
		}
		if !auth.StillAuthorized(c.Request) {
			_ = conn.Close(websocket.StatusPolicyViolation, "authorization expired")
			return
		}
		if !canType {
			// Told {"type":"readonly"} on attach; whatever it sends is ignored.
			continue
		}
		switch kind {
		case websocket.MessageBinary:
			if len(data) == 0 {
				continue
			}
			if input.wait(ctx, len(data)) != nil {
				return
			}
			if a.d.TermInput(viewer, data) == nil {
				typed += int64(len(data))
			}
		case websocket.MessageText:
			var control runnerwire.TermControl
			if json.Unmarshal(data, &control) != nil || control.Type != "resize" {
				continue
			}
			if input.wait(ctx, termResizeCost) != nil {
				return
			}
			_ = a.d.TermResize(viewer, control.Cols, control.Rows)
		}
	}
}

// termWriter sends the viewer's frames in order until the hub lets the viewer
// go or the socket fails, then closes the socket with the reason.
func (a *API) termWriter(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, viewer *runs.TermViewer) {
	defer cancel()
	for {
		frame, err := viewer.Next(ctx)
		if err != nil {
			switch viewer.Reason() {
			case runs.TermSlow:
				_ = conn.Close(websocket.StatusTryAgainLater, "too slow to follow the output; reconnect")
			case runs.TermEnded:
				_ = conn.Close(websocket.StatusNormalClosure, "the run ended")
			default:
				_ = conn.CloseNow()
			}
			return
		}
		kind := websocket.MessageText
		if frame.Binary {
			kind = websocket.MessageBinary
		}
		writeCtx, done := context.WithTimeout(ctx, termWriteTimeout)
		err = conn.Write(writeCtx, kind, frame.Data)
		done()
		if err != nil {
			_ = conn.CloseNow()
			return
		}
	}
}

// termRecheckLoop closes the socket once the viewer's session no longer
// holds, even if they never send anything.
func termRecheckLoop(ctx context.Context, r *http.Request, conn *websocket.Conn) {
	ticker := time.NewTicker(termRecheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !auth.StillAuthorized(r) {
				_ = conn.Close(websocket.StatusPolicyViolation, "authorization expired")
				return
			}
		}
	}
}
