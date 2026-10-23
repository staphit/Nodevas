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
)

const maxCreateBody = 4 << 10

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
}

// New builds the handler set.
func New(reg *runners.Registry) *API {
	return &API{reg: reg}
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

	s := &session{conn: conn}
	detach := a.reg.Attach(runner.ID, s)
	defer detach()
	welcome := runnerwire.Envelope{Type: runnerwire.TypeWelcome, Welcome: &runnerwire.Welcome{
		RunnerID: runner.ID, Name: runner.Name, Owner: runner.OwnerName,
	}}
	if err := s.send(ctx, welcome); err != nil {
		return
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
		default:
			_ = conn.Close(websocket.StatusPolicyViolation, "unknown message type")
			return
		}
	}
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
