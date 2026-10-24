package autopilot

import (
	"slices"
	"sort"
	"strings"

	"nodevas/internal/engine"
)

// The goal's DAG. The board has no parent/child links: what it has is
// dependency edges, From finishing before To, and tags. A goal's tasks are
// every node it waits on, directly or through other tasks, and every node
// tagged goal:<goal id> (GoalTag). A plan run (JobPlan) builds both: it tags
// each task it creates and links the tasks nothing else waits on into the
// goal; a person can do either by hand. Deprecated edges are not
// dependencies and do not count. The goal itself is never a task.
//
// Only task nodes (kind "" or "task") are dispatched. Other kinds a plan may
// hold (gates, start/end markers, scenes) are part of the DAG for ordering
// but have nothing to implement: they are skipped, and a skipped task
// satisfies whatever waits on it.

// GoalTag is the tag that makes a node one of goal id's tasks, as a plan run
// writes it (adapter.GoalTag on the runner).
func GoalTag(id string) string { return "goal:" + id }

// dag is a goal's tasks and the required edges among them.
type dag struct {
	goal string
	// order is every member in topological order (upstream first, then by
	// title, then id): the order the API lists them in.
	order    []string
	upstream map[string][]string
	nodes    map[string]*engine.Node
}

// buildDAG collects the goal's members from the graph. A cycle among them
// (the board allows drawing one) leaves its nodes at the end of the order,
// where nothing ever schedules them; the orchestrator fails them instead.
func buildDAG(g *engine.Graph, goal string) dag {
	d := dag{goal: goal, upstream: map[string][]string{}, nodes: map[string]*engine.Node{}}
	if g == nil || g.NodeByID(goal) == nil {
		return d
	}
	into := map[string][]string{}
	for _, e := range g.Edges {
		if e == nil || !e.IsPrerequisite() || e.From == e.To {
			continue
		}
		into[e.To] = append(into[e.To], e.From)
	}
	members := map[string]bool{}
	queue := []string{goal}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, from := range into[id] {
			if from == goal || members[from] || g.NodeByID(from) == nil {
				continue
			}
			members[from] = true
			queue = append(queue, from)
		}
	}
	tag := GoalTag(goal)
	for _, n := range g.Nodes {
		if n != nil && n.ID != goal && slices.ContainsFunc(n.Tags, func(t string) bool { return strings.EqualFold(strings.TrimSpace(t), tag) }) {
			members[n.ID] = true
		}
	}
	for id := range members {
		d.nodes[id] = g.NodeByID(id)
	}
	for _, e := range g.Edges {
		if e == nil || !e.Blocks() || !members[e.From] || !members[e.To] || e.From == e.To {
			continue
		}
		if !slices.Contains(d.upstream[e.To], e.From) {
			d.upstream[e.To] = append(d.upstream[e.To], e.From)
		}
	}
	for id := range d.upstream {
		sort.Strings(d.upstream[id])
	}
	d.order = topoOrder(d)
	return d
}

// topoOrder is Kahn's order with ties broken by title then id, so the same
// board always lists the same way.
func topoOrder(d dag) []string {
	indegree := map[string]int{}
	down := map[string][]string{}
	for id := range d.nodes {
		indegree[id] += 0
		for _, up := range d.upstream[id] {
			indegree[id]++
			down[up] = append(down[up], id)
		}
	}
	less := func(a, b string) bool {
		ta, tb := strings.ToLower(d.title(a)), strings.ToLower(d.title(b))
		if ta != tb {
			return ta < tb
		}
		return a < b
	}
	var ready []string
	for id, n := range indegree {
		if n == 0 {
			ready = append(ready, id)
		}
	}
	var order []string
	seen := map[string]bool{}
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		seen[id] = true
		for _, next := range down[id] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	var cyclic []string
	for id := range d.nodes {
		if !seen[id] {
			cyclic = append(cyclic, id)
		}
	}
	sort.Slice(cyclic, func(i, j int) bool { return less(cyclic[i], cyclic[j]) })
	return append(order, cyclic...)
}

func (d dag) title(id string) string {
	if n := d.nodes[id]; n != nil && n.Title != "" {
		return n.Title
	}
	return id
}

// dispatchable reports whether a member has something to implement.
func (d dag) dispatchable(id string) bool {
	n := d.nodes[id]
	if n == nil {
		return false
	}
	kind := strings.TrimSpace(n.Kind)
	return kind == "" || kind == "task"
}

// inCycle reports whether a member sits on a dependency cycle (it never
// becomes ready).
func (d dag) inCycle(id string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(cur string) bool {
		for _, up := range d.upstream[cur] {
			if up == id {
				return true
			}
			if seen[up] {
				continue
			}
			seen[up] = true
			if walk(up) {
				return true
			}
		}
		return false
	}
	return walk(id)
}

// externalBlockers are the nodes outside the DAG that still hold a member
// back, by the board's own readiness (its requires expression against
// finished nodes and flags).
func externalBlockers(g *engine.Graph, rs *engine.RunState, d dag, id string) []string {
	if g == nil {
		return nil
	}
	statuses := engine.ComputeStatuses(g, rs)
	blocked := engine.Blocked(g, statuses)[id]
	var out []string
	for _, b := range blocked {
		if _, member := d.nodes[b]; !member && b != d.goal {
			out = append(out, b)
		}
	}
	return out
}
