// Per-node write permission: which class of actor may modify a node.
//
// The check lives in the store rather than in the HTTP handlers because every
// write path already resolves the node under the store's lock; a handler-side
// check would be one more thing each new route has to remember.

package store

import (
	"fmt"
	"net/http"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// ErrNodeWriteDenied is returned when an agent tries to modify a node whose
// write access outranks it. Humans are never denied.
type ErrNodeWriteDenied struct {
	NodeID string
	Access string
	Class  identity.AgentClass
}

func (e *ErrNodeWriteDenied) Error() string {
	if e.Access == engine.WriteAccessHumanOnly {
		return fmt.Sprintf("node %q is human-only: agents may not modify it", e.NodeID)
	}
	return fmt.Sprintf("node %q requires %s access; this agent is a %s",
		e.NodeID, e.Access, e.Class)
}

// writeAccessRank orders a node's requirement. Unknown values rank as
// unrestricted, but validation refuses them before they can be stored.
func writeAccessRank(access string) int {
	switch access {
	case engine.WriteAccessWorker:
		return 1
	case engine.WriteAccessOrchestrator:
		return 2
	case engine.WriteAccessHumanOnly:
		return 3
	default:
		return 0
	}
}

// agentRank orders an actor's class: human > orchestrator > worker.
func agentRank(class identity.AgentClass) int {
	switch class {
	case identity.AgentWorker:
		return 1
	case identity.AgentOrchestrator:
		return 2
	default:
		return 3 // human
	}
}

// checkNodeWrite is the single write-permission decision: an actor may modify
// a node when its rank meets the node's requirement. A nil node passes, so
// call sites that gate "the node if it still exists" stay one line.
func checkNodeWrite(actor identity.Actor, n *engine.Node) error {
	if n == nil || !actor.IsAgent() {
		return nil
	}
	if agentRank(actor.Agent) >= writeAccessRank(n.WriteAccess) {
		return nil
	}
	return &ErrNodeWriteDenied{NodeID: n.ID, Access: n.WriteAccess, Class: actor.Agent}
}

// normalizeWriteAccess maps the spelled-out "all" a client may send onto the
// stored zero value.
func normalizeWriteAccess(v string) string {
	if v == "all" {
		return engine.WriteAccessAll
	}
	return v
}

// ErrNodeTypeDenied is returned when a node's type forbids what was asked:
// an agent working a type agents may not work, an agent finishing a
// people-only type, any status on a type without status, or an unknown type.
// Status is the HTTP status the API answers with.
type ErrNodeTypeDenied struct {
	NodeID, Type, Code, Msg string
	Status                  int
}

func (e *ErrNodeTypeDenied) Error() string { return e.Msg }

// Node-type error codes, shared with the web client.
const (
	CodeNodeTypeForbidsAgent = "NODE_TYPE_FORBIDS_AGENT" // 403/409
	CodeNodeTypePeopleOnly   = "NODE_TYPE_PEOPLE_ONLY"   // 403
	CodeNodeTypeNoStatus     = "NODE_TYPE_NO_STATUS"     // 400
	CodeUnknownNodeType      = "UNKNOWN_NODE_TYPE"       // 400
	CodeNodeTypesAgentEdit   = "NODE_TYPES_AGENT_DENIED" // 403
)

// typeName is how a denial names a type: its label, falling back to the id.
func typeName(t engine.NodeType) string {
	if t.Label != "" {
		return t.Label
	}
	return t.ID
}

// checkNodeTypesEdit refuses an agent that changes the project's type list.
// Types decide what agents may do, so an agent may not redefine them.
func checkNodeTypesEdit(actor identity.Actor, before, after []engine.NodeTypeDef) error {
	if !actor.IsAgent() || nodeTypeDefsEqual(before, after) {
		return nil
	}
	return &ErrNodeTypeDenied{
		Code: CodeNodeTypesAgentEdit, Status: http.StatusForbidden,
		Msg: "agents may not change the project's node types",
	}
}

func nodeTypeDefsEqual(a, b []engine.NodeTypeDef) bool {
	if len(a) != len(b) {
		return false // nil and empty both have length 0
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.Label != y.Label || x.Shape != y.Shape || x.Color != y.Color ||
			x.AgentMode != y.AgentMode || x.Completion != y.Completion {
			return false
		}
		if (x.CountsInProgress == nil) != (y.CountsInProgress == nil) ||
			(x.CountsInProgress != nil && *x.CountsInProgress != *y.CountsInProgress) {
			return false
		}
	}
	return true
}

// restrictedForAgents reports whether a type's completion keeps agents out of
// its status: people-only, automatic and no-status types.
func restrictedForAgents(t engine.NodeType) bool {
	switch t.Completion {
	case engine.CompletionPeople, engine.CompletionAuto, engine.CompletionNone:
		return true
	}
	return false
}

// checkKindChange refuses an agent that moves a node into or out of a type
// whose status agents may not set; otherwise retyping would be a way round
// the status rule.
func checkKindChange(actor identity.Actor, g *engine.Graph, oldKind, newKind string) error {
	if !actor.IsAgent() || oldKind == newKind {
		return nil
	}
	oldType, newType := g.TypeOfKind(oldKind), g.TypeOfKind(newKind)
	if oldType.ID == newType.ID {
		return nil
	}
	for _, t := range []engine.NodeType{oldType, newType} {
		if restrictedForAgents(t) {
			return &ErrNodeTypeDenied{
				Type: t.ID, Code: CodeNodeTypePeopleOnly, Status: http.StatusForbidden,
				Msg: fmt.Sprintf("agents may not change a node to or from a %s node", typeName(t)),
			}
		}
	}
	return nil
}

// checkStatusWrite applies the node type's completion rule to a status write:
// a type without status takes none from anybody, an automatic type takes none
// from agents, and a people-only type cannot be settled by an agent.
func checkStatusWrite(actor identity.Actor, g *engine.Graph, n *engine.Node, status engine.Status) error {
	if n == nil {
		return nil
	}
	t := g.TypeOf(n)
	if t.Completion == engine.CompletionNone {
		return noStatusError(n.ID, t)
	}
	if !actor.IsAgent() {
		return nil
	}
	var custom []engine.StatusDefinition
	if g.UI != nil {
		custom = g.UI.CustomStatuses
	}
	if t.Completion == engine.CompletionAuto ||
		(t.Completion == engine.CompletionPeople && engine.Settled(status, custom)) {
		return &ErrNodeTypeDenied{
			NodeID: n.ID, Type: t.ID, Code: CodeNodeTypePeopleOnly, Status: http.StatusForbidden,
			Msg: fmt.Sprintf("node %q is a %s node: only a person can complete it", n.ID, typeName(t)),
		}
	}
	return nil
}

func noStatusError(nodeID string, t engine.NodeType) error {
	return &ErrNodeTypeDenied{
		NodeID: nodeID, Type: t.ID, Code: CodeNodeTypeNoStatus, Status: http.StatusBadRequest,
		Msg: fmt.Sprintf("node %q is a %s node: it has no status", nodeID, typeName(t)),
	}
}

func unknownNodeTypeError(nodeID, kind string) error {
	return &ErrNodeTypeDenied{
		NodeID: nodeID, Type: kind, Code: CodeUnknownNodeType, Status: http.StatusBadRequest,
		Msg: fmt.Sprintf("unknown node type %q", kind),
	}
}
