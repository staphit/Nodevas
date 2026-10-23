package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"nodevas/internal/runnerwire"
)

// CommandRunner is how the runner looks at its machine. Tests replace it; the
// real one is SystemCommands.
type CommandRunner interface {
	// LookPath finds a program on PATH; an error means it is not installed.
	LookPath(name string) (string, error)
	// Run runs a program and returns its stdout. err is non-nil when it could
	// not start or exited non-zero.
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// SystemCommands runs real programs.
type SystemCommands struct{}

func (SystemCommands) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (SystemCommands) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
	}
	return string(out), err
}

// Kinds are the agent CLIs the runner looks for, in the order it reports them.
var Kinds = []string{"claude", "codex", "gemini"}

// versionTimeout bounds one `<cli> --version`. Some CLIs check for updates on
// start; a hung check must not hold up the hello.
const versionTimeout = 5 * time.Second

// DefaultModels is the built-in model list for an agent kind: "default" (no
// model flag: whatever the CLI itself picks) first, then a few current IDs.
// Claude Code's aliases are stable; the Codex and Gemini IDs date, which is
// why the owner can replace any list in the config, and why Detect asks the
// installed Codex for its own catalog first.
func DefaultModels(kind string) []runnerwire.Model {
	models := []runnerwire.Model{{ID: "default", Label: "CLI default", Default: true}}
	switch kind {
	case "claude":
		models = append(models,
			runnerwire.Model{ID: "opus", Label: "Opus"},
			runnerwire.Model{ID: "sonnet", Label: "Sonnet"},
			runnerwire.Model{ID: "haiku", Label: "Haiku"},
		)
	case "codex":
		models = append(models,
			runnerwire.Model{ID: "gpt-5.5", Label: "GPT-5.5"},
			runnerwire.Model{ID: "gpt-5.4-mini", Label: "GPT-5.4-Mini"},
		)
	case "gemini":
		models = append(models,
			runnerwire.Model{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
			runnerwire.Model{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash"},
		)
	}
	return models
}

// codexCatalog asks the installed Codex for the models it ships with:
// `codex debug models --bundled` prints its built-in catalog without going
// online. Only the ones Codex itself lists in its picker count. nil means
// the command failed or said nothing usable; the built-in list stands.
func codexCatalog(ctx context.Context, cmds CommandRunner, bin string) []runnerwire.Model {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := cmds.Run(ctx, bin, "debug", "models", "--bundled")
	if err != nil {
		return nil
	}
	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(out), "\ufeff")), &catalog) != nil {
		return nil
	}
	sort.SliceStable(catalog.Models, func(i, j int) bool { return catalog.Models[i].Priority < catalog.Models[j].Priority })
	models := []runnerwire.Model{{ID: "default", Label: "CLI default", Default: true}}
	for _, m := range catalog.Models {
		if m.Visibility != "list" || !validModelID(m.Slug) || len(models) >= runnerwire.MaxModels {
			continue
		}
		label := m.DisplayName
		if label == "" {
			label = m.Slug
		}
		models = append(models, runnerwire.Model{ID: m.Slug, Label: truncate(label, 100)})
	}
	if len(models) == 1 {
		return nil
	}
	return models
}

// validModelID keeps a catalog entry to what a model flag plausibly takes.
func validModelID(id string) bool {
	if id == "" || len(id) > 100 || id == "default" {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-:/", r)) {
			return false
		}
	}
	return true
}

// ModelsFor is the model list the runner reports for kind: the config's list
// when it has one, the built-in one otherwise.
func ModelsFor(kind string, cfg *Config) []runnerwire.Model {
	if models, ok := cfg.modelsFor(kind); ok {
		return models
	}
	return DefaultModels(kind)
}

// modelsFor is the config's own model list for kind, if it has one.
func (c *Config) modelsFor(kind string) ([]runnerwire.Model, bool) {
	if c == nil {
		return nil, false
	}
	models, ok := c.Models[kind]
	if !ok || len(models) == 0 {
		return nil, false
	}
	out := append([]runnerwire.Model(nil), models...)
	if len(out) > runnerwire.MaxModels {
		out = out[:runnerwire.MaxModels]
	}
	return out, true
}

// Detect reports each agent CLI: whether it is on PATH, its version, and the
// models runs may ask it for: the config's list, else (for Codex) the CLI's
// own catalog, else the built-in list.
func Detect(ctx context.Context, cmds CommandRunner, cfg *Config) []runnerwire.Agent {
	agents := make([]runnerwire.Agent, 0, len(Kinds))
	for _, kind := range Kinds {
		agent := runnerwire.Agent{Kind: kind, Models: ModelsFor(kind, cfg)}
		bin, err := cmds.LookPath(kind)
		if err != nil {
			agents = append(agents, agent)
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
		out, err := cmds.Run(runCtx, bin, "--version")
		cancel()
		if err != nil {
			agent.Error = truncate(fmt.Sprintf("%s --version: %v", kind, err), 300)
		} else {
			agent.Installed = true
			agent.Version = truncate(firstLine(out), 200)
			if _, configured := cfg.modelsFor(kind); !configured && kind == "codex" {
				if models := codexCatalog(ctx, cmds, bin); models != nil {
					agent.Models = models
				}
			}
		}
		agents = append(agents, agent)
	}
	return agents
}

// BuildInfo is the hello the runner sends: this machine, its agents and the
// repositories its owner allowed.
func BuildInfo(ctx context.Context, cmds CommandRunner, cfg *Config) runnerwire.Info {
	host, _ := os.Hostname()
	repos := []runnerwire.Repo{}
	maxConcurrent := DefaultMaxConcurrent
	autoAccept := &runnerwire.AutoAcceptPolicy{RequireStepUp: true}
	if cfg != nil {
		policy := cfg.AutoAcceptPolicy()
		autoAccept = &policy
		repos = append(repos, cfg.Repos...)
		if len(repos) > runnerwire.MaxRepos {
			repos = repos[:runnerwire.MaxRepos]
		}
		maxConcurrent = cfg.MaxConcurrentRuns()
	}
	return runnerwire.Info{
		Version:       Version(),
		Hostname:      host,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Agents:        Detect(ctx, cmds, cfg),
		Repos:         repos,
		Capabilities:  capabilities(cfg),
		MaxConcurrent: maxConcurrent,
		AutoAccept:    autoAccept,
	}
}

// Version identifies this build: the module version when it has one, else the
// VCS revision Go stamped into the binary, else "dev".
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var revision string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return "dev"
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if dirty {
		revision += "-dirty"
	}
	return revision
}

// InspectRepo checks that path is inside a git work tree and describes it.
// The stored path is the work tree's top level, so adding a subdirectory adds
// the repository it belongs to.
func InspectRepo(ctx context.Context, cmds CommandRunner, path string) (runnerwire.Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return runnerwire.Repo{}, err
	}
	git, err := cmds.LookPath("git")
	if err != nil {
		return runnerwire.Repo{}, fmt.Errorf("git is not on PATH: %w", err)
	}
	out, err := cmds.Run(ctx, git, "-C", abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return runnerwire.Repo{}, fmt.Errorf("%s is not a git work tree: %w", abs, err)
	}
	top := filepath.Clean(filepath.FromSlash(firstLine(out)))
	if top == "" || top == "." {
		return runnerwire.Repo{}, fmt.Errorf("%s is not a git work tree", abs)
	}
	repo := runnerwire.Repo{ID: RepoID(top), Name: filepath.Base(top), Path: top}
	if remote, err := cmds.Run(ctx, git, "-C", top, "remote", "get-url", "origin"); err == nil {
		repo.Remote = firstLine(remote)
	}
	return repo, nil
}

// RepoID is a short, stable ID for a repository path. Jobs name repos by it,
// so a hub never sends a path for the runner to open.
func RepoID(path string) string {
	key := filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		// Case-insensitive filesystems: one directory, one ID.
		key = strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// AddRepo adds repo to the config; adding the same repository twice is an error.
func (c *Config) AddRepo(repo runnerwire.Repo) error {
	for _, existing := range c.Repos {
		if existing.ID == repo.ID {
			return fmt.Errorf("%s is already added (id %s)", repo.Path, repo.ID)
		}
	}
	if len(c.Repos) >= runnerwire.MaxRepos {
		return fmt.Errorf("a runner can hold at most %d repositories", runnerwire.MaxRepos)
	}
	c.Repos = append(c.Repos, repo)
	return nil
}

// RemoveRepo removes the repository with that ID or, failing that, that name.
// A name shared by several repositories is refused rather than guessed.
func (c *Config) RemoveRepo(idOrName string) (runnerwire.Repo, error) {
	idOrName = strings.TrimSpace(idOrName)
	for i, repo := range c.Repos {
		if repo.ID == idOrName {
			c.Repos = append(c.Repos[:i], c.Repos[i+1:]...)
			c.forgetRepo(repo.ID)
			return repo, nil
		}
	}
	match := -1
	for i, repo := range c.Repos {
		if strings.EqualFold(repo.Name, idOrName) {
			if match >= 0 {
				return runnerwire.Repo{}, fmt.Errorf("several repositories are named %q; remove one by id", idOrName)
			}
			match = i
		}
	}
	if match < 0 {
		return runnerwire.Repo{}, fmt.Errorf("no repository %q (see nodevas runner repo list)", idOrName)
	}
	repo := c.Repos[match]
	c.Repos = append(c.Repos[:match], c.Repos[match+1:]...)
	c.forgetRepo(repo.ID)
	return repo, nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
