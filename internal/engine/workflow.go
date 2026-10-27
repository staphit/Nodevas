package engine

// Workflow stages: which stages of the task and goal flows a project runs.
// nil is legacy (the behavior before stages existed). Mirrored by
// web/src/domain/graph/stages.ts, pinned by testdata/workflow_presets.json.
//
// RED PHASE STUB: declarations only, no behavior.

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
var Presets = map[string]Stages{}

// DefaultTestGlobs is what counts as a test file when a project names none.
var DefaultTestGlobs = []string{}

// Defaults applied when a lever is zero.
const (
	DefaultMaxQARounds     = 0
	DefaultMaxTesterRounds = 0
	DefaultMaxGreenTries   = 0
)

// NewProjectWorkflow is the workflow a new project of graphType gets.
func NewProjectWorkflow(graphType string) *Workflow { return nil }

// WorkflowForPreset builds a workflow for a named preset.
func WorkflowForPreset(preset string) *Workflow { return nil }

// EffectiveWorkflow is the goal's override, else the project's, else nil.
func EffectiveWorkflow(g *Graph, goalID string) *Workflow { return nil }

// GoalTag is the tag that makes a node a member of a goal.
func GoalTag(id string) string { return "" }

// GoalOf is the goal a node belongs to (via its goal:<id> tag), or "".
func (g *Graph) GoalOf(n *Node) string { return "" }

// RequiresAcceptance reports whether a workflow needs acceptance criteria
// before an Executor can be dispatched.
func RequiresAcceptance(w *Workflow) bool { return false }

// Normalized returns a copy with zero levers set to their defaults.
func (w *Workflow) Normalized() *Workflow { return w }

// ValidateWorkflow checks one workflow's preset and lever bounds.
func ValidateWorkflow(w *Workflow) error { return nil }

// ValidateGraphWorkflows checks the project workflow and every node override,
// including that an override sits only on an Autopilot type.
func ValidateGraphWorkflows(g *Graph) error { return nil }
