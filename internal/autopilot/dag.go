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
// Only members whose node type has agent mode "dispatch" are dispatched (Task,
// Scene, Event and dispatch-mode custom types). The others stay in the DAG for
// ordering: a type a person completes (Review, Decision, a goal inside the
// goal) holds until the board shows it settled; an automatic one (Start, End)
// is skipped, and a skipped task satisfies whatever waits on it. A node
// without status (Note) is reference material and never a member.

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
	// typeOf resolves a member's node type against the graph it came from.
	typeOf func(*engine.Node) engine.NodeType
}

// buildDAG collects the goal's members from the graph. A cycle among them
// (the board allows drawing one) leaves its nodes at the end of the order,
// where nothing ever schedules them; the orchestrator fails them instead.
func buildDAG(g *engine.Graph, goal string) dag {
	d := dag{goal: goal, upstream: map[string][]string{}, nodes: map[string]*engine.Node{}, typeOf: g.TypeOf}
	if g == nil || g.NodeByID(goal) == nil {
		return d
	}
	noStatus := func(id string) bool {
		n := g.NodeByID(id)
		return n != nil && g.TypeOf(n).Completion == engine.CompletionNone
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
			if from == goal || members[from] || g.NodeByID(from) == nil || noStatus(from) {
				continue
			}
			members[from] = true
			queue = append(queue, from)
		}
	}
	tag := GoalTag(goal)
	for _, n := range g.Nodes {
		if n != nil && n.ID != goal && !noStatus(n.ID) && slices.ContainsFunc(n.Tags, func(t string) bool { return strings.EqualFold(strings.TrimSpace(t), tag) }) {
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

// Member is one of a goal's tasks as GoalMembers lists it.
type Member struct {
	ID    string
	Title string
	Kind  string
	// After are the members it waits on, by id.
	After []string
}

// GoalMembers is the goal's DAG in the order the autopilot works it: every
// member, upstream first, with the members each waits on. The voice copilot
// reads it to summarize a finished breakdown.
func GoalMembers(g *engine.Graph, goal string) []Member {
	d := buildDAG(g, goal)
	out := make([]Member, 0, len(d.order))
	for _, id := range d.order {
		n := d.nodes[id]
		m := Member{ID: id, Title: d.title(id), After: append([]string(nil), d.upstream[id]...)}
		if n != nil {
			m.Kind = n.Kind
		}
		out = append(out, m)
	}
	return out
}

func (d dag) title(id string) string {
	if n := d.nodes[id]; n != nil && n.Title != "" {
		return n.Title
	}
	return id
}

// dispatchable reports whether a member has something an agent implements:
// its type's agent mode is dispatch.
func (d dag) dispatchable(id string) bool {
	n := d.nodes[id]
	if n == nil {
		return false
	}
	return d.nodeType(id).AgentMode == engine.AgentDispatch
}

// nodeType is a member's resolved type (Task when unknown).
func (d dag) nodeType(id string) engine.NodeType {
	if d.typeOf == nil {
		return (*engine.Graph)(nil).TypeOf(d.nodes[id])
	}
	return d.typeOf(d.nodes[id])
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
