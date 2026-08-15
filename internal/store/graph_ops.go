// Server-side graph commands [P2].
//
// A whole-file PUT with a base revision is the right shape for one editor: it
// refuses to overwrite work it has not seen. With two editors it is the wrong
// shape — dragging a card and renaming a different node touch the same file,
// so one of them loses to a 409 that the user can only resolve by redoing the
// work.
//
// These commands are applied by the server under the store's write lock. Each
// one names the smallest thing it changes, so two people editing different
// parts of the same graph never collide. graph.yaml stays structured data
// edited by commands; it is deliberately not a CRDT, because "both edits win"
// is not a meaning that a dependency graph has.

package store

import (
	"fmt"
	"math"
	"strings"

	"nodevas/internal/engine"
	"nodevas/internal/engine/dsl"
	"nodevas/internal/identity"
)

// maxGraphOps bounds one request. A drag emits one op per selected node, so
// this is a selection bound.
const maxGraphOps = 2_000

// GraphOp is one change to graph.yaml.
type GraphOp struct {
	// Kind is one of: move | node-size | node-metadata | add-edge |
	// remove-edge | set-edge-style | timeline-order.
	Kind string `json:"kind"`

	NodeID string   `json:"nodeId,omitempty"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`

	// Metadata fields; a nil pointer means "leave alone", which is what makes
	// two people editing different fields of one node safe.
	Title       *string   `json:"title,omitempty"`
	Kind_       *string   `json:"nodeKind,omitempty"`
	Priority    *string   `json:"priority,omitempty"`
	Assignee    *string   `json:"assignee,omitempty"`
	Deadline    *string   `json:"deadline,omitempty"`
	WriteAccess *string   `json:"writeAccess,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`

	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Relation and Line are pointers because an empty string is a meaningful
	// final value: it means required/default rather than "leave unchanged".
	Relation *string `json:"relation,omitempty"`
	Line     *string `json:"line,omitempty"`

	Order []string `json:"order,omitempty"`
}

// ApplyGraphOps applies a batch of commands atomically and returns the new
// graph revision.
//
// The batch is all-or-nothing: a request that is half-applied would leave the
// user looking at a board that matches neither what they did nor what they
// had.
func (s *Store) ApplyGraphOps(actor identity.Actor, ops []GraphOp) (*engine.Graph, string, error) {
	if len(ops) == 0 {
		return nil, "", fmt.Errorf("no operations given")
	}
	if len(ops) > maxGraphOps {
		return nil, "", fmt.Errorf("too many operations in one request (maximum %d)", maxGraphOps)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	graph, _, err := s.loadGraphLocked()
	if err != nil {
		return nil, "", err
	}
	if graph.UI == nil {
		graph.UI = &engine.UIState{}
	}
	touched := map[string]bool{}
	for _, op := range ops {
		if err := applyGraphOp(actor, graph, op, touched); err != nil {
			return nil, "", err
		}
	}
	if err := ValidateGraphForStorage(graph); err != nil {
		return nil, "", err
	}
	data, err := s.marshalGraph(graph)
	if err != nil {
		return nil, "", err
	}
	updates := make([]fileUpdate, 0, len(touched)+1)
	// Node metadata lives in two places: graph.yaml and the document's
	// frontmatter. Writing only one of them is how they drift apart.
	for id := range touched {
		node := graph.NodeByID(id)
		if node == nil {
			continue
		}
		update, err := s.prepareNodeFileUpdate(node)
		if err != nil {
			return nil, "", fmt.Errorf("sync node %s: %w", id, err)
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	updates = append(updates, fileUpdate{path: s.GraphPath(), data: data})
	if err := s.applyUpdatesLocked(updates); err != nil {
		return nil, "", err
	}
	return graph, Rev(data), nil
}

func applyGraphOp(actor identity.Actor, graph *engine.Graph, op GraphOp, touched map[string]bool) error {
	switch op.Kind {
	// move, node-size and timeline-order stay ungated: they change how the
	// board is drawn, not what the node says, so a restricted node can still
	// be arranged by anyone.
	case "move":
		node := graph.NodeByID(op.NodeID)
		if node == nil {
			return fmt.Errorf("node %q not found", op.NodeID)
		}
		if op.X == nil || op.Y == nil || !finite(*op.X) || !finite(*op.Y) {
			return fmt.Errorf("move needs finite x and y")
		}
		if graph.UI.Positions == nil {
			graph.UI.Positions = map[string]engine.Position{}
		}
		graph.UI.Positions[op.NodeID] = engine.Position{X: *op.X, Y: *op.Y}
	case "node-size":
		if graph.NodeByID(op.NodeID) == nil {
			return fmt.Errorf("node %q not found", op.NodeID)
		}
		if op.Width == nil || op.Height == nil || !finite(*op.Width) || !finite(*op.Height) {
			return fmt.Errorf("node-size needs finite width and height")
		}
		if graph.UI.NodeStyles == nil {
			graph.UI.NodeStyles = map[string]engine.NodeStyle{}
		}
		style := graph.UI.NodeStyles[op.NodeID]
		style.Width = *op.Width
		style.Height = *op.Height
		graph.UI.NodeStyles[op.NodeID] = style
	case "node-metadata":
		node := graph.NodeByID(op.NodeID)
		if node == nil {
			return fmt.Errorf("node %q not found", op.NodeID)
		}
		if err := checkNodeWrite(actor, node); err != nil {
			return err
		}
		if op.WriteAccess != nil {
			access := normalizeWriteAccess(*op.WriteAccess)
			switch access {
			case engine.WriteAccessAll, engine.WriteAccessWorker,
				engine.WriteAccessOrchestrator, engine.WriteAccessHumanOnly:
			default:
				return fmt.Errorf("invalid write_access %q", *op.WriteAccess)
			}
			node.WriteAccess = access
		}
		if op.Title != nil {
			node.Title = *op.Title
		}
		if op.Kind_ != nil {
			node.Kind = *op.Kind_
		}
		if op.Priority != nil {
			node.Priority = *op.Priority
		}
		if op.Assignee != nil {
			node.Assignee = *op.Assignee
		}
		if op.Deadline != nil {
			node.Deadline = *op.Deadline
		}
		if op.Tags != nil {
			node.Tags = append([]string(nil), (*op.Tags)...)
		}
		touched[node.ID] = true
	case "add-edge":
		if graph.NodeByID(op.From) == nil || graph.NodeByID(op.To) == nil {
			return fmt.Errorf("edge %s->%s names a node that does not exist", op.From, op.To)
		}
		// The dependency lands in the To node's requires, so that node's
		// access is the one that matters.
		if err := checkNodeWrite(actor, graph.NodeByID(op.To)); err != nil {
			return err
		}
		if op.From == op.To {
			return fmt.Errorf("a node cannot depend on itself")
		}
		if gateID := booleanGateForOutput(graph, op.To); gateID != "" {
			return fmt.Errorf("edge %s->%s is controlled by logic gate %q", op.From, op.To, gateID)
		}
		if gateID := logicGateOwningEdge(graph, op.From, op.To); gateID != "" {
			return fmt.Errorf("edge %s->%s is controlled by logic gate %q", op.From, op.To, gateID)
		}
		target := graph.NodeByID(op.To)
		for _, edge := range graph.Edges {
			if edge != nil && edge.From == op.From && edge.To == op.To {
				edge.Relation = engine.RelationRequired
				changed, err := appendNodeRequirement(target, op.From)
				if err != nil {
					return err
				}
				if changed {
					touched[target.ID] = true
				}
				return nil // idempotent, including repair of an older edge-only write
			}
		}
		changed, err := appendNodeRequirement(target, op.From)
		if err != nil {
			return err
		}
		if changed {
			touched[target.ID] = true
		}
		graph.Edges = append(graph.Edges, &engine.Edge{From: op.From, To: op.To})
	case "remove-edge":
		if graph.NodeByID(op.From) == nil || graph.NodeByID(op.To) == nil {
			return fmt.Errorf("edge %s->%s names a node that does not exist", op.From, op.To)
		}
		if err := checkNodeWrite(actor, graph.NodeByID(op.To)); err != nil {
			return err
		}
		if gateID := logicGateOwningEdge(graph, op.From, op.To); gateID != "" {
			return fmt.Errorf("edge %s->%s is controlled by logic gate %q", op.From, op.To, gateID)
		}
		target := graph.NodeByID(op.To)
		hasRequiredEdge := false
		for _, edge := range graph.Edges {
			if edge != nil && edge.From == op.From && edge.To == op.To {
				hasRequiredEdge = hasRequiredEdge || edge.Blocks()
			}
		}
		if gateID := booleanGateForOutput(graph, op.To); gateID != "" && hasRequiredEdge {
			return fmt.Errorf("edge %s->%s is controlled by logic gate %q", op.From, op.To, gateID)
		}
		kept := graph.Edges[:0:0]
		removedRequired := false
		for _, edge := range graph.Edges {
			if edge != nil && edge.From == op.From && edge.To == op.To {
				removedRequired = removedRequired || edge.Blocks()
				continue
			}
			kept = append(kept, edge)
		}
		graph.Edges = kept
		hasIncoming := false
		for _, edge := range graph.Edges {
			if edge != nil && edge.To == op.To {
				hasIncoming = true
				break
			}
		}
		if !hasIncoming {
			delete(graph.UI.Gates, op.To)
			delete(graph.UI.WireVertices, "gate:"+op.To)
		}
		if removedRequired {
			changed, err := pruneNodeRequirement(target, op.From)
			if err != nil {
				return err
			}
			if changed {
				touched[target.ID] = true
			}
		}
	case "set-edge-style":
		if op.Relation == nil && op.Line == nil {
			return fmt.Errorf("set-edge-style needs relation or line")
		}
		var selected *engine.Edge
		for _, edge := range graph.Edges {
			if edge != nil && edge.From == op.From && edge.To == op.To {
				selected = edge
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("edge %s->%s not found", op.From, op.To)
		}
		if err := checkNodeWrite(actor, graph.NodeByID(op.To)); err != nil {
			return err
		}
		nextRelation := selected.Relation
		if op.Relation != nil {
			nextRelation = *op.Relation
		}
		if nextRelation != selected.Relation {
			if gateID := logicGateOwningEdge(graph, op.From, op.To); gateID != "" {
				return fmt.Errorf("edge %s->%s is controlled by logic gate %q", op.From, op.To, gateID)
			}
			if gateID := booleanGateForOutput(graph, op.To); gateID != "" &&
				(selected.Blocks() || nextRelation == engine.RelationRequired) {
				return fmt.Errorf("edge %s->%s is controlled by logic gate %q", op.From, op.To, gateID)
			}
			target := graph.NodeByID(op.To)
			var changed bool
			var err error
			if selected.Blocks() && nextRelation != engine.RelationRequired {
				changed, err = pruneNodeRequirement(target, op.From)
			} else if !selected.Blocks() && nextRelation == engine.RelationRequired {
				changed, err = appendNodeRequirement(target, op.From)
			}
			if err != nil {
				return err
			}
			if changed {
				touched[target.ID] = true
			}
			selected.Relation = nextRelation
		}
		if op.Line != nil {
			selected.Line = *op.Line
		}
	case "timeline-order":
		order := make([]string, 0, len(op.Order))
		for _, id := range op.Order {
			if graph.NodeByID(id) == nil {
				return fmt.Errorf("node %q not found", id)
			}
			order = append(order, id)
		}
		graph.UI.TimelineOrder = order
	default:
		return fmt.Errorf("unknown graph operation %q", strings.TrimSpace(op.Kind))
	}
	return nil
}

func containsNodeRef(expr dsl.Expr, nodeID string) bool {
	for _, ref := range dsl.NodeRefs(expr) {
		if ref == nodeID {
			return true
		}
	}
	return false
}

// appendNodeRequirement makes an edge executable without flattening the
// existing boolean/flag expression. The AST renderer adds parentheses when
// required, so `(a or b) and c` cannot silently become `a or (b and c)`.
func appendNodeRequirement(node *engine.Node, sourceID string) (bool, error) {
	expr, parseErr := dsl.Parse(node.Requires)
	if parseErr != nil {
		return false, fmt.Errorf("cannot add dependency to node %q: invalid requires expression: %w", node.ID, parseErr)
	}
	if containsNodeRef(expr, sourceID) {
		return false, nil
	}
	ref := &dsl.NodeRef{ID: sourceID}
	if expr == nil {
		node.Requires = ref.String()
	} else {
		node.Requires = (&dsl.Binary{Op: "and", L: expr, R: ref}).String()
	}
	return true, nil
}

func pruneNodeRequirement(node *engine.Node, sourceID string) (bool, error) {
	expr, parseErr := dsl.Parse(node.Requires)
	if parseErr != nil {
		return false, fmt.Errorf("cannot remove dependency from node %q: invalid requires expression: %w", node.ID, parseErr)
	}
	if expr == nil || !containsNodeRef(expr, sourceID) {
		return false, nil
	}
	pruned := dsl.PruneNodeRefs(expr, func(id string) bool { return id == sourceID })
	if pruned == nil {
		node.Requires = ""
	} else {
		node.Requires = pruned.String()
	}
	return true, nil
}

func booleanGateForOutput(graph *engine.Graph, output string) string {
	if graph == nil || graph.UI == nil {
		return ""
	}
	for _, gate := range graph.UI.LogicGates {
		if relationGateOperator(gate.Operator) {
			continue
		}
		for _, candidate := range gate.OutputNodes() {
			if candidate == output {
				return gate.ID
			}
		}
	}
	return ""
}

func logicGateOwningEdge(graph *engine.Graph, from, to string) string {
	if graph == nil || graph.UI == nil {
		return ""
	}
	for _, gate := range graph.UI.LogicGates {
		input := false
		for _, candidate := range gate.Inputs {
			if candidate == from {
				input = true
				break
			}
		}
		if !input {
			continue
		}
		for _, output := range gate.OutputNodes() {
			if output == to {
				return gate.ID
			}
		}
	}
	return ""
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
