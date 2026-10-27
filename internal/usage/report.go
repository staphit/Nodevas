package usage

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// Report window limits, in days.
const (
	DefaultDays = 30
	MaxDays     = 365
	// MaxSessionsListed caps Report.Sessions.
	MaxSessionsListed = 200
)

// Sources of a Report.Sessions entry.
const (
	SourceSession = "session"
	SourceRun     = "run"
	// SourceCopilot is a copilot conversation's model use.
	SourceCopilot = "copilot"
)

// Bucket is usage summed over some slice of a report.
type Bucket struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	// CostUSD is ReportedCostUSD plus EstimatedCostUSD: what the agents said
	// they cost where they said, an estimate from the price table elsewhere.
	CostUSD          float64 `json:"costUsd"`
	ReportedCostUSD  float64 `json:"reportedCostUsd"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
	// UnpricedTokens are tokens of models with no price and no reported
	// cost: counted, but in no cost.
	UnpricedTokens int64 `json:"unpricedTokens"`
	Runs           int   `json:"runs"`
	Sessions       int   `json:"sessions"`
}

// KeyBucket is a Bucket for one agent, model or day.
type KeyBucket struct {
	Key string `json:"key"`
	// Agent is the agent kind of a byModel entry.
	Agent string `json:"agent,omitempty"`
	Bucket
}

// SessionEntry is one agent session's or run's usage.
type SessionEntry struct {
	ID        string   `json:"id"`
	Source    string   `json:"source"`
	Name      string   `json:"name"`
	ActorName string   `json:"actorName"`
	Agent     string   `json:"agent"`
	Model     string   `json:"model"`
	Models    []string `json:"models"`
	NodeID    string   `json:"nodeId"`
	RunID     string   `json:"runId"`
	FirstAt   string   `json:"firstAt"`
	LastAt    string   `json:"lastAt"`
	Bucket
}

// Report is GET /api/usage.
type Report struct {
	Days       int         `json:"days"`
	Since      string      `json:"since"`
	Currency   string      `json:"currency"`
	PricesAsOf string      `json:"pricesAsOf"`
	Totals     Bucket      `json:"totals"`
	ByAgent    []KeyBucket `json:"byAgent"`
	ByModel    []KeyBucket `json:"byModel"`
	ByDay      []KeyBucket `json:"byDay"`
	// ByRole is usage by the run's role (legacy runs by their kind;
	// "session" and "copilot" for those); ByNode the top nodes by cost
	// across all their runs.
	ByRole   []KeyBucket    `json:"byRole"`
	ByNode   []KeyBucket    `json:"byNode"`
	Sessions []SessionEntry `json:"sessions"`
}

// Query is what a report covers.
type Query struct {
	// Project is the project's name as ?project= takes it.
	Project string
	// Viewer, when set, keeps the runs that person dispatched or that ran
	// on their runners (as the runs list does); "" keeps every run. Agent
	// sessions are the project's, as the roster shows them.
	Viewer string
	// Days is how many UTC days back the report reaches, today included.
	Days int
}

// item is one usage figure: a run, or one model of a session.
type item struct {
	source, id, agent, model, day string
	role, node                    string
	tokens                        Tokens
	reported                      float64
	// settled marks a run whose cost is already worked out: cost, an
	// estimate when estimated, and no cost at all when neither is set.
	settled   bool
	cost      float64
	estimated bool
}

// RoleOf is a run's role for reports: its recorded role, or for a legacy
// run (role "") the role its kind stands for.
func RoleOf(role, kind string) string {
	if role != "" {
		return role
	}
	switch kind {
	case "", "implement", "revise":
		return "executor"
	case "review":
		return "reviewer"
	case "plan":
		return "planner"
	case "test":
		return "tester"
	case "judge":
		return "qa"
	}
	return kind
}

// MaxNodesListed caps Report.ByNode.
const MaxNodesListed = 50

// Report sums usage over q's window: runs by the day they were created,
// sessions by the day each model's totals were last reported. A session's
// totals are cumulative, so a session that began before the window and
// reported inside it counts whole, on the day of its last report.
func (s *Store) Report(ctx context.Context, q Query) (Report, error) {
	days := q.Days
	if days <= 0 {
		days = DefaultDays
	}
	days = min(days, MaxDays)
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(days - 1))
	out := Report{Days: days, Since: since.Format(time.RFC3339), Currency: "USD", PricesAsOf: PricesAsOf}

	table, err := s.Table(ctx)
	if err != nil {
		return out, err
	}
	sinceStamp := since.Format(stampLayout)

	var items []item
	entries := map[string]*SessionEntry{}
	var order []string

	runItems, runEntries, err := s.runUsage(ctx, q, sinceStamp, table)
	if err != nil {
		return out, err
	}
	items = append(items, runItems...)
	for _, e := range runEntries {
		key := SourceRun + "\x00" + e.ID
		entries[key] = e
		order = append(order, key)
	}
	sessionItems, sessionEntries, sessionOrder, err := s.sessionUsage(ctx, q, sinceStamp)
	if err != nil {
		return out, err
	}
	items = append(items, sessionItems...)
	for _, key := range sessionOrder {
		entries[key] = sessionEntries[key]
		order = append(order, key)
	}
	copilotItems, err := s.copilotUsage(ctx, q, sinceStamp)
	if err != nil {
		return out, err
	}
	items = append(items, copilotItems...)

	agg := newAggregate(table)
	byDay := map[string]*bucketAcc{}
	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		byDay[d.Format("2006-01-02")] = &bucketAcc{}
	}
	for _, it := range items {
		agg.add(&agg.totals, it)
		agg.add(agg.bucket(agg.byAgent, it.agent), it)
		agg.add(agg.bucket(agg.byModel, it.agent+"\x00"+it.model), it)
		agg.add(agg.bucket(agg.byRole, it.role), it)
		if it.source == SourceRun && it.node != "" {
			agg.add(agg.bucket(agg.byNode, it.node), it)
		}
		if day, ok := byDay[it.day]; ok {
			agg.add(day, it)
		}
		e := entries[it.source+"\x00"+it.id]
		if e != nil {
			var acc bucketAcc
			acc.b = e.Bucket
			agg.add(&acc, it)
			e.Bucket = acc.b
		}
	}

	out.Totals = agg.totals.result()
	out.ByAgent = sortedBuckets(agg.byAgent, func(key string) KeyBucket { return KeyBucket{Key: key} })
	out.ByModel = sortedBuckets(agg.byModel, func(key string) KeyBucket {
		agent, model, _ := strings.Cut(key, "\x00")
		return KeyBucket{Key: model, Agent: agent}
	})
	out.ByRole = sortedBuckets(agg.byRole, func(key string) KeyBucket { return KeyBucket{Key: key} })
	out.ByNode = sortedBuckets(agg.byNode, func(key string) KeyBucket { return KeyBucket{Key: key} })
	if len(out.ByNode) > MaxNodesListed {
		out.ByNode = out.ByNode[:MaxNodesListed]
	}
	out.ByDay = make([]KeyBucket, 0, len(byDay))
	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		out.ByDay = append(out.ByDay, KeyBucket{Key: key, Bucket: byDay[key].result()})
	}

	out.Sessions = make([]SessionEntry, 0, len(order))
	for _, key := range order {
		e := entries[key]
		if e.Source == SourceRun {
			e.Runs = 1
		} else {
			e.Sessions = 1
		}
		e.CostUSD = round(e.ReportedCostUSD + e.EstimatedCostUSD)
		e.ReportedCostUSD, e.EstimatedCostUSD = round(e.ReportedCostUSD), round(e.EstimatedCostUSD)
		out.Sessions = append(out.Sessions, *e)
	}
	sort.SliceStable(out.Sessions, func(i, j int) bool {
		if out.Sessions[i].LastAt != out.Sessions[j].LastAt {
			return out.Sessions[i].LastAt > out.Sessions[j].LastAt
		}
		return out.Sessions[i].ID < out.Sessions[j].ID
	})
	if len(out.Sessions) > MaxSessionsListed {
		out.Sessions = out.Sessions[:MaxSessionsListed]
	}
	return out, nil
}

// runUsage reads the window's runs that reported usage.
func (s *Store) runUsage(ctx context.Context, q Query, since string, table Table) ([]item, []*SessionEntry, error) {
	query := `
SELECT id, node_id, node_title, agent, model, usage, created_by_name, created_at, started_at, finished_at,
       role, kind, cost_usd, cost_estimated, cost_settled
  FROM runs WHERE project = ? AND created_at >= ? AND usage != ''`
	args := []any{q.Project, since}
	if q.Viewer != "" {
		query += ` AND (created_by = ? OR owner_id = ?)`
		args = append(args, q.Viewer, q.Viewer)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var items []item
	var entries []*SessionEntry
	for rows.Next() {
		var id, nodeID, title, agent, model, raw, by, created, started, finished, role, kind string
		var cost float64
		var estimated, settled bool
		if err := rows.Scan(&id, &nodeID, &title, &agent, &model, &raw, &by, &created, &started, &finished,
			&role, &kind, &cost, &estimated, &settled); err != nil {
			return nil, nil, err
		}
		var u runnerwire.Usage
		if json.Unmarshal([]byte(raw), &u) != nil {
			continue
		}
		resolved := normalizeModel(u.Model)
		if resolved == "" {
			resolved = normalizeModel(model)
		}
		if resolved == "" || resolved == "default" {
			resolved = UnknownModel
		}
		reported := u.CostUSD
		if math.IsNaN(reported) || math.IsInf(reported, 0) || reported < 0 {
			reported = 0
		}
		tokens := Tokens{Input: max(u.InputTokens, 0), Output: max(u.OutputTokens, 0),
			CacheRead: max(u.CacheReadTokens, 0), CacheWrite: max(u.CacheWriteTokens, 0)}
		agent = strings.ToLower(agent)
		if !settled {
			cost, estimated, _ = RunCost(table, agent, model, u)
		}
		if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			cost = 0
		}
		items = append(items, item{source: SourceRun, id: id, agent: agent, model: resolved,
			day: dayOf(created), tokens: tokens, reported: reported, role: RoleOf(role, kind), node: nodeID,
			settled: true, cost: cost, estimated: estimated})
		last := created
		for _, t := range []string{started, finished} {
			if t > last {
				last = t
			}
		}
		name := title
		if name == "" {
			name = "run " + shortID(id)
		}
		entries = append(entries, &SessionEntry{ID: id, Source: SourceRun, Name: name, ActorName: by,
			Agent: agent, Model: resolved, Models: []string{resolved}, NodeID: nodeID, RunID: id,
			FirstAt: created, LastAt: last})
	}
	return items, entries, rows.Err()
}

// sessionUsage reads the window's agent session reports.
func (s *Store) sessionUsage(ctx context.Context, q Query, since string) ([]item, map[string]*SessionEntry, []string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT session_id, model, actor_name, session_name, agent, input_tokens, output_tokens,
       cache_read_tokens, cache_write_tokens, reported_cost_usd, first_at, updated_at
  FROM agent_session_usage WHERE project = ? AND updated_at >= ? AND run_id = ''
 ORDER BY session_id, model`, q.Project, since)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var items []item
	entries := map[string]*SessionEntry{}
	var order []string
	modelTokens := map[string]map[string]int64{}
	for rows.Next() {
		var id, model, actor, name, agent, first, updated string
		var t Tokens
		var reported float64
		if err := rows.Scan(&id, &model, &actor, &name, &agent, &t.Input, &t.Output, &t.CacheRead, &t.CacheWrite,
			&reported, &first, &updated); err != nil {
			return nil, nil, nil, err
		}
		items = append(items, item{source: SourceSession, id: id, agent: agent, model: model,
			day: dayOf(updated), tokens: t, reported: reported, role: SourceSession})
		key := SourceSession + "\x00" + id
		e := entries[key]
		if e == nil {
			if name == "" {
				name = id
			}
			e = &SessionEntry{ID: id, Source: SourceSession, Name: name, ActorName: actor, Agent: agent,
				FirstAt: first, LastAt: updated}
			entries[key] = e
			order = append(order, key)
			modelTokens[key] = map[string]int64{}
		}
		if first < e.FirstAt {
			e.FirstAt = first
		}
		if updated > e.LastAt {
			// The newest report names the session as it is now.
			e.LastAt = updated
			if name != "" {
				e.Name = name
			}
			if actor != "" {
				e.ActorName = actor
			}
		}
		modelTokens[key][model] += t.Total()
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	for key, e := range entries {
		byTokens := modelTokens[key]
		models := make([]string, 0, len(byTokens))
		for m := range byTokens {
			models = append(models, m)
		}
		sort.Slice(models, func(i, j int) bool {
			if byTokens[models[i]] != byTokens[models[j]] {
				return byTokens[models[i]] > byTokens[models[j]]
			}
			return models[i] < models[j]
		})
		e.Models = models
		if len(models) > 0 {
			e.Model = models[0]
		}
	}
	return items, entries, order, nil
}

// bucketAcc sums a Bucket, counting each run and session once.
type bucketAcc struct {
	b    Bucket
	seen map[string]bool
}

func (a *bucketAcc) result() Bucket {
	out := a.b
	out.CostUSD = round(out.ReportedCostUSD + out.EstimatedCostUSD)
	out.ReportedCostUSD, out.EstimatedCostUSD = round(out.ReportedCostUSD), round(out.EstimatedCostUSD)
	return out
}

type aggregate struct {
	table   Table
	prices  map[string]*Price
	totals  bucketAcc
	byAgent map[string]*bucketAcc
	byModel map[string]*bucketAcc
	byRole  map[string]*bucketAcc
	byNode  map[string]*bucketAcc
}

func newAggregate(table Table) *aggregate {
	return &aggregate{table: table, prices: map[string]*Price{},
		byAgent: map[string]*bucketAcc{}, byModel: map[string]*bucketAcc{},
		byRole: map[string]*bucketAcc{}, byNode: map[string]*bucketAcc{}}
}

func (g *aggregate) bucket(m map[string]*bucketAcc, key string) *bucketAcc {
	b := m[key]
	if b == nil {
		b = &bucketAcc{}
		m[key] = b
	}
	return b
}

// price is model's price, looked up once per report.
func (g *aggregate) price(model string) *Price {
	if p, ok := g.prices[model]; ok {
		return p
	}
	var found *Price
	if p, ok := g.table.Lookup(model); ok {
		found = &p
	}
	g.prices[model] = found
	return found
}

func (g *aggregate) add(acc *bucketAcc, it item) {
	b := &acc.b
	b.InputTokens += it.tokens.Input
	b.OutputTokens += it.tokens.Output
	b.CacheReadTokens += it.tokens.CacheRead
	b.CacheWriteTokens += it.tokens.CacheWrite
	b.TotalTokens += it.tokens.Total()
	switch p := g.price(it.model); {
	case it.settled && it.cost > 0 && it.reported > 0 && !it.estimated:
		b.ReportedCostUSD += it.cost
	case it.settled && it.cost > 0:
		// Priced from its tokens: what the report calls an estimate.
		b.EstimatedCostUSD += it.cost
	case it.settled:
		if it.reported <= 0 {
			b.UnpricedTokens += it.tokens.Total()
		}
	case it.reported > 0:
		b.ReportedCostUSD += it.reported
	case p != nil:
		b.EstimatedCostUSD += p.Cost(it.tokens)
	default:
		b.UnpricedTokens += it.tokens.Total()
	}
	if acc.seen == nil {
		acc.seen = map[string]bool{}
	}
	if key := it.source + "\x00" + it.id; !acc.seen[key] {
		acc.seen[key] = true
		if it.source == SourceRun {
			b.Runs++
		} else {
			b.Sessions++
		}
	}
}

// sortedBuckets lists m by cost, then tokens, then key.
func sortedBuckets(m map[string]*bucketAcc, entry func(key string) KeyBucket) []KeyBucket {
	out := make([]KeyBucket, 0, len(m))
	for key, acc := range m {
		e := entry(key)
		e.Bucket = acc.result()
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CostUSD != b.CostUSD {
			return a.CostUSD > b.CostUSD
		}
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Agent < b.Agent
	})
	return out
}

// dayOf is a stored stamp's UTC day.
func dayOf(stamp string) string {
	if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	if len(stamp) >= 10 {
		return stamp[:10]
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// round keeps costs to a millionth of a dollar, so sums print cleanly.
func round(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// copilotUsage reads the window's copilot turns: the project's, or with a
// viewer, that person's.
func (s *Store) copilotUsage(ctx context.Context, q Query, since string) ([]item, error) {
	query := `
SELECT conversation_id, model, input_tokens, output_tokens, created_at
  FROM copilot_usage WHERE project = ? AND created_at >= ?`
	args := []any{q.Project, since}
	if q.Viewer != "" {
		query += ` AND account_id = ?`
		args = append(args, q.Viewer)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []item
	for rows.Next() {
		var conversation, model, created string
		var t Tokens
		if err := rows.Scan(&conversation, &model, &t.Input, &t.Output, &created); err != nil {
			return nil, err
		}
		model = normalizeModel(model)
		if model == "" {
			model = UnknownModel
		}
		items = append(items, item{source: SourceCopilot, id: conversation, agent: SourceCopilot, model: model,
			day: dayOf(created), tokens: t, role: SourceCopilot})
	}
	return items, rows.Err()
}
