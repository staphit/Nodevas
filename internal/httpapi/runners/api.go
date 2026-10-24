// Package runners serves the runner endpoints: /api/runners, where a person
// manages their runners, and /api/runner/connect, the socket a runner keeps
// open to the hub. The hub records and talks; it never executes anything.
package runners

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
)

const maxCreateBody = 4 << 10

// maxRunMessageBytes is the socket's read limit once hello is done: the
// largest message is a changes report, which the runner keeps under
// runnerwire.MaxChangesMessageBytes. What a message may hold is checked by
// type after reading.
const maxRunMessageBytes = runnerwire.MaxChangesMessageBytes

// Timeouts on the connect socket. Variables so tests need not wait a minute.
var (
	// helloTimeout is how long a new connection has to say who it is.
	helloTimeout = 10 * time.Second
	// silenceTimeout drops a runner that stopped pinging.
	silenceTimeout = 3 * runnerwire.PingIntervalSeconds * time.Second
	// writeTimeout bounds one message to a slow runner.
	writeTimeout = 10 * time.Second
)

// API exposes a runners.Registry over HTTP.
type API struct {
	reg *runners.Registry
	// runs applies what a runner reports about its runs. Nil means the
	// socket speaks W0 only: hello and ping.
	runs *runs.Dispatcher
	// llm relays copilot model calls (WithLLM); nil drops their answers.
	llm *runners.LLMRelay
}

// New builds the handler set. dispatcher may be nil.
func New(reg *runners.Registry, dispatcher *runs.Dispatcher) *API {
	return &API{reg: reg, runs: dispatcher}
}

// WithLLM relays copilot model calls made on runners (runnerwire/llm.go)
// through relay. It returns a for chaining.
func (a *API) WithLLM(relay *runners.LLMRelay) *API {
	a.llm = relay
	return a
}

// Register wires /api/runners and /api/runner/connect. The server's auth
// middleware decides who reaches which: a session for /api/runners, a runner
// token for /api/runner/*, never the other way round.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/runners", a.list)
	api.POST("/runners", a.create)
	api.DELETE("/runners/:id", a.revoke)
	api.GET("/runner/connect", a.connect)
}

func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, runners.ErrInvalid):
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
	case errors.Is(err, runners.ErrNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", err, nil)
	case errors.Is(err, runners.ErrUnauthenticated):
		httpx.Err(c, http.StatusUnauthorized, err)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

func (a *API) list(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	list, err := a.reg.List(c.Request.Context(), auth.ActorFrom(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, runnerwire.ListResponse{Runners: list})
}

func (a *API) create(c *gin.Context) {
	var body runnerwire.CreateRequest
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	actor := auth.ActorFrom(c.Request)
	// A runner acts as its owner; an agent minting one would hand itself a
	// credential that outlives its session.
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", errors.New("agents cannot create runners"), nil)
		return
	}
	runner, token, err := a.reg.Create(c.Request.Context(), actor, body.Name)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, runnerwire.CreateResponse{Runner: runner, Token: token})
}

func (a *API) revoke(c *gin.Context) {
	if err := a.reg.Revoke(c.Request.Context(), auth.ActorFrom(c.Request), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// session is one runner's live socket.
type session struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	byeOnce sync.Once
}

func (s *session) send(ctx context.Context, env runnerwire.Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageText, raw)
}

// Send writes one message, bounded by writeTimeout. It is how the dispatcher
// reaches the runner from other requests; writeMu keeps their frames whole.
func (s *session) Send(env runnerwire.Envelope) error {
	return s.send(context.Background(), env)
}

// Bye tells the runner why and closes the socket. It returns at once: the
// registry calls it from a request that should not wait on a slow runner.
func (s *session) Bye(reason string) {
	s.byeOnce.Do(func() {
		go func() {
			_ = s.send(context.Background(), runnerwire.Envelope{Type: runnerwire.TypeBye, Reason: reason})
			_ = s.conn.Close(websocket.StatusNormalClosure, reason)
		}()
	})
}

func readEnvelope(ctx context.Context, conn *websocket.Conn, timeout time.Duration) (runnerwire.Envelope, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		return runnerwire.Envelope{}, err
	}
	if kind != websocket.MessageText {
		return runnerwire.Envelope{}, errors.New("binary message")
	}
	var env runnerwire.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return runnerwire.Envelope{}, err
	}
	return env, nil
}

// connect serves the socket a runner keeps open: hello first, then pings.
func (a *API) connect(c *gin.Context) {
	runner, ok := runners.RunnerFrom(c.Request.Context())
	if !ok {
		httpx.Err(c, http.StatusUnauthorized, runners.ErrUnauthenticated)
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		slog.Warn("runner connect: accept", slog.String("runner", runner.ID), slog.Any("error", err))
		return
	}
	conn.SetReadLimit(runnerwire.MaxHelloBytes)
	ctx := c.Request.Context()

	first, err := readEnvelope(ctx, conn, helloTimeout)
	if err != nil || first.Type != runnerwire.TypeHello || first.Hello == nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	if err := a.reg.SetHello(ctx, runner.ID, *first.Hello); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, closeReason(err))
		return
	}

	// Welcome goes out before the connection is attached: once attached, the
	// dispatcher may send offers from other requests, and the runner must
	// hear who it is first.
	s := &session{conn: conn}
	welcome := runnerwire.Envelope{Type: runnerwire.TypeWelcome, Welcome: &runnerwire.Welcome{
		RunnerID: runner.ID, Name: runner.Name, Owner: runner.OwnerName, OwnerID: runner.OwnerID,
	}}
	if err := s.send(ctx, welcome); err != nil {
		return
	}
	detach := a.reg.Attach(runner.ID, s)
	defer detach()
	// Past the hello, run traffic (event batches, changes) is larger.
	conn.SetReadLimit(maxRunMessageBytes)
	if a.runs != nil {
		a.runs.Attached(ctx, runner.ID, first.Active)
	}

	for {
		env, err := readEnvelope(ctx, conn, silenceTimeout)
		if err != nil {
			// A timeout closes the socket inside Read; anything else means the
			// runner went away or sent garbage.
			_ = conn.CloseNow()
			return
		}
		switch env.Type {
		case runnerwire.TypePing:
			if err := a.reg.Touch(ctx, runner.ID); err != nil {
				slog.Warn("runner touch", slog.String("runner", runner.ID), slog.Any("error", err))
			}
			if err := s.send(ctx, runnerwire.Envelope{Type: runnerwire.TypePong}); err != nil {
				return
			}
		case runnerwire.TypeHello:
			if env.Hello == nil {
				_ = conn.Close(websocket.StatusPolicyViolation, "empty hello")
				return
			}
			if err := a.reg.SetHello(ctx, runner.ID, *env.Hello); err != nil {
				_ = conn.Close(websocket.StatusPolicyViolation, closeReason(err))
				return
			}
			// It may now take more at once, or newly take runs at all.
			if a.runs != nil {
				a.runs.Pump(ctx, runner.ID)
			}
		case runnerwire.TypeAccept, runnerwire.TypeDecline, runnerwire.TypeEvents, runnerwire.TypeFinish,
			runnerwire.TypeChanges, runnerwire.TypePR, runnerwire.TypeMergeResult:
			if a.runs == nil {
				_ = conn.Close(websocket.StatusPolicyViolation, "this hub does not take runs")
				return
			}
			if err := a.runMessage(ctx, runner.ID, env); err != nil {
				// A message about a run that is not this runner's, or that has
				// moved on (a finish after a hub-side cancel), is dropped, not
				// fatal: the socket stays up for the runs that are fine.
				slog.Warn("runner run message dropped",
					slog.String("runner", runner.ID), slog.String("type", env.Type),
					slog.String("run", runID(env)), slog.Any("error", err))
			}
		case runnerwire.TypeLLMDelta, runnerwire.TypeLLMDone:
			// An answer to a copilot model call. One nobody waits for any
			// more (cancelled, timed out) is dropped, never fatal.
			if a.llm != nil {
				_ = a.llm.Deliver(runner.ID, env)
			}
		case runnerwire.TypeTermOut:
			if a.runs == nil {
				_ = conn.Close(websocket.StatusPolicyViolation, "this hub does not take runs")
				return
			}
			a.termOut(ctx, runner.ID, env)
		default:
			_ = conn.Close(websocket.StatusPolicyViolation, "unknown message type")
			return
		}
	}
}

// errMalformed is a run message missing the part its type requires.
var errMalformed = errors.New("malformed run message")

// runMessage applies one run message. The runner's ID comes from its
// authenticated socket, never from the message: the dispatcher refuses any
// run that is not that runner's.
func (a *API) runMessage(ctx context.Context, runnerID string, env runnerwire.Envelope) error {
	switch env.Type {
	case runnerwire.TypeAccept:
		if env.Accept == nil || env.Accept.RunID == "" {
			return errMalformed
		}
		return a.runs.Accept(ctx, runnerID, *env.Accept)
	case runnerwire.TypeDecline:
		if env.RunID == "" {
			return errMalformed
		}
		return a.runs.Decline(ctx, runnerID, env.RunID, env.Reason)
	case runnerwire.TypeEvents:
		if env.RunID == "" {
			return errMalformed
		}
		// Over MaxEventsPerMessage, the dispatcher keeps the first ones.
		return a.runs.Events(ctx, runnerID, env.RunID, env.Events)
	case runnerwire.TypeFinish:
		if env.RunID == "" || env.Result == nil {
			return errMalformed
		}
		return a.runs.Finish(ctx, runnerID, env.RunID, *env.Result)
	case runnerwire.TypeChanges:
		if env.RunID == "" || env.Changes == nil {
			return errMalformed
		}
		// Out of limits, for another runner's run, or for a run that never
		// ran: dropped. A resend after the first report is ignored.
		return a.runs.Changes(ctx, runnerID, env.RunID, *env.Changes)
	case runnerwire.TypePR:
		if env.RunID == "" || env.PR == nil {
			return errMalformed
		}
		return a.runs.PR(ctx, runnerID, env.RunID, *env.PR)
	case runnerwire.TypeMergeResult:
		if env.MergeResult == nil || env.MergeResult.ID == "" || env.MergeResult.RunID == "" {
			return errMalformed
		}
		// For a merge that is not this runner's, or already answered: dropped.
		return a.runs.MergeResult(ctx, runnerID, *env.MergeResult)
	}
	return errMalformed
}

func runID(env runnerwire.Envelope) string {
	if env.Accept != nil {
		return env.Accept.RunID
	}
	if env.MergeResult != nil {
		return env.MergeResult.RunID
	}
	return env.RunID
}

// closeReason fits an error into a close frame, whose reason is capped at 123
// bytes.
func closeReason(err error) string {
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}
