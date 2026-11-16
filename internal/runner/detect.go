package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
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

// outputGrace is how long Run waits for a program's output once the program
// itself is gone. A CLI's children (an update check, a background server)
// can hold its output open long after it exited or was killed.
const outputGrace = time.Second

// Run ends when ctx does, whatever the program started: its whole process
// tree is killed, not only the program, and what still holds its output
// open after that is not waited for.
func (SystemCommands) Run(ctx context.Context, name string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.Command(name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = outputGrace
	prepareTree(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	tree := attachTree(cmd)
	defer tree.close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
		if errors.Is(err, exec.ErrWaitDelay) {
			// It exited cleanly and said what it had to say; only something
			// it left running still held its output open.
			err = nil
		}
	case <-ctx.Done():
		tree.kill()
		// The tree kill can fail as a whole on Windows (taskkill missing or
		// refused, and no job object to fall back on), and then nothing would
		// end the program and this would wait on it forever. Killing the
		// program itself is the last resort; on a process already gone it
		// only returns an error, which is of no interest here.
		_ = cmd.Process.Kill()
		<-done
		err = ctx.Err()
	}
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
	}
	return stdout.String(), err
}

// Kinds are the agent CLIs the runner looks for, in the order it reports them.
// Each is looked up on PATH as adapter.Binary(kind).
var Kinds = []string{"claude", "codex", "gemini", "opencode", "antigravity"}

// versionTimeout bounds one `<cli> --version`. Some CLIs check for updates on
// start; a hung check must not hide the agent for longer than this.
const versionTimeout = 5 * time.Second

// catalogTimeout bounds asking a CLI for its models. OpenCode loads its
// plugins and agy asks its server, so they get longer than --version.
const catalogTimeout = 15 * time.Second

// slowLook is how long a look at the agent CLIs, or one command of it, may
// take before the runner's log says so.
const slowLook = 2 * time.Second

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

// catalogOf is how kind's CLI is asked for its own models and how its answer
// reads; ok is false for a kind that has no such command. quick marks a
// command that answers without going online, bounded as --version is.
func catalogOf(kind string) (args []string, read func(out string) []runnerwire.Model, quick, ok bool) {
	switch kind {
	case "codex":
		return []string{"debug", "models", "--bundled"}, codexCatalog, true, true
	case "opencode":
		return []string{"models"}, openCodeCatalog, false, true
	case "antigravity":
		return []string{"models"}, antigravityCatalog, false, true
	}
	return nil, nil, false, false
}

// codexCatalog reads the models the installed Codex ships with:
// `codex debug models --bundled` prints its built-in catalog without going
// online. Only the ones Codex itself lists in its picker count. nil means
// it said nothing usable; the built-in list stands.
func codexCatalog(out string) []runnerwire.Model {
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
		models = append(models, runnerwire.Model{ID: m.Slug, Label: helloText(label, 100)})
	}
	if len(models) == 1 {
		return nil
	}
	return models
}

// openCodeCatalog reads the models the installed OpenCode's configured
// providers offer: `opencode models` prints one provider/model per line. nil
// means it listed nothing usable.
func openCodeCatalog(out string) []runnerwire.Model {
	var ids, labels []string
	for _, line := range strings.Split(strings.TrimPrefix(out, "\ufeff"), "\n") {
		id := strings.TrimSpace(line)
		if strings.Contains(id, "/") {
			ids, labels = append(ids, id), append(labels, id)
		}
	}
	return catalogModels(ids, labels)
}

// antigravityCatalog reads the models the owner's plan has: `agy models`
// prints "<id>\t<label>" lines after a progress line.
func antigravityCatalog(out string) []runnerwire.Model {
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
		models = append(models, runnerwire.Model{ID: id, Label: helloText(labels[i], 100)})
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
//
// It is one look, start to finish, for a caller that can wait for it. A
// running runner never waits on its connection: it keeps what the last look
// found and looks again in the background (detector.go).
func Detect(ctx context.Context, cmds CommandRunner, cfg *Config) []runnerwire.Agent {
	agents, _ := newProber(cmds).look(ctx, cfg)
	return agents
}

// prober looks at the agent CLIs on this machine. Detect makes one for a
// single look; a running runner keeps one, so that a CLI failing to list its
// models once does not lose the list it gave before.
type prober struct {
	cmds CommandRunner
	// versionTimeout and catalogTimeout bound each command of a look.
	versionTimeout, catalogTimeout time.Duration
	// lastGood is, per agent kind, the model list its CLI gave the last time
	// it answered.
	lastGood map[string][]runnerwire.Model
}

func newProber(cmds CommandRunner) *prober {
	return &prober{
		cmds:           cmds,
		versionTimeout: versionTimeout,
		catalogTimeout: catalogTimeout,
		lastGood:       map[string][]runnerwire.Model{},
	}
}

// look is one full look: which CLIs are here (versions), what models they
// list (catalogs), then the lists cut to what one hello may carry
// (trimModels). The report is what the runner's log says about it.
func (p *prober) look(ctx context.Context, cfg *Config) ([]runnerwire.Agent, report) {
	var said report
	seen := p.versions(ctx, cfg)
	for _, s := range seen {
		said.add(s.version, "")
	}
	p.catalogs(ctx, cfg, seen, &said)
	agents := make([]runnerwire.Agent, len(seen))
	for i, s := range seen {
		agents[i] = s.agent
	}
	said.notes = append(said.notes, trimModels(agents)...)
	return agents, said
}

// sighting is what the first phase of a look says about one agent kind.
type sighting struct {
	agent runnerwire.Agent
	// bin is the CLI's path when runs can use it, else "".
	bin string
	// version is how its --version went; the zero answer when the CLI is
	// not on PATH.
	version answer
}

// versions is the first phase of a look: which agent CLIs are here and
// usable for runs. Every kind is asked at once, so a CLI that hangs on
// --version costs its own timeout and hides no other agent.
func (p *prober) versions(ctx context.Context, cfg *Config) []sighting {
	seen := make([]sighting, len(Kinds))
	var wg sync.WaitGroup
	for i, kind := range Kinds {
		wg.Go(func() { seen[i] = p.version(ctx, cfg, kind) })
	}
	wg.Wait()
	return seen
}

func (p *prober) version(ctx context.Context, cfg *Config, kind string) sighting {
	s := sighting{agent: runnerwire.Agent{Kind: kind, Models: ModelsFor(kind, cfg)}}
	bin, err := p.cmds.LookPath(adapter.Binary(kind))
	if err != nil {
		return s
	}
	s.version = p.ask(ctx, p.versionTimeout, kind, bin, "--version")
	if s.version.err != nil {
		s.agent.Error = helloText(s.version.row(), maxHelloField)
		return s
	}
	s.agent.Version = helloText(firstLine(s.version.out), 200)
	if kind == "opencode" {
		if reason := adapter.OpenCodeUnsupported(s.agent.Version); reason != "" {
			s.agent.Error = helloText(reason, maxHelloField)
			return s
		}
	}
	if kind == "antigravity" {
		if reason := antigravityRunsBlocked(); reason != "" {
			// Every nodevas tool call would be denied: not usable for runs.
			s.agent.Error = helloText(reason, maxHelloField)
			return s
		}
	}
	s.agent.Installed = true
	s.bin = bin
	return s
}

// catalogs is the second phase of a look: each usable CLI that can list its
// own models is asked for them, all at once, unless the config has a list
// for that kind. A CLI that fails or gives no answer keeps the list it gave
// last time, or the built-in one when it never gave any: one bad moment
// must not take models away from the runs that name them.
func (p *prober) catalogs(ctx context.Context, cfg *Config, seen []sighting, said *report) {
	asked := make([]bool, len(seen))
	answers := make([]answer, len(seen))
	var wg sync.WaitGroup
	for i := range seen {
		kind := seen[i].agent.Kind
		args, _, quick, ok := catalogOf(kind)
		if _, configured := cfg.modelsFor(kind); !ok || configured || seen[i].bin == "" {
			// Not asked: a list from before says nothing about it now.
			delete(p.lastGood, kind)
			continue
		}
		timeout := p.catalogTimeout
		if quick {
			timeout = p.versionTimeout
		}
		asked[i] = true
		wg.Go(func() { answers[i] = p.ask(ctx, timeout, kind, seen[i].bin, args...) })
	}
	wg.Wait()
	for i, a := range answers {
		if !asked[i] {
			continue
		}
		kind := seen[i].agent.Kind
		if a.err != nil {
			using := ", using the built-in model list"
			if last, ok := p.lastGood[kind]; ok {
				seen[i].agent.Models = last
				using = ", using the list from the last check"
			}
			said.add(a, using)
			continue
		}
		said.add(a, "")
		_, read, _, _ := catalogOf(kind)
		if models := read(a.out); models != nil {
			seen[i].agent.Models = models
			p.lastGood[kind] = models
		} else {
			// It answered, with nothing usable: the built-in list stands.
			delete(p.lastGood, kind)
		}
	}
}

// answer is how one command of a look went.
type answer struct {
	// command is what ran, as the owner would type it: "opencode models".
	command string
	out     string
	took    time.Duration
	// err is nil when the command answered. timeout is set when it was cut
	// for giving no answer in that long, rather than failing by itself.
	err     error
	timeout time.Duration
}

// ask runs one command of a look, cut at timeout.
func (p *prober) ask(ctx context.Context, timeout time.Duration, kind, bin string, args ...string) answer {
	a := answer{command: adapter.Binary(kind) + " " + strings.Join(args, " ")}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	a.out, a.err = p.cmds.Run(runCtx, bin, args...)
	a.took = time.Since(start)
	if a.err != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		a.timeout = timeout
	}
	return a
}

// row is what the agent's row in the app says about a command that gave
// nothing. It is the same on every look that fails the same way, so such a
// look changes nothing in the hello.
func (a answer) row() string {
	if a.timeout > 0 {
		return fmt.Sprintf("%s: no answer after %s", a.command, a.timeout)
	}
	return fmt.Sprintf("%s: %v", a.command, a.err)
}

// report is what the runner's log says about one look.
type report struct {
	// problems names each command that failed or gave no answer.
	problems []string
	// notes is the rest: a command that answered, but slowly, and a model
	// list cut short.
	notes []string
}

// add says how a command went when that is worth a word: it failed or gave
// no answer (using is then what stands in for its answer), or it was slow.
func (r *report) add(a answer, using string) {
	switch {
	case a.err == nil:
		if a.took > slowLook {
			r.notes = append(r.notes, fmt.Sprintf("%s took %s", a.command, roughly(a.took)))
		}
	case a.timeout > 0:
		r.problems = append(r.problems, a.row()+using)
	default:
		r.problems = append(r.problems, fmt.Sprintf("%s: failed after %s (%s)%s",
			a.command, roughly(a.took), truncate(a.err.Error(), 200), using))
	}
}

// roughly is d as a log line says it: to a tenth of a second, or to a
// hundredth under one second.
func roughly(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(10 * time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}

// trimModels cuts the agents' model lists to what one hello may carry: the
// hub refuses a hello listing more than runnerwire.MaxModels over all its
// agents, and closes the connection with it. The longest list gives up its
// last model until they fit; an agent's default is never dropped. It
// returns a note for each list it cut.
func trimModels(agents []runnerwire.Agent) (notes []string) {
	isDefault := func(m runnerwire.Model) bool { return m.Default || m.ID == "default" }
	keep := make([]int, len(agents))
	defaults := make([]int, len(agents))
	total := 0
	for i, agent := range agents {
		keep[i] = len(agent.Models)
		total += keep[i]
		for _, m := range agent.Models {
			if isDefault(m) {
				defaults[i]++
			}
		}
	}
	for total > runnerwire.MaxModels {
		longest := -1
		for i := range agents {
			if keep[i] > defaults[i] && (longest < 0 || keep[i] > keep[longest]) {
				longest = i
			}
		}
		if longest < 0 {
			break
		}
		keep[longest]--
		total--
	}
	for i := range agents {
		all := agents[i].Models
		if keep[i] == len(all) {
			continue
		}
		// A new list: this one may be the config's or the last look's.
		kept := make([]runnerwire.Model, 0, keep[i])
		room := keep[i] - defaults[i]
		for _, m := range all {
			switch {
			case isDefault(m):
				kept = append(kept, m)
			case room > 0:
				kept = append(kept, m)
				room--
			}
		}
		agents[i].Models = kept
		notes = append(notes, fmt.Sprintf("%s: offering %d of its %d models, all agents together may list %d",
			agents[i].Kind, len(kept), len(all), runnerwire.MaxModels))
	}
	return notes
}

// BuildInfo is the hello the runner sends: this machine, its agents and the
// repositories its owner allowed. Like Detect, it is for a caller that can
// wait for a look at the agents.
func BuildInfo(ctx context.Context, cmds CommandRunner, cfg *Config) runnerwire.Info {
	return helloInfo(cfg, Detect(ctx, cmds, cfg), localLLM(ctx, cfg))
}

// helloInfo is the hello for cfg with what a look at this machine found:
// its agents and local, the copilot model found here (localLLM). It runs
// nothing itself.
func helloInfo(cfg *Config, agents []runnerwire.Agent, local *runnerwire.LLMInfo) runnerwire.Info {
	host, _ := os.Hostname()
	repos := []runnerwire.Repo{}
	maxConcurrent := DefaultMaxConcurrent
	autoAccept := &runnerwire.AutoAcceptPolicy{RequireStepUp: true}
	if cfg != nil {
		policy := cfg.AutoAcceptPolicy()
		autoAccept = &policy
		repos = helloRepos(cfg)
		maxConcurrent = cfg.MaxConcurrentRuns()
	}
	llm := copilotInfo(cfg, local)
	caps := capabilities(cfg)
	if llm != nil && llm.Auto {
		caps = append(caps, runnerwire.CapLLM)
	}
	info := runnerwire.Info{
		Version:       Version(),
		Hostname:      host,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Agents:        agents,
		Repos:         repos,
		Capabilities:  caps,
		MaxConcurrent: maxConcurrent,
		AutoAccept:    autoAccept,
		LLM:           llm,
		Advisor:       advisorInfo(cfg),
		// A nil cfg reports false.
		PermissionRelay: cfg != nil && cfg.PermissionRelay,
	}
	// How high the hub may set maxConcurrent (settings.go), only while the
	// owner lets it.
	if cfg.RemoteSettingsOn() {
		info.MaxConcurrentCeiling = cfg.MaxConcurrentCeilingOrDefault()
	}
	return info
}

// helloRepos is the hello's repos: whether a repo has a test command, never
// the command itself.
func helloRepos(cfg *Config) []runnerwire.Repo {
	repos := append([]runnerwire.Repo{}, cfg.Repos...)
	if len(repos) > runnerwire.MaxRepos {
		repos = repos[:runnerwire.MaxRepos]
	}
	for i := range repos {
		command, _ := cfg.RepoTestCommand(repos[i].ID)
		repos[i].Verify = len(command) > 0
	}
	return repos
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
// settings, else local, the model found on this machine (localLLM).
func copilotInfo(cfg *Config, local *runnerwire.LLMInfo) *runnerwire.LLMInfo {
	info, _, reason := cfg.CopilotLLM()
	if reason == "" {
		return &info
	}
	return local
}

// localLLM is the local model this runner offers for the copilot
// (llm_auto.go), or nil: none was found, or the owner set a provider or
// turned the copilot off.
func localLLM(ctx context.Context, cfg *Config) *runnerwire.LLMInfo {
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

// maxHelloField is the most bytes the hub takes in one short field of a
// hello (internal/runners maxShortField). Over it, or with text that is not
// valid UTF-8, the hub refuses the whole hello and closes the connection, so
// the runner would never come online.
const maxHelloField = 256

// helloText is s cut to n bytes, at most maxHelloField, for a field of the
// hello: on a character boundary, with bytes that are not UTF-8 (a console
// in another code page) dropped.
func helloText(s string, n int) string {
	return truncateUTF8(strings.ToValidUTF8(s, ""), min(n, maxHelloField))
}
