package analytics

import (
	"time"

	"nodevas/internal/runnerwire"
)

// Tracks: story work (scene and event nodes) is scored apart from code.
const (
	TrackAll   = "all"
	TrackCode  = "code"
	TrackStory = "story"
)

// DefaultDays is the default window, in UTC days, today included.
const DefaultDays = 30

// Query is what a scorecard or insights report covers.
type Query struct {
	// OwnerID keeps the runs on that owner's runners (scorecards, tiers).
	OwnerID string
	// Project keeps one project's runs (insights); "" every project.
	Project string
	// Viewer, when set, keeps the runs that person made or that ran on
	// their runners (a non-admin's insights).
	Viewer string
	Days   int
	Track  string
	Now    time.Time
}

// ScorecardRow is one (role, track, agent, model) key's metrics. A nil
// metric had a zero denominator.
type ScorecardRow struct {
	Role             string   `json:"role"`
	Track            string   `json:"track"`
	Agent            string   `json:"agent"`
	Model            string   `json:"model"`
	Runs             int      `json:"runs"`
	Verdicts         int      `json:"verdicts"`
	Samples          int      `json:"samples"`
	SuccessRate      *float64 `json:"successRate"`
	Reliability      *float64 `json:"reliability"`
	Delivery         *float64 `json:"delivery"`
	DeliveryAdjusted *float64 `json:"deliveryAdjusted"`
	FirstPassRate    *float64 `json:"firstPassRate"`
	ReworkRounds     *float64 `json:"reworkRounds"`
	CostUSD          float64  `json:"costUsd"`
	CostPerDelivered *float64 `json:"costPerDelivered"`
	CostEstimated    bool     `json:"costEstimated"`
	MedianSeconds    *float64 `json:"medianSeconds"`
	TDDRate          *float64 `json:"tddRate"`
	Accuracy         *float64 `json:"accuracy"`
	Score            float64  `json:"score"`
	Tier             string   `json:"tier"`
}

// Scorecards is GET /api/analytics/scorecards.
type Scorecards struct {
	Days       int            `json:"days"`
	Since      string         `json:"since"`
	MinSamples int            `json:"minSamples"`
	Rows       []ScorecardRow `json:"rows"`
}

// CostBucket is one dimension value's cost per delivered task.
type CostBucket struct {
	Key              string   `json:"key"`
	Agent            string   `json:"agent,omitempty"`
	CostUSD          float64  `json:"costUsd"`
	Runs             int      `json:"runs"`
	Delivered        int      `json:"delivered"`
	CostPerDelivered *float64 `json:"costPerDelivered"`
}

// Totals are an insights report's totals.
type Totals struct {
	CostUSD          float64  `json:"costUsd"`
	EstimatedCostUSD float64  `json:"estimatedCostUsd"`
	Runs             int      `json:"runs"`
	Units            int      `json:"units"`
	Delivered        int      `json:"delivered"`
	CostPerDelivered *float64 `json:"costPerDelivered"`
	FirstPassRate    *float64 `json:"firstPassRate"`
	ReworkRounds     *float64 `json:"reworkRounds"`
}

// Hotspot is a node by its waste.
type Hotspot struct {
	NodeID    string  `json:"nodeId"`
	NodeTitle string  `json:"nodeTitle"`
	CostUSD   float64 `json:"costUsd"`
	Runs      int     `json:"runs"`
}

// Waste is spend that delivered nothing.
type Waste struct {
	CostUSD          float64   `json:"costUsd"`
	FailedCostUSD    float64   `json:"failedCostUsd"`
	RetriedCostUSD   float64   `json:"retriedCostUsd"`
	AbandonedCostUSD float64   `json:"abandonedCostUsd"`
	Hotspots         []Hotspot `json:"hotspots"`
}

// Insights is GET /api/analytics/insights.
type Insights struct {
	Days        int          `json:"days"`
	Since       string       `json:"since"`
	Currency    string       `json:"currency"`
	Totals      Totals       `json:"totals"`
	ByRole      []CostBucket `json:"byRole"`
	ByAgent     []CostBucket `json:"byAgent"`
	ByModel     []CostBucket `json:"byModel"`
	ByNodeType  []CostBucket `json:"byNodeType"`
	Waste       Waste        `json:"waste"`
	Suggestions []Suggestion `json:"suggestions"`
}

// Change is what applying a suggestion changes.
type Change struct {
	Type         string                 `json:"type"`
	RuleID       string                 `json:"ruleId,omitempty"`
	From         *runnerwire.AgentModel `json:"from,omitempty"`
	To           *runnerwire.AgentModel `json:"to,omitempty"`
	Role         string                 `json:"role,omitempty"`
	DailyCostUSD float64                `json:"dailyCostUsd,omitempty"`
	Project      string                 `json:"project,omitempty"`
	On           bool                   `json:"on,omitempty"`
	Row          string                 `json:"row,omitempty"`
}

// Change types.
const (
	ChangeRoutingRuleTarget = "routing_rule_target"
	ChangeRoleBudget        = "role_budget"
	ChangeAutoRouting       = "auto_routing"
	ChangeOpenSettings      = "open_settings"
)

// Suggestion is one rule-based suggestion. Change nil means nothing to
// apply (only to read).
type Suggestion struct {
	Key    string         `json:"key"`
	Kind   string         `json:"kind"`
	Params map[string]any `json:"params"`
	Change *Change        `json:"change"`
}

// Suggestion kinds, in rule order.
const (
	SuggestCheaperEqualTier  = "cheaper_equal_tier"
	SuggestDemoteUnreliable  = "demote_unreliable"
	SuggestEnableAutoRouting = "enable_auto_routing"
	SuggestRoleBudget        = "role_budget"
	SuggestTesterFirst       = "tester_first"
	// MaxSuggestions caps a report's suggestions.
	MaxSuggestions = 5
)
