package engine

import (
	"fmt"
	"regexp"
	"strings"

	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// Agent roles: what an agent run may do on the board (spec: agent roles and
// workflow stages). Built-ins are fixed presets; a project can add its own
// (custom-role-*). graph.yaml stores the list sparsely under agent_roles.
// Mirrored by web/src/domain/graph/roles.ts, pinned by
// testdata/agent_roles_builtin.json.

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
	// CapVerdict: a reviewer's or QA's structured verdict (submit_verdict).
	CapVerdict Capability = "verdict"
)

// Capabilities lists every capability in display order.
var Capabilities = []Capability{CapRead, CapClaim, CapStatus, CapBody, CapCreate, CapLink, CapMeta, CapAcceptance, CapMessage, CapVerdict}

// ToolCapabilities maps each MCP tool to the capabilities it needs. The single
// source: the MCP server and the hub both read it.
var ToolCapabilities = map[string][]Capability{
	"get_node":          {CapRead},
	"get_graph_outline": {CapRead},
	"search_nodes":      {CapRead},
	"get_ready_tasks":   {CapRead},
	"validate_graph":    {CapRead},
	"list_projects":     {CapRead},
	"search_documents":  {CapRead},
	"index_documents":   {CapRead},
	"claim_task":        {CapClaim},
	"release_task":      {CapClaim},
	"set_node_status":   {CapStatus},
	"update_node_body":  {CapBody},
	"create_node":       {CapCreate},
	"create_subgraph":   {CapCreate, CapLink},
	"link_nodes":        {CapLink},
	"update_node_meta":  {CapMeta},
	"set_acceptance":    {CapAcceptance},
	"ask_user":          {CapMessage},
	"approve_tool":      {CapMessage},
	"check_inbox":       {CapMessage},
	"send_message":      {CapMessage},
	"list_team":         {CapMessage},
	"save_attachment":   {CapMessage},
	"submit_verdict":    {CapVerdict},
}

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

func builtinRole(id, label string, rank identity.AgentClass, tools, writeTypes, acceptanceTypes []string) AgentRole {
	return AgentRole{ID: id, Label: label, Builtin: true, Rank: rank, Tools: tools,
		WriteTypes: writeTypes, AcceptanceTypes: acceptanceTypes, Caps: CapsForTools(tools)}
}

var builtinAgentRoles = []AgentRole{
	builtinRole(RolePlanner, "Planner", identity.AgentOrchestrator,
		[]string{"get_node", "get_graph_outline", "search_nodes", "create_subgraph", "create_node", "link_nodes", "set_acceptance", "ask_user", "approve_tool", "check_inbox", "send_message"},
		[]string{"task", "scene", "event", "note", "gate", "choice"},
		[]string{"task", "scene", "event", "goal"}),
	builtinRole(RoleQA, "QA", identity.AgentOrchestrator,
		[]string{"get_node", "get_graph_outline", "set_acceptance", "create_subgraph", "link_nodes", "ask_user", "approve_tool", "check_inbox", "send_message", "submit_verdict"},
		[]string{"task", "scene", "event"},
		[]string{"goal"}),
	builtinRole(RoleTester, "Tester", identity.AgentWorker,
		[]string{"get_node", "ask_user", "approve_tool", "check_inbox", "send_message"},
		[]string{}, []string{}),
	builtinRole(RoleExecutor, "Executor", identity.AgentWorker,
		[]string{"claim_task", "get_node", "set_node_status", "release_task", "ask_user", "approve_tool", "check_inbox", "send_message"},
		[]string{"task", "scene", "event"}, []string{}),
	builtinRole(RoleReviewer, "Reviewer", identity.AgentWorker,
		[]string{"get_node", "approve_tool", "check_inbox", "send_message", "submit_verdict"},
		[]string{}, []string{}),
	builtinRole(RoleResearcher, "Researcher", identity.AgentWorker,
		[]string{"get_node", "get_graph_outline", "search_nodes", "search_documents", "create_node", "update_node_body", "link_nodes", "ask_user", "approve_tool", "check_inbox", "send_message"},
		[]string{"note"}, []string{}),
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (r AgentRole) clone() AgentRole {
	r.Tools = cloneStrings(r.Tools)
	r.WriteTypes = cloneStrings(r.WriteTypes)
	r.AcceptanceTypes = cloneStrings(r.AcceptanceTypes)
	if r.Caps != nil {
		caps := make([]Capability, len(r.Caps))
		copy(caps, r.Caps)
		r.Caps = caps
	}
	return r
}

// BuiltinAgentRoles returns a copy of the built-in table, in display order.
func BuiltinAgentRoles() []AgentRole {
	out := make([]AgentRole, len(builtinAgentRoles))
	for i, r := range builtinAgentRoles {
		out[i] = r.clone()
	}
	return out
}

func builtinAgentRole(id string) (AgentRole, bool) {
	for _, r := range builtinAgentRoles {
		if r.ID == id {
			return r.clone(), true
		}
	}
	return AgentRole{}, false
}

// IsBuiltinRole reports whether id names a built-in role.
func IsBuiltinRole(id string) bool {
	_, ok := builtinAgentRole(id)
	return ok
}

var customRoleIDPattern = regexp.MustCompile(`^custom-role-[A-Za-z0-9_-]{1,48}$`)

// ValidRoleID reports whether id is a built-in id or a well-formed custom id.
func ValidRoleID(id string) bool {
	return IsBuiltinRole(id) || customRoleIDPattern.MatchString(id)
}

// CapsForTools is the union of the capabilities tools need, in Capabilities
// order.
func CapsForTools(tools []string) []Capability {
	held := map[Capability]bool{}
	for _, tool := range tools {
		for _, c := range ToolCapabilities[tool] {
			held[c] = true
		}
	}
	var out []Capability
	for _, c := range Capabilities {
		if held[c] {
			out = append(out, c)
		}
	}
	return out
}

// applyOverride applies a project's tools/types override to a built-in role.
func applyOverride(r AgentRole, def AgentRoleDef) AgentRole {
	if def.Tools != nil {
		r.Tools = cloneStrings(def.Tools)
		r.Caps = CapsForTools(r.Tools)
	}
	if def.WriteTypes != nil {
		r.WriteTypes = cloneStrings(def.WriteTypes)
	}
	if def.AcceptanceTypes != nil {
		r.AcceptanceTypes = cloneStrings(def.AcceptanceTypes)
	}
	return r
}

func resolveCustomRole(def AgentRoleDef) AgentRole {
	r := AgentRole{
		ID:              def.ID,
		Label:           strings.TrimSpace(def.Label),
		BasedOn:         def.BasedOn,
		Rank:            identity.AgentClass(def.Rank),
		Tools:           cloneStrings(def.Tools),
		WriteTypes:      cloneStrings(def.WriteTypes),
		AcceptanceTypes: cloneStrings(def.AcceptanceTypes),
		Instructions:    def.Instructions,
	}
	if r.Tools == nil {
		r.Tools = []string{}
	}
	if r.WriteTypes == nil {
		r.WriteTypes = []string{}
	}
	if r.AcceptanceTypes == nil {
		r.AcceptanceTypes = []string{}
	}
	// Anything but an explicit orchestrator ranks as a worker: a malformed
	// rank can never raise a run.
	if r.Rank != identity.AgentOrchestrator {
		r.Rank = identity.AgentWorker
	}
	r.Caps = CapsForTools(r.Tools)
	return r
}

// ResolvedAgentRoles lists built-ins with overrides applied, then custom roles.
func (g *Graph) ResolvedAgentRoles() []AgentRole {
	out := BuiltinAgentRoles()
	if g == nil {
		return out
	}
	for _, def := range g.AgentRoles {
		if IsBuiltinRole(def.ID) {
			for i := range out {
				if out[i].ID == def.ID {
					out[i] = applyOverride(out[i], def)
				}
			}
		}
	}
	for _, def := range g.AgentRoles {
		if !IsBuiltinRole(def.ID) && customRoleIDPattern.MatchString(def.ID) {
			out = append(out, resolveCustomRole(def))
		}
	}
	return out
}

// RoleOf resolves one role id against the project.
func (g *Graph) RoleOf(id string) (AgentRole, bool) {
	if r, ok := builtinAgentRole(id); ok {
		if g != nil {
			for _, def := range g.AgentRoles {
				if def.ID == id {
					r = applyOverride(r, def)
				}
			}
		}
		return r, true
	}
	if g == nil || !customRoleIDPattern.MatchString(id) {
		return AgentRole{}, false
	}
	for _, def := range g.AgentRoles {
		if def.ID == id {
			return resolveCustomRole(def), true
		}
	}
	return AgentRole{}, false
}

// Slot is the built-in pipeline slot a role fills: its own id for a built-in,
// BasedOn for a custom role.
func (r AgentRole) Slot() string {
	if r.Builtin {
		return r.ID
	}
	return r.BasedOn
}

// HasCap reports whether the role holds a capability.
func (r AgentRole) HasCap(c Capability) bool {
	for _, held := range r.Caps {
		if held == c {
			return true
		}
	}
	return false
}

// HasTool reports whether the role's allowlist names tool.
func (r AgentRole) HasTool(tool string) bool { return containsString(r.Tools, tool) }

// MayWriteType reports whether the role may create, edit, claim or set the
// status of a node of type kind.
func (r AgentRole) MayWriteType(kind string) bool { return containsString(r.WriteTypes, kind) }

// MayAcceptType reports whether the role may write acceptance criteria on a
// node of type kind.
func (r AgentRole) MayAcceptType(kind string) bool { return containsString(r.AcceptanceTypes, kind) }

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// RoleClass is the agent class a role id ranks as in g: orchestrator for the
// planner and QA, worker otherwise; "" when the role is unknown.
func RoleClass(g *Graph, id string) identity.AgentClass {
	r, ok := g.RoleOf(id)
	if !ok {
		return ""
	}
	return r.Rank
}

// knownRoleType accepts a built-in type id or a well-formed custom type id
// (whether the project defines that custom type is a graph-level concern).
func knownRoleType(kind string) bool {
	return IsBuiltinNodeKind(kind) || customNodeTypeIDPattern.MatchString(kind)
}

// ValidateAgentRoleDefs checks a stored agent_roles list.
func ValidateAgentRoleDefs(defs []AgentRoleDef) error {
	problems := AgentRoleDefProblems(defs)
	if len(problems) == 0 {
		return nil
	}
	return locale.Errorf("agent_roles: %s", "agent_roles：%s", strings.Join(problems, "; "))
}

// AgentRoleDefProblems lists each rule an agent_roles list breaks.
func AgentRoleDefProblems(defs []AgentRoleDef) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	checkLists := func(def AgentRoleDef) {
		for _, tool := range def.Tools {
			if len(ToolCapabilities[tool]) == 0 {
				add("role %q names unknown tool %q", def.ID, tool)
			}
		}
		for _, kind := range def.WriteTypes {
			if !knownRoleType(kind) {
				add("role %q names unknown write type %q", def.ID, kind)
			}
		}
		for _, kind := range def.AcceptanceTypes {
			if !knownRoleType(kind) {
				add("role %q names unknown acceptance type %q", def.ID, kind)
			}
		}
	}
	seen := map[string]bool{}
	customs := 0
	for _, def := range defs {
		if seen[def.ID] {
			add("duplicate role %q", def.ID)
			continue
		}
		seen[def.ID] = true
		if IsBuiltinRole(def.ID) {
			if def.Label != "" || def.BasedOn != "" || def.Rank != "" || def.Instructions != "" {
				add("built-in role %q: only tools, write_types and acceptance_types can change", def.ID)
			}
			checkLists(def)
			continue
		}
		customs++
		if !customRoleIDPattern.MatchString(def.ID) {
			add("invalid custom role id %q", def.ID)
			continue
		}
		if label := strings.TrimSpace(def.Label); label == "" || len([]rune(label)) > 64 {
			add("custom role %q has an empty or oversized label", def.ID)
		}
		if !IsBuiltinRole(def.BasedOn) {
			add("custom role %q must be based on a built-in role, not %q", def.ID, def.BasedOn)
		}
		switch identity.AgentClass(def.Rank) {
		case identity.AgentWorker, identity.AgentOrchestrator:
		default:
			add("custom role %q has invalid rank %q (worker or orchestrator)", def.ID, def.Rank)
		}
		if len(def.Instructions) > MaxRoleInstructionsBytes {
			add("custom role %q: instructions exceed %d bytes", def.ID, MaxRoleInstructionsBytes)
		}
		checkLists(def)
	}
	if customs > MaxCustomRoles {
		add("too many custom roles (%d, at most %d)", customs, MaxCustomRoles)
	}
	return problems
}
