package analytics

import (
	"context"
	"time"

	"nodevas/internal/db"
)

// Tiers: senior, mid and probation, from a rolling window's scorecards.
// Deterministic; a tier only ever chooses an agent and model, never a role,
// tools, write types or auto-accept.

// Tier names.
const (
	TierProbation = "probation"
	TierMid       = "mid"
	TierSenior    = "senior"
)

// Tier thresholds.
const (
	WindowDays       = 30
	MinSamples       = 10
	SeniorMinSamples = 20
	SeniorScore      = 0.80
	MidScore         = 0.60
)

// Tier is the tier of a key with samples and score.
func Tier(samples int, score float64) string {
	switch {
	case samples < MinSamples:
		return TierProbation
	case score >= SeniorScore && samples >= SeniorMinSamples:
		return TierSenior
	case score >= MidScore:
		return TierMid
	}
	return TierProbation
}

// TierRank orders tiers: probation 0, mid 1, senior 2; -1 unknown.
func TierRank(tier string) int {
	switch tier {
	case TierProbation:
		return 0
	case TierMid:
		return 1
	case TierSenior:
		return 2
	}
	return -1
}

// DefaultRequiredTier is the tier a role's work needs by default, by the
// role's built-in slot (a custom role's base role).
func DefaultRequiredTier(slot string) string {
	switch slot {
	case "reviewer", "qa":
		return TierSenior
	case "researcher":
		return TierProbation
	}
	return TierMid
}

// RequiredTier is the tier role (of built-in slot) needs under settings.
func RequiredTier(s Settings, role, slot string) string {
	if t, ok := s.RequiredTiers[role]; ok && TierRank(t) >= 0 {
		return t
	}
	if slot == "" {
		slot = role
	}
	return DefaultRequiredTier(slot)
}

// TierRow is one key's tier (GET /api/analytics/tiers).
type TierRow struct {
	Role    string  `json:"role"`
	Agent   string  `json:"agent"`
	Model   string  `json:"model"`
	Samples int     `json:"samples"`
	Score   float64 `json:"score"`
	Tier    string  `json:"tier"`
}

// Tiers is GET /api/analytics/tiers.
type Tiers struct {
	Days             int                `json:"days"`
	MinSamples       int                `json:"minSamples"`
	SeniorMinSamples int                `json:"seniorMinSamples"`
	Thresholds       map[string]float64 `json:"thresholds"`
	Required         map[string]string  `json:"required"`
	Rows             []TierRow          `json:"rows"`
}

// TiersFor is owner's tiers over the rolling window (track all), with the
// required tiers of project's settings (project "" the owner's default).
func TiersFor(ctx context.Context, database *db.DB, ownerID, project string, now time.Time) (Tiers, error) {
	out := Tiers{Days: WindowDays, MinSamples: MinSamples, SeniorMinSamples: SeniorMinSamples,
		Thresholds: map[string]float64{TierSenior: SeniorScore, TierMid: MidScore},
		Required:   map[string]string{}, Rows: []TierRow{}}
	cards, err := ScorecardsFor(ctx, database, Query{OwnerID: ownerID, Days: WindowDays, Track: TrackAll, Now: now})
	if err != nil {
		return out, err
	}
	settings, err := LoadSettings(ctx, database, ownerID, project)
	if err != nil {
		return out, err
	}
	for _, role := range builtinRoles {
		out.Required[role] = RequiredTier(settings, role, role)
	}
	for role, tier := range settings.RequiredTiers {
		if TierRank(tier) >= 0 {
			out.Required[role] = tier
		}
	}
	for _, r := range cards.Rows {
		out.Rows = append(out.Rows, TierRow{Role: r.Role, Agent: r.Agent, Model: r.Model, Samples: r.Samples, Score: r.Score, Tier: r.Tier})
	}
	return out, nil
}

// builtinRoles are the built-in role ids.
var builtinRoles = []string{"planner", "qa", "tester", "executor", "reviewer", "researcher"}
