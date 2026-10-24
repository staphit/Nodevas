package agents

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/usage"
)

// Session token usage:
//
//	POST /api/agents/sessions/:id/usage {agent?, models: [usage.ModelUsage], actor?} → {ok}
//
// An external session's hook reports its running totals per model; each
// report replaces the last for those models (see usage.Store.RecordSession).

// maxUsageBody fits usage.MaxModelsPerReport entries with room to spare.
const maxUsageBody = 32 << 10

// UseUsage enables the usage route: reports go to store, and notify (if set)
// hears the project key a report was recorded under, for realtime.
func (a *API) UseUsage(store *usage.Store, notify func(projectKey string)) {
	a.usage, a.usageNotify = store, notify
}

func (a *API) postSessionUsage(c *gin.Context) {
	if a.usage == nil {
		httpx.Coded(c, http.StatusServiceUnavailable, "UNAVAILABLE", errors.New("usage is not recorded on this server"), nil)
		return
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	var body struct {
		Actor  string             `json:"actor"`
		Agent  string             `json:"agent"`
		Models []usage.ModelUsage `json:"models"`
	}
	if !httpx.DecodeJSONLimit(c, &body, maxUsageBody) {
		return
	}
	invalid := func(msg string) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New(msg), nil)
	}
	if sessionID == "" || len(sessionID) > 256 {
		invalid("session id is missing or too long")
		return
	}
	name := a.projectName(c.Request)
	if name == "" {
		invalid("no project selected")
		return
	}
	projectKey := a.projectKey(c.Request)
	actorID, _ := resolveActor(c.Request, body.Actor)
	session, found := a.hub.SessionByID(projectKey, sessionID)
	if !found {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", errors.New("no such session; heartbeat first"), nil)
		return
	}
	// A session ID is bound to the actor that registered it, as in Touch.
	if !strings.EqualFold(session.ActorID, actorID) {
		fail(c, agents.ErrRefused)
		return
	}
	agent := session.Agent
	if agent == "" {
		agent = strings.TrimSpace(body.Agent)
	}
	if len(agent) > 64 {
		invalid("agent is too long")
		return
	}
	err := a.usage.RecordSession(c.Request.Context(), usage.SessionReport{
		Project: name, SessionID: session.ID, SessionName: session.Name,
		ActorID: session.ActorID, ActorName: session.ActorName, Agent: agent, Models: body.Models,
	})
	if errors.Is(err, usage.ErrInvalid) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
		return
	}
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	if a.usageNotify != nil {
		a.usageNotify(projectKey)
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}
