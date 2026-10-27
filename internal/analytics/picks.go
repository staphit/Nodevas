package analytics

import (
	"context"
	"encoding/json"

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

// ListPicks is the newest picks in project, owner's ("" every owner's).
func ListPicks(ctx context.Context, database *db.DB, ownerID, project string, limit int) ([]RoutePick, error) {
	// TODO(green): implement.
	return nil, nil
}
