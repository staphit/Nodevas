package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// commitRepoConfig commits agent config of every kind into repo's main.
func commitRepoConfig(t *testing.T, repo string) {
	t.Helper()
	files := map[string]string{
		".opencode/plugin/marker.js": "export const M = async () => ({})\n",
		"opencode.json":              `{"mcp":{"evil":{"type":"local","command":["x"]}}}`,
		".gemini/settings.json":      `{"hooks":{}}`,
		".agents/mcp_config.json":    `{"mcpServers":{}}`,
		".codex/config.toml":         "[mcp_servers.evil]\ncommand = \"x\"\n",
		".claude/settings.json":      `{"hooks":{}}`,
		".env":                       "GOOGLE_GEMINI_BASE_URL=https://example.test\n",
	}
	for name, content := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "agent config")
}

// A plan's agent never sees the repository's own agent config; what it
// writes of its own is removed after, and is not "changed files".
func TestPlanRemovesRepoConfig(t *testing.T) {
	env := startJobRunner(t, nil)
	commitRepoConfig(t, env.repo)
	env.hub.hello()
	job := testJob("run-plan-cfg", "repoconfig: plan it")
	job.Kind = runnerwire.JobPlan
	env.hub.offer(job)
	if r := env.hub.wait("run-plan-cfg", runnerwire.TypeFinish).Result; r.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", r)
	}
	if found := env.recorded(t, "run-plan-cfg", "found"); found != "." {
		t.Fatalf("the agent found the repository's config: %s", found)
	}
	events := eventsOf(env.hub.messages("run-plan-cfg"))
	if !hasEvent(events, "removed the worktree's own agent config") || !hasEvent(events, ".opencode") ||
		!hasEvent(events, ".claude/settings.json") {
		t.Errorf("no note of the removal: %+v", events)
	}
	if !hasEvent(events, "the agent wrote agent config into its read-only worktree; removed: .codex") {
		t.Errorf("no note of the agent's own config: %+v", events)
	}
	if hasEvent(events, "the planner changed files") {
		t.Errorf("the runner's own removals counted as the planner's changes: %+v", events)
	}
}

// A judge reads at whatever base the hub named, like a plan: its agent never
// sees that base's agent config, and runs read only.
func TestJudgeRemovesRepoConfig(t *testing.T) {
	env := startJobRunner(t, nil)
	commitRepoConfig(t, env.repo)
	env.hub.hello()
	job := testJob("run-judge-cfg", "repoconfig: judge it")
	job.Kind, job.Role = runnerwire.JobJudge, "reviewer"
	job.Acceptance = []runnerwire.AcceptanceItem{{ID: "a1", Text: "it works"}}
	env.hub.offer(job)
	env.hub.wait("run-judge-cfg", runnerwire.TypeFinish)
	if found := env.recorded(t, "run-judge-cfg", "found"); found != "." {
		t.Fatalf("the judge found the repository's config: %s", found)
	}
	var args []string
	if err := json.Unmarshal([]byte(env.recorded(t, "run-judge-cfg", "args")), &args); err != nil {
		t.Fatal(err)
	}
	if line := strings.Join(args, " "); !strings.Contains(line, "--setting-sources user --disallowedTools "+adapter.ClaudeReadOnlyDeny) {
		t.Errorf("judge args: %s", line)
	}
	events := eventsOf(env.hub.messages("run-judge-cfg"))
	if !hasEvent(events, "removed the worktree's own agent config") || !hasEvent(events, "for a judge") {
		t.Errorf("events = %+v", events)
	}
}

// The owner's shell-capable settings do not reach a judge, which runs read
// only like a review.
func TestShellAccessJudge(t *testing.T) {
	cfg := Config{PermissionMode: "bypassPermissions", AllowedTools: []string{"Bash"}, CodexSandbox: "danger-full-access",
		GeminiApprovalMode: "yolo", OpenCodePermission: "full", AntigravityMode: "full"}
	for _, agent := range []string{"claude", "codex", "gemini", "opencode", "antigravity"} {
		job := testJob("run-1", "x")
		job.Agent, job.Kind = agent, runnerwire.JobJudge
		if shell := cfg.ShellAccess(job); shell != "" {
			t.Errorf("%s judge: ShellAccess = %q", agent, shell)
		}
	}
}

// An implement run on the owner's branch keeps the repository's config.
func TestImplementKeepsRepoConfig(t *testing.T) {
	env := startJobRunner(t, nil)
	commitRepoConfig(t, env.repo)
	env.hub.hello()
	env.hub.offer(testJob("run-impl-cfg", "repoconfig: do it"))
	env.hub.wait("run-impl-cfg", runnerwire.TypeFinish)
	found := strings.Split(strings.TrimSuffix(env.recorded(t, "run-impl-cfg", "found"), "."), ",")
	for _, want := range []string{".opencode", "opencode.json", ".gemini", ".agents", ".codex", ".claude/settings.json", ".env"} {
		if !slices.Contains(found, want) {
			t.Errorf("implement run lost %s: %q", want, found)
		}
	}
}

func TestClearRepoConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gemini/settings.json")
	write(".claude/settings.json")
	write(".claude/agents/a.md")
	write("src/.agents/mcp_config.json")
	write("keep.txt")
	removed, err := clearRepoConfig(dir, []string{".gemini", ".claude/settings.json", ".codex", ".agents"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{".gemini", ".claude/settings.json"}) {
		t.Fatalf("removed %q", removed)
	}
	for name, gone := range map[string]bool{".gemini": true, ".claude/settings.json": true, ".claude/agents/a.md": false,
		"src/.agents/mcp_config.json": false, "keep.txt": false} {
		_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name)))
		if gone != os.IsNotExist(err) {
			t.Errorf("%s: gone %v, err %v", name, gone, err)
		}
	}
	// A file where a directory on the way should be is removed itself.
	write(".codex")
	if removed, err := clearRepoConfig(dir, []string{".codex/config.toml"}); err != nil || !slices.Equal(removed, []string{".codex"}) {
		t.Fatalf("removed %q, %v", removed, err)
	}
}

// A symlink on the way is removed itself; what it points at stays.
func TestClearRepoConfigSymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	owners := filepath.Join(outside, "settings.json")
	if err := os.WriteFile(owners, []byte("the owner's"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".claude")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("no symlinks here: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".gemini")); err != nil {
		t.Fatal(err)
	}
	removed, err := clearRepoConfig(dir, []string{".claude/settings.json", ".gemini"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{".claude", ".gemini"}) {
		t.Fatalf("removed %q", removed)
	}
	if b, err := os.ReadFile(owners); err != nil || string(b) != "the owner's" {
		t.Fatalf("the link's target was touched: %q, %v", b, err)
	}
}

func TestChangedFilesSkipsRepoConfig(t *testing.T) {
	repo := newTestRepo(t)
	commitRepoConfig(t, repo)
	if _, err := clearRepoConfig(repo, []string{".gemini", ".env", ".opencode", "opencode.json", ".agents", ".codex",
		".claude/settings.json"}); err != nil {
		t.Fatal(err)
	}
	if out, err := changedFiles(t.Context(), repo); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("changed %q, %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := changedFiles(t.Context(), repo); err != nil || !strings.Contains(out, "README") {
		t.Fatalf("changed %q, %v", out, err)
	}
}

// On Windows, a directory junction (what an unprivileged user can make) on
// the way, or as the path itself, is removed itself.
func TestClearRepoConfigJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows'")
	}
	dir, outside := t.TempDir(), t.TempDir()
	owners := filepath.Join(outside, "settings.json")
	if err := os.WriteFile(owners, []byte("the owner's"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".claude", ".gemini"} {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, name), outside).CombinedOutput(); err != nil {
			t.Fatalf("mklink: %v %s", err, out)
		}
	}
	removed, err := clearRepoConfig(dir, []string{".claude/settings.json", ".gemini"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{".claude", ".gemini"}) {
		t.Fatalf("removed %q", removed)
	}
	if b, err := os.ReadFile(owners); err != nil || string(b) != "the owner's" {
		t.Fatalf("the junction's target was touched: %q, %v", b, err)
	}
}

// Agent config a run writes is not committed with its work: a later run on
// that branch (a revise, a handoff, an implement or judge at that base) would
// load it, and some of it runs programs as the owner. The run says what it
// left out, and the rest of the work is committed as usual.
func TestRunDoesNotCommitAgentConfig(t *testing.T) {
	env := startJobRunner(t, nil)
	env.hub.hello()
	env.hub.offer(testJob("run-plant", "plant: do it"))
	finish := env.hub.wait("run-plant", runnerwire.TypeFinish)
	if finish.Result == nil || finish.Result.Status != runnerwire.RunSucceeded {
		t.Fatalf("result = %+v", finish.Result)
	}
	tree := strings.Fields(gitT(t, env.repo, "ls-tree", "-r", "--name-only", BranchName("n-1", "run-plant")))
	if !slices.Contains(tree, "hello.txt") {
		t.Fatalf("the run's work was not committed: %q", tree)
	}
	for _, name := range []string{".codex/config.toml", ".opencode/plugin/x.js", ".Claude/settings.json", "opencode.json", ".env"} {
		if slices.Contains(tree, name) {
			t.Errorf("the run committed %s: %q", name, tree)
		}
	}
	// Deeper down, a CLI does not read it as the repository's config.
	if !slices.Contains(tree, "docs/.gemini/settings.json") {
		t.Errorf("the run lost docs/.gemini/settings.json: %q", tree)
	}
	for _, msg := range env.hub.messages("run-plant") {
		if msg.Type != runnerwire.TypeChanges {
			continue
		}
		for _, f := range msg.Changes.Files {
			if strings.HasPrefix(f.Path, ".codex") || f.Path == "opencode.json" {
				t.Errorf("changes list %s", f.Path)
			}
		}
	}
	if events := eventsOf(env.hub.messages("run-plant")); !hasEvent(events, "left out of the commit") ||
		!hasEvent(events, ".codex") {
		t.Errorf("events = %+v", events)
	}
}

// Agent config a commit leaves out stays in the worktree, as the run's
// events tell the person: removing the worktree would throw it away. That
// holds for a run whose only change is that config, and for one whose other
// work went to a handoff branch, was pushed to the PR it revised, or went
// into a PR that was then merged.
func TestWithheldConfigKeepsTheWorktree(t *testing.T) {
	kept := func(t *testing.T, env *jobEnv, runID string) {
		t.Helper()
		path := filepath.Join(env.client.Config.DataDir, "worktrees", runID, ".codex", "config.toml")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the agent config left out of the commit is gone: %v", err)
		}
	}
	t.Run("nothing else changed", func(t *testing.T) {
		env := startJobRunner(t, nil)
		env.hub.hello()
		env.hub.offer(testJob("run-cfg-only", "repoconfig: add an MCP server"))
		finish := env.hub.wait("run-cfg-only", runnerwire.TypeFinish)
		if finish.Result == nil || finish.Result.Status != runnerwire.RunSucceeded {
			t.Fatalf("result = %+v", finish.Result)
		}
		kept(t, env, "run-cfg-only")
		if events := eventsOf(env.hub.messages("run-cfg-only")); hasEvent(events, "worktree removed") ||
			!hasEvent(events, "worktree kept at") {
			t.Errorf("events = %+v", events)
		}
	})
	t.Run("handed off", func(t *testing.T) {
		env := startJobRunner(t, nil)
		env.hub.hello()
		env.hub.offer(testJob("run-cfg-q", "plant-quota: add hello"))
		finish := env.hub.wait("run-cfg-q", runnerwire.TypeFinish)
		if finish.Result == nil || finish.Result.HandoffBranch == "" {
			t.Fatalf("result = %+v", finish.Result)
		}
		kept(t, env, "run-cfg-q")
	})
	t.Run("revised", func(t *testing.T) {
		env := startReviewRunner(t, nil)
		env.hub.hello()
		env.hub.offer(prJob("run-cfg-rvs", runnerwire.JobRevise, "plant: Fix it"))
		result := env.hub.wait("run-cfg-rvs", runnerwire.TypeFinish).Result
		if result.Status != runnerwire.RunSucceeded {
			t.Fatalf("result = %+v", result)
		}
		if head := gitT(t, env.bare, "rev-parse", "refs/heads/"+reviewBranch); head == env.head {
			t.Fatal("the revise pushed nothing")
		}
		kept(t, env.jobEnv, "run-cfg-rvs")
	})
	t.Run("PR merged", func(t *testing.T) {
		gh := &ghFake{url: "https://github.com/o/r/pull/7"}
		gh.setView("OPEN", "")
		env := startPRRunner(t, gh, func(c *Client) { c.PRPollInterval = 30 * time.Millisecond })
		withOrigin(t, env.repo)
		env.hub.hello()
		env.hub.offer(testJob("run-cfg-pr", "plant: do it"))
		env.hub.wait("run-cfg-pr", runnerwire.TypeFinish)
		if got := env.client.jobs.watchedRuns(); !slices.Equal(got, []string{"run-cfg-pr"}) {
			t.Fatalf("watched %v", got)
		}
		gh.setView("MERGED", "success")
		deadline := time.Now().Add(10 * time.Second)
		for len(env.client.jobs.watchedRuns()) != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got := env.client.jobs.watchedRuns(); len(got) != 0 {
			t.Fatalf("still watched %v", got)
		}
		kept(t, env, "run-cfg-pr")
	})
}
