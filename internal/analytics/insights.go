package analytics

import (
	"context"
	"sort"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/runnerwire"
)

// maxHotspots caps Waste.Hotspots.
const maxHotspots = 5

// InsightsFor computes q.Project's insights over q's window (without
// suggestions; see Suggestions).
func InsightsFor(ctx context.Context, database *db.DB, q Query) (Insights, error) {
	w := windowOf(q.Days, q.Now)
	out := Insights{Days: w.days, Since: w.sinceOut(), Currency: "USD",
		ByRole: []CostBucket{}, ByAgent: []CostBucket{}, ByModel: []CostBucket{}, ByNodeType: []CostBucket{},
		Waste: Waste{Hotspots: []Hotspot{}}, Suggestions: []Suggestion{}}
	h, err := loadHistory(ctx, database, filter{owner: q.OwnerID, project: q.Project, viewer: q.Viewer, since: w.sinceStamp()})
	if err != nil {
		return out, err
	}
	units := buildUnits(h)
	in := computeInsights(h, units)
	in.Days, in.Since, in.Currency, in.Suggestions = out.Days, out.Since, out.Currency, out.Suggestions
	return in, nil
}

// dimension sums cost and delivered units per value.
type dimension struct {
	cost      map[string]float64
	runs      map[string]int
	delivered map[string]map[*unit]bool
	agent     map[string]string
}

func newDimension() *dimension {
	return &dimension{cost: map[string]float64{}, runs: map[string]int{}, delivered: map[string]map[*unit]bool{}, agent: map[string]string{}}
}

func (d *dimension) add(key string, r *run, u *unit) {
	d.cost[key] += r.Cost
	d.runs[key]++
	if u.delivered {
		if d.delivered[key] == nil {
			d.delivered[key] = map[*unit]bool{}
		}
		d.delivered[key][u] = true
	}
}

func (d *dimension) buckets() []CostBucket {
	out := make([]CostBucket, 0, len(d.cost))
	for key, cost := range d.cost {
		delivered := len(d.delivered[key])
		out = append(out, CostBucket{Key: key, Agent: d.agent[key], CostUSD: round6(cost), Runs: d.runs[key],
			Delivered: delivered, CostPerDelivered: costRate(cost, float64(delivered))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// computeInsights works out a history's insights (no window fields).
func computeInsights(h *history, units []*unit) Insights {
	out := Insights{ByRole: []CostBucket{}, ByAgent: []CostBucket{}, ByModel: []CostBucket{}, ByNodeType: []CostBucket{},
		Waste: Waste{Hotspots: []Hotspot{}}, Suggestions: []Suggestion{}}
	unitOf := map[string]*unit{}
	for _, u := range units {
		for _, r := range u.runs {
			unitOf[r.ID] = u
		}
	}
	byRole, byAgent, byModel, byType := newDimension(), newDimension(), newDimension(), newDimension()
	var cost, estimated float64
	runs, ended, delivered, firstPass, revise := 0, 0, 0, 0, 0
	for _, u := range units {
		if !u.ended {
			continue
		}
		ended++
		revise += u.revise
		if u.delivered {
			delivered++
			if u.revise == 0 {
				firstPass++
			}
		}
		for _, r := range u.runs {
			if r.isVerify() {
				continue
			}
			runs++
			cost += r.Cost
			if r.Estimated {
				estimated += r.Cost
			}
			byRole.add(r.roleKey(), r, u)
			byAgent.add(r.Agent, r, u)
			key := r.Agent + "\x00" + r.modelKey()
			byModel.add(key, r, u)
			byModel.agent[key] = r.Agent
			nodeType := r.NodeType
			if nodeType == "" {
				nodeType = "unknown"
			}
			byType.add(nodeType, r, u)
		}
	}
	out.Totals = Totals{CostUSD: round6(cost), EstimatedCostUSD: round6(estimated), Runs: runs, Units: ended,
		Delivered: delivered, CostPerDelivered: costRate(cost, float64(delivered)),
		FirstPassRate: rate(float64(firstPass), float64(ended)), ReworkRounds: rate(float64(revise), float64(ended))}
	out.ByRole = byRole.buckets()
	out.ByAgent = byAgent.buckets()
	out.ByModel = byModel.buckets()
	for i := range out.ByModel {
		agent, model := splitModelKey(out.ByModel[i].Key)
		out.ByModel[i].Key, out.ByModel[i].Agent = model, agent
	}
	out.ByNodeType = byType.buckets()
	out.Waste = waste(h, unitOf)
	return out
}

func splitModelKey(key string) (agent, model string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}

// wasteRuns are the runs that delivered nothing, with why.
type wasteRun struct {
	r                          *run
	failed, retried, abandoned bool
}

func wasteOf(h *history, unitOf map[string]*unit) []wasteRun {
	retriedFrom := map[string]bool{}
	for _, r := range h.runs {
		if r.RetryOf != "" {
			retriedFrom[r.RetryOf] = true
		}
	}
	var out []wasteRun
	for _, r := range h.runs {
		if r.isVerify() {
			continue
		}
		wr := wasteRun{r: r}
		wr.failed = r.Status == runnerwire.RunFailed || r.Status == runnerwire.RunCancelled
		wr.retried = r.Status == runnerwire.RunFailed && retriedFrom[r.ID]
		if u := unitOf[r.ID]; u != nil && u.ended && !u.delivered {
			wr.abandoned = true
		}
		if wr.failed || wr.retried || wr.abandoned {
			out = append(out, wr)
		}
	}
	return out
}

func waste(h *history, unitOf map[string]*unit) Waste {
	out := Waste{Hotspots: []Hotspot{}}
	type node struct {
		title string
		cost  float64
		runs  int
	}
	nodes := map[string]*node{}
	var failed, retried, abandoned, total float64
	for _, wr := range wasteOf(h, unitOf) {
		c := wr.r.Cost
		if wr.failed {
			failed += c
		}
		if wr.retried {
			retried += c
		}
		if wr.abandoned {
			abandoned += c
		}
		total += c
		n := nodes[wr.r.Node]
		if n == nil {
			n = &node{title: wr.r.Title}
			nodes[wr.r.Node] = n
		}
		n.cost += c
		n.runs++
	}
	out.CostUSD, out.FailedCostUSD, out.RetriedCostUSD, out.AbandonedCostUSD = round6(total), round6(failed), round6(retried), round6(abandoned)
	for id, n := range nodes {
		out.Hotspots = append(out.Hotspots, Hotspot{NodeID: id, NodeTitle: n.title, CostUSD: round6(n.cost), Runs: n.runs})
	}
	sort.Slice(out.Hotspots, func(i, j int) bool {
		if out.Hotspots[i].CostUSD != out.Hotspots[j].CostUSD {
			return out.Hotspots[i].CostUSD > out.Hotspots[j].CostUSD
		}
		return out.Hotspots[i].NodeID < out.Hotspots[j].NodeID
	})
	if len(out.Hotspots) > maxHotspots {
		out.Hotspots = out.Hotspots[:maxHotspots]
	}
	return out
}

// Overview is GET /api/analytics/overview.
type Overview struct {
	ByProject []ProjectCost `json:"byProject"`
}

// ProjectCost is one project's spend and deliveries.
type ProjectCost struct {
	Key              string   `json:"key"`
	CostUSD          float64  `json:"costUsd"`
	Delivered        int      `json:"delivered"`
	CostPerDelivered *float64 `json:"costPerDelivered"`
}

// OverviewFor sums projects' insights totals over the window; viewer ""
// sees every run.
func OverviewFor(ctx context.Context, database *db.DB, projects []string, viewer string, days int, now time.Time) (Overview, error) {
	out := Overview{ByProject: []ProjectCost{}}
	for _, project := range projects {
		in, err := InsightsFor(ctx, database, Query{Project: project, Viewer: viewer, Days: days, Now: now})
		if err != nil {
			return out, err
		}
		if in.Totals.Runs == 0 {
			continue
		}
		out.ByProject = append(out.ByProject, ProjectCost{Key: project, CostUSD: in.Totals.CostUSD,
			Delivered: in.Totals.Delivered, CostPerDelivered: in.Totals.CostPerDelivered})
	}
	sort.Slice(out.ByProject, func(i, j int) bool {
		if out.ByProject[i].CostUSD != out.ByProject[j].CostUSD {
			return out.ByProject[i].CostUSD > out.ByProject[j].CostUSD
		}
		return out.ByProject[i].Key < out.ByProject[j].Key
	})
	return out, nil
}
