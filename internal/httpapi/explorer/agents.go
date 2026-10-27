package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/agents"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/runnerwire"
	"nodevas/internal/runs"
	"nodevas/internal/usage"
)

// Limits of the Agent tab.
const (
	maxRecentRuns  = 20
	maxActionsList = 1000
)

// Tokens are an agent's token counts.
type Tokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	CacheRead int64 `json:"cacheRead"`
}

// VerdictSummary is a run's structured verdict in brief.
type VerdictSummary struct {
	Outcome  string   `json:"outcome"`
	Severity string   `json:"severity"`
	Reasons  []string `json:"reasons"`
}

// FileMark is one file an agent touched.
type FileMark struct {
	Path string `json:"path"`
	Mark string `json:"mark"`
}

// AgentItem is one run or external session in the Agent tab. Role is only
// ever a run's recorded role: a session's own claim is not a role.
type AgentItem struct {
	Kind          string          `json:"kind"`
	ID            string          `json:"id"`
	RunID         string          `json:"runId,omitempty"`
	SessionID     string          `json:"sessionId,omitempty"`
	Agent         string          `json:"agent"`
	Model         string          `json:"model"`
	Role          string          `json:"role,omitempty"`
	State         string          `json:"state"`
	NodeID        string          `json:"nodeId"`
	NodeTitle     string          `json:"nodeTitle"`
	Tokens        Tokens          `json:"tokens"`
	CostUSD       float64         `json:"costUsd"`
	CostEstimated bool            `json:"costEstimated"`
	Verdict       *VerdictSummary `json:"verdict,omitempty"`
	StartedAt     string          `json:"startedAt"`
	FinishedAt    string          `json:"finishedAt"`
	RunnerName    string          `json:"runnerName,omitempty"`
	TestState     string          `json:"testState,omitempty"`
	Files         []FileMark      `json:"files"`
}

// agentsList is GET /api/explorer/agents: the project's live and recent
// runs the viewer may see, and its external sessions.
func (a *API) agentsList(c *gin.Context) {
	actor, ok := person(c)
	if !ok {
		return
	}
	st, name, ok := a.project(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	out := struct {
		Live     []AgentItem `json:"live"`
		External []AgentItem `json:"external"`
		Recent   []AgentItem `json:"recent"`
	}{Live: []AgentItem{}, External: []AgentItem{}, Recent: []AgentItem{}}

	titles := map[string]string{}
	if g, _, err := st.LoadGraph(); err == nil && g != nil {
		for _, n := range g.Nodes {
			titles[n.ID] = n.Title
		}
	}
	if a.o.Runs != nil {
		viewer := actor.ID
		if actor.IsAdmin() {
			viewer = ""
		}
		live, err := a.o.Runs.List(ctx, runs.Filter{Project: name, Active: true, Viewer: viewer})
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
		recentAll, err := a.o.Runs.List(ctx, runs.Filter{Project: name, Viewer: viewer, Limit: 200})
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
		var recent []runs.Record
		for _, r := range recentAll {
			if runnerwire.RunFinal(r.Status) && r.Kind != runnerwire.JobVerify {
				recent = append(recent, r)
				if len(recent) == maxRecentRuns {
					break
				}
			}
		}
		var all []runs.Record
		for _, r := range live {
			if r.Kind != runnerwire.JobVerify {
				all = append(all, r)
			}
		}
		liveCount := len(all)
		all = append(all, recent...)
		items, err := a.runItems(ctx, all)
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
		out.Live = append(out.Live, items[:liveCount]...)
		out.Recent = append(out.Recent, items[liveCount:]...)
	}
	if a.o.Agents != nil {
		key := st.Root()
		var prices usage.Table
		if a.o.Runs != nil {
			prices = a.o.Runs.PriceTable(ctx)
		} else {
			prices = usage.NewTable(nil)
		}
		for _, s := range a.o.Agents.ListSessions(key, "", false, nil) {
			if s.Run {
				continue
			}
			item := AgentItem{Kind: "session", ID: s.ID, SessionID: s.ID, Agent: s.Agent, Model: s.Model,
				State: "offline", NodeID: s.NodeID, NodeTitle: titles[s.NodeID], Files: []FileMark{},
				StartedAt: millis(s.Since)}
			if s.Online {
				item.State = "online"
			}
			a.sessionUsage(ctx, name, s.ID, prices, &item)
			item.Files = a.sessionFiles(ctx, name, s.ID)
			out.External = append(out.External, item)
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func millis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// runItems turns runs into items with their verdicts, files and test state.
func (a *API) runItems(ctx context.Context, list []runs.Record) ([]AgentItem, error) {
	ids := make([]string, 0, len(list))
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	verdicts, err := a.o.Runs.VerdictsForRuns(ctx, ids)
	if err != nil {
		return nil, err
	}
	files, err := a.runFiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	tests := map[string]string{}
	out := make([]AgentItem, 0, len(list))
	for _, r := range list {
		item := AgentItem{Kind: "run", ID: r.ID, RunID: r.ID, Agent: r.Agent, Model: r.Model, Role: r.Role,
			State: r.Status, NodeID: r.NodeID, NodeTitle: r.NodeTitle, CostUSD: r.CostUSD, CostEstimated: r.CostEstimated,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, RunnerName: r.RunnerName, Files: files[r.ID]}
		if item.Files == nil {
			item.Files = []FileMark{}
		}
		if r.Usage != nil {
			item.Tokens = Tokens{Input: r.Usage.InputTokens, Output: r.Usage.OutputTokens, CacheRead: r.Usage.CacheReadTokens}
		}
		if v, ok := verdicts[r.ID]; ok {
			item.Verdict = &VerdictSummary{Outcome: v.Outcome, Severity: v.Severity, Reasons: v.Reasons}
		}
		if r.PipelineID != "" && testsRole(r) {
			state, seen := tests[r.PipelineID]
			if !seen {
				state = a.pipelineTestState(ctx, r.PipelineID)
				tests[r.PipelineID] = state
			}
			item.TestState = state
		}
		out = append(out, item)
	}
	return out, nil
}

// testsRole: the run's work is measured by the tests (Executor, Tester).
func testsRole(r runs.Record) bool {
	switch r.Role {
	case engine.RoleExecutor, engine.RoleTester:
		return true
	case "":
		return r.Kind == runnerwire.JobImplement || r.Kind == runnerwire.JobRevise || r.Kind == runnerwire.JobTest
	}
	return false
}

// pipelineTestState is the pipeline's latest verify: red, green, or "".
func (a *API) pipelineTestState(ctx context.Context, pipelineID string) string {
	var kind, outcome string
	err := a.o.Runs.DB().QueryRowContext(ctx, `
SELECT kind, outcome FROM run_evidence WHERE pipeline_id = ? AND kind IN (?, ?) AND outcome IN ('pass', 'fail')
 ORDER BY at DESC, id DESC LIMIT 1`, pipelineID, runnerwire.PhaseVerifyRed, runnerwire.PhaseVerifyGreen).Scan(&kind, &outcome)
	if err != nil {
		return ""
	}
	if kind == runnerwire.PhaseVerifyGreen && outcome == "pass" {
		return "green"
	}
	return "red"
}

// runFiles are each run's changed files from run_changes.
func (a *API) runFiles(ctx context.Context, ids []string) (map[string][]FileMark, error) {
	out := map[string][]FileMark{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := a.o.Runs.DB().QueryContext(ctx, `SELECT run_id, files FROM run_changes WHERE run_id IN (`+
		strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var files []runnerwire.FileChange
		if json.Unmarshal([]byte(raw), &files) != nil {
			continue
		}
		marks := make([]FileMark, 0, len(files))
		for _, f := range files {
			marks = append(marks, FileMark{Path: f.Path, Mark: fileMark(f.Status)})
		}
		out[id] = marks
	}
	return out, rows.Err()
}

func fileMark(status string) string {
	switch status {
	case runnerwire.FileAdded:
		return "A"
	case runnerwire.FileDeleted:
		return "D"
	case runnerwire.FileRenamed:
		return "R"
	}
	return "M"
}

// sessionUsage fills a session's tokens and cost from its reports.
func (a *API) sessionUsage(ctx context.Context, project, sessionID string, prices usage.Table, item *AgentItem) {
	if a.o.DB == nil {
		return
	}
	rows, err := a.o.DB.QueryContext(ctx, `
SELECT model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reported_cost_usd
  FROM agent_session_usage WHERE project = ? AND session_id = ?`, project, sessionID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var t usage.Tokens
		var reported float64
		if rows.Scan(&model, &t.Input, &t.Output, &t.CacheRead, &t.CacheWrite, &reported) != nil {
			return
		}
		item.Tokens.Input += t.Input
		item.Tokens.Output += t.Output
		item.Tokens.CacheRead += t.CacheRead
		switch p, ok := prices.Lookup(model); {
		case reported > 0:
			item.CostUSD += reported
		case ok:
			item.CostUSD += p.Cost(t)
			item.CostEstimated = true
		}
		if item.Model == "" {
			item.Model = model
		}
	}
}

// sessionFiles are the files a session reported changing, newest node
// first.
func (a *API) sessionFiles(ctx context.Context, project, sessionID string) []FileMark {
	out := []FileMark{}
	if a.o.DB == nil {
		return out
	}
	rows, err := a.o.DB.QueryContext(ctx, `
SELECT files FROM session_changes WHERE project = ? AND session_id = ? ORDER BY updated_at DESC`, project, sessionID)
	if err != nil {
		return out
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			return out
		}
		var files []string
		if json.Unmarshal([]byte(raw), &files) != nil {
			continue
		}
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				out = append(out, FileMark{Path: f, Mark: "M"})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// sessionActions is GET /api/explorer/sessions/:id/actions: an external
// session's action log, oldest first.
func (a *API) sessionActions(c *gin.Context) {
	if _, ok := person(c); !ok {
		return
	}
	st, _, ok := a.project(c)
	if !ok {
		return
	}
	limit := 200
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxActionsList {
			httpx.Coded(c, http.StatusBadRequest, "INVALID_ARGUMENT", errors.New("limit is between 1 and 1000"), nil)
			return
		}
		limit = n
	}
	actions := []agents.Action{}
	if a.o.Agents != nil {
		list, err := a.o.Agents.Actions(c.Request.Context(), st.Root(), c.Param("id"), limit)
		if err != nil {
			httpx.Err(c, http.StatusInternalServerError, err)
			return
		}
		if list != nil {
			actions = list
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"actions": actions})
}
