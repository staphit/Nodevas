package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type presetsFixture struct {
	Presets  map[string]Stages `json:"presets"`
	Defaults struct {
		MaxQARounds     int      `json:"maxQaRounds"`
		MaxTesterRounds int      `json:"maxTesterRounds"`
		MaxGreenTries   int      `json:"maxGreenTries"`
		TestGlobs       []string `json:"testGlobs"`
	} `json:"defaults"`
	NewProject struct {
		Workflow struct{ Preset, Judge string } `json:"workflow"`
		Story    struct{ Preset, Judge string } `json:"story"`
	} `json:"newProject"`
}

func readPresetsFixture(t *testing.T) presetsFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "workflow_presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture presetsFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestWorkflowPresetsMatchFixture(t *testing.T) {
	fixture := readPresetsFixture(t)
	if !reflect.DeepEqual(Presets, fixture.Presets) {
		t.Fatalf("Presets drifted from the fixture:\n got %+v\nwant %+v", Presets, fixture.Presets)
	}
	if DefaultMaxQARounds != fixture.Defaults.MaxQARounds || DefaultMaxTesterRounds != fixture.Defaults.MaxTesterRounds ||
		DefaultMaxGreenTries != fixture.Defaults.MaxGreenTries {
		t.Errorf("defaults = %d/%d/%d, want %+v", DefaultMaxQARounds, DefaultMaxTesterRounds, DefaultMaxGreenTries, fixture.Defaults)
	}
	if !reflect.DeepEqual(DefaultTestGlobs, fixture.Defaults.TestGlobs) {
		t.Errorf("DefaultTestGlobs = %v, want %v", DefaultTestGlobs, fixture.Defaults.TestGlobs)
	}
	for _, preset := range []string{PresetLight, PresetStandard, PresetFull} {
		w := WorkflowForPreset(preset)
		if w == nil || w.Preset != preset || w.Stages != fixture.Presets[preset] {
			t.Errorf("WorkflowForPreset(%s) = %+v", preset, w)
		}
	}
	if WorkflowForPreset("turbo") != nil {
		t.Error("an unknown preset built a workflow")
	}
}

func TestNilWorkflowIsLegacy(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "g", Kind: "goal"}, {ID: "t", Tags: []string{"goal:g"}}}}
	if EffectiveWorkflow(g, "g") != nil || EffectiveWorkflow(g, "") != nil || EffectiveWorkflow(nil, "") != nil {
		t.Fatal("a project without a workflow is not legacy")
	}
	if RequiresAcceptance(nil) {
		t.Fatal("legacy requires acceptance")
	}
	// A graph without a workflow writes none.
	out, err := MarshalGraph(&Graph{Version: 1, Nodes: []*Node{{ID: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "workflow") {
		t.Fatalf("legacy graph.yaml mentions workflow:\n%s", out)
	}
}

func TestGoalOverrideWinsOverProject(t *testing.T) {
	full := WorkflowForPreset(PresetFull)
	g := &Graph{
		Workflow: WorkflowForPreset(PresetLight),
		Nodes: []*Node{
			{ID: "big", Kind: "goal", Workflow: full},
			{ID: "small", Kind: "goal"},
			{ID: "t1", Tags: []string{"backend", GoalTag("big")}},
			{ID: "t2", Tags: []string{GoalTag("small")}},
			{ID: "loose"},
		},
	}
	if GoalTag("big") != "goal:big" {
		t.Fatalf("GoalTag = %q", GoalTag("big"))
	}
	if got := g.GoalOf(g.NodeByID("t1")); got != "big" {
		t.Fatalf("GoalOf(t1) = %q", got)
	}
	if got := g.GoalOf(g.NodeByID("loose")); got != "" {
		t.Fatalf("GoalOf(loose) = %q", got)
	}
	if w := EffectiveWorkflow(g, "big"); w == nil || w.Preset != PresetFull || !w.Stages.GoalQA {
		t.Fatalf("big goal workflow = %+v", w)
	}
	if w := EffectiveWorkflow(g, "small"); w == nil || w.Preset != PresetLight {
		t.Fatalf("small goal workflow = %+v", w)
	}
	if w := EffectiveWorkflow(g, ""); w == nil || w.Preset != PresetLight {
		t.Fatalf("no goal workflow = %+v", w)
	}
	// An override on a project without a workflow still applies to its goal.
	legacy := &Graph{Nodes: []*Node{{ID: "big", Kind: "goal", Workflow: full}}}
	if w := EffectiveWorkflow(legacy, "big"); w == nil || w.Preset != PresetFull {
		t.Fatalf("override on a legacy project = %+v", w)
	}
}

func TestWorkflowOnlyOnAutopilotTypes(t *testing.T) {
	w := WorkflowForPreset(PresetStandard)
	ok := &Graph{Nodes: []*Node{{ID: "g", Kind: "goal", Workflow: w}}}
	if err := ValidateGraphWorkflows(ok); err != nil {
		t.Fatalf("goal override refused: %v", err)
	}
	customAutopilot := &Graph{
		NodeTypes: []NodeTypeDef{{ID: "custom-type-epic", Label: "Epic", AgentMode: AgentAutopilot, Completion: CompletionPeople, CountsInProgress: boolPtr(true)}},
		Nodes:     []*Node{{ID: "e", Kind: "custom-type-epic", Workflow: w}},
	}
	if err := ValidateGraphWorkflows(customAutopilot); err != nil {
		t.Fatalf("custom autopilot override refused: %v", err)
	}
	for _, kind := range []string{"", "task", "scene", "note", "gate"} {
		bad := &Graph{Nodes: []*Node{{ID: "n", Kind: kind, Workflow: w}}}
		err := ValidateGraphWorkflows(bad)
		if err == nil || !strings.Contains(err.Error(), "Autopilot") {
			t.Errorf("override on %q: err = %v", kind, err)
		}
	}
}

func TestWorkflowBounds(t *testing.T) {
	base := func(mutate func(*Workflow)) *Workflow {
		w := WorkflowForPreset(PresetStandard)
		if w == nil {
			w = &Workflow{Preset: PresetStandard, Stages: Stages{Planner: true, Tester: true, Reviewer: true}}
		}
		mutate(w)
		return w
	}
	cases := []struct {
		name string
		w    *Workflow
		want string
	}{
		{"valid", base(func(*Workflow) {}), ""},
		{"custom", base(func(w *Workflow) { w.Preset, w.Stages = PresetCustom, Stages{Tester: true} }), ""},
		{"unknown preset", base(func(w *Workflow) { w.Preset = "turbo" }), "preset"},
		{"preset mismatch", base(func(w *Workflow) { w.Stages.CI = true }), "preset"},
		{"skip negative", base(func(w *Workflow) { w.ReviewerSkipBelowLines = -1 }), "reviewer_skip_below_lines"},
		{"skip huge", base(func(w *Workflow) { w.ReviewerSkipBelowLines = 10001 }), "reviewer_skip_below_lines"},
		{"qa rounds", base(func(w *Workflow) { w.MaxQARounds = 6 }), "max_qa_rounds"},
		{"tester rounds", base(func(w *Workflow) { w.MaxTesterRounds = 4 }), "max_tester_rounds"},
		{"green tries", base(func(w *Workflow) { w.MaxGreenTries = 9 }), "max_green_tries"},
		{"too many globs", base(func(w *Workflow) { w.TestGlobs = make([]string, 33) }), "test_globs"},
		{"absolute glob", base(func(w *Workflow) { w.TestGlobs = []string{"/etc/**"} }), "test_globs"},
		{"judge", base(func(w *Workflow) { w.Judge = "vibes" }), "judge"},
	}
	for _, c := range cases {
		err := ValidateWorkflow(c.w)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	n := (&Workflow{Preset: PresetLight, Stages: Stages{Planner: true}}).Normalized()
	if n.MaxQARounds != 2 || n.MaxTesterRounds != 2 || n.MaxGreenTries != 3 || len(n.TestGlobs) == 0 {
		t.Fatalf("Normalized = %+v", n)
	}
}

func TestStoryDefaultsToChecklistJudge(t *testing.T) {
	fixture := readPresetsFixture(t)
	code := NewProjectWorkflow("workflow")
	if code == nil || code.Preset != fixture.NewProject.Workflow.Preset || code.Judge != "" {
		t.Fatalf("new code project workflow = %+v", code)
	}
	if plain := NewProjectWorkflow(""); plain == nil || plain.Preset != PresetStandard {
		t.Fatalf("new untyped project workflow = %+v", plain)
	}
	story := NewProjectWorkflow("story")
	if story == nil || story.Preset != fixture.NewProject.Story.Preset || story.Judge != JudgeChecklist {
		t.Fatalf("new story project workflow = %+v", story)
	}
}

func TestRequiresAcceptance(t *testing.T) {
	cases := []struct {
		w    *Workflow
		want bool
	}{
		{nil, false},
		{&Workflow{Preset: PresetLight, Stages: Stages{Planner: true}}, false},
		{&Workflow{Preset: PresetCustom, Stages: Stages{Tester: true}}, true},
		{&Workflow{Preset: PresetCustom, Stages: Stages{Reviewer: true}}, true},
		{&Workflow{Preset: PresetLight, Stages: Stages{Planner: true}, Judge: JudgeChecklist}, true},
		{&Workflow{Preset: PresetCustom, Stages: Stages{CI: true}}, false},
	}
	for _, c := range cases {
		if got := RequiresAcceptance(c.w); got != c.want {
			t.Errorf("RequiresAcceptance(%+v) = %v, want %v", c.w, got, c.want)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*_test.go", "internal/engine/roles_test.go", true},
		{"**/*_test.go", "roles_test.go", true},
		{"**/*_test.go", "internal/engine/roles.go", false},
		{"**/*.test.ts", "web/src/domain/graph/stages.test.ts", true},
		{"**/test/**", "pkg/test/helpers/x.go", true},
		{"**/__tests__/**", "web/__tests__/a.tsx", true},
		{"e2e/**", "e2e/roles.spec.ts", true},
		{"e2e/**", "web/e2e/roles.spec.ts", false},
		{"**/test_*.py", "tools/test_cli.py", true},
		{"*.go", "a/b.go", false},
		// Windows separators are refused, never translated.
		{"**/*_test.go", `internal\engine\roles_test.go`, false},
		{`**\*_test.go`, "internal/engine/roles_test.go", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
	if !MatchAnyGlob(DefaultTestGlobs, "web/src/x.spec.tsx") || MatchAnyGlob(DefaultTestGlobs, "package.json") {
		t.Error("MatchAnyGlob over the defaults is wrong")
	}
}

func TestWorkflowRoundTripsGraphYAML(t *testing.T) {
	g := &Graph{Version: 1, Workflow: &Workflow{Preset: PresetCustom, Stages: Stages{Tester: true, CI: true}, MaxGreenTries: 4},
		Nodes: []*Node{{ID: "g", Kind: "goal", Workflow: &Workflow{Preset: PresetFull, Stages: Stages{Planner: true, GoalQA: true, Tester: true, Reviewer: true, CI: true, GoalReview: true}}}}}
	out, err := MarshalGraph(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "goal_qa:") || !strings.Contains(string(out), "max_green_tries: 4") {
		t.Fatalf("graph.yaml:\n%s", out)
	}
	back, err := ParseGraph(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Workflow, g.Workflow) || !reflect.DeepEqual(back.Nodes[0].Workflow, g.Nodes[0].Workflow) {
		t.Fatalf("round trip lost the workflow: %+v / %+v", back.Workflow, back.Nodes[0].Workflow)
	}
	raw, _ := json.Marshal(g)
	if !strings.Contains(string(raw), `"workflow":{"preset":"custom","stages":{"planner":false,"goalQa":false,"tester":true`) {
		t.Fatalf("JSON: %s", raw)
	}
}
