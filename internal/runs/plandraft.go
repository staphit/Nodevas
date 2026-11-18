package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"nodevas/internal/runnerwire"
)

// P6: a draft plan run (Record.Draft) does not write its create_subgraph to
// the board; the hub records it here, one row per run in run_plan_drafts,
// and the copilot's follow-up turns it into a draft card for the person.
//
// A table of its own, not a runs column: the draft is a blob of up to
// runnerwire.MaxPlanDraftBytes (2 MiB), and runColumns is read on every
// proxied request; a row of its own also gives concurrent calls of one run a
// row lock (UpdatePlanDraft). Rows are kept as the record of what the agent
// proposed and are never pruned, as runs are never pruned; that growth is
// accepted. A capture whose follow-up is dropped on purpose (the person
// deleted the conversation or lost write access to the project, so the plan
// could not be created anyway) stays here with no reader.
//
// There is deliberately no read route (no GET /api/runs/:id/draft): the plan
// reaches the person inside the conversation entry, and the follow-up reads
// it through Options.PlanDraft with admin false. PlanDraftFor's admin path is
// for a recovery route, should one ever be needed; it holds the visibility
// rule (creator, runner owner, administrator) in one place for that route.

// MergePlanDraft adds one call's nodes to what a draft holds: a key given
// again replaces that node in place, a new key is appended, and every
// dependsOn/refs entry "draft:<key>" naming a key of the result becomes
// "<key>". Calls is held.Calls+1. Pure: held and add are not changed.
func MergePlanDraft(held runnerwire.PlanDraft, add []runnerwire.PlanDraftNode) runnerwire.PlanDraft {
	out := runnerwire.PlanDraft{Calls: held.Calls + 1, Nodes: make([]runnerwire.PlanDraftNode, 0, len(held.Nodes)+len(add))}
	at := make(map[string]int, len(held.Nodes)+len(add))
	for _, n := range held.Nodes {
		at[n.Key] = len(out.Nodes)
		out.Nodes = append(out.Nodes, n)
	}
	for _, n := range add {
		if i, ok := at[n.Key]; ok {
			out.Nodes[i] = n
			continue
		}
		at[n.Key] = len(out.Nodes)
		out.Nodes = append(out.Nodes, n)
	}
	rewrite := func(targets []string) []string {
		if len(targets) == 0 {
			return targets
		}
		next := make([]string, len(targets))
		for i, target := range targets {
			if key, ok := strings.CutPrefix(target, runnerwire.PlanDraftIDPrefix); ok {
				if _, known := at[key]; known {
					target = key
				}
			}
			next[i] = target
		}
		return next
	}
	for i := range out.Nodes {
		out.Nodes[i].DependsOn = rewrite(out.Nodes[i].DependsOn)
		out.Nodes[i].Refs = rewrite(out.Nodes[i].Refs)
	}
	return out
}

func decodePlanDraft(raw string) (runnerwire.PlanDraft, error) {
	var out runnerwire.PlanDraft
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return runnerwire.PlanDraft{}, err
	}
	return out, nil
}

// PlanDraft is the run's recorded draft; the zero value when nothing was
// recorded.
func (s *Store) PlanDraft(ctx context.Context, runID string) (runnerwire.PlanDraft, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT draft FROM run_plan_drafts WHERE run_id = ?`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return runnerwire.PlanDraft{}, nil
	}
	if err != nil {
		return runnerwire.PlanDraft{}, err
	}
	return decodePlanDraft(raw)
}

// UpdatePlanDraft runs update on the run's draft under a row lock and stores
// what it returns; an error from update stores nothing and is returned as is.
// Concurrent calls for one run take turns: the first one's row (or its
// insert) blocks the next until it commits.
func (s *Store) UpdatePlanDraft(ctx context.Context, runID string,
	update func(held runnerwire.PlanDraft) (runnerwire.PlanDraft, error)) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.stamp()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO run_plan_drafts (run_id, draft, updated_at) VALUES (?, '', ?)
ON CONFLICT (run_id) DO NOTHING`, runID, now); err != nil {
			return err
		}
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT draft FROM run_plan_drafts WHERE run_id = ? FOR UPDATE`, runID).Scan(&raw); err != nil {
			return err
		}
		held, err := decodePlanDraft(raw)
		if err != nil {
			return err
		}
		next, err := update(held)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE run_plan_drafts SET draft = ?, updated_at = ? WHERE run_id = ?`,
			string(encoded), now, runID)
		return err
	})
}

// PlanDraftFor is PlanDraft for a reader: only for a draft run, and only to
// its creator, its runner's owner or an administrator (admin); anyone else,
// like a run that is not a draft, gets ErrNotFound.
func (s *Store) PlanDraftFor(ctx context.Context, runID, accountID string, admin bool) (runnerwire.PlanDraft, error) {
	r, err := s.Get(ctx, runID)
	if err != nil {
		return runnerwire.PlanDraft{}, err
	}
	if !r.Draft || (!admin && (accountID == "" || (accountID != r.CreatedBy && accountID != r.OwnerID))) {
		return runnerwire.PlanDraft{}, ErrNotFound
	}
	return s.PlanDraft(ctx, runID)
}
