// Package agents serves the HTTP endpoints for the agent collaboration network:
// live session heartbeats and team roster, agent-to-agent and human-to-agent
// messaging, long-polled inboxes, and human-in-the-loop ask_user approvals.
package agents

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
	"nodevas/internal/project"
	"nodevas/internal/usage"
)

const (
	maxHeartbeatBody = 16 << 10
	// maxMessageBody leaves room for the most a message may attach
	// (agents.MaxFiles of agents.MaxFileBytes) after JSON escaping, which can
	// grow text several times over; the per-file limits are checked after
	// decoding, against the text itself.
	maxMessageBody = 64<<10 + 6*agents.MaxFiles*agents.MaxFileBytes
	maxInboxBody   = 8 << 10
	maxAskBody     = 64 << 10
)

// API exposes an agents.Hub over HTTP.
type API struct {
	pm  *project.ProjectManager
	hub *agents.Hub
	// usage records session token usage; nil leaves the route answering 503.
	usage       *usage.Store
	usageNotify func(projectKey string)
	// runUsage records an interactive run's usage from its session's
	// report (UseRunUsage); nil records none.
	runUsage func(ctx context.Context, runID string, models []usage.ModelUsage) error
	// isRun reports whether a request comes from a run through its runner's
	// proxy (UseRunCheck); nil means none does.
	isRun func(*http.Request) bool
	// runOf names the run a request comes from (UseRunOf); nil means none.
	runOf func(*http.Request) string
}

// UseRunCheck tells the API how to recognise a run's request (the server's
// runner proxy marks them).
func (a *API) UseRunCheck(isRun func(*http.Request) bool) { a.isRun = isRun }

// UseRunOf tells the API how to read which run a request comes from: the
// server's runner proxy vouches for it, so it is never taken from the body.
func (a *API) UseRunOf(runOf func(*http.Request) string) { a.runOf = runOf }

func (a *API) runIDOf(r *http.Request) string {
	if a.runOf == nil {
		return ""
	}
	return a.runOf(r)
}

// fromRun reports whether a request acting as actorID is a run's: proxied by
// a runner, or a local "run:" actor (`nodevas mcp --actor run:<agent>`).
func (a *API) fromRun(r *http.Request, actorID string) bool {
	return (a.isRun != nil && a.isRun(r)) || strings.HasPrefix(strings.ToLower(actorID), "run:")
}

// unreadCount is the hub's count of sessionName's unread mail, as a run's
// inbox when the request is a run's (CheckRunInbox).
func (a *API) unreadCount(r *http.Request, projectKey, sessionName, actorID string) int {
	if a.fromRun(r, actorID) {
		return a.hub.RunUnreadCount(r.Context(), projectKey, sessionName, actorID)
	}
	return a.hub.UnreadCount(r.Context(), projectKey, sessionName, actorID)
}

// New builds the HTTP handler set for the agent network.
func New(pm *project.ProjectManager, hub *agents.Hub) *API {
	return &API{pm: pm, hub: hub}
}

// Register wires `/api/agents/*` routes onto the router group.
func (a *API) Register(api *gin.RouterGroup) {
	group := api.Group("/agents")
	group.GET("", a.getRoster)
	group.POST("/heartbeat", a.postHeartbeat)
	group.POST("/leave", a.postLeave)
	group.POST("/sessions/:id/inbound", a.postInbound)
	group.GET("/messages", a.getMessages)
	group.POST("/messages", a.postMessage)
	group.POST("/messages/:id/approve", a.postApprove)
	group.POST("/messages/:id/reject", a.postReject)
	group.GET("/messages/:id/files/:name", a.getMessageFile)
	group.POST("/inbox", a.postInbox)
	group.GET("/asks", a.getAsks)
	group.POST("/asks", a.postAsk)
	group.POST("/asks/:id/answer", a.postAskAnswer)
	group.POST("/sessions/:id/changes", a.postSessionChanges)
	group.POST("/sessions/:id/usage", a.postSessionUsage)
	api.GET("/nodes/:id/session-changes", a.getNodeSessionChanges)
}

func (a *API) projectKey(r *http.Request) string {
	if st := httpx.StoreFor(r, a.pm); st != nil {
		return st.Root()
	}
	return httpx.RequestProjectName(r)
}

func (a *API) liveClaimsAndUsers(r *http.Request) ([]agents.ClaimInfo, []engine.User) {
	st := httpx.StoreFor(r, a.pm)
	if st == nil {
		return nil, nil
	}
	rawClaims := st.LiveClaims()
	g, _, err := st.LoadGraph()
	titles := make(map[string]string)
	var users []engine.User
	if err == nil && g != nil {
		users = g.Users
		for _, n := range g.Nodes {
			titles[n.ID] = n.Title
		}
	}
	claims := make([]agents.ClaimInfo, 0, len(rawClaims))
	for _, c := range rawClaims {
		claims = append(claims, agents.ClaimInfo{
			NodeID: c.NodeID,
			Title:  titles[c.NodeID],
			Owner:  c.Owner,
		})
	}
	return claims, users
}

// resolveActor combines the authenticated server identity with the optional
// `--actor` attribution sent by an MCP process. On loopback (`local`), the
// attribution names the actor; on a shared server with accounts, the
// authenticated account ID is preserved as the owner identity.
func resolveActor(r *http.Request, bodyActor string) (actorID, actorName string) {
	authed := auth.ActorFrom(r)
	trimmed := strings.TrimSpace(bodyActor)
	if (authed.ID == "" || authed.ID == "local") && trimmed != "" {
		return trimmed, strings.TrimPrefix(trimmed, "mcp:")
	}
	if authed.ID != "" {
		name := authed.Name
		if name == "" {
			name = authed.ID
		}
		return authed.ID, name
	}
	if trimmed != "" {
		return trimmed, strings.TrimPrefix(trimmed, "mcp:")
	}
	return "local", "local"
}

func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agents.ErrInvalid):
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
	case errors.Is(err, agents.ErrNotFound):
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", err, nil)
	case errors.Is(err, agents.ErrRefused):
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED", err, nil)
	case errors.Is(err, agents.ErrRateLimited):
		httpx.Coded(c, http.StatusTooManyRequests, "RATE_LIMITED", err, nil)
	case errors.Is(err, agents.ErrLoopDetected):
		httpx.Coded(c, http.StatusConflict, "CONFLICT", err, nil)
	case errors.Is(err, agents.ErrAskBusy), errors.Is(err, agents.ErrNotHeld):
		httpx.Coded(c, http.StatusConflict, "CONFLICT", err, nil)
	default:
		httpx.Err(c, http.StatusInternalServerError, err)
	}
}

func (a *API) getRoster(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	projectKey := a.projectKey(c.Request)
	filter := c.Query("filter")
	all := c.Query("all") == "true" || c.Query("all") == "1"
	callerSession := strings.TrimSpace(c.Query("session"))
	callerActor, _ := resolveActor(c.Request, c.Query("actor"))

	claims, users := a.liveClaimsAndUsers(c.Request)
	sessions := a.hub.ListSessions(projectKey, filter, all, claims)
	if sessions == nil {
		sessions = []agents.Session{}
	}
	if users == nil {
		users = []engine.User{}
	}
	unread := 0
	if callerSession != "" || callerActor != "" {
		unread = a.unreadCount(c.Request, projectKey, callerSession, callerActor)
	}
	c.JSON(http.StatusOK, map[string]any{
		"sessions":    sessions,
		"users":       users,
		"unreadCount": unread,
	})
}

func (a *API) postHeartbeat(c *gin.Context) {
	var body struct {
		SessionID string `json:"sessionId"`
		Actor     string `json:"actor"`
		Name      string `json:"name"`
		Agent     string `json:"agent"`
		Role      string `json:"role"`
		Repo      string `json:"repo"`
		Branch    string `json:"branch"`
		Cwd       string `json:"cwd"`
		State     string `json:"state"`
		Doing     string `json:"doing"`
		NodeID    string `json:"nodeId"`
		Model     string `json:"model"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxHeartbeatBody) {
		return
	}
	projectKey := a.projectKey(c.Request)
	actorID, actorName := resolveActor(c.Request, body.Actor)
	role := strings.TrimSpace(body.Role)
	if authed := auth.ActorFrom(c.Request); authed.Agent != "" {
		role = string(authed.Agent)
	}
	session, err := a.hub.Touch(agents.Heartbeat{
		SessionID: body.SessionID,
		Project:   projectKey,
		ActorID:   actorID,
		ActorName: actorName,
		Name:      body.Name,
		Agent:     body.Agent,
		Role:      role,
		Repo:      body.Repo,
		Branch:    body.Branch,
		Cwd:       body.Cwd,
		State:     body.State,
		Doing:     body.Doing,
		NodeID:    body.NodeID,
		Model:     body.Model,
		Run:       a.fromRun(c.Request, actorID),
		RunID:     a.runIDOf(c.Request),
	})
	if err != nil {
		fail(c, err)
		return
	}
	unread := a.unreadCount(c.Request, projectKey, session.Name, actorID)
	// W6: the node this session holds, so `nodevas hook` reports its edits
	// only while there is one to report them against.
	claims, _ := a.liveClaimsAndUsers(c.Request)
	_, claimed, _ := a.hub.ClaimedNode(projectKey, session.ID, claims)
	c.JSON(http.StatusOK, map[string]any{
		"ok":            true,
		"session":       session,
		"unreadCount":   unread,
		"claimedNodeId": claimed,
	})
}

func (a *API) postLeave(c *gin.Context) {
	var body struct {
		SessionID string `json:"sessionId"`
		Actor     string `json:"actor"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxHeartbeatBody) {
		return
	}
	actorID, _ := resolveActor(c.Request, body.Actor)
	if err := a.hub.Leave(a.projectKey(c.Request), body.SessionID, actorID); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// humanViewer is the caller of a route a person uses: who they are as the
// server authenticated them, never an attribution from the body.
func humanViewer(r *http.Request) agents.Viewer {
	authed := auth.ActorFrom(r)
	return agents.Viewer{ID: authed.ID, Admin: authed.IsAdmin(), Agent: authed.IsAgent()}
}

// refuseAgent answers 403 for a request from an agent and reports whether it
// did. Holding, approving and rejecting are what a person does about agents;
// an agent that could do them could let its own mail through.
func refuseAgent(c *gin.Context, what *locale.Message) bool {
	if !auth.ActorFrom(c.Request).IsAgent() {
		return false
	}
	httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
		locale.Errorf("only a person may %s", "只有人可以%s", what), nil)
	return true
}

func (a *API) postInbound(c *gin.Context) {
	if refuseAgent(c, locale.Say("change a session's inbound policy", "更改工作階段的收信政策")) {
		return
	}
	var body struct {
		Inbound string `json:"inbound"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxInboxBody) {
		return
	}
	viewer := humanViewer(c.Request)
	session, err := a.hub.SetInbound(a.projectKey(c.Request), c.Param("id"), body.Inbound, viewer.ID, viewer.Admin)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) getMessages(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	projectKey := a.projectKey(c.Request)
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	var msgs []agents.Message
	var err error
	if status == agents.StatusHeld {
		// The held queue is what a person decides on; an agent reading it
		// would read its mail before anyone let it through.
		if refuseAgent(c, locale.Say("list held messages", "列出被擱置的訊息")) {
			return
		}
		msgs, err = a.hub.ListHeld(c.Request.Context(), projectKey, humanViewer(c.Request))
	} else {
		msgs, err = a.hub.ListMessages(c.Request.Context(), projectKey, c.Query("nodeId"), status, 150)
		if err == nil {
			// Held and rejected mail shows only to those it concerns, and
			// never to an agent.
			viewer := humanViewer(c.Request)
			kept := msgs[:0]
			for _, m := range msgs {
				if viewer.MaySee(m) {
					kept = append(kept, m)
				}
			}
			msgs = kept
		}
	}
	if err != nil {
		fail(c, err)
		return
	}
	if msgs == nil {
		msgs = []agents.Message{}
	}
	c.JSON(http.StatusOK, map[string]any{"messages": msgs})
}

func (a *API) postApprove(c *gin.Context) { a.decide(c, true) }
func (a *API) postReject(c *gin.Context)  { a.decide(c, false) }

func (a *API) decide(c *gin.Context, approve bool) {
	if refuseAgent(c, locale.Say("approve or reject held messages", "核准或拒絕被擱置的訊息")) {
		return
	}
	msg, err := a.hub.Decide(c.Request.Context(), a.projectKey(c.Request), c.Param("id"), approve, humanViewer(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, msg)
}

// getMessageFile serves one attachment as a download. It is never rendered:
// the type is plain text whatever the name says, nosniff stops a browser
// guessing otherwise, and the disposition makes it a file to save, so an
// attached .html cannot run as a page on this origin.
func (a *API) getMessageFile(c *gin.Context) {
	authed := auth.ActorFrom(c.Request)
	viewerID, _ := resolveActor(c.Request, c.Query("actor"))
	viewer := agents.Viewer{ID: viewerID, Admin: authed.IsAdmin(), Agent: authed.IsAgent()}
	info, content, err := a.hub.FileContent(c.Request.Context(), a.projectKey(c.Request), c.Param("id"), c.Param("name"), viewer)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name}))
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(content))
}

func (a *API) postMessage(c *gin.Context) {
	var body struct {
		Actor       string             `json:"actor"`
		FromSession string             `json:"fromSession"`
		SenderAgent string             `json:"senderAgent"`
		To          string             `json:"to"`
		Message     string             `json:"message"`
		ReplyTo     string             `json:"replyTo"`
		NodeID      string             `json:"nodeId"`
		Notify      bool               `json:"notify"`
		Files       []agents.FileInput `json:"files"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxMessageBody) {
		return
	}
	projectKey := a.projectKey(c.Request)
	authed := auth.ActorFrom(c.Request)
	actorID, _ := resolveActor(c.Request, body.Actor)
	isHuman := authed.Agent == "" && strings.TrimSpace(body.FromSession) == ""
	fromRun := a.fromRun(c.Request, actorID)
	if fromRun && a.hub.PersonSession(projectKey, body.FromSession) {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("a run may not send as a person's session", "執行不能以人的工作階段身分傳送"), nil)
		return
	}
	if fromRun {
		isHuman = false
	}
	claims, _ := a.liveClaimsAndUsers(c.Request)

	result, err := a.hub.Send(c.Request.Context(), agents.SendInput{
		Project:       projectKey,
		SenderActor:   actorID,
		SenderSession: body.FromSession,
		SenderAgent:   body.SenderAgent,
		IsHuman:       isHuman,
		To:            body.To,
		ReplyTo:       body.ReplyTo,
		NodeID:        body.NodeID,
		Body:          body.Message,
		Notify:        body.Notify,
		Files:         body.Files,
		Claims:        claims,
		FromRun:       fromRun,
	})
	if err != nil {
		fail(c, err)
		return
	}
	unread := 0
	if !isHuman {
		unread = a.unreadCount(c.Request, projectKey, body.FromSession, actorID)
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok":          true,
		"threadId":    result.ThreadID,
		"deliveries":  result.Deliveries,
		"unreadCount": unread,
	})
}

func (a *API) postInbox(c *gin.Context) {
	var body struct {
		Session     string `json:"session"`
		Actor       string `json:"actor"`
		WaitSeconds int    `json:"waitSeconds"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxInboxBody) {
		return
	}
	projectKey := a.projectKey(c.Request)
	actorID, _ := resolveActor(c.Request, body.Actor)
	wait := time.Duration(body.WaitSeconds) * time.Second
	if wait < 0 {
		wait = 0
	}
	if wait > agents.MaxInboxWait {
		wait = agents.MaxInboxWait
	}
	if wait > 0 {
		controller := http.NewResponseController(c.Writer)
		_ = controller.SetWriteDeadline(time.Now().Add(wait + 30*time.Second))
	}
	if a.fromRun(c.Request, actorID) && a.hub.PersonSession(projectKey, body.Session) {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("a run may not read a person's session inbox", "執行不能讀取人的工作階段收件匣"), nil)
		return
	}
	checkInbox := a.hub.CheckInbox
	if a.fromRun(c.Request, actorID) {
		checkInbox = a.hub.CheckRunInbox
	}
	msgs, err := checkInbox(c.Request.Context(), projectKey, body.Session, actorID, wait)
	if err != nil {
		if errors.Is(err, c.Request.Context().Err()) {
			return
		}
		fail(c, err)
		return
	}
	if msgs == nil {
		msgs = []agents.Message{}
	}
	c.JSON(http.StatusOK, map[string]any{
		"messages": msgs,
	})
}

func (a *API) getAsks(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	projectKey := a.projectKey(c.Request)
	asks, err := a.hub.ListAsks(c.Request.Context(), projectKey, c.Query("status"))
	if err != nil {
		fail(c, err)
		return
	}
	if asks == nil {
		asks = []agents.Ask{}
	}
	c.JSON(http.StatusOK, map[string]any{"asks": asks})
}

func (a *API) postAsk(c *gin.Context) {
	var body struct {
		SessionID      string   `json:"sessionId"`
		SessionName    string   `json:"sessionName"`
		Actor          string   `json:"actor"`
		Agent          string   `json:"agent"`
		Question       string   `json:"question"`
		AboutMsgID     string   `json:"aboutMsgId"`
		NodeID         string   `json:"nodeId"`
		Kind           string   `json:"kind"`
		Options        []string `json:"options"`
		Tool           string   `json:"tool"`
		ToolInput      string   `json:"toolInput"`
		TimeoutSeconds int      `json:"timeoutSeconds"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxAskBody) {
		return
	}
	projectKey := a.projectKey(c.Request)
	actorID, _ := resolveActor(c.Request, body.Actor)
	fromRun := a.fromRun(c.Request, actorID)
	sessionName := strings.TrimSpace(body.SessionName)
	if sessionName == "" {
		sessionName = strings.TrimSpace(body.SessionID)
	}
	if fromRun && a.hub.PersonSession(projectKey, sessionName) {
		// The answer, and any approval it forwards, would be the person's.
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("a run may not ask as a person's session", "執行不能以人的工作階段身分提問"), nil)
		return
	}
	// An agent seldom names the node it asks about; a run's is the one its
	// runner's proxy vouches it was dispatched on.
	nodeID := strings.TrimSpace(body.NodeID)
	if nodeID == "" {
		nodeID = auth.ActorFrom(c.Request).RunNodeID
	}
	timeout := agents.AskTimeout(body.Kind, time.Duration(body.TimeoutSeconds)*time.Second)
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetWriteDeadline(time.Now().Add(timeout + 30*time.Second))

	decided, err := a.hub.CreateAndWait(c.Request.Context(), agents.AskInput{
		Project:     projectKey,
		SessionID:   body.SessionID,
		SessionName: body.SessionName,
		ActorID:     actorID,
		Agent:       body.Agent,
		Question:    body.Question,
		AboutMsgID:  body.AboutMsgID,
		NodeID:      nodeID,
		Kind:        body.Kind,
		Options:     body.Options,
		Tool:        body.Tool,
		ToolInput:   body.ToolInput,
		// The run is the transport's word, never the body's: an allow for
		// the run is keyed on it.
		RunID:   a.runIDOf(c.Request),
		FromRun: fromRun,
	}, timeout)
	if err != nil {
		if errors.Is(err, c.Request.Context().Err()) {
			return
		}
		fail(c, err)
		return
	}
	unread := a.unreadCount(c.Request, projectKey, body.SessionName, actorID)
	c.JSON(http.StatusOK, map[string]any{
		"ask":         decided,
		"unreadCount": unread,
	})
}

func (a *API) postAskAnswer(c *gin.Context) {
	authed := auth.ActorFrom(c.Request)
	if authed.Agent != "" {
		httpx.Coded(c, http.StatusForbidden, "PERMISSION_DENIED",
			locale.Errorf("only a human session may answer ask_user requests", "只有人的工作階段可以回答 ask_user 請求"), nil)
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
		Answer   string `json:"answer"`
	}
	// A text answer runs to MaxAskAnswerRunes characters of any script.
	if !httpx.DecodeJSONLimit(c, &body, maxAskBody) {
		return
	}
	projectKey := a.projectKey(c.Request)
	ask, err := a.hub.AnswerWith(c.Request.Context(), projectKey, c.Param("id"), agents.AnswerInput{
		Decision: body.Decision, Note: body.Note, Answer: body.Answer,
	}, humanViewer(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok":  true,
		"ask": ask,
	})
}
