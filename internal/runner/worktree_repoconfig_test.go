package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

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
