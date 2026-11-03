package analytics

import "testing"

// B-32: tier thresholds and the required tier defaults.
func TestTierThresholds(t *testing.T) {
	for _, c := range []struct {
		samples int
		score   float64
		want    string
	}{
		{9, 0.95, TierProbation},
		{0, 0, TierProbation},
		{10, 0.79, TierMid},
		{10, 0.60, TierMid},
		{19, 0.85, TierMid},
		{20, 0.80, TierSenior},
		{25, 0.59, TierProbation},
	} {
		if got := Tier(c.samples, c.score); got != c.want {
			t.Errorf("Tier(%d, %v) = %q, want %q", c.samples, c.score, got, c.want)
		}
	}
	if TierRank(TierProbation) != 0 || TierRank(TierMid) != 1 || TierRank(TierSenior) != 2 {
		t.Error("tier ranks")
	}
	for slot, want := range map[string]string{
		"executor": TierMid, "tester": TierMid, "reviewer": TierSenior, "qa": TierSenior,
		"planner": TierMid, "researcher": TierProbation,
	} {
		if got := DefaultRequiredTier(slot); got != want {
			t.Errorf("DefaultRequiredTier(%s) = %q, want %q", slot, got, want)
		}
	}
}
