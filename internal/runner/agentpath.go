package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Agents the owner added by hand ("I already have it"; config agentPaths):
// a program the runner did not find on PATH, picked in the app on the
// built-in runner or set with `nodevas runner set agentPath <kind> <path>`.
// Running it is running code, so it is checked before it is kept
// (checkAgentPath, then --version in applySettings) and looked at again,
// with a stat, every time it is used (agentBinary): a file that is gone
// falls back to PATH, never to anything else. The path stays on this
// computer: it is in Info.Local for the owner, never in Info.Agents.

// errAgentPathGone: the program added for an agent kind is gone, and the
// kind's program is not on PATH either.
var errAgentPathGone = errors.New("the program added for it is no longer there")

// agentPathGoneError is Agent.Error for errAgentPathGone; the app matches it
// (runnerAgentProblem → local.agentPathGone).
func agentPathGoneError(kind string) string {
	return kind + ": " + errAgentPathGone.Error()
}

// agentPathFor is the program added for kind in cfg, or "".
func agentPathFor(cfg *Config, kind string) string {
	if cfg == nil {
		return ""
	}
	return cfg.AgentPaths[kind]
}

// agentBinary is the program to run for kind: the one added for it while it
// still passes checkAgentPath against cfg as it is now (where its link points
// now, the folders agents work in now: a folder added since that holds it
// makes it unusable), else kind's program on PATH. A program added and gone
// or no longer usable, with none on PATH, is errAgentPathGone.
func agentBinary(cmds CommandRunner, cfg *Config, kind string) (string, error) {
	added := agentPathFor(cfg, kind)
	if added != "" {
		if _, err := checkAgentPath(cfg, kind, added); err == nil {
			return added, nil
		}
	}
	bin, err := cmds.LookPath(adapter.Binary(kind))
	if err != nil && added != "" {
		return "", errAgentPathGone
	}
	return bin, err
}

// agentPathTempDirs are the temp dirs an added program may not be in: this
// process's, and the ones every account on this computer can write to (on
// macOS os.TempDir is a per-user folder, and /tmp is not it); a var so
// tests, whose files are all under the real one, can name another.
var agentPathTempDirs = func() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return append(dirs, filepath.Join(root, "Temp"))
	}
	return append(dirs, "/tmp", "/private/tmp", "/var/tmp")
}

// checkAgentPath checks path as the program for agent kind, on the disk this
// time (checkAgentPathShape checks its form): it must be there, be (or, as a
// link or junction, finally name) a regular file this computer can start,
// and neither it nor the file it names may be inside a folder agents work in
// (cfg's repos), the runner's data (worktrees among it) or a temp dir, where
// an agent could have put it. A link must name a file that is the agent's
// too (agentTargetNamed), never a shell or another program elsewhere.
//
// It returns path, cleaned, which is what is kept: the link the person
// picked keeps working when its installer moves it to a new version, its
// folder (where node lives, for an npm CLI) is the one put on PATH, and
// agentBinary checks it again, link and all, at every use. Refusals never
// name the folder.
func checkAgentPath(cfg *Config, kind, path string) (string, error) {
	if err := checkAgentPathShape(kind, path); err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	missing := &agentPathError{runnerwire.AgentPathRefusedMissing, "agentPath: that file is not there"}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", missing
		}
		return "", &agentPathError{runnerwire.AgentPathRefusedMissing, "agentPath: that file cannot be read"}
	}
	real, err := resolveFolder(path)
	if err != nil {
		// A link to nothing names no program.
		return "", missing
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", missing
	}
	if !info.Mode().IsRegular() {
		return "", &agentPathError{runnerwire.AgentPathRefusedNotProgram, "agentPath: that is not a file"}
	}
	if !agentTargetNamed(kind, path, real) {
		return "", &agentPathError{runnerwire.AgentPathRefusedName,
			fmt.Sprintf("agentPath: that link names a program that is not %s's", kind)}
	}
	g := &repoGuard{fold: runtime.GOOS == "windows" || runtime.GOOS == "darwin"}
	for _, dir := range agentPathForbidden(cfg) {
		if g.within(real, dir) || g.within(path, dir) {
			return "", &agentPathError{runnerwire.AgentPathRefusedPlace,
				"agentPath: that file is inside a folder agents work in, the runner's data or the temp folder"}
		}
	}
	if !executableFile(real, info) {
		return "", &agentPathError{runnerwire.AgentPathRefusedNotProgram, "agentPath: that file is not a program this computer can start"}
	}
	return path, nil
}

// agentTargetNamed reports whether real, the file the picked path finally
// names, is kind's program: path itself (its name was checked), a file named
// like the program (one ending aside: codex.js, claude.exe), or a file in a
// folder whose name holds the program's or the kind's (claude-code/cli.js,
// gemini-cli/dist/index.js, claude/versions/2.1.0). /usr/bin/python3 or
// cmd.exe behind a link named claude is none of these.
func agentTargetNamed(kind, path, real string) bool {
	if real == path {
		return true
	}
	names := []string{strings.ToLower(adapter.Binary(kind)), strings.ToLower(kind)}
	base := strings.ToLower(filepath.Base(real))
	if slices.Contains(names, strings.TrimSuffix(base, filepath.Ext(base))) {
		return true
	}
	for dir := filepath.Dir(real); ; {
		name := strings.ToLower(filepath.Base(dir))
		for _, want := range names {
			if strings.Contains(name, want) {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// agentPathForbidden are the folders an added program may not be in, each
// as given and as resolved.
func agentPathForbidden(cfg *Config) []string {
	var dirs []string
	if cfg != nil {
		for _, repo := range cfg.Repos {
			dirs = append(dirs, repo.Path)
		}
		if data, err := cfg.RunDataDir(); err == nil {
			dirs = append(dirs, data)
		}
	}
	dirs = append(dirs, agentPathTempDirs()...)
	return withResolved(dedupe(dirs))
}

// agentPathEnv is what syncAgentPathDirs did to PATH: base is PATH without
// the added programs' folders, applied what it set PATH to last.
var agentPathEnv struct {
	sync.Mutex
	captured      bool
	base, applied string
}

// syncAgentPathDirs puts the folders of the programs added in cfg at the
// end of the process's PATH, once each, and takes away the ones no longer
// added: a program added by hand may start others from its own folder (a
// `#!/usr/bin/env node` CLI needs node), and an app opened from the desktop
// has a shorter PATH than a terminal. Appending never puts one of them before
// a system program. PATH changed by anything else since is taken as the new
// base.
func syncAgentPathDirs(cfg *Config) {
	agentPathEnv.Lock()
	defer agentPathEnv.Unlock()
	current := os.Getenv("PATH")
	if !agentPathEnv.captured || current != agentPathEnv.applied {
		agentPathEnv.base, agentPathEnv.captured = current, true
	}
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	key := func(dir string) string {
		dir = filepath.Clean(dir)
		if fold {
			return strings.ToLower(dir)
		}
		return dir
	}
	var seen []string
	for _, dir := range filepath.SplitList(agentPathEnv.base) {
		if dir != "" {
			seen = append(seen, key(dir))
		}
	}
	next := agentPathEnv.base
	if cfg != nil {
		for _, kind := range sortedKeys(cfg.AgentPaths) {
			path := cfg.AgentPaths[kind]
			if path == "" || !filepath.IsAbs(path) {
				continue
			}
			dir := filepath.Dir(path)
			if slices.Contains(seen, key(dir)) {
				continue
			}
			seen = append(seen, key(dir))
			if next != "" {
				next += string(os.PathListSeparator)
			}
			next += dir
		}
	}
	agentPathEnv.applied = next
	if next != current {
		_ = os.Setenv("PATH", next)
	}
}

// commandRunner is c.Commands, or the real machine's.
func (c *Client) commandRunner() CommandRunner {
	if c.Commands != nil {
		return c.Commands
	}
	return SystemCommands{}
}

// agentPathAnswers is the check that the program at path answers as the
// agent: `--version`, bounded as a look's is.
func agentPathAnswers(cmds CommandRunner, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	if _, err := cmds.Run(ctx, path, "--version"); err != nil {
		return &agentPathError{runnerwire.AgentPathRefusedNoAnswer,
			fmt.Sprintf("agentPath: that file did not answer --version: %s",
				truncate(strings.ReplaceAll(err.Error(), path, filepath.Base(path)), 200))}
	}
	return nil
}
