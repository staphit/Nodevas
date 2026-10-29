// Package copilot serves the voice copilot's HTTP API (COPILOT.md, "HTTP
// API"): status, conversations, the streamed turn and the three ways the
// browser answers a running turn.
//
// Ownership is decided in internal/copilot, by account id. Every lookup that
// misses — including one for somebody else's conversation or turn — answers
// 404 here, so the API never confirms that another person's id exists.
package copilot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/copilot"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/logging"
)

// Request body caps. A turn carries one utterance plus a little context; the
// answers carry an id and a short value, or a client tool's output.
const (
	maxSmallBody        = 8 << 10
	maxTurnBody         = 64 << 10
	maxClientResultBody = 256 << 10
	// A browser model answer is at most copilot.MaxBrowserOutputBytes of
	// text, JSON-escaped, plus an id.
	maxModelResultBody = 2*copilot.MaxBrowserOutputBytes + 8<<10
	maxSettingsBody    = 16 << 10
)

// writeGrace is how far each event pushes the response's write deadline. The
// server's absolute deadline is generous, but a conversation that waits on
// permissions and keeps talking can outlast any fixed one; each write moves
// it so a live stream is never cut mid-turn and a stalled one still is.
const writeGrace = 2 * time.Minute

// heartbeatEvery is how often a quiet stream gets a `ping` line. A turn parked
// on a permission card writes nothing for up to two minutes, and proxies in
// front of a shared server (Cloudflare Tunnel, nginx) close a response that
// has been idle for less than that.
const heartbeatEvery = 15 * time.Second

// API serves the copilot endpoints.
type API struct {
	service *copilot.Service
}

// New builds the handler set.
func New(service *copilot.Service) *API {
	return &API{service: service}
}

// Register wires the routes.
func (a *API) Register(api *gin.RouterGroup) {
	group := api.Group("/copilot")
	group.GET("/status", a.getStatus)
	group.GET("/conversations", a.listConversations)
	group.POST("/conversations", a.createConversation)
	group.GET("/conversations/:id", a.getConversation)
	group.PATCH("/conversations/:id", a.patchConversation)
	group.DELETE("/conversations/:id", a.deleteConversation)
	group.POST("/conversations/:id/turns", a.postTurn)
	group.POST("/conversations/:id/turns/:turn/decisions", a.postDecision)
	group.POST("/conversations/:id/turns/:turn/client-results", a.postClientResult)
	group.POST("/conversations/:id/turns/:turn/model-results", a.postModelResult)
	group.POST("/conversations/:id/turns/:turn/cancel", a.postCancel)

	admin := group.Group("")
	admin.Use(httpx.RequireAdmin)
	admin.GET("/settings", a.getSettings)
	admin.PUT("/settings", a.putSettings)
}

// fail maps the service's errors to statuses.
func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, copilot.ErrNotFound):
		httpx.Err(c, http.StatusNotFound, locale.Errorf("not found", "找不到"))
	case errors.Is(err, copilot.ErrInvalid), errors.Is(err, copilot.ErrInvalidSettings):
		httpx.Err(c, http.StatusBadRequest, err)
	case errors.Is(err, copilot.ErrBusy):
		httpx.Coded(c, http.StatusConflict, "turn_in_progress", err, nil)
	case errors.Is(err, copilot.ErrUnavailable):
		// The reason is the operator's configuration, not a secret, and the
		// panel shows it so the person knows whom to ask.
		httpx.Coded(c, http.StatusServiceUnavailable, "copilot_unavailable", locale.Errorf("service unavailable", "服務無法使用"),
			map[string]any{"reason": strings.TrimPrefix(err.Error(), copilot.ErrUnavailable.Error()+": ")})
	case errors.Is(err, copilot.ErrBridge):
		httpx.Err(c, http.StatusBadGateway, err)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

func (a *API) getStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	status, err := a.service.Status(c.Request.Context(), auth.ActorFrom(c.Request),
		browserModel(c.Query("browserModel")))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (a *API) listConversations(c *gin.Context) {
	conversations, err := a.service.ListConversations(c.Request.Context(),
		auth.ActorFrom(c.Request), httpx.RequestProjectName(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"conversations": conversations})
}

func (a *API) createConversation(c *gin.Context) {
	var body struct {
		Project string       `json:"project"`
		Mode    copilot.Mode `json:"mode"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxSmallBody) {
		return
	}
	project := strings.Trim(strings.TrimSpace(body.Project), "/")
	if project == "" {
		project = httpx.RequestProjectName(c.Request)
	}
	conversation, err := a.service.CreateConversation(c.Request.Context(),
		auth.ActorFrom(c.Request), project, body.Mode)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, map[string]any{"conversation": conversation})
}

func (a *API) getConversation(c *gin.Context) {
	conversation, entries, err := a.service.Conversation(c.Request.Context(),
		auth.ActorFrom(c.Request), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{"conversation": conversation, "entries": entries})
}

func (a *API) patchConversation(c *gin.Context) {
	var body struct {
		Mode  *copilot.Mode `json:"mode"`
		Title *string       `json:"title"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxSmallBody) {
		return
	}
	conversation, err := a.service.UpdateConversation(c.Request.Context(), auth.ActorFrom(c.Request),
		c.Param("id"), copilot.ConversationPatch{Mode: body.Mode, Title: body.Title})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"conversation": conversation})
}

func (a *API) deleteConversation(c *gin.Context) {
	if err := a.service.DeleteConversation(c.Request.Context(), auth.ActorFrom(c.Request), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// postTurn runs a turn and streams its events as NDJSON.
//
// The status line waits for the first event. Everything that can refuse a
// turn — a conversation that is not the caller's, a turn already running, no
// API key — happens before it, so those still arrive as ordinary JSON errors
// with their own status rather than as a 200 stream that fails on line one.
func (a *API) postTurn(c *gin.Context) {
	var body struct {
		Text        string              `json:"text"`
		Source      string              `json:"source"`
		Mode        copilot.Mode        `json:"mode"`
		Context     copilot.TurnContext `json:"context"`
		ClientTools []string            `json:"clientTools"`
		// BrowserModel is the browser's Prompt API availability.
		BrowserModel string `json:"browserModel"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxTurnBody) {
		return
	}
	stream := newEventStream(c)
	stopHeartbeat := stream.heartbeat(heartbeatEvery)
	// Deferred as well as called below: a Turn that panics (recovered by the
	// router) must not leave the ticker writing into a finished response.
	defer stopHeartbeat()
	err := a.service.Turn(c.Request.Context(), copilot.TurnRequest{
		Actor:          auth.ActorFrom(c.Request),
		ConversationID: c.Param("id"),
		Text:           body.Text,
		Source:         body.Source,
		Mode:           body.Mode,
		Context:        body.Context,
		ClientTools:    body.ClientTools,
		Host:           c.Request.Host,
		Cookie:         c.Request.Header.Get("Cookie"),
		RemoteAddr:     auth.ClientIP(c.Request),
		Session:        sessionFingerprint(c.Request),
		BrowserModel:   browserModel(body.BrowserModel),
	}, stream.send)
	stopHeartbeat()
	if err != nil && !stream.started {
		fail(c, err)
	}
}

// eventStream writes one JSON object per line and flushes each, so the panel
// can show text as it is spoken and answer a permission card while the turn
// waits on it.
type eventStream struct {
	// mu serialises the turn's events with the heartbeat's pings.
	mu         sync.Mutex
	c          *gin.Context
	controller *http.ResponseController
	started    bool
	broken     bool
}

func newEventStream(c *gin.Context) *eventStream {
	return &eventStream{c: c, controller: http.NewResponseController(c.Writer)}
}

// heartbeat pings the stream until the returned func is called. It only
// writes once the turn has started, so turn_started stays the first line.
// The returned func may be called more than once.
func (s *eventStream) heartbeat(every time.Duration) func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		// A panic here would take the whole server down with it: nothing
		// above a bare goroutine recovers. Losing the pings is the lesser harm.
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("copilot heartbeat panicked",
					slog.Any("panic", recovered), slog.String("stack", string(debug.Stack())))
			}
		}()
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.mu.Lock()
				started := s.started
				s.mu.Unlock()
				if started {
					s.send(copilot.Event{Type: "ping"})
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
}

func (s *eventStream) send(event copilot.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return
	}
	line, err := json.Marshal(event)
	if err != nil {
		slog.Warn("copilot event could not be encoded", logging.Err(err))
		return
	}
	if !s.started {
		s.started = true
		header := s.c.Writer.Header()
		header.Set("Content-Type", "application/x-ndjson; charset=utf-8")
		header.Set("Cache-Control", "no-store")
		header.Set("X-Content-Type-Options", "nosniff")
		// Tells nginx-style proxies not to buffer the stream into one blob.
		header.Set("X-Accel-Buffering", "no")
		s.c.Status(http.StatusOK)
	}
	// Unsupported on some writer chains; the absolute deadline then applies.
	_ = s.controller.SetWriteDeadline(time.Now().Add(writeGrace))
	if _, err := s.c.Writer.Write(append(line, '\n')); err != nil {
		// The person went away. The request context is what stops the turn;
		// this only keeps the rest of the turn from writing into a dead socket.
		s.broken = true
		return
	}
	s.c.Writer.Flush()
}

func (a *API) postDecision(c *gin.Context) {
	var body struct {
		RequestID string           `json:"requestId"`
		Decision  copilot.Decision `json:"decision"`
		Message   string           `json:"message"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxSmallBody) {
		return
	}
	if err := a.service.Decide(auth.ActorFrom(c.Request), c.Param("id"), c.Param("turn"),
		body.RequestID, body.Decision, body.Message); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (a *API) postClientResult(c *gin.Context) {
	var body struct {
		CallID  string `json:"callId"`
		Output  string `json:"output"`
		IsError bool   `json:"isError"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxClientResultBody) {
		return
	}
	if err := a.service.ClientResult(auth.ActorFrom(c.Request), c.Param("id"), c.Param("turn"),
		body.CallID, body.Output, body.IsError); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// postModelResult takes the browser's answer to a model_request. The
// service accepts it only from the session that started the turn.
func (a *API) postModelResult(c *gin.Context) {
	var body struct {
		RequestID string `json:"requestId"`
		Output    string `json:"output"`
		Error     string `json:"error"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxModelResultBody) {
		return
	}
	if err := a.service.ModelResult(auth.ActorFrom(c.Request), sessionFingerprint(c.Request),
		c.Param("id"), c.Param("turn"), body.RequestID, body.Output, body.Error); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// browserModel keeps only the availabilities the Prompt API reports.
func browserModel(value string) string {
	switch value {
	case copilot.BrowserAvailable, copilot.BrowserDownloadable, copilot.BrowserDownloading:
		return value
	}
	return ""
}

// sessionFingerprint names the sign-in session a request belongs to without
// carrying the session secret around: a hash of its cookie. Empty on a
// loopback-only server, which has no sessions.
func sessionFingerprint(r *http.Request) string {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(cookie.Value))
	return hex.EncodeToString(sum[:16])
}

func (a *API) postCancel(c *gin.Context) {
	if err := a.service.Cancel(auth.ActorFrom(c.Request), c.Param("id"), c.Param("turn")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (a *API) getSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, err := a.service.Settings(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (a *API) putSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var update copilot.SettingsUpdate
	if !httpx.DecodeJSONLimit(c, &update, maxSettingsBody) {
		return
	}
	view, err := a.service.UpdateSettings(c.Request.Context(), update)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}
