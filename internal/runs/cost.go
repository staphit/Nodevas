package runs

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
	"nodevas/internal/usage"
)

// Run cost (agent analytics): runs.cost_usd is settled from a run's usage
// when it reports one, priced by usage.RunCost, so budgets and analytics sum
// one column instead of re-parsing every usage figure.

// priceTableTTL is how long the price table is cached.
const priceTableTTL = time.Minute

// backfillBatch is how many legacy rows BackfillCosts settles per query.
const backfillBatch = 500

// priceTable is the owner's custom prices over the built-in ones, cached
// for a minute. A read failure falls back to the built-in table.
func (s *Store) priceTable(ctx context.Context) usage.Table {
	s.priceMu.Lock()
	defer s.priceMu.Unlock()
	now := time.Now()
	if s.pricesRead && now.Sub(s.pricesAt) < priceTableTTL {
		return s.prices
	}
	table := usage.NewTable(nil)
	if s.usage != nil {
		if t, err := s.usage.Table(ctx); err == nil {
			table = t
		}
	}
	s.prices, s.pricesAt, s.pricesRead = table, now, true
	return table
}

// runCost is what usage u of a run of agent on model costs.
func (s *Store) runCost(ctx context.Context, agent, model string, u runnerwire.Usage) (float64, bool) {
	return runCostAt(s.priceTable(ctx), agent, model, u)
}

func runCostAt(table usage.Table, agent, model string, u runnerwire.Usage) (float64, bool) {
	cost, estimated, _ := usage.RunCost(table, strings.ToLower(agent), model, u)
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return 0, false
	}
	return cost, estimated
}

// BackfillCosts settles the cost of runs recorded before cost_usd existed
// (cost_settled false, usage set), in batches, and reports how many.
func (s *Store) BackfillCosts(ctx context.Context) (int, error) {
	table := s.priceTable(ctx)
	total := 0
	for {
		rows, err := s.db.QueryContext(ctx, `
SELECT id, agent, model, usage FROM runs
 WHERE cost_settled = false AND usage <> '' ORDER BY id LIMIT ?`, backfillBatch)
		if err != nil {
			return total, err
		}
		type pending struct{ id, agent, model, raw string }
		var batch []pending
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.id, &p.agent, &p.model, &p.raw); err != nil {
				rows.Close()
				return total, err
			}
			batch = append(batch, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return total, err
		}
		for _, p := range batch {
			var u runnerwire.Usage
			cost, estimated := 0.0, false
			if json.Unmarshal([]byte(p.raw), &u) == nil {
				cost, estimated = runCostAt(table, p.agent, p.model, u)
			}
			if _, err := s.db.ExecContext(ctx, `
UPDATE runs SET cost_usd = ?, cost_estimated = ?, cost_settled = true WHERE id = ? AND cost_settled = false`,
				cost, estimated, p.id); err != nil {
				return total, err
			}
			total++
		}
		if len(batch) < backfillBatch {
			return total, nil
		}
	}
}

// sessionUsageMarker is set on a run's usage recorded from its session's
// report, so a later report may replace it while a runner's own figure is
// never overwritten.
const sessionUsageMarker = "session"

// SetUsageFromSession records an interactive run's usage from its agent
// session's report: the models summed (model = the one with the most
// tokens) and the cost settled. Only an interactive run whose usage is
// empty or came from an earlier session report is changed.
func (s *Store) SetUsageFromSession(ctx context.Context, runID string, models []usage.ModelUsage) error {
	if runID == "" || len(models) == 0 {
		return nil
	}
	r, err := s.Get(ctx, runID)
	if err != nil {
		return err
	}
	if r.Mode != runnerwire.ModeInteractive {
		return nil
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT usage FROM runs WHERE id = ?`, runID).Scan(&raw); err != nil {
		return err
	}
	if raw != "" {
		var prior struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(raw), &prior) != nil || prior.Source != sessionUsageMarker {
			return nil
		}
	}
	table := s.priceTable(ctx)
	var sum runnerwire.Usage
	var cost float64
	estimated := false
	var best int64 = -1
	for _, m := range models {
		u := runnerwire.Usage{InputTokens: max(m.InputTokens, 0), OutputTokens: max(m.OutputTokens, 0),
			CacheReadTokens: max(m.CacheReadTokens, 0), CacheWriteTokens: max(m.CacheWriteTokens, 0),
			CostUSD: m.CostUSD, Model: m.Model}
		sum.InputTokens += u.InputTokens
		sum.OutputTokens += u.OutputTokens
		sum.CacheReadTokens += u.CacheReadTokens
		sum.CacheWriteTokens += u.CacheWriteTokens
		if u.CostUSD > 0 && !math.IsInf(u.CostUSD, 0) {
			sum.CostUSD += u.CostUSD
		}
		c, est := runCostAt(table, r.Agent, r.Model, u)
		cost += c
		estimated = estimated || est
		if total := u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens; total > best {
			best, sum.Model = total, m.Model
		}
	}
	encoded, err := json.Marshal(struct {
		runnerwire.Usage
		Source string `json:"source"`
	}{sum, sessionUsageMarker})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE runs SET usage = ?, cost_usd = ?, cost_estimated = ?, cost_settled = true WHERE id = ? AND mode = ?`,
		string(encoded), cost, estimated, runID, runnerwire.ModeInteractive)
	return err
}

// PriceTable is the price table run costs are settled with (cached for a
// minute): auto routing prices a combination it has no history for with it.
func (s *Store) PriceTable(ctx context.Context) usage.Table { return s.priceTable(ctx) }

// DB is the workspace database the store keeps runs in, for the read-only
// analytics over them (internal/analytics).
func (s *Store) DB() *db.DB { return s.db }
