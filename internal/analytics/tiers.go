package analytics

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
	// TODO(green): implement.
	return ""
}

// TierRank orders tiers: probation 0, mid 1, senior 2; -1 unknown.
func TierRank(tier string) int {
	// TODO(green): implement.
	return -1
}

// DefaultRequiredTier is the tier a role's work needs by default, by the
// role's built-in slot (a custom role's base role).
func DefaultRequiredTier(slot string) string {
	// TODO(green): implement.
	return ""
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
