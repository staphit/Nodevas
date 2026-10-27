package usage

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"sync"
	"time"

	"nodevas/internal/db"
)

// Limits on one session report.
const (
	MaxModelsPerReport = 32
	// MaxTokens bounds one token count: far past any session, short of
	// overflowing a sum of many.
	MaxTokens = int64(1e13)
	// MaxCostUSD bounds one reported cost.
	MaxCostUSD = 1e7
)

// ChangedEvent is the realtime event a session report sends its project.
const ChangedEvent = "usage-changed"

// UnknownModel stands for a model the agent did not name.
const UnknownModel = "unknown"

// stampLayout is fixed width, as on runs, so stored stamps sort as text.
const stampLayout = "2006-01-02T15:04:05.000000Z07:00"

// ModelUsage is one model's running totals in a session, as its hook
// reports them.
type ModelUsage struct {
	Model            string  `json:"model"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd"`
}

// SessionReport is one report from a session's hook.
type SessionReport struct {
	// Project is the project's name as ?project= takes it.
	Project     string
	SessionID   string
	SessionName string
	ActorID     string
	ActorName   string
	Agent       string
	Models      []ModelUsage
	// RunID ties the session to the interactive run it is the agent of, so
	// the report counts its tokens on the run, not twice.
	RunID string
}

// Store keeps session usage and custom prices in the workspace database.
type Store struct {
	db  *db.DB
	now func() time.Time

	mu   sync.Mutex
	last time.Time
}

// NewStore is a Store on database.
func NewStore(database *db.DB) *Store {
	return &Store{db: database, now: time.Now}
}

// SetClock replaces the store's clock (tests).
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// stamp is now, strictly after the last stamp this store handed out.
func (s *Store) stamp() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now().UTC().Truncate(time.Microsecond)
	if !t.After(s.last) {
		t = s.last.Add(time.Microsecond)
	}
	s.last = t
	return t.Format(stampLayout)
}

// ValidateReport checks a session report and settles its model names
// ("" becomes UnknownModel, case folded); two entries for one model are
// refused rather than guessed between.
func ValidateReport(r *SessionReport) error {
	if strings.TrimSpace(r.Project) == "" || strings.TrimSpace(r.SessionID) == "" {
		return invalidf("project and session are required")
	}
	if len(r.Models) == 0 {
		return invalidf("no models reported")
	}
	if len(r.Models) > MaxModelsPerReport {
		return invalidf("at most %d models per report", MaxModelsPerReport)
	}
	seen := make(map[string]bool, len(r.Models))
	for i := range r.Models {
		m := &r.Models[i]
		m.Model = normalizeModel(m.Model)
		if m.Model == "" {
			m.Model = UnknownModel
		}
		if !ValidModelName(m.Model) {
			return invalidf("a model name is too long or not text")
		}
		if seen[m.Model] {
			return invalidf("model %q is reported twice", m.Model)
		}
		seen[m.Model] = true
		for _, n := range []int64{m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWriteTokens} {
			if n < 0 || n > MaxTokens {
				return invalidf("token counts are between 0 and %d", MaxTokens)
			}
		}
		if math.IsNaN(m.CostUSD) || math.IsInf(m.CostUSD, 0) || m.CostUSD < 0 || m.CostUSD > MaxCostUSD {
			return invalidf("costUsd is between 0 and %.0f", MaxCostUSD)
		}
	}
	return nil
}

// RecordSession stores a session's report: each model's totals replace what
// was stored for it, since every report is cumulative.
func (s *Store) RecordSession(ctx context.Context, r SessionReport) error {
	if err := ValidateReport(&r); err != nil {
		return err
	}
	now := s.stamp()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, m := range r.Models {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_session_usage (project, session_id, model, actor_id, actor_name, session_name, agent,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reported_cost_usd, first_at, updated_at, run_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project, session_id, model) DO UPDATE SET
    run_id = CASE WHEN excluded.run_id <> '' THEN excluded.run_id ELSE agent_session_usage.run_id END,
    actor_id = excluded.actor_id, actor_name = excluded.actor_name,
    session_name = excluded.session_name, agent = excluded.agent,
    input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
    cache_read_tokens = excluded.cache_read_tokens, cache_write_tokens = excluded.cache_write_tokens,
    reported_cost_usd = excluded.reported_cost_usd, updated_at = excluded.updated_at`,
				r.Project, r.SessionID, m.Model, r.ActorID, r.ActorName, r.SessionName, strings.ToLower(strings.TrimSpace(r.Agent)),
				m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWriteTokens, m.CostUSD, now, now, r.RunID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SessionModels is every model one session reported in project, its latest
// totals each.
func (s *Store) SessionModels(ctx context.Context, project, sessionID string) ([]ModelUsage, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reported_cost_usd
  FROM agent_session_usage WHERE project = ? AND session_id = ? ORDER BY model`, project, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelUsage
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Model, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CacheWriteTokens, &m.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CustomPrices are the owner's own prices.
func (s *Store) CustomPrices(ctx context.Context) ([]Price, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_write_per_mtok
  FROM usage_prices ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Price{}
	for rows.Next() {
		p := Price{Source: SourceCustom}
		if err := rows.Scan(&p.Model, &p.InputPerMTok, &p.OutputPerMTok, &p.CacheReadPerMTok, &p.CacheWritePerMTok); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetCustomPrices replaces every custom price.
func (s *Store) SetCustomPrices(ctx context.Context, prices []Price, by string) error {
	if err := ValidatePrices(prices); err != nil {
		return err
	}
	now := s.stamp()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_prices`); err != nil {
			return err
		}
		for _, p := range prices {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO usage_prices (model, input_per_mtok, output_per_mtok, cache_read_per_mtok, cache_write_per_mtok, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
				normalizeModel(p.Model), p.InputPerMTok, p.OutputPerMTok, p.CacheReadPerMTok, p.CacheWritePerMTok, now, by); err != nil {
				return err
			}
		}
		return nil
	})
}

// Table is the prices in force: the custom ones over the built-in ones.
func (s *Store) Table(ctx context.Context) (Table, error) {
	custom, err := s.CustomPrices(ctx)
	if err != nil {
		return Table{}, err
	}
	return NewTable(custom), nil
}
