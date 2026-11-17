package runner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// agentFileName is kind's program as this OS names it: with .exe on Windows,
// where nothing else may be added as an agent.
func agentFileName(kind string) string {
	if runtime.GOOS == "windows" {
		return adapter.Binary(kind) + ".exe"
	}
	return adapter.Binary(kind)
}

// writeAgentFile makes an executable file for kind in dir.
func writeAgentFile(t *testing.T, dir, kind string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, agentFileName(kind))
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 1.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// noAgentTempDir makes checkAgentPath treat only a folder of the test's as
// the temp dir, since every file a test makes is under the real one.
func noAgentTempDir(t *testing.T) string {
	t.Helper()
	fake := t.TempDir()
	prev := agentPathTempDirs
	agentPathTempDirs = func() []string { return []string{fake} }
	t.Cleanup(func() { agentPathTempDirs = prev })
	return fake
}

// resolved is path as checkAgentPath keeps it.
func resolved(t *testing.T, path string) string {
	t.Helper()
	out, err := resolveFolder(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAgentBinaryPrefersConfiguredPath(t *testing.T) {
	noAgentTempDir(t)
	added := writeAgentFile(t, t.TempDir(), "claude")
	cmds := &fakeCommands{paths: map[string]string{"claude": "/bin/claude"}}
	cfg := &Config{AgentPaths: map[string]string{"claude": added}}
	if bin, err := agentBinary(cmds, cfg, "claude"); err != nil || bin != added {
		t.Fatalf("agentBinary = %q, %v; want %q", bin, err, added)
	}
	// Other kinds, and no config at all, look on PATH as before.
	if bin, err := agentBinary(cmds, nil, "claude"); err != nil || bin != "/bin/claude" {
		t.Fatalf("no config: %q, %v", bin, err)
	}
	if _, err := agentBinary(cmds, cfg, "codex"); err == nil || errors.Is(err, errAgentPathGone) {
		t.Fatalf("codex, on no PATH and never added: %v", err)
	}
}

// The checks are made again at every use, against the config as it is now:
// a folder added later that holds the program (an agent's merged work could
// replace it) makes it unusable, and nothing else is run in its place.
func TestAgentBinaryRechecksPlaceAtUse(t *testing.T) {
	noAgentTempDir(t)
	tools := filepath.Join(t.TempDir(), "tools")
	added := writeAgentFile(t, tools, "claude")
	cfg := &Config{DataDir: t.TempDir(), AgentPaths: map[string]string{"claude": added}}
	none := &fakeCommands{paths: map[string]string{}}
	if bin, err := agentBinary(none, cfg, "claude"); err != nil || bin != added {
		t.Fatalf("before: %q, %v", bin, err)
	}
	cfg.Repos = []runnerwire.Repo{{ID: "r1", Name: "tools", Path: tools}}
	if bin, err := agentBinary(none, cfg, "claude"); !errors.Is(err, errAgentPathGone) || bin != "" {
		t.Fatalf("inside a folder added since: %q, %v", bin, err)
	}
	onPath := &fakeCommands{paths: map[string]string{"claude": "/bin/claude"}}
	if bin, err := agentBinary(onPath, cfg, "claude"); err != nil || bin != "/bin/claude" {
		t.Fatalf("inside a folder added since, on PATH: %q, %v", bin, err)
	}
}

// The link the person picked is kept, not the versioned file it named then:
// an installer that moves the link to a new version and deletes the old one
// leaves the agent working.
func TestAgentPathFollowsUpdatedLink(t *testing.T) {
	noAgentTempDir(t)
	share := filepath.Join(t.TempDir(), "claude", "versions")
	v1 := writeAgentFile(t, share, "claude")
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, agentFileName("claude"))
	symlinkOrSkip(t, v1, link)
	cfg := &Config{DataDir: t.TempDir()}
	kept, err := checkAgentPath(cfg, "claude", link)
	if err != nil || kept != link {
		t.Fatalf("checkAgentPath = %q, %v; want the link %q", kept, err, link)
	}
	cfg.AgentPaths = map[string]string{"claude": kept}
	v2 := filepath.Join(t.TempDir(), "claude", "versions", "next", agentFileName("claude"))
	writeAgentFile(t, filepath.Dir(v2), "claude")
	if err := os.Remove(v1); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, v2, link)
	if got, err := agentBinary(&fakeCommands{paths: map[string]string{}}, cfg, "claude"); err != nil || got != link {
		t.Fatalf("after the update: %q, %v; want %q", got, err, link)
	}
}

// A link named like the agent that names some other program (a shell, an
// interpreter) is not the agent; one that names the agent's own files is.
func TestCheckAgentPathLinkTarget(t *testing.T) {
	noAgentTempDir(t)
	cfg := &Config{DataDir: t.TempDir()}
	otherName := "python3"
	if runtime.GOOS == "windows" {
		otherName = "cmd.exe"
	}
	other := filepath.Join(t.TempDir(), "usr", "bin", otherName)
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), agentFileName("claude"))
	symlinkOrSkip(t, other, bad)
	if got, err := checkAgentPath(cfg, "claude", bad); agentPathRefusal(err) != runnerwire.AgentPathRefusedName || got != "" {
		t.Fatalf("a link to %s: %q, %v", otherName, got, err)
	}
	if runtime.GOOS != "windows" {
		// npm: bin/claude → lib/node_modules/@anthropic-ai/claude-code/cli.js
		cli := filepath.Join(t.TempDir(), "lib", "node_modules", "@anthropic-ai", "claude-code", "cli.js")
		if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cli, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		npm := filepath.Join(t.TempDir(), "bin", "claude")
		if err := os.MkdirAll(filepath.Dir(npm), 0o755); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, cli, npm)
		if got, err := checkAgentPath(cfg, "claude", npm); err != nil || got != npm {
			t.Fatalf("npm's link: %q, %v; want %q", got, err, npm)
		}
	}
}

// The name rule for a link's target, without a disk (links need a privilege
// on Windows).
func TestAgentTargetNamed(t *testing.T) {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	p := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	link := p("opt", "homebrew", "bin", "claude")
	for _, c := range []struct {
		kind, real string
		ok         bool
	}{
		{"claude", link, true},
		{"claude", p("opt", "homebrew", "lib", "node_modules", "@anthropic-ai", "claude-code", "cli.js"), true},
		{"claude", p("Users", "me", ".local", "share", "claude", "versions", "2.0.14"), true},
		{"codex", p("opt", "homebrew", "Cellar", "codex", "0.9", "bin", "codex"), true},
		{"codex", p("lib", "node_modules", "@openai", "codex", "bin", "codex.js"), true},
		{"gemini", p("lib", "node_modules", "@google", "gemini-cli", "dist", "index.js"), true},
		{"antigravity", p("Applications", "Antigravity.app", "Contents", "bin", "agy"), true},
		{"claude", p("usr", "bin", "python3"), false},
		{"claude", p("Windows", "System32", "cmd.exe"), false},
		{"codex", p("lib", "node_modules", "@anthropic-ai", "claude-code", "cli.js"), false},
	} {
		if got := agentTargetNamed(c.kind, link, c.real); got != c.ok {
			t.Errorf("%s → %s: %v, want %v", c.kind, c.real, got, c.ok)
		}
	}
}

// The temp folders every account can write to are refused, not only this
// process's own.
func TestAgentPathTempDirsIncludeShared(t *testing.T) {
	dirs := agentPathTempDirs()
	want := []string{"/tmp", "/private/tmp", "/var/tmp"}
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		want = []string{filepath.Join(root, "Temp")}
	}
	for _, w := range append(want, os.TempDir()) {
		if !slices.Contains(dirs, w) {
			t.Errorf("agentPathTempDirs %q lacks %q", dirs, w)
		}
	}
}

func TestAgentBinaryFallsBackToPATH(t *testing.T) {
	gone := filepath.Join(t.TempDir(), agentFileName("claude"))
	cfg := &Config{AgentPaths: map[string]string{"claude": gone}}
	onPath := &fakeCommands{paths: map[string]string{"claude": "/bin/claude"}}
	if bin, err := agentBinary(onPath, cfg, "claude"); err != nil || bin != "/bin/claude" {
		t.Fatalf("gone, on PATH: %q, %v", bin, err)
	}
	// Gone and not on PATH either: said so, and nothing else is run.
	if bin, err := agentBinary(&fakeCommands{paths: map[string]string{}}, cfg, "claude"); !errors.Is(err, errAgentPathGone) || bin != "" {
		t.Fatalf("gone everywhere: %q, %v", bin, err)
	}
	// A folder where the file was is not the program either.
	dir := filepath.Join(t.TempDir(), agentFileName("claude"))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.AgentPaths["claude"] = dir
	if _, err := agentBinary(&fakeCommands{paths: map[string]string{}}, cfg, "claude"); !errors.Is(err, errAgentPathGone) {
		t.Fatalf("a folder: %v", err)
	}
}

// checkAgentPath takes only a program named like the agent's, outside every
// folder agents work in, Nodevas's data and the temp dir, that this computer
// can start; it keeps the file a link names.
func TestCheckAgentPath(t *testing.T) {
	fakeTemp := noAgentTempDir(t)
	repo := t.TempDir()
	data := t.TempDir()
	cfg := &Config{DataDir: data, Repos: []runnerwire.Repo{{ID: "r1", Name: "demo", Path: repo}}}

	outside := writeAgentFile(t, filepath.Join(t.TempDir(), "bin"), "claude")
	if got, err := checkAgentPath(cfg, "claude", outside); err != nil || got != outside {
		t.Fatalf("a program outside: %q, %v", got, err)
	}

	folder := filepath.Join(t.TempDir(), agentFileName("claude"))
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	wrongName := writeAgentFile(t, t.TempDir(), "codex")
	ps1 := filepath.Join(t.TempDir(), "claude.ps1")
	if err := os.WriteFile(ps1, []byte("echo 1"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ path, refused string }{
		"relative":    {filepath.Join("bin", agentFileName("claude")), runnerwire.AgentPathRefusedInvalid},
		"nul":         {outside + "\x00", runnerwire.AgentPathRefusedInvalid},
		"too long":    {filepath.Join(filepath.Dir(outside), strings.Repeat("a", runnerwire.MaxAgentPathBytes), agentFileName("claude")), runnerwire.AgentPathRefusedInvalid},
		"missing":     {filepath.Join(t.TempDir(), agentFileName("claude")), runnerwire.AgentPathRefusedMissing},
		"directory":   {folder, runnerwire.AgentPathRefusedNotProgram},
		"wrong name":  {wrongName, runnerwire.AgentPathRefusedName},
		"windows ps1": {ps1, runnerwire.AgentPathRefusedName},
		"in a repo":   {writeAgentFile(t, filepath.Join(repo, "tools"), "claude"), runnerwire.AgentPathRefusedPlace},
		"in data":     {writeAgentFile(t, filepath.Join(data, "worktrees", "run-1"), "claude"), runnerwire.AgentPathRefusedPlace},
		"in temp":     {writeAgentFile(t, filepath.Join(fakeTemp, "x"), "claude"), runnerwire.AgentPathRefusedPlace},
	}
	if runtime.GOOS != "windows" {
		plain := filepath.Join(t.TempDir(), "claude")
		if err := os.WriteFile(plain, []byte("text"), 0o644); err != nil {
			t.Fatal(err)
		}
		cases["no x bit"] = struct{ path, refused string }{plain, runnerwire.AgentPathRefusedNotProgram}
	} else {
		bare := filepath.Join(t.TempDir(), "claude")
		if err := os.WriteFile(bare, []byte("#!/bin/sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		cases["windows no extension"] = struct{ path, refused string }{bare, runnerwire.AgentPathRefusedNotProgram}
	}
	for name, c := range cases {
		got, err := checkAgentPath(cfg, "claude", c.path)
		if err == nil || got != "" || agentPathRefusal(err) != c.refused {
			t.Errorf("%s: %q, %v (refused %q), want %q", name, got, err, agentPathRefusal(err), c.refused)
		}
		if err != nil && strings.Contains(err.Error(), filepath.Dir(c.path)) && c.refused != runnerwire.AgentPathRefusedInvalid {
			t.Errorf("%s: the refusal names the folder: %v", name, err)
		}
	}
	if _, err := checkAgentPath(cfg, "nope", outside); agentPathRefusal(err) != runnerwire.AgentPathRefusedInvalid {
		t.Errorf("unknown kind: %v", err)
	}

	// A link outside that names a program inside a repo is the program
	// inside the repo.
	inRepo := writeAgentFile(t, filepath.Join(repo, "linked"), "claude")
	link := filepath.Join(t.TempDir(), agentFileName("claude"))
	if err := os.Symlink(inRepo, link); err != nil {
		t.Logf("no symlinks here: %v", err)
	} else if got, err := checkAgentPath(cfg, "claude", link); agentPathRefusal(err) != runnerwire.AgentPathRefusedPlace {
		t.Errorf("a link into a repo: %q, %v", got, err)
	}
	// A link to a program elsewhere is kept as the link picked.
	elsewhere := writeAgentFile(t, filepath.Join(t.TempDir(), "real"), "claude")
	link2 := filepath.Join(t.TempDir(), agentFileName("claude"))
	if err := os.Symlink(elsewhere, link2); err == nil {
		if got, err := checkAgentPath(cfg, "claude", link2); err != nil || got != link2 {
			t.Errorf("a link elsewhere: %q, %v; want %q", got, err, link2)
		}
	}
}

// The folders of the added programs go at the end of PATH, once each, and
// leave it again when the program is no longer used.
func TestSyncAgentPathDirsAppendsOnce(t *testing.T) {
	base := strings.Join([]string{filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")}, string(os.PathListSeparator))
	t.Setenv("PATH", base)
	added := writeAgentFile(t, filepath.Join(t.TempDir(), "node", "bin"), "claude")
	also := writeAgentFile(t, filepath.Join(t.TempDir(), "agy"), "antigravity")
	cfg := &Config{AgentPaths: map[string]string{"claude": added, "antigravity": also}}
	want := base + string(os.PathListSeparator) + filepath.Dir(also) + string(os.PathListSeparator) + filepath.Dir(added)
	for range 3 {
		syncAgentPathDirs(cfg)
		if got := os.Getenv("PATH"); got != want {
			t.Fatalf("PATH = %q\nwant  %q", got, want)
		}
	}
	syncAgentPathDirs(&Config{AgentPaths: map[string]string{"claude": added}})
	if got := os.Getenv("PATH"); got != base+string(os.PathListSeparator)+filepath.Dir(added) {
		t.Fatalf("one dropped: PATH = %q", got)
	}
	syncAgentPathDirs(&Config{})
	if got := os.Getenv("PATH"); got != base {
		t.Fatalf("none: PATH = %q, want %q", got, base)
	}
	// A folder already on PATH is not added again.
	onPath := writeAgentFile(t, strings.Split(base, string(os.PathListSeparator))[1], "claude")
	syncAgentPathDirs(&Config{AgentPaths: map[string]string{"claude": onPath}})
	if got := os.Getenv("PATH"); got != base {
		t.Fatalf("already there: PATH = %q", got)
	}
	// Somebody else's change to PATH is the new base, not undone.
	t.Setenv("PATH", base+string(os.PathListSeparator)+"/elsewhere")
	syncAgentPathDirs(cfg)
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, base+string(os.PathListSeparator)+"/elsewhere"+string(os.PathListSeparator)) {
		t.Fatalf("after another change: PATH = %q", got)
	}
}
