package engine

import (
	"fmt"
	"strings"
)

// Workflow stages: which stages of the task and goal flows a project runs.
// nil is legacy (the behavior before stages existed). Mirrored by
// web/src/domain/graph/stages.ts, pinned by testdata/workflow_presets.json.

const (
	PresetLight    = "light"
	PresetStandard = "standard"
	PresetFull     = "full"
	PresetCustom   = "custom"
)

// JudgeChecklist is a story project's judge: acceptance checklists judged by
// a model instead of a test command.
const JudgeChecklist = "checklist"

type Workflow struct {
	Preset                 string   `yaml:"preset" json:"preset"`
	Stages                 Stages   `yaml:"stages" json:"stages"`
	ReviewerSkipBelowLines int      `yaml:"reviewer_skip_below_lines,omitempty" json:"reviewerSkipBelowLines,omitempty"`
	MaxQARounds            int      `yaml:"max_qa_rounds,omitempty" json:"maxQaRounds,omitempty"`
	MaxTesterRounds        int      `yaml:"max_tester_rounds,omitempty" json:"maxTesterRounds,omitempty"`
	MaxGreenTries          int      `yaml:"max_green_tries,omitempty" json:"maxGreenTries,omitempty"`
	TestGlobs              []string `yaml:"test_globs,omitempty" json:"testGlobs,omitempty"`
	Judge                  string   `yaml:"judge,omitempty" json:"judge,omitempty"`
}

type Stages struct {
	Planner    bool `yaml:"planner" json:"planner"`
	GoalQA     bool `yaml:"goal_qa" json:"goalQa"`
	Tester     bool `yaml:"tester" json:"tester"`
	Reviewer   bool `yaml:"reviewer" json:"reviewer"`
	CI         bool `yaml:"ci" json:"ci"`
	GoalReview bool `yaml:"goal_review" json:"goalReview"`
}

// Presets are the named stage sets; custom is any other.
var Presets = map[string]Stages{
	PresetLight:    {Planner: true},
	PresetStandard: {Planner: true, Tester: true, Reviewer: true},
	PresetFull:     {Planner: true, Tester: true, Reviewer: true, GoalQA: true, CI: true, GoalReview: true},
}

// DefaultTestGlobs is what counts as a test file when a project names none.
var DefaultTestGlobs = []string{"**/*_test.go", "**/*.test.ts", "**/*.test.tsx", "**/*.spec.ts", "**/*.spec.tsx",
	"**/test/**", "**/tests/**", "**/__tests__/**", "e2e/**", "**/test_*.py", "**/*_test.py"}

// Defaults applied when a lever is zero.
const (
	DefaultMaxQARounds     = 2
	DefaultMaxTesterRounds = 2
	DefaultMaxGreenTries   = 3
)

// Lever bounds.
const (
	MaxReviewerSkipBelowLines = 10000
	MaxQARoundsLimit          = 5
	MaxTesterRoundsLimit      = 3
	MaxGreenTriesLimit        = 5
	MaxTestGlobs              = 32
)

// NewProjectWorkflow is the workflow a new project of graphType gets.
func NewProjectWorkflow(graphType string) *Workflow {
	w := WorkflowForPreset(PresetStandard)
	if graphType == "story" {
		w.Judge = JudgeChecklist
	}
	return w
}

// WorkflowForPreset builds a workflow for a named preset.
func WorkflowForPreset(preset string) *Workflow {
	stages, ok := Presets[preset]
	if !ok {
		return nil
	}
	return &Workflow{Preset: preset, Stages: stages}
}

// EffectiveWorkflow is the goal's override, else the project's, else nil.
func EffectiveWorkflow(g *Graph, goalID string) *Workflow {
	if g == nil {
		return nil
	}
	if goalID != "" {
		if goal := g.NodeByID(goalID); goal != nil && goal.Workflow != nil {
			return goal.Workflow
		}
	}
	return g.Workflow
}

// GoalTag is the tag that makes a node a member of a goal.
func GoalTag(id string) string { return "goal:" + id }

// GoalOf is the goal a node belongs to (via its goal:<id> tag), or "". A tag
// naming a node that exists wins over one that does not.
func (g *Graph) GoalOf(n *Node) string {
	if n == nil {
		return ""
	}
	first := ""
	for _, tag := range n.Tags {
		id, ok := strings.CutPrefix(tag, "goal:")
		if !ok || id == "" {
			continue
		}
		if g != nil && g.NodeByID(id) != nil {
			return id
		}
		if first == "" {
			first = id
		}
	}
	return first
}

// RequiresAcceptance reports whether a workflow needs acceptance criteria
// before an Executor can be dispatched.
func RequiresAcceptance(w *Workflow) bool {
	return w != nil && (w.Stages.Tester || w.Stages.Reviewer || w.Judge == JudgeChecklist)
}

// Staged reports whether a workflow runs any task-level gate (the staged
// pipeline); Light (and legacy) do not.
func (w *Workflow) Staged() bool {
	return w != nil && (w.Stages.Tester || w.Stages.Reviewer || w.Stages.CI || w.Judge == JudgeChecklist)
}

// Normalized returns a copy with zero levers set to their defaults.
func (w *Workflow) Normalized() *Workflow {
	if w == nil {
		return nil
	}
	out := *w
	if out.MaxQARounds == 0 {
		out.MaxQARounds = DefaultMaxQARounds
	}
	if out.MaxTesterRounds == 0 {
		out.MaxTesterRounds = DefaultMaxTesterRounds
	}
	if out.MaxGreenTries == 0 {
		out.MaxGreenTries = DefaultMaxGreenTries
	}
	if len(out.TestGlobs) == 0 {
		out.TestGlobs = append([]string(nil), DefaultTestGlobs...)
	} else {
		out.TestGlobs = append([]string(nil), out.TestGlobs...)
	}
	return &out
}

// ValidateWorkflow checks one workflow's preset and lever bounds.
func ValidateWorkflow(w *Workflow) error {
	if w == nil {
		return nil
	}
	if w.Preset != PresetCustom {
		stages, ok := Presets[w.Preset]
		if !ok {
			return fmt.Errorf("workflow: unknown preset %q", w.Preset)
		}
		if stages != w.Stages {
			return fmt.Errorf("workflow: stages do not match preset %q (use custom)", w.Preset)
		}
	}
	if w.ReviewerSkipBelowLines < 0 || w.ReviewerSkipBelowLines > MaxReviewerSkipBelowLines {
		return fmt.Errorf("workflow: reviewer_skip_below_lines must be 0..%d", MaxReviewerSkipBelowLines)
	}
	if w.MaxQARounds < 0 || w.MaxQARounds > MaxQARoundsLimit {
		return fmt.Errorf("workflow: max_qa_rounds must be 1..%d", MaxQARoundsLimit)
	}
	if w.MaxTesterRounds < 0 || w.MaxTesterRounds > MaxTesterRoundsLimit {
		return fmt.Errorf("workflow: max_tester_rounds must be 1..%d", MaxTesterRoundsLimit)
	}
	if w.MaxGreenTries < 0 || w.MaxGreenTries > MaxGreenTriesLimit {
		return fmt.Errorf("workflow: max_green_tries must be 1..%d", MaxGreenTriesLimit)
	}
	if len(w.TestGlobs) > MaxTestGlobs {
		return fmt.Errorf("workflow: test_globs holds at most %d patterns", MaxTestGlobs)
	}
	for _, glob := range w.TestGlobs {
		if strings.TrimSpace(glob) == "" || len(glob) > 200 || strings.HasPrefix(glob, "/") ||
			strings.Contains(glob, `\`) || strings.Contains(glob, "..") || strings.Contains(glob, ":") {
			return fmt.Errorf("workflow: test_globs has an invalid pattern %q (relative, slash-separated)", glob)
		}
	}
	switch w.Judge {
	case "", JudgeChecklist:
	default:
		return fmt.Errorf("workflow: unknown judge %q", w.Judge)
	}
	return nil
}

// ValidateGraphWorkflows checks the project workflow and every node override,
// including that an override sits only on an Autopilot type.
func ValidateGraphWorkflows(g *Graph) error {
	if g == nil {
		return nil
	}
	if err := ValidateWorkflow(g.Workflow); err != nil {
		return err
	}
	for _, n := range g.Nodes {
		if n == nil || n.Workflow == nil {
			continue
		}
		if g.TypeOf(n).AgentMode != AgentAutopilot {
			return fmt.Errorf("node %q: only an Autopilot type (a Goal) can override workflow stages", n.ID)
		}
		if err := ValidateWorkflow(n.Workflow); err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
	}
	return nil
}
