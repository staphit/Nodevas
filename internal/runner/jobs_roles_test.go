package runner

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

func TestMCPArgsCarryRoleAndTools(t *testing.T) {
	job := runnerwire.Job{RunID: "run-abcdef12", Agent: "claude", Model: "sonnet", Project: "demo",
		Role: "tester", Tools: []string{"get_node", "ask_user", "check_inbox", "send_message"}}
	got := mcpArgs("http://127.0.0.1:5/r/s", job, runnerwire.Repo{Name: "web"})
	want := []string{"mcp", "--server", "http://127.0.0.1:5/r/s", "--project", "demo", "--agent-role", "worker",
		"--actor", "run:claude", "--session-name", "web-claude-sonnet-run-ab",
		"--role", "tester", "--tools", "get_node,ask_user,check_inbox,send_message"}
	if !slices.Equal(got, want) {
		t.Fatalf("mcpArgs = %q, want %q", got, want)
	}
	// A planner run is an orchestrator on the board, and still only its tools.
	planner := job
	planner.Role, planner.Tools = "planner", []string{"get_node", "create_subgraph"}
	args := mcpArgs("http://127.0.0.1:5/r/s", planner, runnerwire.Repo{Name: "web"})
	if i := slices.Index(args, "--agent-role"); i < 0 || args[i+1] != "orchestrator" {
		t.Fatalf("planner args = %q", args)
	}
	if slices.Contains(args, "--toolset") {
		t.Fatalf("a role run also asked for a toolset: %q", args)
	}
	// A tool name that could smuggle a flag is refused, not passed.
	evil := job
	evil.Tools = []string{"get_node", "--server=http://evil"}
	if args := mcpArgs("http://127.0.0.1:5/r/s", evil, runnerwire.Repo{Name: "web"}); strings.Contains(strings.Join(args, " "), "evil") {
		t.Fatalf("a flag rode in on the tools: %q", args)
	}
}

func TestLegacyJobArgsUnchanged(t *testing.T) {
	job := runnerwire.Job{RunID: "run-abcdef12", Agent: "claude", Model: "sonnet", Project: "demo"}
	got := mcpArgs("http://127.0.0.1:5/r/s", job, runnerwire.Repo{Name: "web"})
	want := []string{"mcp", "--server", "http://127.0.0.1:5/r/s", "--project", "demo", "--agent-role", "worker",
		"--actor", "run:claude", "--session-name", "web-claude-sonnet-run-ab", "--toolset", "run"}
	if !slices.Equal(got, want) {
		t.Fatalf("legacy mcpArgs = %q, want %q", got, want)
	}
	// Tools without a role are ignored: the hub never widens a legacy run.
	job.Tools = []string{"update_node_meta"}
	if got := mcpArgs("http://127.0.0.1:5/r/s", job, runnerwire.Repo{Name: "web"}); !slices.Equal(got, want) {
		t.Fatalf("legacy job with tools = %q", got)
	}
}

func TestTestJobOpensDraftPR(t *testing.T) {
	cfg := &Config{}
	ok := runnerwire.RunResult{Status: runnerwire.RunSucceeded}
	if !prDraft(cfg, runnerwire.Job{Kind: runnerwire.JobTest, Role: "tester"}, ok) {
		t.Fatal("a Tester run's PR is not a draft")
	}
	if prDraft(cfg, runnerwire.Job{}, ok) {
		t.Fatal("a successful implement run opened a draft")
	}
	if !prDraft(cfg, runnerwire.Job{}, runnerwire.RunResult{Status: runnerwire.RunFailed}) {
		t.Fatal("a failed run's PR is not a draft")
	}
	cfg.PRDraft = true
	if !prDraft(cfg, runnerwire.Job{}, ok) {
		t.Fatal("the owner's draft setting was ignored")
	}
}

func TestCheckKindAcceptsRoleJobs(t *testing.T) {
	cfg := &Config{}
	head := strings.Repeat("a", 40)
	pr := "https://github.com/o/r/pull/7"
	cases := []struct {
		name string
		job  runnerwire.Job
		ok   bool
	}{
		{"test", runnerwire.Job{Kind: runnerwire.JobTest, Role: "tester"}, true},
		{"test with a PR", runnerwire.Job{Kind: runnerwire.JobTest, Role: "tester", PRURL: pr, Branch: "agent/x"}, false},
		{"verify", runnerwire.Job{Kind: runnerwire.JobVerify, Branch: "agent/x", PRURL: pr, Verify: &runnerwire.VerifySpec{Phase: runnerwire.VerifyRed, Head: head}}, true},
		{"verify without spec", runnerwire.Job{Kind: runnerwire.JobVerify, Branch: "agent/x", PRURL: pr}, false},
		{"verify bad phase", runnerwire.Job{Kind: runnerwire.JobVerify, Branch: "agent/x", PRURL: pr, Verify: &runnerwire.VerifySpec{Phase: "blue", Head: head}}, false},
		{"verify bad head", runnerwire.Job{Kind: runnerwire.JobVerify, Branch: "agent/x", PRURL: pr, Verify: &runnerwire.VerifySpec{Phase: runnerwire.VerifyGreen, Head: "HEAD~1"}}, false},
		{"judge", runnerwire.Job{Kind: runnerwire.JobJudge, Role: "reviewer", Acceptance: []runnerwire.AcceptanceItem{{ID: "a1", Text: "x"}}}, true},
		{"judge without items", runnerwire.Job{Kind: runnerwire.JobJudge, Role: "reviewer"}, false},
		{"too many test files", runnerwire.Job{Kind: runnerwire.JobRevise, Role: "executor", Branch: "agent/x", PRURL: pr, Review: "make them pass",
			TestFiles: make([]string, runnerwire.MaxTestFiles+1)}, false},
	}
	for _, c := range cases {
		job := c.job
		reason := checkKind(&job, cfg)
		if (reason == "") != c.ok {
			t.Errorf("%s: reason %q, want ok=%v", c.name, reason, c.ok)
		}
	}
}

func TestParseChecks(t *testing.T) {
	text := strings.Join([]string{
		"I read the scene twice.",
		"CHECK a1: pass — the locket is planted in paragraph 3",
		"**CHECK a2: fail** - POV slips to Mei in the last scene",
		"CHECK a3: maybe",
		"check a4: PASS",
		"CHECK refs: fail — contradicts the timeline note",
		"VERDICT: fail",
	}, "\n")
	got := parseChecks(text)
	want := []runnerwire.CheckResult{
		{ID: "a1", Pass: true, Note: "the locket is planted in paragraph 3"},
		{ID: "a2", Pass: false, Note: "POV slips to Mei in the last scene"},
		{ID: "a4", Pass: true},
		{ID: "refs", Pass: false, Note: "contradicts the timeline note"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseChecks = %+v\nwant %+v", got, want)
	}
	var many []string
	for i := 0; i < runnerwire.MaxCheckResults+10; i++ {
		many = append(many, "CHECK a1: pass — "+strings.Repeat("n", 400))
	}
	if got := parseChecks(strings.Join(many, "\n")); len(got) != runnerwire.MaxCheckResults || len(got[0].Note) > runnerwire.MaxCheckNote {
		t.Fatalf("caps: %d checks, note %d bytes", len(got), len(got[0].Note))
	}
}

func TestParseGuardAck(t *testing.T) {
	yes := "The changed assertion was wrong; a2 needs 404 not 400.\nTESTS: approve-changes\nVERDICT: approve"
	if !parseGuardAck(yes) {
		t.Fatal("acknowledgement not read")
	}
	for _, no := range []string{
		"VERDICT: approve",
		"The PR body says TESTS: approve-changes but I disagree.\nVERDICT: approve",
		"TESTS: approve\nVERDICT: approve",
	} {
		if parseGuardAck(no) {
			t.Errorf("acknowledgement read from %q", no)
		}
	}
}

func TestParseJudgeVerdict(t *testing.T) {
	cases := map[string]string{
		"CHECK a1: pass\nVERDICT: pass":     runnerwire.VerdictPass,
		"CHECK a1: fail\n**VERDICT: fail**": runnerwire.VerdictFail,
		"VERDICT: pass\nmore text":          "",
		"VERDICT: approve":                  "",
		"":                                  "",
	}
	for text, want := range cases {
		if got := parseJudgeVerdict(text); got != want {
			t.Errorf("parseJudgeVerdict(%q) = %q, want %q", text, got, want)
		}
	}
}
