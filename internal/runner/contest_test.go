package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// openPRGH fakes gh for open_pr workspace action tests (pr list, pr create, pr view).
type openPRGH struct {
	mu        sync.Mutex
	calls     [][]string
	bodies    []string
	listJSON  string // JSON returned by `gh pr list`; default "[]"
	createURL string
	viewJSON  string
}

func (g *openPRGH) run(_, stdin string, args []string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, slices.Clone(args))
	if len(args) >= 2 && args[0] == "pr" {
		switch args[1] {
		case "list":
			if g.listJSON != "" {
				return g.listJSON, nil
			}
			return "[]", nil
		case "create":
			g.bodies = append(g.bodies, stdin)
			return "Creating pull request...\n" + g.createURL + "\n", nil
		case "view":
			if g.viewJSON != "" {
				return g.viewJSON, nil
			}
			return `{"number":42,"state":"OPEN","isDraft":false,"headRefOid":"feedface","statusCheckRollup":[]}`, nil
		}
	}
	return "", nil
}

func (g *openPRGH) hasCall(subcmd string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.calls {
		if len(c) >= 2 && c[0] == "pr" && c[1] == subcmd {
			return true
		}
	}
	return false
}

func newOpenPREnv(t *testing.T, gh *openPRGH) (*wsEnv, string) {
	t.Helper()
	hermeticGit(t)
	repo := newTestRepo(t)
	bare := withOrigin(t, repo)
	dataDir := t.TempDir()
	wt, err := CreateWorktree(context.Background(), repo, dataDir, wsRun, wsNode, "main")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt.Path, "feature.txt", []byte("winner work\n"))
	gitT(t, wt.Path, "add", "-A")
	gitT(t, wt.Path, "commit", "-q", "-m", "Winner approach\n\nImplemented the feature cleanly.\n\nNodevas-Node: "+wsNode+"\nNodevas-Run: "+wsRun)
	tip := gitT(t, wt.Path, "rev-parse", "HEAD")
	cfg := &Config{DataDir: dataDir, Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: repo}}}
	log := &safeLog{}
	c := &Client{
		Config:   cfg,
		Log:      log,
		Commands: &fakeCommands{paths: map[string]string{"gh": "/bin/gh"}, gh: gh.run},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	j := newJobs(ctx, c)
	j.loadPRs()
	j.loadMerges()
	j.loadWorkspaceActions()
	env := &wsEnv{j: j, cfg: cfg, repo: repo, dataDir: dataDir, wt: wt, tip: tip, log: log}
	return env, bare
}

func (e *wsEnv) openPRJob(id string) runnerwire.WorkspaceActionJob {
	return runnerwire.WorkspaceActionJob{
		ID:        id,
		RunID:     wsRun,
		AccountID: wsOwner,
		Action:    runnerwire.WorkspaceOpenPR,
		RepoID:    "r1",
		NodeID:    wsNode,
		Branch:    e.wt.Branch,
		Base:      "main",
		HeadSHA:   e.tip,
	}
}

func TestOpenPRActionOwnerOnlyOwnBranch(t *testing.T) {
	gh := &openPRGH{createURL: "https://github.com/o/r/pull/42"}
	env, bare := newOpenPREnv(t, gh)

	// Non-owner is refused without touching git or gh.
	nonOwner := env.openPRJob("op-non-owner")
	nonOwner.AccountID = "u-other"
	if out := env.act(t, nonOwner); out.Code != runnerwire.WorkspaceErrRefused {
		t.Fatalf("non-owner outcome = %+v, want %s", out, runnerwire.WorkspaceErrRefused)
	}
	if gh.hasCall("create") || gitT(t, bare, "branch", "--list", "agent/*") != "" {
		t.Fatal("non-owner open_pr pushed or called gh pr create")
	}

	// Wrong branch name is refused as not_own_branch.
	wrongBranch := env.openPRJob("op-wrong-branch")
	wrongBranch.Branch = "main"
	if out := env.act(t, wrongBranch); out.Code != runnerwire.WorkspaceErrNotOwnBranch {
		t.Fatalf("wrong branch outcome = %+v, want %s", out, runnerwire.WorkspaceErrNotOwnBranch)
	}
	otherBranch := env.openPRJob("op-other-branch")
	otherBranch.Branch = "agent/n-1-deadbeef"
	if out := env.act(t, otherBranch); out.Code != runnerwire.WorkspaceErrNotOwnBranch {
		t.Fatalf("other branch outcome = %+v, want %s", out, runnerwire.WorkspaceErrNotOwnBranch)
	}

	// Wrong job kind (revise) is refused as not_own_branch.
	wrongKind := env.openPRJob("op-wrong-kind")
	wrongKind.Kind = runnerwire.JobRevise
	if out := env.act(t, wrongKind); out.Code != runnerwire.WorkspaceErrNotOwnBranch {
		t.Fatalf("revise kind outcome = %+v, want %s", out, runnerwire.WorkspaceErrNotOwnBranch)
	}

	// Owner + own branch succeeds, pushes branch, opens PR, watches it, and keeps worktree live.
	out := env.act(t, env.openPRJob("op-ok"))
	if out.Code != "" || out.PRURL != gh.createURL || out.Action != runnerwire.WorkspaceOpenPR {
		t.Fatalf("open_pr outcome = %+v, want PRURL %s", out, gh.createURL)
	}
	if got := gitT(t, bare, "rev-parse", "refs/heads/"+env.wt.Branch); got != env.tip {
		t.Fatalf("remote branch at %q, want %s", got, env.tip)
	}
	if !gh.hasCall("create") {
		t.Fatal("gh pr create was not called")
	}
	if !worktreeExists(env.wt.Path) {
		t.Fatal("open_pr removed the run's worktree")
	}
	if w := env.j.openWatch(wsRun); w == nil || w.URL != gh.createURL {
		t.Fatalf("PR watch = %+v, want %s", w, gh.createURL)
	}
}

func TestOpenPRRefusesExistingPR(t *testing.T) {
	t.Run("watched PR", func(t *testing.T) {
		gh := &openPRGH{createURL: "https://github.com/o/r/pull/99"}
		env, bare := newOpenPREnv(t, gh)
		env.j.watch(watchedPR{
			RunID: wsRun, NodeID: wsNode, URL: "https://github.com/o/r/pull/7",
			Worktree: env.wt.Path, Repo: env.repo, Branch: env.wt.Branch,
			PR: runnerwire.PRInfo{URL: "https://github.com/o/r/pull/7", Number: 7, State: runnerwire.PROpen},
		})
		out := env.act(t, env.openPRJob("op-has-watch"))
		if out.Code != runnerwire.WorkspaceErrHasPR {
			t.Fatalf("outcome = %+v, want %s", out, runnerwire.WorkspaceErrHasPR)
		}
		if gh.hasCall("create") || gitT(t, bare, "branch", "--list", "agent/*") != "" {
			t.Fatal("open_pr pushed or created PR when watched PR already existed")
		}
	})

	t.Run("existing PR on GitHub", func(t *testing.T) {
		gh := &openPRGH{
			listJSON:  `[{"url":"https://github.com/o/r/pull/12"}]`,
			createURL: "https://github.com/o/r/pull/99",
		}
		env, bare := newOpenPREnv(t, gh)
		out := env.act(t, env.openPRJob("op-has-remote"))
		if out.Code != runnerwire.WorkspaceErrHasPR {
			t.Fatalf("outcome = %+v, want %s", out, runnerwire.WorkspaceErrHasPR)
		}
		if gh.hasCall("create") || gitT(t, bare, "branch", "--list", "agent/*") != "" {
			t.Fatal("open_pr pushed or created PR when GitHub already had an open PR")
		}
	})
}

func TestOpenPRNoForcePush(t *testing.T) {
	gh := &openPRGH{createURL: "https://github.com/o/r/pull/42"}
	env, bare := newOpenPREnv(t, gh)

	// Push an unrelated commit directly to origin/<branch> so pushing env.tip would require a force push.
	otherClone := t.TempDir()
	gitT(t, otherClone, "clone", "-q", bare, ".")
	gitT(t, otherClone, "checkout", "-q", "-b", env.wt.Branch)
	writeFile(t, otherClone, "diverged.txt", []byte("someone else\n"))
	gitT(t, otherClone, "add", "-A")
	gitT(t, otherClone, "commit", "-q", "-m", "diverged remote commit")
	gitT(t, otherClone, "push", "-q", "origin", env.wt.Branch)
	remoteTip := gitT(t, bare, "rev-parse", "refs/heads/"+env.wt.Branch)

	out := env.act(t, env.openPRJob("op-no-force"))
	if out.Code == "" {
		t.Fatalf("expected non-fast-forward push to be refused, got %+v", out)
	}
	if got := gitT(t, bare, "rev-parse", "refs/heads/"+env.wt.Branch); got != remoteTip {
		t.Fatalf("remote branch was overwritten: got %s, want %s", got, remoteTip)
	}
	if gh.hasCall("create") {
		t.Fatal("gh pr create was called after push failed")
	}
}

func TestCompareJobIsReadOnly(t *testing.T) {
	if !adapter.ReadOnly(runnerwire.Job{Kind: runnerwire.JobCompare}) {
		t.Fatal("adapter.ReadOnly(JobCompare) = false, want true")
	}

	// Verify read-only permission modes for compare jobs across adapters.
	cfg := &Config{PermissionMode: "bypassPermissions"}
	for _, tc := range []struct {
		agent    string
		wantMode string
	}{
		{"codex", "read-only"},
		{"gemini", "default"},
		{"opencode", "read-only"},
		{"antigravity", "plan"},
		{"claude", adapter.DefaultClaudePermissionMode},
	} {
		mode, text := runPermission(cfg, runnerwire.Job{Kind: runnerwire.JobCompare, Agent: tc.agent}, false)
		if mode != tc.wantMode || !strings.Contains(text, "for a compare") {
			t.Errorf("%s compare permission = (%q, %q), want mode %q with 'for a compare'", tc.agent, mode, text, tc.wantMode)
		}
	}

	// Run a real compare job end-to-end: repo config stripped, agent edits discarded, no commit/PR/worktree left.
	gh := &ghFake{url: "https://github.com/o/r/pull/55"}
	env := startPRRunner(t, gh, nil)
	bare := withOrigin(t, env.repo)
	// Plant repo-local agent config on main that read-only runs must strip.
	writeFile(t, env.repo, ".claude/settings.json", []byte(`{"hooks":{}}`))
	writeFile(t, env.repo, ".mcp.json", []byte(`{"mcpServers":{}}`))
	gitT(t, env.repo, "add", "-A")
	gitT(t, env.repo, "commit", "-q", "-m", "add repo config")
	gitT(t, env.repo, "push", "-q", "origin", "main")
	env.hub.hello()

	job := runnerwire.Job{
		RunID:       "run-cmp-1",
		Project:     "demo",
		NodeID:      "n-1",
		NodeTitle:   "Compare entries",
		Prompt:      "edit: try to modify files in compare\n```json\n{\"scores\":[{\"entry\":\"A\",\"score\":8,\"why\":\"good\"}],\"pick\":\"A\"}\n```",
		Agent:       "claude",
		Model:       "sonnet",
		RepoID:      "r1",
		RequestedBy: "patrick",
		Kind:        runnerwire.JobCompare,
		NoPR:        true,
		Contest:     &runnerwire.JobContest{ID: "contest-1", Label: "J", Of: 2},
	}
	env.hub.offer(job)
	finish := env.hub.wait("run-cmp-1", runnerwire.TypeFinish)
	if finish.Result == nil || finish.Result.Status != runnerwire.RunSucceeded {
		t.Fatalf("compare finish = %+v", finish.Result)
	}

	// No changes or PR messages sent; nothing pushed; worktree and branch removed.
	for _, msg := range env.hub.messages("run-cmp-1") {
		if msg.Type == runnerwire.TypeChanges {
			t.Fatalf("compare job sent changes: %+v", msg.Changes)
		}
		if msg.Type == runnerwire.TypePR {
			t.Fatalf("compare job sent PR: %+v", msg.PR)
		}
	}
	if creates, _ := gh.createCalls(); len(creates) != 0 {
		t.Fatalf("compare job called gh pr create: %v", creates)
	}
	if got := gitT(t, bare, "branch", "--list", "agent/*"); got != "" {
		t.Fatalf("compare job pushed branch: %q", got)
	}
	if got := gitT(t, env.repo, "branch", "--list", "agent/*"); got != "" {
		t.Fatalf("compare job left branch behind: %q", got)
	}
	if worktreeExists(filepath.Join(env.client.Config.DataDir, "worktrees", "run-cmp-1")) {
		t.Fatal("compare job left its worktree behind")
	}
	if !hubHasEvent(env, "run-cmp-1", "removed the worktree's own agent config") {
		t.Error("compare job did not strip repo-local agent config")
	}
	if !hubHasEvent(env, "run-cmp-1", "the reviewer changed files; discarded") {
		t.Error("compare job did not log discarded edits")
	}
	// Verify claude got read-only flags.
	var args []string
	if raw := env.recorded(t, "run-cmp-1", "args"); json.Unmarshal([]byte(raw), &args) != nil {
		t.Fatalf("parse args %s", raw)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--setting-sources user") || !strings.Contains(joined, "--disallowedTools "+adapter.ClaudeReadOnlyDeny) {
		t.Errorf("compare claude args lack read-only flags: %s", joined)
	}
}

func TestCheckGlobalAllowsContestAndCompare(t *testing.T) {
	cfg := &Config{}

	// Global contest entry (implement + Contest).
	entry := globalTestJob("run-gc-a", "do it")
	entry.Contest = &runnerwire.JobContest{ID: "contest-1", Label: "A", Of: 2}
	entry.Coordinated = true
	if reason := checkGlobal(&entry, cfg); reason != "" {
		t.Fatalf("global contest entry declined: %q", reason)
	}

	// Global contest reviewer (compare + Contest label J).
	cmpJob := globalTestJob("run-gc-j", "score them")
	cmpJob.Kind = runnerwire.JobCompare
	cmpJob.Contest = &runnerwire.JobContest{ID: "contest-1", Label: "J", Of: 2}
	if reason := checkGlobal(&cmpJob, cfg); reason != "" {
		t.Fatalf("global contest compare declined: %q", reason)
	}

	// Global compare WITHOUT contest is refused.
	bareCmp := globalTestJob("run-gc-bad", "score them")
	bareCmp.Kind = runnerwire.JobCompare
	if reason := checkGlobal(&bareCmp, cfg); reason != "malformed global job" {
		t.Fatalf("global compare without contest = %q, want malformed global job", reason)
	}

	// Global compare with non-J label is refused.
	badLabel := globalTestJob("run-gc-badlabel", "score them")
	badLabel.Kind = runnerwire.JobCompare
	badLabel.Contest = &runnerwire.JobContest{ID: "contest-1", Label: "A", Of: 2}
	if reason := checkGlobal(&badLabel, cfg); reason != "malformed global job" {
		t.Fatalf("global compare with label A = %q, want malformed global job", reason)
	}

	// Global review is still refused.
	review := globalTestJob("run-gc-rev", "review")
	review.Kind = runnerwire.JobReview
	if reason := checkGlobal(&review, cfg); reason != "malformed global job" {
		t.Fatalf("global review = %q, want malformed global job", reason)
	}

	// Global handoff is still refused even with contest.
	handoff := entry
	handoff.HandoffBranch = "nodevas/handoff/run-x"
	if reason := checkGlobal(&handoff, cfg); reason != "malformed global job" {
		t.Fatalf("global contest with handoff = %q, want malformed global job", reason)
	}
}

func TestEndNoPRKeepsBranchOpensNoPR(t *testing.T) {
	gh := &ghFake{url: "https://github.com/o/r/pull/88"}
	gh.setView("OPEN", "pending")
	var bare string
	env, started := startInteractiveRunner(t, func(c *Client) {
		c.endQuitGap = 10 * time.Millisecond
		c.endGrace = 40 * time.Millisecond
		cmds := c.Commands.(*fakeCommands)
		cmds.paths["gh"] = "/bin/gh"
		cmds.gh = gh.run
	})
	bare = withOrigin(t, env.repo)
	env.hub.hello()

	// Node interactive run with autoPR on and NoPR initially false (normal node run).
	job := interactiveJob("run-end-nopr")
	job.NoPR = false
	env.hub.offer(job)
	st := waitStarted(t, started)
	accept := env.hub.wait("run-end-nopr", runnerwire.TypeAccept).Accept

	// Simulate edits made in the worktree before take-over End with NoPR.
	if err := os.WriteFile(filepath.Join(st.cmd.Dir, "wip.txt"), []byte("work in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Request graceful End with NoPR=true over the hub socket (node take-over).
	env.hub.send(runnerwire.Envelope{Type: runnerwire.TypeEnd, RunID: "run-end-nopr", NoPR: true})
	finish := env.hub.wait("run-end-nopr", runnerwire.TypeFinish)
	if finish.Result == nil || finish.Result.Status != runnerwire.RunSucceeded || finish.Result.EndReason != runnerwire.EndReasonEnded {
		t.Fatalf("finish = %+v, want succeeded/ended", finish.Result)
	}

	// Work is committed on the branch locally, but NO PR is opened and nothing is pushed.
	var changes *runnerwire.RunChanges
	for _, msg := range env.hub.messages("run-end-nopr") {
		if msg.Type == runnerwire.TypePR {
			t.Fatalf("End with NoPR opened a PR: %+v", msg.PR)
		}
		if msg.Type == runnerwire.TypeChanges && msg.Changes != nil {
			changes = msg.Changes
		}
	}
	if changes == nil || changes.Commit == "" {
		t.Fatalf("End with NoPR did not commit changes: %+v", changes)
	}
	if got := gitT(t, env.repo, "rev-parse", "refs/heads/"+accept.Branch); got != changes.Commit {
		t.Fatalf("branch %s at %q, want %s", accept.Branch, got, changes.Commit)
	}
	if got := gitT(t, bare, "branch", "--list", "agent/*"); got != "" {
		t.Fatalf("End with NoPR pushed branch: %q", got)
	}
	if creates, _ := gh.createCalls(); len(creates) != 0 {
		t.Fatalf("End with NoPR called gh pr create: %v", creates)
	}
}

func TestCapabilitiesAdvertiseCoord(t *testing.T) {
	if !slices.Contains(capabilities(nil), runnerwire.CapCoord) {
		t.Fatalf("capabilities(nil) = %v, want %q", capabilities(nil), runnerwire.CapCoord)
	}
	if !slices.Contains(capabilities(&Config{}), runnerwire.CapCoord) {
		t.Fatalf("capabilities(&Config{}) = %v, want %q", capabilities(&Config{}), runnerwire.CapCoord)
	}
	off := false
	cfg := &Config{Interactive: &off, GlobalRuns: &off}
	if !slices.Contains(capabilities(cfg), runnerwire.CapCoord) {
		t.Fatalf("capabilities(off cfg) = %v, want %q", capabilities(cfg), runnerwire.CapCoord)
	}
}

func TestMCPArgsCoordinatedToolset(t *testing.T) {
	repo := runnerwire.Repo{ID: "r1", Name: "web"}
	job := runnerwire.Job{
		RunID:       "run-abcdef",
		Project:     "demo",
		NodeID:      "n-1",
		Agent:       "claude",
		Model:       "sonnet",
		Coordinated: true,
	}
	args := mcpArgs("/tmp/mcp-server", job, repo)
	i := slices.Index(args, "--toolset")
	if i < 0 || i+1 >= len(args) || args[i+1] != "coordinated" {
		t.Fatalf("coordinated mcpArgs = %q, want --toolset coordinated", args)
	}

	// Role job with Coordinated drops claim/status/release/subgraph tools from --tools.
	roleJob := job
	roleJob.Role = "executor"
	roleJob.Tools = []string{"claim_task", "get_node", "set_node_status", "release_task", "create_subgraph", "check_inbox"}
	rargs := mcpArgs("/tmp/mcp-server", roleJob, repo)
	ti := slices.Index(rargs, "--tools")
	if ti < 0 || ti+1 >= len(rargs) {
		t.Fatalf("coordinated role mcpArgs = %q, want --tools", rargs)
	}
	gotTools := strings.Split(rargs[ti+1], ",")
	for _, bad := range []string{"claim_task", "set_node_status", "release_task", "create_subgraph"} {
		if slices.Contains(gotTools, bad) {
			t.Errorf("coordinated role --tools kept %q: %q", bad, gotTools)
		}
	}
	if !slices.Contains(gotTools, "get_node") || !slices.Contains(gotTools, "check_inbox") {
		t.Errorf("coordinated role --tools dropped allowed tools: %q", gotTools)
	}

	// Verify JSON serialization of ContestEntry on wire still passes through runner.
	raw, _ := json.Marshal(runnerwire.JobContest{ID: "c1", Label: "B", Of: 2})
	if !strings.Contains(string(raw), `"label":"B"`) {
		t.Fatalf("unexpected JobContest json: %s", raw)
	}
}
