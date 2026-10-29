package engine

import (
	"fmt"
	"regexp"
	"strings"

	"nodevas/internal/locale"
)

// Node types: what a node is, which decides how it behaves and how it looks.
//
// Every node has exactly one type. The built-in ones are fixed presets; a
// project can add its own (custom-type-*) with the same attributes, so there is
// one model for both. graph.yaml stores the list sparsely: a custom type is
// always written out, a built-in only when its shape or colour was changed.
// The table here is mirrored by web/src/domain/graph/nodeTypes.ts and pinned
// by testdata/node_types_builtin.json, which both test suites read.

// AgentMode says whether, and how, agents may work a node of this type.
type AgentMode string

const (
	AgentNone      AgentMode = "none"
	AgentDispatch  AgentMode = "dispatch"
	AgentAutopilot AgentMode = "autopilot"
)

// Completion says who may finish a node of this type.
type Completion string

const (
	CompletionAnyone Completion = "anyone" // a person, or a successful run
	CompletionPeople Completion = "people" // humans only (agents refused)
	CompletionAuto   Completion = "auto"   // derived done when prerequisites settle
	CompletionNone   Completion = "none"   // no status; outside the flow
)

// NodeTypeDef is the stored (sparse) form, Graph.NodeTypes.
type NodeTypeDef struct {
	ID               string     `yaml:"id" json:"id"`
	Label            string     `yaml:"label,omitempty" json:"label,omitempty"`
	Shape            string     `yaml:"shape,omitempty" json:"shape,omitempty"`
	Color            string     `yaml:"color,omitempty" json:"color,omitempty"`
	AgentMode        AgentMode  `yaml:"agent_mode,omitempty" json:"agentMode,omitempty"`
	Completion       Completion `yaml:"completion,omitempty" json:"completion,omitempty"`
	CountsInProgress *bool      `yaml:"counts_in_progress,omitempty" json:"countsInProgress,omitempty"`
}

// NodeType is the resolved form (every field set).
type NodeType struct {
	ID               string     `json:"id"`
	Label            string     `json:"label"` // English for built-ins; web localizes by id
	Builtin          bool       `json:"builtin"`
	Shape            string     `json:"shape"`
	Color            string     `json:"color,omitempty"`
	AgentMode        AgentMode  `json:"agentMode"`
	Completion       Completion `json:"completion"`
	CountsInProgress bool       `json:"countsInProgress"`
}

// NodeShapes are the outlines a card can be drawn with.
var NodeShapes = []string{"rect", "round", "pill", "ellipse", "diamond", "hexagon", "dashed", "thick"}

// CustomNodeTypePrefix starts every project-defined type id.
const CustomNodeTypePrefix = "custom-type-"

// MaxCustomNodeTypes caps how many types one project may define.
const MaxCustomNodeTypes = 64

// NodeKindTask is the type a node without one, or with an unknown one, has.
const NodeKindTask = "task"

var builtinNodeTypes = []NodeType{
	{ID: "task", Label: "Task", Builtin: true, Shape: "round", AgentMode: AgentDispatch, Completion: CompletionAnyone, CountsInProgress: true},
	{ID: "goal", Label: "Goal", Builtin: true, Shape: "thick", Color: "#62b2c1", AgentMode: AgentAutopilot, Completion: CompletionPeople, CountsInProgress: true},
	{ID: "scene", Label: "Scene", Builtin: true, Shape: "round", AgentMode: AgentDispatch, Completion: CompletionAnyone, CountsInProgress: true},
	{ID: "event", Label: "Event", Builtin: true, Shape: "ellipse", AgentMode: AgentDispatch, Completion: CompletionAnyone, CountsInProgress: true},
	{ID: "gate", Label: "Review", Builtin: true, Shape: "hexagon", Color: "#f0a35e", AgentMode: AgentNone, Completion: CompletionPeople, CountsInProgress: true},
	{ID: "choice", Label: "Decision", Builtin: true, Shape: "diamond", AgentMode: AgentNone, Completion: CompletionPeople, CountsInProgress: true},
	{ID: "start", Label: "Start", Builtin: true, Shape: "pill", AgentMode: AgentNone, Completion: CompletionAuto, CountsInProgress: false},
	{ID: "end", Label: "End", Builtin: true, Shape: "pill", AgentMode: AgentNone, Completion: CompletionAuto, CountsInProgress: false},
	{ID: "note", Label: "Note", Builtin: true, Shape: "dashed", AgentMode: AgentNone, Completion: CompletionNone, CountsInProgress: false},
}

// kindAliases are accepted on input only; the canonical id is what is stored.
var kindAliases = map[string]string{
	"review":   "gate",
	"decision": "choice",
}

// BuiltinNodeTypes returns a copy of the built-in table, in display order.
func BuiltinNodeTypes() []NodeType {
	out := make([]NodeType, len(builtinNodeTypes))
	copy(out, builtinNodeTypes)
	return out
}

func builtinNodeType(id string) (NodeType, bool) {
	for _, t := range builtinNodeTypes {
		if t.ID == id {
			return t, true
		}
	}
	return NodeType{}, false
}

// IsBuiltinNodeKind reports whether id names a built-in type.
func IsBuiltinNodeKind(id string) bool {
	_, ok := builtinNodeType(id)
	return ok
}

// NormalizeNodeKind trims and lower-cases a kind and resolves the input
// aliases (review→gate, decision→choice). "" stays "" (Task). A custom id
// keeps its case, because the id pattern allows upper-case letters and the
// lookup is exact.
func NormalizeNodeKind(kind string) string {
	k := strings.TrimSpace(kind)
	if strings.HasPrefix(k, CustomNodeTypePrefix) {
		return k
	}
	k = strings.ToLower(k)
	if alias, ok := kindAliases[k]; ok {
		return alias
	}
	return k
}

// resolveDef turns a stored custom definition into its resolved form.
func resolveDef(def NodeTypeDef) NodeType {
	t := NodeType{
		ID:         def.ID,
		Label:      strings.TrimSpace(def.Label),
		Shape:      def.Shape,
		Color:      def.Color,
		AgentMode:  def.AgentMode,
		Completion: def.Completion,
	}
	if t.Shape == "" {
		t.Shape = "round"
	}
	if t.AgentMode == "" {
		t.AgentMode = AgentNone
	}
	if t.Completion == "" {
		t.Completion = CompletionAnyone
	}
	if def.CountsInProgress != nil {
		t.CountsInProgress = *def.CountsInProgress
	}
	return t
}

// ResolvedNodeTypes lists built-ins with their overrides applied, then the
// project's custom types in stored order.
func (g *Graph) ResolvedNodeTypes() []NodeType {
	out := BuiltinNodeTypes()
	if g == nil {
		return out
	}
	for _, def := range g.NodeTypes {
		if IsBuiltinNodeKind(def.ID) {
			for i := range out {
				if out[i].ID == def.ID {
					if def.Shape != "" {
						out[i].Shape = def.Shape
					}
					if def.Color != "" {
						out[i].Color = def.Color
					}
				}
			}
		}
	}
	for _, def := range g.NodeTypes {
		if !IsBuiltinNodeKind(def.ID) {
			out = append(out, resolveDef(def))
		}
	}
	return out
}

// nodeTypeByID resolves one type id; ok is false for an unknown id.
func (g *Graph) nodeTypeByID(id string) (NodeType, bool) {
	if t, ok := builtinNodeType(id); ok {
		if g != nil {
			for _, def := range g.NodeTypes {
				if def.ID == id {
					if def.Shape != "" {
						t.Shape = def.Shape
					}
					if def.Color != "" {
						t.Color = def.Color
					}
				}
			}
		}
		return t, true
	}
	if g == nil || !strings.HasPrefix(id, CustomNodeTypePrefix) {
		return NodeType{}, false
	}
	for _, def := range g.NodeTypes {
		if def.ID == id {
			return resolveDef(def), true
		}
	}
	return NodeType{}, false
}

// TypeOfKind resolves a kind value: "" and anything unknown are Task.
func (g *Graph) TypeOfKind(kind string) NodeType {
	k := NormalizeNodeKind(kind)
	if k == "" {
		k = NodeKindTask
	}
	if t, ok := g.nodeTypeByID(k); ok {
		return t
	}
	t, _ := g.nodeTypeByID(NodeKindTask)
	return t
}

// TypeOf resolves a node's type. "" and unknown or undefined kinds are Task.
func (g *Graph) TypeOf(n *Node) NodeType {
	if n == nil {
		return g.TypeOfKind("")
	}
	return g.TypeOfKind(n.Kind)
}

// KnownNodeKind reports whether kind is "", a built-in id, or a custom id the
// project defines. Aliases are not known here; normalize first.
func (g *Graph) KnownNodeKind(kind string) bool {
	if kind == "" {
		return true
	}
	_, ok := g.nodeTypeByID(kind)
	return ok
}

var customNodeTypeIDPattern = regexp.MustCompile(`^custom-type-[A-Za-z0-9_-]{1,48}$`)

// nodeTypeColor mirrors the store's safeCSSColor: no ':', '/', quotes or
// escapes, so a colour cannot reach url() or image-set().
var nodeTypeColor = regexp.MustCompile(`^[#a-zA-Z0-9(),.% -]*$`)

// ValidNodeShape reports whether s is "" or one of NodeShapes.
func ValidNodeShape(s string) bool {
	if s == "" {
		return true
	}
	for _, shape := range NodeShapes {
		if shape == s {
			return true
		}
	}
	return false
}

// ValidateNodeTypeDefs checks a stored node_types list; it returns every
// problem joined, or nil.
func ValidateNodeTypeDefs(defs []NodeTypeDef) error {
	problems := NodeTypeDefProblems(defs)
	if len(problems) == 0 {
		return nil
	}
	return locale.Errorf("node_types: %s", "node_types：%s", strings.Join(problems, "; "))
}

// NodeTypeDefProblems lists each rule a node_types list breaks.
func NodeTypeDefProblems(defs []NodeTypeDef) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	seen := map[string]bool{}
	labels := map[string]bool{}
	customs := 0
	for _, def := range defs {
		if seen[def.ID] {
			add("duplicate node type %q", def.ID)
			continue
		}
		seen[def.ID] = true
		if !ValidNodeShape(def.Shape) {
			add("node type %q has unknown shape %q", def.ID, def.Shape)
		}
		if len(def.Color) > 32 || !nodeTypeColor.MatchString(def.Color) {
			add("node type %q has an invalid color", def.ID)
		}
		if IsBuiltinNodeKind(def.ID) {
			if def.Label != "" || def.AgentMode != "" || def.Completion != "" || def.CountsInProgress != nil {
				add("built-in type %q: only shape and color can change", def.ID)
			}
			continue
		}
		customs++
		if !customNodeTypeIDPattern.MatchString(def.ID) || !ValidNodeID(def.ID) {
			add("invalid custom node type id %q", def.ID)
		}
		label := strings.TrimSpace(def.Label)
		if label == "" || len([]rune(label)) > 64 {
			add("custom node type %q has an empty or oversized label", def.ID)
		} else {
			key := strings.ToLower(label)
			if labels[key] {
				add("duplicate custom node type label %q", label)
			}
			labels[key] = true
		}
		switch def.AgentMode {
		case AgentNone, AgentDispatch, AgentAutopilot:
		default:
			add("custom node type %q has invalid agent mode %q", def.ID, def.AgentMode)
		}
		switch def.Completion {
		case CompletionAnyone, CompletionPeople, CompletionAuto, CompletionNone:
		default:
			add("custom node type %q has invalid completion %q", def.ID, def.Completion)
		}
		if def.CountsInProgress == nil {
			add("custom node type %q must say whether it counts in progress", def.ID)
		}
		if (def.Completion == CompletionAuto || def.Completion == CompletionNone) && def.AgentMode != AgentNone {
			add("custom node type %q: a %s type cannot be worked by agents", def.ID, def.Completion)
		}
		if def.Completion == CompletionNone && def.CountsInProgress != nil && *def.CountsInProgress {
			add("custom node type %q: a type without status cannot count in progress", def.ID)
		}
		if def.AgentMode == AgentAutopilot && def.Completion != CompletionAnyone && def.Completion != CompletionPeople {
			add("custom node type %q: an Autopilot type must be completed by anyone or by people", def.ID)
		}
	}
	if customs > MaxCustomNodeTypes {
		add("too many custom node types (maximum %d)", MaxCustomNodeTypes)
	}
	return problems
}
