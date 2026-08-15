// Package engine implements the core graph model: parsing, validation,
// and node status computation. It performs no IO — callers hand it bytes.
package engine

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"nodevas/internal/engine/dsl"
)

var nodeIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// ValidNodeID reports whether id is safe for graph references and node
// filenames. Keeping this rule in the engine prevents API and storage
// validation from drifting apart.
func ValidNodeID(id string) bool {
	return len(id) <= 128 && nodeIDPattern.MatchString(id)
}

// ValidProjectRef reports whether a link's project path is safe to store: a
// slash-separated name with no traversal and no absolute root. It is
// deliberately stricter than the filesystem allows, because a link is
// user-typed text that ends up addressing a directory.
func ValidProjectRef(path string) bool {
	if path == "" || len(path) > 512 || strings.HasPrefix(path, "/") ||
		strings.HasSuffix(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." ||
			strings.TrimSpace(segment) != segment {
			return false
		}
		if strings.ContainsAny(segment, `:*?"<>|`) {
			return false
		}
	}
	return true
}

// Graph mirrors graph.yaml. graph.yaml is the single source of truth for
// structure and gate logic; node markdown files carry only content.
type Graph struct {
	Version int            `yaml:"version" json:"version"`
	Type    string         `yaml:"type,omitempty" json:"type,omitempty"` // workflow | story
	Users   []User         `yaml:"users,omitempty" json:"users,omitempty"`
	Nodes   []*Node        `yaml:"nodes" json:"nodes"`
	Edges   []*Edge        `yaml:"edges,omitempty" json:"edges,omitempty"`
	Flags   map[string]any `yaml:"flags,omitempty" json:"flags,omitempty"`
	UI      *UIState       `yaml:"ui,omitempty" json:"ui,omitempty"`
}

// User is a project-level assignable person. ID stays stable when the display
// name changes, so node assignments do not depend on mutable labels.
type User struct {
	ID    string `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
}

type Node struct {
	ID       string `yaml:"id" json:"id"`
	Title    string `yaml:"title,omitempty" json:"title,omitempty"`
	Kind     string `yaml:"kind,omitempty" json:"kind,omitempty"`         // task|scene|choice|gate|start|end
	Priority string `yaml:"priority,omitempty" json:"priority,omitempty"` // urgent|high|medium|low
	Assignee string `yaml:"assignee,omitempty" json:"assignee,omitempty"`
	// Deadline is a local time "2006-01-02T15:04" or date "2006-01-02"
	// (interpreted as end of that day). Empty = no deadline.
	Deadline string   `yaml:"deadline,omitempty" json:"deadline,omitempty"`
	Requires string   `yaml:"requires,omitempty" json:"requires,omitempty"`
	Tags     []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Effects  []Effect `yaml:"effects,omitempty" json:"effects,omitempty"`
	// Links are named references to other nodes in the same workspace. They
	// are not dependencies: nothing waits on them. Documents insert them by
	// label with `/`, and the editor writes `[[…]]` into the markdown.
	Links []NodeLinkRef `yaml:"links,omitempty" json:"links,omitempty"`
}

// NodeLinkRef is one named reference. Project empty means this node's own
// project, so a project can be renamed or moved without breaking its links.
type NodeLinkRef struct {
	Label   string `yaml:"label" json:"label"`
	Project string `yaml:"project,omitempty" json:"project,omitempty"`
	Node    string `yaml:"node" json:"node"`
}

type Effect struct {
	Set string `yaml:"set,omitempty" json:"set,omitempty"` // e.g. "found_cat = true"
}

// Edge is one dependency wire. Meaning and looks are separate fields: a
// required dependency may still be drawn dashed, and a deprecated one keeps
// its line while dropping out of every dependency computation.
type Edge struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
	// Relation is what the edge means: "" (required), "optional" (does not
	// block) or "deprecated" (does not block and is not a prerequisite).
	Relation string `yaml:"relation,omitempty" json:"relation,omitempty"`
	// Line is how the edge is drawn: "" (from the relation), "solid",
	// "dashed" or "dotted".
	Line string `yaml:"line,omitempty" json:"line,omitempty"`
}

// EdgeRelation values.
const (
	RelationRequired   = ""
	RelationOptional   = "optional"
	RelationDeprecated = "deprecated"
)

// Blocks reports whether the edge is a prerequisite that can hold its target
// back. Only required edges do.
func (e *Edge) Blocks() bool { return e != nil && e.Relation == RelationRequired }

// IsPrerequisite reports whether the edge counts as a dependency at all.
// A deprecated edge is kept for the record only.
func (e *Edge) IsPrerequisite() bool {
	return e != nil && e.Relation != RelationDeprecated
}

// UIState holds editor-only presentation data, kept separate from semantics.
type UIState struct {
	Positions map[string]Position `yaml:"positions,omitempty" json:"positions,omitempty"`
	// TimelineOrder is independent from graph positions and controls only the
	// node sequence shown in vertical and horizontal timelines.
	TimelineOrder []string `yaml:"timeline_order,omitempty" json:"timelineOrder,omitempty"`
	// Gates stores each target node's dependency-gate placement.
	Gates map[string]GatePlacement `yaml:"gates,omitempty" json:"gates,omitempty"`
	// LogicGates are first-class editor gates. Incomplete wiring is preserved
	// as a visible draft; complete gates are also materialized into Requires.
	LogicGates []LogicGate `yaml:"logic_gates,omitempty" json:"logicGates,omitempty"`
	// EdgeLabels stores optional-edge label placement keyed by "from->to".
	EdgeLabels map[string]EdgeLabelPlacement `yaml:"edge_labels,omitempty" json:"edgeLabels,omitempty"`
	// WireVertices stores user-defined bend points keyed by "from->to" or
	// "gate:target". Coordinates are relative to the graph content origin.
	WireVertices map[string][]Position `yaml:"wire_vertices,omitempty" json:"wireVertices,omitempty"`
	// Plans are expected milestones for timeline planning. Actual lifecycle
	// transitions are stored separately in run/journal.jsonl.
	Plans map[string][]PlanMilestone `yaml:"plans,omitempty" json:"plans,omitempty"`
	// PlanStatuses are project-wide custom expected milestone types.
	PlanStatuses []PlanStatusDefinition `yaml:"plan_statuses,omitempty" json:"planStatuses,omitempty"`
	// CustomStatuses are project-wide user-defined node lifecycle states.
	CustomStatuses []StatusDefinition `yaml:"custom_statuses,omitempty" json:"customStatuses,omitempty"`
	// NodeStyles contains visual-only card dimensions, color and outline shape.
	NodeStyles map[string]NodeStyle `yaml:"node_styles,omitempty" json:"nodeStyles,omitempty"`
	// EntryOverrides answers "is this node a starting point?" by hand. Without
	// an entry the graph decides: a node nothing points at is a start. Some
	// isolated nodes are not beginnings, so the author can say so.
	EntryOverrides map[string]bool `yaml:"entry_overrides,omitempty" json:"entryOverrides,omitempty"`
	// Groups and Annotations are freeform canvas decorations.
	Groups      []CanvasGroup      `yaml:"groups,omitempty" json:"groups,omitempty"`
	Annotations []CanvasAnnotation `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	// SavedViews are named filter/sort presets shared with the project.
	SavedViews []SavedView `yaml:"saved_views,omitempty" json:"savedViews,omitempty"`
}

type NodeStyle struct {
	Width  float64 `yaml:"width,omitempty" json:"width,omitempty"`
	Height float64 `yaml:"height,omitempty" json:"height,omitempty"`
	Color  string  `yaml:"color,omitempty" json:"color,omitempty"`
	// TextColor overrides the theme's text colour on this card.
	TextColor string `yaml:"text_color,omitempty" json:"textColor,omitempty"`
	// BorderColor overrides the card's outline, including the shaped ones.
	BorderColor string `yaml:"border_color,omitempty" json:"borderColor,omitempty"`
	// Align and VAlign place the card's text: left/center/right and
	// top/middle/bottom. Empty means the shape's default.
	Align  string `yaml:"align,omitempty" json:"align,omitempty"`
	VAlign string `yaml:"valign,omitempty" json:"valign,omitempty"`
	// Shape is the drawn outline: rect (default), round, pill, ellipse,
	// diamond or hexagon. Purely visual — layout still uses the box.
	Shape string `yaml:"shape,omitempty" json:"shape,omitempty"`
}

type CanvasGroup struct {
	ID     string  `yaml:"id" json:"id"`
	Title  string  `yaml:"title" json:"title"`
	X      float64 `yaml:"x" json:"x"`
	Y      float64 `yaml:"y" json:"y"`
	Width  float64 `yaml:"width" json:"width"`
	Height float64 `yaml:"height" json:"height"`
	Color  string  `yaml:"color,omitempty" json:"color,omitempty"`
	// TextColor overrides the theme's text colour on this card.
	TextColor string `yaml:"text_color,omitempty" json:"textColor,omitempty"`
	// Align and VAlign place the card's text: left/center/right and
	// top/middle/bottom. Empty means the shape's default.
	Align  string `yaml:"align,omitempty" json:"align,omitempty"`
	VAlign string `yaml:"valign,omitempty" json:"valign,omitempty"`
}

type CanvasAnnotation struct {
	ID     string  `yaml:"id" json:"id"`
	Text   string  `yaml:"text" json:"text"`
	X      float64 `yaml:"x" json:"x"`
	Y      float64 `yaml:"y" json:"y"`
	Width  float64 `yaml:"width" json:"width"`
	Height float64 `yaml:"height" json:"height"`
	Color  string  `yaml:"color,omitempty" json:"color,omitempty"`
	// TextColor overrides the theme's text colour on this card.
	TextColor string `yaml:"text_color,omitempty" json:"textColor,omitempty"`
	// Align and VAlign place the card's text: left/center/right and
	// top/middle/bottom. Empty means the shape's default.
	Align  string `yaml:"align,omitempty" json:"align,omitempty"`
	VAlign string `yaml:"valign,omitempty" json:"valign,omitempty"`
}

type SavedView struct {
	ID         string   `yaml:"id" json:"id"`
	Name       string   `yaml:"name" json:"name"`
	Statuses   []string `yaml:"statuses,omitempty" json:"statuses,omitempty"`
	Assignees  []string `yaml:"assignees,omitempty" json:"assignees,omitempty"`
	Tags       []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Priorities []string `yaml:"priorities,omitempty" json:"priorities,omitempty"`
	Sort       string   `yaml:"sort,omitempty" json:"sort,omitempty"`
}

type LogicGate struct {
	ID       string   `yaml:"id" json:"id"`
	Operator string   `yaml:"operator" json:"operator"`
	X        float64  `yaml:"x" json:"x"`
	Y        float64  `yaml:"y" json:"y"`
	Inputs   []string `yaml:"inputs" json:"inputs"`
	// Output is the single output of a boolean gate. Relation gates ("optional",
	// "deprecated") are many-to-many and use Outputs instead; only one of the two
	// fields is ever written for a given gate.
	Output  string   `yaml:"output,omitempty" json:"output,omitempty"`
	Outputs []string `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Applied string   `yaml:"applied,omitempty" json:"applied,omitempty"`
}

// OutputNodes lists every node this gate drives, whichever field holds them.
func (g LogicGate) OutputNodes() []string {
	if len(g.Outputs) > 0 {
		return g.Outputs
	}
	if g.Output != "" {
		return []string{g.Output}
	}
	return nil
}

type GatePlacement struct {
	Ratio *float64 `yaml:"ratio,omitempty" json:"ratio,omitempty"`
	X     *float64 `yaml:"x,omitempty" json:"x,omitempty"`
	Y     *float64 `yaml:"y,omitempty" json:"y,omitempty"`
}

type EdgeLabelPlacement struct {
	Ratio float64 `yaml:"ratio" json:"ratio"`
}

type Position struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

type PlanMilestone struct {
	Date   string `yaml:"date" json:"date"`                     // local date: YYYY-MM-DD
	Time   string `yaml:"time,omitempty" json:"time,omitempty"` // optional local time: HH:mm
	Status Status `yaml:"status" json:"status"`                 // built-in or custom-* planning status
	Note   string `yaml:"note,omitempty" json:"note,omitempty"` // visible planning annotation
}

type PlanStatusDefinition struct {
	ID    string `yaml:"id" json:"id"`
	Label string `yaml:"label" json:"label"`
}

type StatusDefinition struct {
	ID    string `yaml:"id" json:"id"`
	Label string `yaml:"label" json:"label"`
	Color string `yaml:"color" json:"color"`
	Shape string `yaml:"shape" json:"shape"`
	// Settled marks a state that counts as finished the way done and skipped
	// do: a node in it satisfies whatever depends on it instead of holding it
	// up. Off by default, so an existing custom state keeps blocking until
	// someone says it should not.
	Settled bool `yaml:"settled,omitempty" json:"settled,omitempty"`
}

// ParseGraph decodes graph.yaml bytes.
func ParseGraph(data []byte) (*Graph, error) {
	var g Graph
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("graph.yaml: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("graph.yaml: multiple YAML documents are not supported")
		}
		return nil, fmt.Errorf("graph.yaml: %w", err)
	}
	return &g, nil
}

// MarshalGraph encodes a graph back to YAML.
func MarshalGraph(g *Graph) ([]byte, error) {
	return yaml.Marshal(g)
}

// NodeByID returns the node with the given id, or nil.
func (g *Graph) NodeByID(id string) *Node {
	for _, n := range g.Nodes {
		if n != nil && n.ID == id {
			return n
		}
	}
	return nil
}

// RequiresExpr parses the node's requires field. nil Expr = unconstrained.
func (n *Node) RequiresExpr() (dsl.Expr, *dsl.ParseError) {
	return dsl.Parse(n.Requires)
}
