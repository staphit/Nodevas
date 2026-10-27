package engine

import (
	"nodevas/internal/identity"
)

// Agent roles: what an agent run may do on the board (spec: agent roles and
// workflow stages). Built-ins are fixed presets; a project can add its own
// (custom-role-*). graph.yaml stores the list sparsely under agent_roles.
// Mirrored by web/src/domain/graph/roles.ts, pinned by
// testdata/agent_roles_builtin.json.
//
// RED PHASE STUB: declarations only, no behavior.

type RoleID = string

const (
	RolePlanner    = "planner"
	RoleQA         = "qa"
	RoleTester     = "tester"
	RoleExecutor   = "executor"
	RoleReviewer   = "reviewer"
	RoleResearcher = "researcher"
)

// CustomRolePrefix starts every project-defined role id.
const CustomRolePrefix = "custom-role-"

// MaxCustomRoles caps how many roles one project may define.
const MaxCustomRoles = 32

// MaxRoleInstructionsBytes caps a custom role's instructions.
const MaxRoleInstructionsBytes = 2 << 10

type Capability string

const (
	CapRead       Capability = "read"
	CapClaim      Capability = "claim"
	CapStatus     Capability = "status"
	CapBody       Capability = "body"
	CapCreate     Capability = "create"
	CapLink       Capability = "link"
	CapMeta       Capability = "meta"
	CapAcceptance Capability = "acceptance"
	CapMessage    Capability = "message"
)

// Capabilities lists every capability in display order.
var Capabilities = []Capability{}

// ToolCapabilities maps each MCP tool to the capabilities it needs. The single
// source: the MCP server and the hub both read it.
var ToolCapabilities = map[string][]Capability{}

// AgentRoleDef is the stored (sparse) form, Graph.AgentRoles.
type AgentRoleDef struct {
	ID              string   `yaml:"id" json:"id"`
	Label           string   `yaml:"label,omitempty" json:"label,omitempty"`
	BasedOn         string   `yaml:"based_on,omitempty" json:"basedOn,omitempty"`
	Rank            string   `yaml:"rank,omitempty" json:"rank,omitempty"`
	Tools           []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	WriteTypes      []string `yaml:"write_types,omitempty" json:"writeTypes,omitempty"`
	AcceptanceTypes []string `yaml:"acceptance_types,omitempty" json:"acceptanceTypes,omitempty"`
	Instructions    string   `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

// AgentRole is the resolved form (every field set).
type AgentRole struct {
	ID              string              `json:"id"`
	Label           string              `json:"label"`
	Builtin         bool                `json:"builtin"`
	BasedOn         string              `json:"basedOn,omitempty"`
	Rank            identity.AgentClass `json:"rank"`
	Tools           []string            `json:"tools"`
	WriteTypes      []string            `json:"writeTypes"`
	AcceptanceTypes []string            `json:"acceptanceTypes"`
	Caps            []Capability        `json:"caps"`
	Instructions    string              `json:"instructions,omitempty"`
}

// BuiltinAgentRoles returns a copy of the built-in table, in display order.
func BuiltinAgentRoles() []AgentRole { return nil }

// IsBuiltinRole reports whether id names a built-in role.
func IsBuiltinRole(id string) bool { return false }

// ValidRoleID reports whether id is a built-in id or a well-formed custom id.
func ValidRoleID(id string) bool { return false }

// CapsForTools is the union of the capabilities tools need, in Capabilities
// order.
func CapsForTools(tools []string) []Capability { return nil }

// ResolvedAgentRoles lists built-ins with overrides applied, then custom roles.
func (g *Graph) ResolvedAgentRoles() []AgentRole { return nil }

// RoleOf resolves one role id against the project.
func (g *Graph) RoleOf(id string) (AgentRole, bool) { return AgentRole{}, false }

// Slot is the built-in pipeline slot a role fills: its own id for a built-in,
// BasedOn for a custom role.
func (r AgentRole) Slot() string { return "" }

// HasCap reports whether the role holds a capability.
func (r AgentRole) HasCap(c Capability) bool { return false }

// RoleClass is the agent class a role id ranks as in g: orchestrator for the
// planner and QA, worker otherwise; "" when the role is unknown.
func RoleClass(g *Graph, id string) identity.AgentClass { return "" }

// ValidateAgentRoleDefs checks a stored agent_roles list.
func ValidateAgentRoleDefs(defs []AgentRoleDef) error { return nil }
