package analytics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"nodevas/internal/db"
)

// Pick sources and reason codes.
const (
	PickManual = "manual"
	PickAuto   = "auto"
	PickNone   = "none"

	ReasonManual        = "manual"
	ReasonCheapest      = "cheapest_meeting_tier"
	ReasonExplore       = "explore"
	ReasonNoneMeetsTier = "none_meets_tier"
	ReasonBudget        = "budget_exceeded"
	ReasonNoRunner      = "no_runner"
	ReasonOverBudget    = "over_budget"
)

// RoutePick is one logged routing decision.
type RoutePick struct {
	ID           string          `json:"id"`
	OwnerID      string          `json:"-"`
	Project      string          `json:"project"`
	NodeID       string          `json:"nodeId"`
	Role         string          `json:"role"`
	Source       string          `json:"source"`
	RuleID       string          `json:"ruleId"`
	RunnerID     string          `json:"runnerId"`
	Agent        string          `json:"agent"`
	Model        string          `json:"model"`
	Tier         string          `json:"tier"`
	RequiredTier string          `json:"requiredTier"`
	Explore      bool            `json:"explore"`
	Reason       json.RawMessage `json:"reason"`
	RunID        string          `json:"runId"`
	CreatedAt    string          `json:"createdAt"`
}

// PickReason is a pick's decoded reason.
type PickReason struct {
	Code       string      `json:"code"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Candidate is one runner/agent/model combination auto routing weighed.
// Cost -1 is unpriced.
type Candidate struct {
	RunnerID string  `json:"runnerId"`
	Agent    string  `json:"agent"`
	Model    string  `json:"model"`
	Tier     string  `json:"tier"`
	Samples  int     `json:"samples"`
	Score    float64 `json:"score"`
	Cost     float64 `json:"cost"`
	Dropped  string  `json:"dropped,omitempty"`
}

// MaxPicksListed caps ListPicks.
const MaxPicksListed = 200

// maxPickCandidates caps the candidates a pick's reason keeps.
const maxPickCandidates = 10

// ListPicks is the newest picks in project, owner's ("" every owner's).
func ListPicks(ctx context.Context, database *db.DB, ownerID, project string, limit int) ([]RoutePick, error) {
	if limit <= 0 || limit > MaxPicksListed {
		limit = 50
	}
	query := `SELECT id, owner_id, project, node_id, role, source, rule_id, runner_id, agent, model, tier,
       required_tier, explore, reason, run_id, created_at FROM route_picks WHERE project = ?`
	args := []any{project}
	if ownerID != "" {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := database.QueryContext(ctx, query+` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoutePick{}
	for rows.Next() {
		var p RoutePick
		var reason string
		if err := rows.Scan(&p.ID, &p.OwnerID, &p.Project, &p.NodeID, &p.Role, &p.Source, &p.RuleID, &p.RunnerID,
			&p.Agent, &p.Model, &p.Tier, &p.RequiredTier, &p.Explore, &reason, &p.RunID, &p.CreatedAt); err != nil {
			return nil, err
		}
		if !json.Valid([]byte(reason)) {
			reason = `{}`
		}
		p.Reason = json.RawMessage(reason)
		out = append(out, p)
	}
	return out, rows.Err()
}

// pickIDs hands out increasing pick ids, so picks made in the same instant
// still list newest first.
var pickIDs struct {
	sync.Mutex
	last int64
}

func newPickID(now time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	pickIDs.Lock()
	n := time.Now().UnixNano()
	if t := now.UnixNano(); t > n {
		n = t
	}
	if n <= pickIDs.last {
		n = pickIDs.last + 1
	}
	pickIDs.last = n
	pickIDs.Unlock()
	return fmt.Sprintf("%016x%s", n, hex.EncodeToString(suffix[:])), nil
}

// logPick stores one pick and returns its id.
func logPick(ctx context.Context, database *db.DB, p RoutePick, reason PickReason, now time.Time) (string, error) {
	id, err := newPickID(now)
	if err != nil {
		return "", err
	}
	if len(reason.Candidates) > maxPickCandidates {
		reason.Candidates = reason.Candidates[:maxPickCandidates]
	}
	raw, err := json.Marshal(reason)
	if err != nil {
		return "", err
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO route_picks (id, owner_id, project, node_id, role, source, rule_id, runner_id, agent, model, tier,
                         required_tier, explore, reason, run_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?)`,
		id, p.OwnerID, p.Project, p.NodeID, p.Role, p.Source, p.RuleID, p.RunnerID, p.Agent, p.Model, p.Tier,
		p.RequiredTier, p.Explore, string(raw), formatStamp(now))
	return id, err
}
