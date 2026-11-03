package agents

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/locale"
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

// UseRunUsage records an interactive run's usage from its agent session's
// reports (runs.Store.SetUsageFromSession).
func (a *API) UseRunUsage(record func(ctx context.Context, runID string, models []usage.ModelUsage) error) {
	a.runUsage = record
}

func (a *API) postSessionUsage(c *gin.Context) {
	if a.usage == nil {
		httpx.Coded(c, http.StatusServiceUnavailable, "UNAVAILABLE", locale.Errorf("usage is not recorded on this server", "這個伺服器沒有記錄用量"), nil)
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
	invalid := func(english, chinese string) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", locale.Errorf(english, chinese), nil)
	}
	if sessionID == "" || len(sessionID) > 256 {
		invalid("session id is missing or too long", "工作階段代號缺少或太長")
		return
	}
	name := a.projectName(c.Request)
	if name == "" {
		invalid("no project selected", "沒有選擇專案")
		return
	}
	projectKey := a.projectKey(c.Request)
	actorID, _ := resolveActor(c.Request, body.Actor)
	session, found := a.hub.SessionByID(projectKey, sessionID)
	if !found {
		httpx.Coded(c, http.StatusNotFound, "NOT_FOUND", locale.Errorf("no such session; heartbeat first", "沒有這個工作階段；請先送出心跳"), nil)
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
		invalid("agent is too long", "agent 太長")
		return
	}
	err := a.usage.RecordSession(c.Request.Context(), usage.SessionReport{
		Project: name, SessionID: session.ID, SessionName: session.Name,
		ActorID: session.ActorID, ActorName: session.ActorName, Agent: agent, Models: body.Models,
		RunID: session.RunID,
	})
	if errors.Is(err, usage.ErrInvalid) {
		httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", err, nil)
		return
	}
	if err != nil {
		httpx.Err(c, http.StatusInternalServerError, err)
		return
	}
	// An interactive run's session: its tokens are the run's.
	if session.RunID != "" && a.runUsage != nil {
		if models, err := a.usage.SessionModels(c.Request.Context(), name, session.ID); err == nil {
			if err := a.runUsage(c.Request.Context(), session.RunID, models); err != nil {
				slog.Warn("agents: run usage from its session", slog.String("run", session.RunID), slog.Any("error", err))
			}
		}
	}
	var total int64
	for _, m := range body.Models {
		total += m.InputTokens + m.OutputTokens + m.CacheReadTokens + m.CacheWriteTokens
	}
	a.hub.LogAction(projectKey, session.ID, session.RunID, agents.ActionUsage, "", strconv.FormatInt(total, 10))
	if a.usageNotify != nil {
		a.usageNotify(projectKey)
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}
