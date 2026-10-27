// Agent roles on the board: which role may do what to which node type, the
// acceptance lock, and the workflow stage gates (spec: agent roles and
// workflow stages, section 2.4).
//
// RED PHASE STUB: declarations only, no behavior.

package store

import (
	"errors"

	"nodevas/internal/engine"
	"nodevas/internal/identity"
)

// Role and stage error codes, shared with the web client.
const (
	CodeRoleDenied         = "ROLE_DENIED"              // 403
	CodeStageGatePending   = "STAGE_GATE_PENDING"       // 409
	CodeAcceptanceLocked   = "ACCEPTANCE_LOCKED"        // 409
	CodeRolesAgentEdit     = "AGENT_ROLES_AGENT_DENIED" // 403
	CodeWorkflowAgentEdit  = "WORKFLOW_AGENT_DENIED"    // 403
	CodeAcceptanceRequired = "ACCEPTANCE_REQUIRED"      // 409
	CodeUnknownRole        = "UNKNOWN_ROLE"             // 403
)

// ErrRoleDenied is a role, stage or acceptance refusal. Status is the HTTP
// status the API answers with.
type ErrRoleDenied struct {
	NodeID, Role, Code, Msg string
	Status                  int
}

func (e *ErrRoleDenied) Error() string { return e.Msg }

var errRolesNotImplemented = errors.New("agent roles: not implemented")

// SetAcceptance replaces a node's acceptance items (POST
// /api/nodes/:id/acceptance, the set_acceptance tool). Item ids are kept;
// new items get the next free aN id. Returns the new graph revision.
func (s *Store) SetAcceptance(actor identity.Actor, nodeID string, items []engine.AcceptanceItem, baseRev string) (string, error) {
	return "", errRolesNotImplemented
}

// LockAcceptance locks a node's acceptance as the hub system actor, at
// Executor dispatch.
func (s *Store) LockAcceptance(nodeID string) error { return errRolesNotImplemented }

// AppendEvidence records a pipeline evidence event (red/green run, diff
// guard, judge checks) on a node's journal. Replay ignores it for status.
func (s *Store) AppendEvidence(nodeID, kind, summary string) error { return errRolesNotImplemented }

// CreatedByRun is the run whose agent created a node, or "".
func (s *Store) CreatedByRun(nodeID string) string { return "" }
