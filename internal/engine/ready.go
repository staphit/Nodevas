package engine

import (
	"sort"
	"strings"

	"nodevas/internal/engine/dsl"
)

// Readiness: which nodes could be picked up right now.
//
// This is not what ComputeStatuses answers. There, `ready` means only "nobody
// has moved this node yet", because a status is an editable record rather than
// a workflow lock — a person may mark anything done in any order, and the
// editor deliberately does not stop them. That is the right rule for a canvas
// somebody is drawing on, and the wrong one for an agent asking what to work
// on: it would hand back every untouched node in the graph, prerequisites
// unmet, and the agent would start at the end.
//
// So readiness is computed separately and advisory. It gates nothing: nothing
// here can refuse a transition, and CanTransition still accepts any valid
// target. It only answers "what is actionable", which is a question the graph
// has always been able to answer and nothing has ever asked it.
//
// The dependency half mirrors the editor's own blocked/blocking analysis
// (web/src/analysis.ts) so the agent's list agrees with the picture a person is
// looking at. The two must be changed together; they are duplicated because the
// canvas computes it live in the browser and the agent cannot.

// builtinSettled are the statuses that always count as finished. `deprecated`
// is here for the same reason as in the editor: work that was dropped is not
// work that is still owed, so it must not hold up what comes after it.
var builtinSettled = map[Status]bool{
	StatusDone:       true,
	StatusSkipped:    true,
	StatusDeprecated: true,
}

// Settled reports whether a status satisfies whatever depends on it. A
// project-defined state does when it is marked settled.
func Settled(status Status, definitions []StatusDefinition) bool {
	if builtinSettled[status] {
		return true
	}
	for _, definition := range definitions {
		if definition.ID == string(status) && definition.Settled {
			return true
		}
	}
	return false
}

// statusDefinitions pulls the project's custom states out of the graph.
func statusDefinitions(g *Graph) []StatusDefinition {
	if g == nil || g.UI == nil {
		return nil
	}
	return g.UI.CustomStatuses
}

// gateSatisfied applies a logic gate to the set of finished nodes.
//
// Every operator but `must` needs at least two inputs: a half-wired gate is
// kept on the canvas as a visible draft, and a draft must not be read as a
// satisfied condition just because it has nothing to check.
func gateSatisfied(gate LogicGate, done map[string]bool) bool {
	completed := 0
	for _, id := range gate.Inputs {
		if done[id] {
			completed++
		}
	}
	switch gate.Operator {
	case "must":
		return len(gate.Inputs) == 1 && completed == 1
	case "and":
		return len(gate.Inputs) >= 2 && completed == len(gate.Inputs)
	case "or":
		return len(gate.Inputs) >= 2 && completed >= 1
	case "xor":
		return len(gate.Inputs) >= 2 && completed == 1
	case "nand":
		return len(gate.Inputs) >= 2 && completed < len(gate.Inputs)
	case "nor":
		return len(gate.Inputs) >= 2 && completed == 0
	}
	return false
}

// gateBlockers explains a false gate using node ids only. Positive gates wait
// on unfinished inputs. Negative gates are held back by the completed inputs
// that make their boolean condition false. An unwired gate has no input ids,
// so callers must use gateSatisfied — not slice length — to distinguish it
// from a satisfied gate.
func gateBlockers(gate LogicGate, done map[string]bool) []string {
	if gateSatisfied(gate, done) {
		return nil
	}
	inputs := dedupe(gate.Inputs)
	completed := make([]string, 0, len(inputs))
	pending := make([]string, 0, len(inputs))
	for _, id := range inputs {
		if done[id] {
			completed = append(completed, id)
		} else {
			pending = append(pending, id)
		}
	}

	switch gate.Operator {
	case "xor":
		if len(completed) > 1 {
			return completed
		}
	case "nand", "nor":
		if len(completed) > 0 {
			return completed
		}
	case "must", "and", "or":
		// These gates are false while required inputs are still pending.
	default:
		// Unknown and malformed gates fail closed too.
	}
	if len(pending) > 0 {
		return pending
	}
	return inputs
}

func readinessGates(g *Graph) map[string]LogicGate {
	gates := map[string]LogicGate{}
	if g == nil || g.UI == nil {
		return gates
	}
	for _, gate := range g.UI.LogicGates {
		// Relation gates describe their edges; they are not boolean conditions
		// on a singular output. Legacy files can still carry Output on one, so
		// exclude them explicitly to keep Go aligned with the Web analyzer.
		if gate.Operator == "optional" || gate.Operator == "deprecated" {
			continue
		}
		if gate.Output != "" {
			gates[gate.Output] = gate
		}
	}
	return gates
}

func logicGateComplete(gate LogicGate) bool {
	switch gate.Operator {
	case "must":
		return len(gate.Inputs) == 1 && gate.Output != ""
	case "and", "or", "xor", "nand", "nor":
		return len(gate.Inputs) >= 2 && gate.Output != ""
	}
	return false
}

// Blocked returns every node whose executable `requires` condition is false.
// Required edges are only the canvas projection of that expression: an old
// edge-only write must never make the Web analyzer or an agent invent a lock.
// This public view uses graph-default flags; ComputeReadiness supplies runtime
// overrides through blockedWithFlags below.
//
// Nodes that are themselves settled are left out: a finished node is not
// waiting for anything, whatever its condition says.
func Blocked(g *Graph, statuses map[string]Status) map[string][]string {
	flags := map[string]any{}
	if g != nil {
		for name, value := range g.Flags {
			flags[name] = value
		}
	}
	return blockedWithFlags(g, statuses, flags)
}

func blockedWithFlags(g *Graph, statuses map[string]Status, flags map[string]any) map[string][]string {
	blocked := map[string][]string{}
	if g == nil {
		return blocked
	}
	definitions := statusDefinitions(g)
	done := map[string]bool{}
	for _, node := range g.Nodes {
		if node == nil {
			continue
		}
		if Settled(statusOf(statuses, node.ID), definitions) {
			done[node.ID] = true
		}
	}

	gates := readinessGates(g)
	context := dsl.Context{
		Done:  func(id string) bool { return done[id] },
		Flags: flags,
	}

	for _, node := range g.Nodes {
		if node == nil || Settled(statusOf(statuses, node.ID), definitions) {
			continue
		}
		if gate, hasGate := gates[node.ID]; hasGate && !logicGateComplete(gate) {
			blocked[node.ID] = gateBlockers(gate, done)
			continue
		}
		expr, parseErr := node.RequiresExpr()
		if parseErr != nil {
			blocked[node.ID] = nil // malformed conditions fail closed
			continue
		}
		if !dsl.Eval(expr, context) {
			blocked[node.ID] = requiresBlockers(expr, done)
		}
	}
	return blocked
}

func requiresBlockers(expr dsl.Expr, done map[string]bool) []string {
	refs := dedupe(dsl.NodeRefs(expr))
	completed := make([]string, 0, len(refs))
	pending := make([]string, 0, len(refs))
	for _, id := range refs {
		if done[id] {
			completed = append(completed, id)
		} else {
			pending = append(pending, id)
		}
	}
	switch value := expr.(type) {
	case *dsl.Unary:
		if len(completed) > 0 {
			return completed
		}
		return refs
	case *dsl.Binary:
		if value.Op == "xor" && len(completed) > 1 {
			return completed
		}
		if value.Op == "nand" || value.Op == "nor" {
			if len(completed) > 0 {
				return completed
			}
			return refs
		}
	}
	if len(pending) > 0 {
		return pending
	}
	return completed
}

// statusOf is ComputeStatuses' answer for one node, defaulting the way the rest
// of the system does.
func statusOf(statuses map[string]Status, id string) Status {
	if status, ok := statuses[id]; ok {
		return status
	}
	return StatusReady
}

func dedupe(ids []string) []string {
	if len(ids) < 2 {
		return ids
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique
}

// ReadyNode is one actionable task.
type ReadyNode struct {
	ID       string   `json:"id"`
	Title    string   `json:"title,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Assignee string   `json:"assignee,omitempty"`
	Deadline string   `json:"deadline,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	// BlockedBy identifies the inputs relevant to a blocked prerequisite or
	// gate condition. It can be empty for an unwired gate whose condition is
	// false; Reason and the gate fields still explain why the node is blocked.
	BlockedBy []string `json:"blockedBy,omitempty"`
	// Reason says why a node is not ready, for the blocked listing:
	// "prerequisites", "gate_condition", or "requires".
	Reason string `json:"reason,omitempty"`
	// GateID and GateOperator make a false boolean condition distinguishable
	// from unfinished prerequisites. They are present only for gate_condition.
	GateID       string `json:"gateId,omitempty"`
	GateOperator string `json:"gateOperator,omitempty"`
}

// Readiness is the whole picture: what can be worked on, and what cannot.
type Readiness struct {
	Ready   []ReadyNode
	Blocked []ReadyNode
	// Busy counts nodes somebody already moved off ready — started, done,
	// failed and the rest. They are neither actionable nor waiting.
	Busy int
}

// ComputeReadiness sorts every node in the graph into ready, blocked or busy.
//
// A node is ready when nobody has moved it yet and its `requires` expression
// holds against completed nodes and the effective flags. Required edges are a
// visual projection and are deliberately not consulted. This is advisory, not
// a lifecycle lock: a person can still move any node to any valid status.
func ComputeReadiness(g *Graph, rs *RunState) Readiness {
	result := Readiness{}
	if g == nil {
		return result
	}
	statuses := ComputeStatuses(g, rs)
	flags := EffectiveFlags(g, rs)
	blocked := blockedWithFlags(g, statuses, flags)
	gates := readinessGates(g)

	for _, node := range g.Nodes {
		if node == nil {
			continue
		}
		if statusOf(statuses, node.ID) != StatusReady {
			result.Busy++
			continue
		}
		entry := ReadyNode{
			ID:       node.ID,
			Title:    node.Title,
			Kind:     node.Kind,
			Priority: node.Priority,
			Assignee: node.Assignee,
			Deadline: node.Deadline,
			Tags:     node.Tags,
		}
		if waiting, isBlocked := blocked[node.ID]; isBlocked {
			entry.BlockedBy = waiting
			if gate, ok := gates[node.ID]; ok {
				entry.Reason = "gate_condition"
				entry.GateID = gate.ID
				entry.GateOperator = gate.Operator
			} else if len(waiting) > 0 {
				entry.Reason = "prerequisites"
			} else {
				entry.Reason = "requires"
			}
			result.Blocked = append(result.Blocked, entry)
			continue
		}
		result.Ready = append(result.Ready, entry)
	}
	sortTasks(result.Ready)
	sortTasks(result.Blocked)
	return result
}

// priorityRank orders the queue. An unset priority sorts after every stated
// one: "nobody said" is weaker evidence than "somebody said low".
func priorityRank(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "urgent":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	}
	return 4
}

// sortTasks gives the queue a total order, so the same graph always produces
// the same list and an agent paging through it cannot see a node twice or miss
// one. Priority first, then the nearest deadline, then the id.
//
// A node with no deadline sorts after every node that has one, rather than
// sorting as the empty string and coming first.
func sortTasks(tasks []ReadyNode) {
	sort.SliceStable(tasks, func(a, b int) bool {
		left, right := tasks[a], tasks[b]
		if rank, other := priorityRank(left.Priority), priorityRank(right.Priority); rank != other {
			return rank < other
		}
		if left.Deadline != right.Deadline {
			if left.Deadline == "" {
				return false
			}
			if right.Deadline == "" {
				return true
			}
			return left.Deadline < right.Deadline
		}
		return left.ID < right.ID
	})
}
