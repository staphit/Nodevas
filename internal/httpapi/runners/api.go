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
	"nodevas/internal/buildinfo"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
	"nodevas/internal/runners"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/safego"
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
	// repo carries read-only repo browsing answers (WithRepo).
	repo *runners.RepoRelay
	// settings carries runner settings changes (WithSettings; settings.go).
	settings *runners.SettingsRelay
	// repoAdmin carries the built-in runner's repo changes (WithRepoAdmin;
	// repos.go); nil serves none.
	repoAdmin *RepoAdminOptions
	// link enables device links (WithLinks; link.go); nil serves none.
	link *LinkOptions
	// single: the hub has no accounts (WithEdition), so it is the
	// single-user edition and may welcome its own runner as built in.
	single bool
}

// WithEdition says whether the hub is the single-user edition: no accounts,
// the person at the machine is the only user (runnerwire/edition.go). Its
// welcomes say so, and the runner it started for itself at this startup
// (Registry.SetBuiltIn) is welcomed as built in when it connects straight
// from this machine. It returns a for chaining.
func (a *API) WithEdition(single bool) *API {
	a.single = single
	return a
}

// welcomeFor is the welcome a runner connecting with request gets: the
// edition, and whether the hub vouches that it is the built-in runner. The
// runner relaxes nothing on this word alone; it checks its own side too.
func (a *API) welcomeFor(request *http.Request, runner runnerwire.Runner) runnerwire.Welcome {
	welcome := runnerwire.Welcome{
		RunnerID: runner.ID, Name: runner.Name, Owner: runner.OwnerName, OwnerID: runner.OwnerID,
		Edition: runnerwire.EditionTeam,
	}
	if a.single {
		welcome.Edition = runnerwire.EditionSingle
		// The runner serve minted or reused at this startup, on a socket
		// straight from this machine: a loopback peer relaying nobody.
		welcome.BuiltIn = a.reg.IsBuiltInID(runner.ID) && auth.DirectLoopback(request)
	}
	return welcome
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

// WithRepo relays read-only repo browsing answers (runnerwire/repobrowse.go)
// through relay. It returns a for chaining.
func (a *API) WithRepo(relay *runners.RepoRelay) *API {
	a.repo = relay
	return a
}

// Register wires /api/runners, /api/runner/connect and /api/runner/version. The server's auth
// middleware decides who reaches which: a session for /api/runners, a runner
// token for /api/runner/*, never the other way round.
func (a *API) Register(api *gin.RouterGroup) {
	api.GET("/runners", a.list)
	api.POST("/runners", a.create)
	api.DELETE("/runners/:id", a.revoke)
	api.GET("/runner/connect", a.connect)
	api.GET("/runner/version", a.version)
	a.registerSettings(api)
	a.registerRepoAdmin(api)
	a.registerLinks(api)
}

// version is GET runnerwire.VersionPath: the commit this hub was built from,
// for a runner token only (the middleware sees to that).
func (a *API) version(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, runnerwire.VersionResponse{Commit: buildinfo.Commit()})
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

// listedRunner is a runner as GET /api/runners lists it: the registry's
// record, and whether it is this hub's own runner.
type listedRunner struct {
	runnerwire.Runner
	// HubLocal: the runner a single-user hub registered or kept for itself
	// at this startup (Registry.SetBuiltIn), whether or not it has
	// connected. BuiltIn says the same only once the runner's welcome did,
	// so until then the app could not tell "this computer's own runner,
	// still starting" from any other offline runner. It is what the
	// registry was told at startup, never the runner's claim, and it
	// allows nothing: adding repos and changing settings stay on BuiltIn.
	HubLocal bool `json:"hubLocal,omitempty"`
}

// listResponse is the body of GET /api/runners: runnerwire.ListResponse
// with each runner as listed.
type listResponse struct {
	Runners []listedRunner `json:"runners"`
}

func (a *API) list(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	list, err := a.reg.List(c.Request.Context(), auth.ActorFrom(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	out := listResponse{Runners: make([]listedRunner, 0, len(list))}
	for _, runner := range list {
		out.Runners = append(out.Runners, listedRunner{
			Runner: runner,
			// As with the welcome: a hub with accounts has no runner of
			// its own, whatever the registry was told.
			HubLocal: a.single && a.reg.IsBuiltInID(runner.ID),
		})
	}
	c.JSON(http.StatusOK, out)
}

func (a *API) create(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	// A runner acts as its owner; an agent minting one would hand itself a
	// credential that outlives its session.
	if actor.IsAgent() {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", locale.Errorf("agents cannot create runners", "agent 不能建立 runner"), nil)
		return
	}
	// The token outlives the session that mints it and can pose as the
	// owner's copilot model, so minting one takes what a device link takes: a
	// fresh step-up where there are accounts. A stolen session cookie alone
	// must not turn into a credential that signing in again does not end.
	if a.link != nil && !a.freshStepUp(c) {
		return
	}
	var body runnerwire.CreateRequest
	if !httpx.DecodeJSONLimit(c, &body, maxCreateBody) {
		return
	}
	runner, token, err := a.reg.Create(c.Request.Context(), actor, body.Name)
	if err != nil {
		fail(c, err)
		return
	}
	a.noticeNewRunner(c, actor, runner)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, runnerwire.CreateResponse{Runner: runner, Token: token})
}

// noticeNewRunner mails the owner the notice a redeemed device link sends:
// the alarm for a runner they did not add. Nothing is sent on a server
// without accounts or mail.
func (a *API) noticeNewRunner(c *gin.Context, actor identity.Actor, runner runnerwire.Runner) {
	if a.link == nil || a.link.Mail == nil || a.link.Owner == nil {
		return
	}
	account, ok := a.link.Owner(c.Request.Context(), actor.ID)
	if !ok || account.Email == "" {
		return
	}
	source, at, language := auth.ClientIP(c.Request), time.Now(), httpx.Language(c.Request)
	safego.Go("runner notice mail", func() { a.mailRedeem(account.Email, runner, "", "", source, at, language) })
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
			defer safego.Recover("runner bye")
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
		return runnerwire.Envelope{}, locale.Errorf("binary message", "二進位訊息")
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
	welcome := a.welcomeFor(c.Request, runner)
	if err := s.send(ctx, runnerwire.Envelope{Type: runnerwire.TypeWelcome, Welcome: &welcome}); err != nil {
		return
	}
	a.reg.SetWelcomed(runner.ID, welcome.BuiltIn)
	detach := a.reg.Attach(runner.ID, s)
	defer func() {
		detach()
		// What it asked about its offers goes with it, unless a newer
		// connection already took its place.
		if a.runs != nil {
			a.runs.Detached(context.Background(), runner.ID)
		}
	}()
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
			runnerwire.TypeChanges, runnerwire.TypePR, runnerwire.TypeMergeResult, runnerwire.TypeWorkspaceResult,
			runnerwire.TypeOfferAsk:
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
		case runnerwire.TypeLLMDelta, runnerwire.TypeLLMDone, runnerwire.TypeDecideDone:
			// An answer to a copilot model call. One nobody waits for any
			// more (cancelled, timed out) is dropped, never fatal.
			if a.llm != nil {
				_ = a.llm.Deliver(runner.ID, env)
			}
		case runnerwire.TypeRepoDone:
			// A repo browse answer; one nobody waits for is dropped.
			if a.repo != nil {
				_ = a.repo.Deliver(runner.ID, env)
			}
		case runnerwire.TypeSettingsDone:
			// A settings answer, after the hello that carries the change;
			// one nobody waits for is dropped.
			if a.settings != nil {
				_ = a.settings.Deliver(runner.ID, env)
			}
		case runnerwire.TypeRepoAdminDone:
			// A repo admin answer, after the hello with the new repos; one
			// nobody waits for is dropped.
			if a.repoAdmin != nil && a.repoAdmin.Relay != nil {
				_ = a.repoAdmin.Relay.Deliver(runner.ID, env)
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
var errMalformed = locale.Errorf("malformed run message", "執行訊息格式錯誤")

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
		return a.runs.Decline(ctx, runnerID, env.RunID, env.Reason, env.Code)
	case runnerwire.TypeOfferAsk:
		if env.OfferAsk == nil || env.OfferAsk.RunID == "" {
			return errMalformed
		}
		// For a run that is not this runner's, or no longer offered: dropped.
		return a.runs.OfferAsk(ctx, runnerID, *env.OfferAsk)
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
	case runnerwire.TypeWorkspaceResult:
		if env.WorkspaceResult == nil || env.WorkspaceResult.ID == "" || env.WorkspaceResult.RunID == "" {
			return errMalformed
		}
		// For an action that is not this runner's, or already answered: dropped.
		return a.runs.WorkspaceResult(ctx, runnerID, *env.WorkspaceResult)
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
	if env.WorkspaceResult != nil {
		return env.WorkspaceResult.RunID
	}
	if env.OfferAsk != nil {
		return env.OfferAsk.RunID
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
