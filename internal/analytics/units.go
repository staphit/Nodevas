package analytics

import (
	"sort"

	"nodevas/internal/runnerwire"
)

// unit is one delivery attempt: a pipeline, or a lineage of runs retrying
// one another (the root of its retry_of chain).
type unit struct {
	key       string
	pipeline  string
	runs      []*run
	ended     bool
	delivered bool
	revise    int
	// d is the unit's difficulty, from its first run.
	d     float64
	first *run
	// owners: the Executor's (first implement run, else first revise) and
	// the Tester's (first test run).
	execOwner, testOwner *run
	// tdd: a verify_red pass came before the first verify_green pass;
	// green: any verify_green pass.
	tdd, green bool
}

func (u *unit) track() string { return u.first.track() }

// buildUnits groups a history's runs into units.
func buildUnits(h *history) []*unit {
	byKey := map[string]*unit{}
	var order []string
	runUnit := map[string]*unit{}
	for _, r := range h.runs {
		key := ""
		if r.Pipeline != "" {
			key = "p:" + r.Pipeline
		} else {
			key = "l:" + lineageRoot(h, r)
		}
		u := byKey[key]
		if u == nil {
			u = &unit{key: key, pipeline: r.Pipeline}
			byKey[key] = u
			order = append(order, key)
		}
		u.runs = append(u.runs, r)
		runUnit[r.ID] = u
	}
	units := make([]*unit, 0, len(order))
	for _, key := range order {
		u := byKey[key]
		sort.SliceStable(u.runs, func(i, j int) bool { return u.runs[i].Created < u.runs[j].Created })
		u.first = u.runs[0]
		u.d = difficulty(u.first)
		active := false
		var firstRevise *run
		for _, r := range u.runs {
			if r.active() {
				active = true
			}
			switch r.Kind {
			case runnerwire.JobImplement:
				if u.execOwner == nil && !r.isVerify() {
					u.execOwner = r
				}
			case runnerwire.JobRevise:
				u.revise++
				if firstRevise == nil {
					firstRevise = r
				}
			case runnerwire.JobTest:
				if u.testOwner == nil {
					u.testOwner = r
				}
			}
		}
		if u.execOwner == nil {
			u.execOwner = firstRevise
		}
		status, known := h.pipelines[u.pipeline]
		if u.pipeline != "" && known && status != "" {
			u.ended = status != runnerwire.PipelineRunning
			u.delivered = status == runnerwire.PipelineApproved
		} else {
			u.ended = !active
			for _, r := range u.runs {
				if r.Status == runnerwire.RunSucceeded {
					switch r.Kind {
					case runnerwire.JobImplement, runnerwire.JobRevise, runnerwire.JobPlan, runnerwire.JobTest:
						u.delivered = true
					}
				}
			}
		}
		units = append(units, u)
	}
	// TDD discipline from the verify evidence.
	firstRed := map[*unit]string{}
	firstGreen := map[*unit]string{}
	for _, e := range h.evidence {
		var u *unit
		if e.pipeline != "" {
			u = byKey["p:"+e.pipeline]
		}
		if u == nil {
			u = runUnit[e.runID]
		}
		if u == nil {
			continue
		}
		switch e.kind {
		case runnerwire.PhaseVerifyRed:
			if _, ok := firstRed[u]; !ok {
				firstRed[u] = e.at
			}
		case runnerwire.PhaseVerifyGreen:
			if _, ok := firstGreen[u]; !ok {
				firstGreen[u] = e.at
			}
		}
	}
	for u, green := range firstGreen {
		u.green = true
		if red, ok := firstRed[u]; ok && red < green {
			u.tdd = true
		}
	}
	return units
}

// lineageRoot is the first run of r's retry chain within the history.
func lineageRoot(h *history, r *run) string {
	seen := map[string]bool{r.ID: true}
	cur := r
	for cur.RetryOf != "" {
		parent, ok := h.byID[cur.RetryOf]
		if !ok || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		cur = parent
	}
	return cur.ID
}

// difficulty weighs a unit by its acceptance size and a goal's breadth.
func difficulty(first *run) float64 {
	d := 1 + 0.1*float64(min(max(first.Acceptance, 0), 10))
	if first.NodeType == "goal" {
		d += 0.5
	}
	return d
}
