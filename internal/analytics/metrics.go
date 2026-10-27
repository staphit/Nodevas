package analytics

import (
	"context"
	"sort"

	"nodevas/internal/db"
	"nodevas/internal/engine"
	"nodevas/internal/runnerwire"
)

// infraFailures are failure kinds that say nothing about the work: the
// provider or the CLI was not there (reliability).
var infraFailures = map[string]bool{
	runnerwire.FailureRateLimit: true,
	runnerwire.FailureQuota:     true,
	runnerwire.FailureModel:     true,
	"agent_unavailable":         true,
}

// slotOf is a role key's built-in slot ("" for a custom role).
func slotOf(role string) string {
	switch role {
	case engine.RolePlanner, engine.RoleQA, engine.RoleTester, engine.RoleExecutor, engine.RoleReviewer, engine.RoleResearcher:
		return role
	}
	return ""
}

// ownsUnits reports whether a role's work is measured by the units it owns.
func ownsUnits(role string) bool {
	return role == engine.RoleExecutor || role == engine.RoleTester
}

func judgesWork(role string) bool { return role == engine.RoleReviewer || role == engine.RoleQA }

type rowKey struct{ role, agent, model string }

// acc sums one key's metrics.
type acc struct {
	runs, succeeded, infra int
	cost                   float64
	estimated              bool
	seconds                []float64
	verdicts, wrong        int
	// owned units
	owned, delivered, firstPass, revise int
	dSum, dDelivered                    float64
	greenUnits, tddUnits                int
}

// ScorecardsFor computes the scorecards of q.OwnerID's runs over q's window
// on q.Track.
func ScorecardsFor(ctx context.Context, database *db.DB, q Query) (Scorecards, error) {
	w := windowOf(q.Days, q.Now)
	track := q.Track
	if track != TrackCode && track != TrackStory {
		track = TrackAll
	}
	out := Scorecards{Days: w.days, Since: w.sinceOut(), MinSamples: MinSamples, Rows: []ScorecardRow{}}
	h, err := loadHistory(ctx, database, filter{owner: q.OwnerID, project: q.Project, viewer: q.Viewer, since: w.sinceStamp()})
	if err != nil {
		return out, err
	}
	out.Rows = scoreRows(h, buildUnits(h), track)
	return out, nil
}

func onTrack(track, t string) bool { return track == TrackAll || track == t }

// scoreRows computes every key's row on track.
func scoreRows(h *history, units []*unit, track string) []ScorecardRow {
	accs := map[rowKey]*acc{}
	get := func(k rowKey) *acc {
		a := accs[k]
		if a == nil {
			a = &acc{}
			accs[k] = a
		}
		return a
	}
	for _, r := range h.runs {
		if r.isVerify() || !r.ended() || !onTrack(track, r.track()) {
			continue
		}
		a := get(rowKey{r.roleKey(), r.Agent, r.modelKey()})
		a.runs++
		a.cost += r.Cost
		a.estimated = a.estimated || r.Estimated
		switch {
		case r.Status == runnerwire.RunSucceeded:
			a.succeeded++
			if s, ok := r.seconds(); ok {
				a.seconds = append(a.seconds, s)
			}
		case r.Status == runnerwire.RunFailed && infraFailures[r.FailureKind]:
			a.infra++
		}
		if v, ok := h.verdicts[r.ID]; ok {
			a.verdicts++
			if v.contradicted {
				a.wrong++
			}
		}
	}
	for _, u := range units {
		if !u.ended || !onTrack(track, u.track()) {
			continue
		}
		for _, owner := range []*run{u.execOwner, u.testOwner} {
			if owner == nil {
				continue
			}
			role := owner.roleKey()
			if !ownsUnits(slotOf(role)) {
				continue
			}
			a := get(rowKey{role, owner.Agent, owner.modelKey()})
			a.owned++
			a.dSum += u.d
			a.revise += u.revise
			if u.delivered {
				a.delivered++
				a.dDelivered += u.d
				if u.revise == 0 {
					a.firstPass++
				}
			}
			if u.green {
				a.greenUnits++
				if u.tdd {
					a.tddUnits++
				}
			}
		}
	}
	rows := make([]ScorecardRow, 0, len(accs))
	for k, a := range accs {
		if a.runs == 0 {
			continue
		}
		rows = append(rows, a.row(k, track))
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		return a.Model < b.Model
	})
	return rows
}

func (a *acc) row(k rowKey, track string) ScorecardRow {
	slot := slotOf(k.role)
	r := ScorecardRow{Role: k.role, Track: track, Agent: k.agent, Model: k.model, Runs: a.runs,
		CostUSD: round6(a.cost), CostEstimated: a.estimated}
	success := float64(a.succeeded) / float64(a.runs)
	reliability := 1 - float64(a.infra)/float64(a.runs)
	r.SuccessRate = rate(float64(a.succeeded), float64(a.runs))
	r.Reliability = rate(float64(a.runs-a.infra), float64(a.runs))
	r.MedianSeconds = median(a.seconds)

	var delivery, deliveryAdj, firstPass, tdd, accuracy *float64
	if ownsUnits(slot) {
		r.Samples = a.owned
		if a.owned > 0 {
			d := float64(a.delivered) / float64(a.owned)
			fp := float64(a.firstPass) / float64(a.owned)
			delivery, firstPass = &d, &fp
			r.Delivery = rate(float64(a.delivered), float64(a.owned))
			r.FirstPassRate = rate(float64(a.firstPass), float64(a.owned))
			r.ReworkRounds = rate(float64(a.revise), float64(a.owned))
		}
		if a.dSum > 0 {
			da := a.dDelivered / a.dSum
			deliveryAdj = &da
			r.DeliveryAdjusted = rate(a.dDelivered, a.dSum)
		}
		if a.greenUnits > 0 {
			t := float64(a.tddUnits) / float64(a.greenUnits)
			tdd = &t
			r.TDDRate = rate(float64(a.tddUnits), float64(a.greenUnits))
		}
		r.CostPerDelivered = costRate(a.cost, float64(a.delivered))
	} else {
		r.Samples = a.runs
		r.CostPerDelivered = costRate(a.cost, float64(a.runs))
	}
	if judgesWork(slot) {
		r.Verdicts = a.verdicts
		r.Samples = a.verdicts
		if a.verdicts > 0 {
			acc := 1 - float64(a.wrong)/float64(a.verdicts)
			accuracy = &acc
			r.Accuracy = rate(float64(a.verdicts-a.wrong), float64(a.verdicts))
		}
	}

	// A null term counts as the delivery (difficulty-adjusted); a role with
	// no units delivers as often as its runs succeed.
	fallback := success
	if deliveryAdj != nil {
		fallback = *deliveryAdj
	}
	or := func(v *float64) float64 {
		if v == nil {
			return fallback
		}
		return *v
	}
	d := success
	if delivery != nil {
		d = *delivery
	}
	var score float64
	switch slot {
	case engine.RoleExecutor:
		score = 0.40*or(deliveryAdj) + 0.30*or(firstPass) + 0.15*reliability + 0.15*or(tdd)
	case engine.RoleTester:
		score = 0.50*or(tdd) + 0.30*reliability + 0.20*d
	case engine.RoleReviewer, engine.RoleQA:
		score = 0.60*or(accuracy) + 0.20*reliability + 0.20*d
	default:
		score = 0.60*d + 0.40*reliability
	}
	r.Score = roundRate(score)
	r.Tier = Tier(r.Samples, r.Score)
	return r
}

// median is the middle value (the mean of the two middle ones for an even
// count), nil for none.
func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	m := sorted[n/2]
	if n%2 == 0 {
		m = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	m = roundRate(m)
	return &m
}
