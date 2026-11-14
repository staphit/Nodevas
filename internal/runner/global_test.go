package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// globalTestJob is a global agent's job as the hub offers it: no project, no
// node, a slug, and no pull request asked for.
func globalTestJob(runID, prompt string) runnerwire.Job {
	return runnerwire.Job{RunID: runID, NodeTitle: "Tidy the README", Prompt: prompt, Agent: "claude", Model: "sonnet",
		RepoID: "r1", RequestedBy: "patrick", Global: true, Slug: "tidy-readme", NoPR: true}
}

func declineOf(t *testing.T, env *jobEnv, runID string) string {
	t.Helper()
	return env.hub.wait(runID, runnerwire.TypeDecline).Reason
}

// A global offer must have the shape of one, and the owner may turn them
// off; any other offer must name a project and a node, or its `nodevas mcp
// --project ""` would work on whatever project the hub has open.
func TestGlobalOfferChecks(t *testing.T) {
	env := startJobRunner(t, nil)
	hello := env.hub.hello()
	for _, c := range []string{runnerwire.CapGlobalRuns, runnerwire.CapGlobalRunsOn, runnerwire.CapRemoteSettings} {
		if !slices.Contains(hello.Hello.Capabilities, c) {
			t.Errorf("hello lacks %s: %q", c, hello.Hello.Capabilities)
		}
	}
	if hello.Hello.MaxConcurrentCeiling != runnerwire.DefaultMaxConcurrentCeiling {
		t.Errorf("hello ceiling = %d", hello.Hello.MaxConcurrentCeiling)
	}
	malformed := map[string]func(*runnerwire.Job){
		"run-g-node":    func(j *runnerwire.Job) { j.NodeID = "n-1" },
		"run-g-project": func(j *runnerwire.Job) { j.Project = "demo" },
		"run-g-kind":    func(j *runnerwire.Job) { j.Kind = runnerwire.JobReview },
		"run-g-role":    func(j *runnerwire.Job) { j.Role = "executor" },
		"run-g-tools":   func(j *runnerwire.Job) { j.Tools = []string{"get_node"} },
		"run-g-slug":    func(j *runnerwire.Job) { j.Slug = "Tidy README" },
		"run-g-noslug":  func(j *runnerwire.Job) { j.Slug = "" },
		"run-g-handoff": func(j *runnerwire.Job) { j.HandoffBranch = "nodevas/handoff/run-x" },
		"run-g-branch":  func(j *runnerwire.Job) { j.Branch = "agent/x" },
	}
	for runID, spoil := range malformed {
		job := globalTestJob(runID, "ok: tidy")
		spoil(&job)
		env.hub.offer(job)
	}
	for runID := range malformed {
		if reason := declineOf(t, env, runID); reason != "malformed global job" {
			t.Errorf("%s: declined %q", runID, reason)
		}
	}
	noProject := testJob("run-noproject", "ok:")
	noProject.Project = ""
	noNode := testJob("run-nonode", "ok:")
	noNode.NodeID = ""
	env.hub.offer(noProject)
	env.hub.offer(noNode)
	for _, runID := range []string{"run-noproject", "run-nonode"} {
		if reason := declineOf(t, env, runID); !strings.Contains(reason, "no project or node") {
			t.Errorf("%s: declined %q", runID, reason)
		}
	}
	// Nothing ran: no worktree, no agent.
	declined := []string{"run-noproject", "run-nonode"}
	for runID := range malformed {
		declined = append(declined, runID)
	}
	for _, runID := range declined {
		if _, err := os.Stat(filepath.Join(env.record, runID)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: the agent ran", runID)
		}
	}

	off := startJobRunner(t, func(c *Client) {
		no := false
		c.Config.GlobalRuns = &no
	})
	hello = off.hub.hello()
	if !slices.Contains(hello.Hello.Capabilities, runnerwire.CapGlobalRuns) ||
		slices.Contains(hello.Hello.Capabilities, runnerwire.CapGlobalRunsOn) {
		t.Errorf("globalRuns off: capabilities %q", hello.Hello.Capabilities)
	}
	off.hub.offer(globalTestJob("run-g-off", "ok: tidy"))
	if reason := declineOf(t, off, "run-g-off"); !strings.Contains(reason, "turned off") ||
		!strings.Contains(reason, "nodevas runner set globalRuns on") {
		t.Errorf("globalRuns off: declined %q", reason)
	}
}

// A global agent's run: its own branch named by the slug, no board (no MCP
// server, no proxy door, a preamble without board tools), and without a pull
// request asked for, its commit kept on the runner: nothing pushed, no gh.
func TestGlobalRunKeepsItsBranch(t *testing.T) {
	gh := &ghFake{url: "https://github.com/o/r/pull/9"}
	gh.setView("OPEN", "pending")
	env := startPRRunner(t, gh, nil)
	bare := withOrigin(t, env.repo)
	env.hub.hello()
	env.hub.offer(globalTestJob("run-global", "edit: tidy the README"))
	finish := env.hub.wait("run-global", runnerwire.TypeFinish)
	if finish.Result.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", finish.Result)
	}
	branch := "agent/tidy-readme-run-glob"
	accept := env.hub.wait("run-global", runnerwire.TypeAccept).Accept
	if accept.Branch != branch || accept.Base != "main" {
		t.Fatalf("accept = %+v", accept)
	}
	var changes *runnerwire.RunChanges
	for _, msg := range env.hub.messages("run-global") {
		if msg.Type == runnerwire.TypePR {
			t.Fatalf("a run with no pull request asked reported one: %+v", msg.PR)
		}
		if msg.Changes != nil {
			changes = msg.Changes
		}
	}
	if changes == nil || len(changes.Commit) != 40 || len(changes.Files) != 3 {
		t.Fatalf("changes = %+v", changes)
	}
	if creates, _ := gh.createCalls(); len(creates) != 0 {
		t.Fatalf("gh pr create ran: %q", creates)
	}
	if got := gitT(t, env.repo, "rev-parse", "refs/heads/"+branch); got != changes.Commit {
		t.Fatalf("branch at %s, commit %s", got, changes.Commit)
	}
	if got := gitT(t, bare, "branch", "--list", "agent/*"); got != "" {
		t.Fatalf("pushed: %q", got)
	}
	wt := filepath.Join(env.client.Config.DataDir, "worktrees", "run-global")
	if _, err := os.Stat(filepath.Join(wt, "hello.txt")); err != nil {
		t.Fatalf("worktree not kept: %v", err)
	}
	message := gitT(t, env.repo, "log", "-1", "--format=%B", branch)
	if !strings.HasPrefix(message, "Tidy the README\n") || !strings.Contains(message, "Nodevas-Run: run-global") ||
		strings.Contains(message, "Nodevas-Node") {
		t.Fatalf("commit message:\n%s", message)
	}
	if !hubHasEvent(env, "run-global", "kept on the runner: branch "+branch) ||
		!hubHasEvent(env, "run-global", "no pull request asked") || !hubHasEvent(env, "run-global", "no board (a global agent)") {
		t.Errorf("events: %+v", env.hub.messages("run-global"))
	}

	// No board: the preamble names no board tool, the MCP config holds no
	// server, and no proxy door was made for it.
	stdin := env.recorded(t, "run-global", "stdin")
	if !strings.Contains(stdin, "as a global agent (no board, no project)") || !strings.Contains(stdin, "edit: tidy the README") {
		t.Errorf("stdin = %q", stdin)
	}
	for _, word := range []string{"claim_task", "set_node_status", "ask_user", "get_node"} {
		if strings.Contains(stdin, word) {
			t.Errorf("the prompt names %s:\n%s", word, stdin)
		}
	}
	var mcp struct {
		MCPServers map[string]adapter.MCPServer `json:"mcpServers"`
	}
	if raw := env.recorded(t, "run-global", "mcp.json"); json.Unmarshal([]byte(raw), &mcp) != nil || mcp.MCPServers == nil ||
		len(mcp.MCPServers) != 0 {
		t.Errorf("mcp config = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(env.record, "run-global", "mcp-server")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a run with no board got a proxy door: %v", err)
	}
	args := env.recorded(t, "run-global", "args")
	if strings.Contains(args, "mcp__nodevas") || strings.Contains(args, "permission-prompt-tool") {
		t.Errorf("args = %s", args)
	}
}

// Asked for a pull request, a global agent's run opens one like any run's,
// described as its task's, without a node or project.
func TestGlobalRunOpensAskedPR(t *testing.T) {
	gh := &ghFake{url: "https://github.com/o/r/pull/9"}
	gh.setView("OPEN", "pending")
	env := startPRRunner(t, gh, nil)
	bare := withOrigin(t, env.repo)
	env.hub.hello()
	job := globalTestJob("run-gpr", "edit: tidy the README")
	job.NoPR = false
	env.hub.offer(job)
	env.hub.wait("run-gpr", runnerwire.TypeFinish)
	if pr := env.hub.wait("run-gpr", runnerwire.TypePR).PR; pr == nil || pr.URL != gh.url || pr.Error != "" {
		t.Fatalf("pr = %+v", pr)
	}
	creates, bodies := gh.createCalls()
	if len(creates) != 1 || !slices.Contains(creates[0], "Tidy the README") {
		t.Fatalf("gh pr create = %q", creates)
	}
	body := bodies[0]
	if !strings.Contains(body, "- **Task:** Tidy the README (global agent)") || !strings.Contains(body, "Nodevas-Run: run-gpr") {
		t.Errorf("body:\n%s", body)
	}
	for _, unwanted := range []string{"**Node:**", "**Project:**", "Nodevas-Node", "popout"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body has %q:\n%s", unwanted, body)
		}
	}
	if got := gitT(t, bare, "branch", "--list", "agent/tidy-readme-run-gpr"); got == "" {
		t.Error("branch not pushed")
	}
}

// A global agent stopped by a usage limit is not handed off (there is no
// next run for its node): its partial work is committed on its own branch,
// kept with its worktree.
func TestGlobalUsageLimitKeepsWorkOnItsBranch(t *testing.T) {
	env := startPRRunner(t, nil, nil)
	env.hub.hello()
	env.hub.offer(globalTestJob("run-gq", "edit-quota: tidy"))
	result := env.hub.wait("run-gq", runnerwire.TypeFinish).Result
	if result.Status != runnerwire.RunFailed || result.FailureKind != runnerwire.FailureQuota ||
		result.HandoffBranch != "" || result.HandoffNote != "" {
		t.Fatalf("result = %+v", result)
	}
	if got := gitT(t, env.repo, "branch", "--list", "nodevas/handoff/*"); got != "" {
		t.Fatalf("handoff branch made: %q", got)
	}
	head := gitT(t, env.repo, "rev-parse", "refs/heads/agent/tidy-readme-run-gq")
	if files := gitT(t, env.repo, "show", "--name-only", "--format=", head); !strings.Contains(files, "hello.txt") {
		t.Fatalf("partial work not committed on the run's branch: %q", files)
	}
	if _, err := os.Stat(filepath.Join(env.client.Config.DataDir, "worktrees", "run-gq", "hello.txt")); err != nil {
		t.Fatalf("worktree not kept: %v", err)
	}
	dataDir := env.client.Config.DataDir
	if entries, _ := loadHandoffs(dataDir); len(entries) != 0 {
		t.Fatalf("handoff recorded: %+v", entries)
	}
}

// An interactive global agent: its TUI gets no nodevas server either.
func TestGlobalInteractiveRunHasNoBoard(t *testing.T) {
	env, started := startInteractiveRunner(t, nil)
	env.hub.hello()
	job := globalTestJob("run-gint", "ok: tidy")
	job.Mode = runnerwire.ModeInteractive
	env.hub.offer(job)
	st := waitStarted(t, started)
	args := st.cmd.Args
	if !strings.Contains(args[1], "as a global agent") || slices.Contains(args, "--allowedTools") ||
		strings.Contains(strings.Join(args, " "), "claim_task") {
		t.Fatalf("command %q", args)
	}
	i := slices.Index(args, "--mcp-config")
	if i < 0 {
		t.Fatalf("no MCP config: %q", args)
	}
	var mcp struct {
		MCPServers map[string]adapter.MCPServer `json:"mcpServers"`
	}
	raw, err := os.ReadFile(args[i+1])
	if err != nil || json.Unmarshal(raw, &mcp) != nil || mcp.MCPServers == nil || len(mcp.MCPServers) != 0 {
		t.Fatalf("mcp config = %s (%v)", raw, err)
	}
	st.term.exit <- 0
	if finish := env.hub.wait("run-gint", runnerwire.TypeFinish); finish.Result.Status != runnerwire.RunSucceeded {
		t.Fatalf("finish = %+v", finish.Result)
	}
	if !hubHasEvent(env, "run-gint", "no board (a global agent)") {
		t.Error("start event does not say there is no board")
	}
}

func TestDescribeGlobalOffer(t *testing.T) {
	job := globalTestJob("run-g1", "tidy it")
	text := describeOffer(Offer{Job: job, Repo: runnerwire.Repo{Name: "demo", Path: "/src/demo"}})
	for _, want := range []string{"global agent: Tidy the README", "repository:   demo", "base:         ",
		"branch:       agent/tidy-readme-run-g1", "pull request: no (kept on this machine)", "tidy it"} {
		if !strings.Contains(text, want) {
			t.Errorf("offer lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"project:", "node:"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("offer has %q:\n%s", unwanted, text)
		}
	}
	job.NoPR = false
	if text := describeOffer(Offer{Job: job}); strings.Contains(text, "pull request: no") {
		t.Errorf("an offer asking for a PR says no:\n%s", text)
	}
}

// A global run's workspace: its own branch is named by its slug, for
// run_info and for a local merge or a discard; a merge's trailers name no
// node.
func TestGlobalWorkspace(t *testing.T) {
	const slug = "tidy-readme"
	newEnv := func(t *testing.T) *wsEnv {
		t.Helper()
		hermeticGit(t)
		repo := newTestRepo(t)
		dataDir := t.TempDir()
		wt, err := CreateWorktree(context.Background(), repo, dataDir, wsRun, branchKey("", slug), "main")
		if err != nil {
			t.Fatal(err)
		}
		if wt.Branch != "agent/tidy-readme-run-ws" {
			t.Fatalf("branch %q", wt.Branch)
		}
		writeFile(t, wt.Path, "feature.txt", []byte("feature\n"))
		gitT(t, wt.Path, "add", "-A")
		gitT(t, wt.Path, "commit", "-q", "-m", "the run's work")
		env := &wsEnv{gh: &wsGH{}, repo: repo, dataDir: dataDir, wt: wt, log: &safeLog{},
			tip: gitT(t, wt.Path, "rev-parse", "HEAD"),
			cfg: &Config{DataDir: dataDir, Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: repo}}}}
		env.j = env.start(t)
		return env
	}
	info := func(t *testing.T, env *wsEnv, slug string) runnerwire.RunWorkspaceInfo {
		t.Helper()
		done := env.j.browseRun(context.Background(), wsOwner, runnerwire.RepoRequest{ID: "q", AccountID: wsOwner,
			RepoID: "r1", Op: runnerwire.RepoOpRunInfo, RunID: wsRun, Slug: slug, Branch: env.wt.Branch, Base: "main"})
		if done.Code != "" || done.RunInfo == nil {
			t.Fatalf("run_info = %+v", done)
		}
		return *done.RunInfo
	}
	env := newEnv(t)
	if got := info(t, env, slug); !got.Exists || !got.OwnBranch || got.Branch != env.wt.Branch {
		t.Fatalf("run_info = %+v", got)
	}
	if got := info(t, env, "other"); got.OwnBranch {
		t.Fatalf("another slug's branch counts as the run's own: %+v", got)
	}
	job := func(id, action, slug string) runnerwire.WorkspaceActionJob {
		return runnerwire.WorkspaceActionJob{ID: id, RunID: wsRun, AccountID: wsOwner, Action: action, RepoID: "r1",
			Slug: slug, Branch: env.wt.Branch, Base: "main", HeadSHA: env.tip}
	}
	if out := env.act(t, job("w-0", runnerwire.WorkspaceMergeLocal, "")); out.Code != runnerwire.WorkspaceErrNotOwnBranch {
		t.Fatalf("merge without the slug = %+v", out)
	}
	out := env.act(t, job("w-1", runnerwire.WorkspaceMergeLocal, slug))
	if out.Code != "" || !out.BranchDeleted {
		t.Fatalf("merge = %+v", out)
	}
	msg := gitT(t, env.repo, "log", "-1", "--format=%B", "main")
	if !strings.Contains(msg, "Merge branch '"+env.wt.Branch+"' into main") || !strings.Contains(msg, "Nodevas-Run: "+wsRun) ||
		strings.Contains(msg, "Nodevas-Node") {
		t.Fatalf("merge message:\n%s", msg)
	}

	env = newEnv(t)
	if out := env.act(t, job("d-1", runnerwire.WorkspaceDiscard, slug)); out.Code != "" || !out.WorktreeRemoved || !out.BranchDeleted {
		t.Fatalf("discard = %+v", out)
	}
	if worktreeExists(env.wt.Path) || env.branchExists(t) {
		t.Fatal("worktree or branch left")
	}
}

func TestBranchKey(t *testing.T) {
	for _, c := range []struct{ node, slug, want string }{
		{"n-1", "tidy", "n-1"}, {"", "tidy", "tidy"}, {"", "", runnerwire.DefaultGlobalSlug},
	} {
		if got := branchKey(c.node, c.slug); got != c.want {
			t.Errorf("branchKey(%q, %q) = %q", c.node, c.slug, got)
		}
	}
	if got := BranchName(branchKey("", "fix-the-login-page"), "0123456789abcdef"); got != "agent/fix-the-login-page-01234567" {
		t.Errorf("branch %q", got)
	}
}
