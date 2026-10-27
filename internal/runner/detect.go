package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"nodevas/internal/buildinfo"
	"nodevas/internal/runner/adapter"
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
// Each is looked up on PATH as adapter.Binary(kind).
var Kinds = []string{"claude", "codex", "gemini", "opencode", "antigravity"}

// versionTimeout bounds one `<cli> --version`. Some CLIs check for updates on
// start; a hung check must not hold up the hello.
const versionTimeout = 5 * time.Second

// catalogTimeout bounds asking a CLI for its models. OpenCode loads its
// plugins and agy asks its server, so they get longer than --version.
const catalogTimeout = 15 * time.Second

// DefaultModels is the built-in model list for an agent kind: "default" (no
// model flag: whatever the CLI itself picks) first, then a few current IDs.
// Claude Code's aliases are stable; the Codex and Gemini IDs date, which is
// why the owner can replace any list in the config, and why Detect asks the
// installed Codex, OpenCode and agy for their own catalogs first. OpenCode's
// and agy's models depend on the owner's providers and plan, so they have no
// built-in IDs.
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

// openCodeCatalog asks the installed OpenCode for the models its configured
// providers offer: `opencode models` prints one provider/model per line. nil
// means the command failed or listed nothing usable.
func openCodeCatalog(ctx context.Context, cmds CommandRunner, bin string) []runnerwire.Model {
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	out, err := cmds.Run(ctx, bin, "models")
	if err != nil {
		return nil
	}
	var ids, labels []string
	for _, line := range strings.Split(strings.TrimPrefix(out, "\ufeff"), "\n") {
		id := strings.TrimSpace(line)
		if strings.Contains(id, "/") {
			ids, labels = append(ids, id), append(labels, id)
		}
	}
	return catalogModels(ids, labels)
}

// antigravityCatalog asks the installed agy for the models the owner's plan
// has: `agy models` prints "<id>\t<label>" lines after a progress line.
func antigravityCatalog(ctx context.Context, cmds CommandRunner, bin string) []runnerwire.Model {
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	out, err := cmds.Run(ctx, bin, "models")
	if err != nil {
		return nil
	}
	var ids, labels []string
	for _, line := range strings.Split(strings.TrimPrefix(out, "\ufeff"), "\n") {
		id, label, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		id, label = strings.TrimSpace(id), strings.TrimSpace(label)
		if label == "" {
			label = id
		}
		ids, labels = append(ids, id), append(labels, label)
	}
	return catalogModels(ids, labels)
}

// catalogModels is a CLI's model list: "default" first, then each valid id
// with its label, up to runnerwire.MaxModels. nil when none is valid.
func catalogModels(ids, labels []string) []runnerwire.Model {
	models := []runnerwire.Model{{ID: "default", Label: "CLI default", Default: true}}
	for i, id := range ids {
		if !validModelID(id) || len(models) >= runnerwire.MaxModels {
			continue
		}
		models = append(models, runnerwire.Model{ID: id, Label: truncate(labels[i], 100)})
	}
	if len(models) == 1 {
		return nil
	}
	return models
}

// AntigravityMCPRule is the permission rule agy needs in the owner's
// settings for a headless run to call the nodevas tools; without it every
// call is denied without asking.
const AntigravityMCPRule = "mcp(nodevas/*)"

// antigravitySettingsPath is agy's own settings file, where that rule lives.
// Tests point it elsewhere.
var antigravitySettingsPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), nil
}

// antigravityRunsBlocked says why agy cannot take runs here, or "" when its
// settings allow the nodevas tools. The runner only reads the file; `nodevas
// setup-agent --agents antigravity` is what adds the rule.
func antigravityRunsBlocked() string {
	const fix = "antigravity: runs need \"" + AntigravityMCPRule + "\" in permissions.allow of " +
		"~/.gemini/antigravity-cli/settings.json; run `nodevas setup-agent --runs-only --agents antigravity`"
	path, err := antigravitySettingsPath()
	if err != nil {
		return fix
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fix
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &settings) != nil {
		return fix + " (the file does not parse)"
	}
	for _, rule := range settings.Permissions.Allow {
		switch strings.TrimSpace(rule) {
		case AntigravityMCPRule, "mcp(*)":
			return ""
		}
	}
	return fix
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
		bin, err := cmds.LookPath(adapter.Binary(kind))
		if err != nil {
			agents = append(agents, agent)
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
		out, err := cmds.Run(runCtx, bin, "--version")
		cancel()
		if err != nil {
			agent.Error = truncate(fmt.Sprintf("%s --version: %v", adapter.Binary(kind), err), 300)
			agents = append(agents, agent)
			continue
		}
		agent.Version = truncate(firstLine(out), 200)
		if kind == "opencode" {
			if reason := adapter.OpenCodeUnsupported(agent.Version); reason != "" {
				agent.Error = truncate(reason, 300)
				agents = append(agents, agent)
				continue
			}
		}
		if kind == "antigravity" {
			if reason := antigravityRunsBlocked(); reason != "" {
				// Every nodevas tool call would be denied: not usable for runs.
				agent.Error = truncate(reason, 300)
				agents = append(agents, agent)
				continue
			}
		}
		agent.Installed = true
		if _, configured := cfg.modelsFor(kind); !configured {
			var models []runnerwire.Model
			switch kind {
			case "codex":
				models = codexCatalog(ctx, cmds, bin)
			case "opencode":
				models = openCodeCatalog(ctx, cmds, bin)
			case "antigravity":
				models = antigravityCatalog(ctx, cmds, bin)
			}
			if models != nil {
				agent.Models = models
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
		// Whether a repo has a test command, never the command itself.
		for i := range repos {
			command, _ := cfg.RepoTestCommand(repos[i].ID)
			repos[i].Verify = len(command) > 0
		}
		maxConcurrent = cfg.MaxConcurrentRuns()
	}
	llm := copilotInfo(ctx, cfg)
	caps := capabilities(cfg)
	if llm != nil && llm.Auto {
		caps = append(caps, runnerwire.CapLLM)
	}
	return runnerwire.Info{
		Version:       Version(),
		Hostname:      host,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Agents:        Detect(ctx, cmds, cfg),
		Repos:         repos,
		Capabilities:  caps,
		MaxConcurrent: maxConcurrent,
		AutoAccept:    autoAccept,
		LLM:           llm,
		Advisor:       advisorInfo(cfg),
	}
}

// advisorInfo is what the hello says about the autopilot advisor's model
// calls (never the key), or nil when this runner does not make them.
func advisorInfo(cfg *Config) *runnerwire.LLMInfo {
	info, _, reason := cfg.AdvisorLLM()
	if reason != "" {
		return nil
	}
	return &info
}

// copilotInfo is what the hello says about the copilot's model calls (never
// the key), or nil when this runner does not make them: the owner's
// settings, else a local model found on this machine (llm_auto.go).
func copilotInfo(ctx context.Context, cfg *Config) *runnerwire.LLMInfo {
	info, _, reason := cfg.CopilotLLM()
	if reason == "" {
		return &info
	}
	if auto, ok := autoCopilotLLM(ctx, cfg); ok {
		return &auto
	}
	return nil
}

// Version identifies this build: the module version when it has one, else the
// commit it was built from, else "dev" (internal/buildinfo).
func Version() string { return buildinfo.Version() }

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
