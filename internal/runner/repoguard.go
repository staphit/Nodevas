package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"nodevas/internal/runnerwire"
)

// Folders agents must never work in (repo admin, repoadmin.go). Adding a
// repo from the app is a click, not a typed command, so the runner refuses
// the folders where a click is most likely a mistake and an agent most
// harmful: a whole drive, the home folder (and the folders above it), the
// places keys, passwords and app data live, the system's own folders, and
// Nodevas's (runner tokens, worktrees). Each folder is checked as given and
// as resolved (symlinks and, on Windows, junctions), and so is the git top
// level a folder belongs to: a home folder that is itself a git repo (a
// dotfiles repo) is refused as home, whichever subfolder was picked.

// repoGuard holds the folders the rules name, so a test can name its own.
type repoGuard struct {
	// fold: the file system ignores case (Windows, macOS).
	fold bool
	// home is the user's home folder: refused, as is every folder above it,
	// and everything under a dot-folder directly in it is secret.
	home string
	// homeParents are refused as home themselves (C:\Users, /home, /Users).
	homeParents []string
	// homeLike are refused as home, with every folder above them (the
	// OneDrive roots).
	homeLike []string
	// secret are refused with everything under and above them (%APPDATA%,
	// %LOCALAPPDATA%, ~/Library).
	secret []string
	// system are refused with everything under them; systemTop names
	// top-level folders of any drive refused with everything under them
	// (patterns, as filepath.Match takes them); systemExact are refused
	// themselves only.
	system      []string
	systemTop   []string
	systemExact []string
	// nodevas are Nodevas's own folders: refused with everything under and
	// above them.
	nodevas []string
	// isRoot reports a drive, volume or file system root.
	isRoot func(string) bool
}

// repoGuard is the guard in effect: the test's, else this machine's.
func (c *Client) repoGuard() *repoGuard {
	if c.guard != nil {
		return c.guard
	}
	return defaultRepoGuard(c.nodevasFolders())
}

// nodevasFolders are this runner's own: the user config dir's nodevas
// folder (runner tokens), its run data dir (worktrees) and the folder of its
// settings file.
func (c *Client) nodevasFolders() []string {
	var dirs []string
	if root, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(root, "nodevas"))
	}
	if data, err := c.cfg().RunDataDir(); err == nil {
		dirs = append(dirs, data)
	}
	if c.ConfigPath != "" {
		if abs, err := filepath.Abs(c.ConfigPath); err == nil {
			dirs = append(dirs, filepath.Dir(abs))
		}
	}
	return dirs
}

// withResolved is dirs with each one's resolved form beside it, where that
// differs: a check compares resolved paths too.
func withResolved(dirs []string) []string {
	var out []string
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		dir = filepath.Clean(dir)
		out = append(out, dir)
		if resolved, err := resolveFolder(dir); err == nil && resolved != dir {
			out = append(out, resolved)
		}
	}
	return out
}

// check says why path (absolute, clean) is refused (runnerwire.RepoRefused*),
// or "" when agents may work there.
func (g *repoGuard) check(path string) string {
	path = filepath.Clean(path)
	if g.isRoot != nil && g.isRoot(path) {
		return runnerwire.RepoRefusedRoot
	}
	if g.home != "" && g.within(g.home, path) {
		return runnerwire.RepoRefusedHome
	}
	for _, dir := range g.homeParents {
		if g.same(path, dir) {
			return runnerwire.RepoRefusedHome
		}
	}
	for _, dir := range g.homeLike {
		if g.within(dir, path) {
			return runnerwire.RepoRefusedHome
		}
	}
	for _, dir := range g.nodevas {
		if g.within(path, dir) {
			return runnerwire.RepoRefusedNodevas
		}
	}
	if g.home != "" && g.within(path, g.home) {
		rel, err := filepath.Rel(g.key(g.home), g.key(path))
		if first, _, _ := strings.Cut(rel, string(filepath.Separator)); err == nil && strings.HasPrefix(first, ".") && first != ".." {
			return runnerwire.RepoRefusedSecret
		}
	}
	for _, dir := range g.secret {
		if g.within(path, dir) || g.within(dir, path) {
			return runnerwire.RepoRefusedSecret
		}
	}
	// A folder that holds Nodevas's (a project folder that took a runner
	// data dir in): its worktrees and tokens would be in reach.
	for _, dir := range g.nodevas {
		if g.within(dir, path) {
			return runnerwire.RepoRefusedNodevas
		}
	}
	for _, dir := range g.system {
		if g.within(path, dir) {
			return runnerwire.RepoRefusedSystem
		}
	}
	for _, dir := range g.systemExact {
		if g.same(path, dir) {
			return runnerwire.RepoRefusedSystem
		}
	}
	if top := topFolder(path); top != "" {
		for _, pattern := range g.systemTop {
			if g.fold {
				top, pattern = strings.ToLower(top), strings.ToLower(pattern)
			}
			if ok, _ := filepath.Match(pattern, top); ok {
				return runnerwire.RepoRefusedSystem
			}
		}
	}
	return ""
}

// key is path as the file system compares it.
func (g *repoGuard) key(path string) string {
	path = filepath.Clean(path)
	if g.fold {
		return strings.ToLower(path)
	}
	return path
}

func (g *repoGuard) same(a, b string) bool { return b != "" && g.key(a) == g.key(b) }

// within reports whether child is parent or a folder under it.
func (g *repoGuard) within(child, parent string) bool {
	if strings.TrimSpace(parent) == "" {
		return false
	}
	rel, err := filepath.Rel(g.key(parent), g.key(child))
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// topFolder is the first folder of path below its drive or root, or "".
func topFolder(path string) string {
	rest := strings.TrimPrefix(path[len(filepath.VolumeName(path)):], string(filepath.Separator))
	first, _, _ := strings.Cut(rest, string(filepath.Separator))
	return first
}

// dedupe drops empty and repeated folders.
func dedupe(dirs []string) []string {
	var out []string
	for _, dir := range dirs {
		if dir = strings.TrimSpace(dir); dir != "" && !slices.Contains(out, filepath.Clean(dir)) {
			out = append(out, filepath.Clean(dir))
		}
	}
	return out
}
