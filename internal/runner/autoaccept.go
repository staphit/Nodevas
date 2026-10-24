package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"nodevas/internal/runnerwire"
)

// Auto-accept (W6). An offer is taken without anyone at the terminal only
// when the owner's allowlist in runner.json covers it: auto-accept is on, the
// dispatcher stepped up on the hub (unless the owner waived that), and the
// repository, agent and mode are all listed. Anything else is asked about at
// the terminal, or declined with what was missing when nobody is there.

// Warnings the runner prints about auto-accept.
const (
	AutoAcceptWarning = "WARNING: auto-accept is on. Runs dispatched from the hub start on this machine without " +
		"anyone here saying yes, within the repositories, agents and modes on the auto-accept list."
	StepUpOptionalWarning = "WARNING: auto-accept does not require a step-up. Anyone holding a signed-in hub " +
		"session as you (a stolen browser cookie is enough) can run agents on this machine. " +
		"Undo with: nodevas runner autoaccept step-up required"
	LegacyAutoAcceptWarning = "WARNING: runner.json had the old \"autoAccept\": true, which accepted everything. " +
		"It now auto-accepts only the repositories added here and the agents installed here, headless only, " +
		"and still WITHOUT a step-up. Require one with: nodevas runner autoaccept step-up required"
	AllowShellWarning = "WARNING: auto-accept now also takes runs whose agent may run any shell command here " +
		"(Codex danger-full-access, OpenCode/agy full, agy auto, Gemini yolo, Claude bypassPermissions/auto " +
		"or Bash in allowedTools). A task or a file in the repository can talk such an agent into running " +
		"anything as you. A step-up only proves the dispatcher re-entered a passcode; it does not protect " +
		"you from a compromised hub. Undo with: nodevas runner autoaccept allow-shell off"
	RemoteTypingWarning = "WARNING: the dispatcher may now type into auto-accepted interactive runs' terminals, " +
		"answering the agent's permission prompts as you. Undo with: nodevas runner autoaccept remote-typing off"
)

// Modes as the CLI names them; the wire spells headless "".
const (
	ModeNameHeadless    = "headless"
	ModeNameInteractive = "interactive"
)

// UnmarshalJSON reads runner.json, migrating W1's boolean autoAccept: false
// is off; true becomes a policy that keeps working as before for this
// machine's repositories, minus the agents (MigrateLegacyAutoAccept fills
// those in from what is installed, and says so once).
func (c *Config) UnmarshalJSON(raw []byte) error {
	type plain Config
	aux := struct {
		*plain
		AutoAccept json.RawMessage `json:"autoAccept,omitempty"`
	}{plain: (*plain)(c)}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	c.AutoAccept, c.legacyAutoAccept = nil, false
	value := strings.TrimSpace(string(aux.AutoAccept))
	switch value {
	case "", "null", "false":
		return nil
	case "true":
		policy := runnerwire.AutoAcceptPolicy{Enabled: true, Modes: []string{runnerwire.ModeHeadless}}
		for _, repo := range c.Repos {
			policy.Repos = append(policy.Repos, repo.ID)
		}
		c.AutoAccept, c.legacyAutoAccept = &policy, true
		return nil
	}
	var policy runnerwire.AutoAcceptPolicy
	if err := json.Unmarshal(aux.AutoAccept, &policy); err != nil {
		return fmt.Errorf("autoAccept: %w", err)
	}
	c.AutoAccept = &policy
	return nil
}

// LegacyAutoAccept reports a W1 `"autoAccept": true` still to be migrated.
func (c *Config) LegacyAutoAccept() bool { return c.legacyAutoAccept }

// MigrateLegacyAutoAccept finishes migrating W1's `"autoAccept": true` with
// the agent kinds installed here, and returns the warning to print once; ""
// when there was nothing to migrate. The new shape is written on the next save.
func (c *Config) MigrateLegacyAutoAccept(installed []string) string {
	if !c.legacyAutoAccept || c.AutoAccept == nil {
		return ""
	}
	c.legacyAutoAccept = false
	c.AutoAccept.Agents = append([]string(nil), installed...)
	return LegacyAutoAcceptWarning
}

// InstalledKinds is the agent kinds found working on this machine.
func InstalledKinds(ctx context.Context, cmds CommandRunner, cfg *Config) []string {
	var kinds []string
	for _, agent := range Detect(ctx, cmds, cfg) {
		if agent.Installed {
			kinds = append(kinds, agent.Kind)
		}
	}
	return kinds
}

// AutoAcceptPolicy is the policy in effect: a copy, with the defaults (off,
// step-up required) when none is set.
func (c *Config) AutoAcceptPolicy() runnerwire.AutoAcceptPolicy {
	if c.AutoAccept == nil {
		return runnerwire.AutoAcceptPolicy{RequireStepUp: true}
	}
	p := *c.AutoAccept
	p.Repos = slices.Clone(p.Repos)
	p.Agents = slices.Clone(p.Agents)
	p.Modes = slices.Clone(p.Modes)
	return p
}

// AutoAcceptOn reports whether auto-accept is switched on at all.
func (c *Config) AutoAcceptOn() bool { return c.AutoAccept != nil && c.AutoAccept.Enabled }

// policy is the stored policy, created with its defaults for a change.
func (c *Config) policy() *runnerwire.AutoAcceptPolicy {
	if c.AutoAccept == nil {
		c.AutoAccept = &runnerwire.AutoAcceptPolicy{RequireStepUp: true}
	}
	c.legacyAutoAccept = false
	return c.AutoAccept
}

// DefaultAutoAccept is what switching auto-accept on from nothing allows:
// every repository added here, the agents installed here, headless runs only,
// and only from a dispatcher who stepped up.
func (c *Config) DefaultAutoAccept(installed []string) runnerwire.AutoAcceptPolicy {
	p := runnerwire.AutoAcceptPolicy{Enabled: true, RequireStepUp: true,
		Agents: slices.Clone(installed), Modes: []string{runnerwire.ModeHeadless}}
	for _, repo := range c.Repos {
		p.Repos = append(p.Repos, repo.ID)
	}
	return p
}

// SetAutoAccept is `runner set autoAccept true|false`: on resets the policy
// to DefaultAutoAccept, off switches it off and keeps its lists.
func (c *Config) SetAutoAccept(on bool, installed []string) {
	if on {
		p := c.DefaultAutoAccept(installed)
		c.AutoAccept, c.legacyAutoAccept = &p, false
		return
	}
	c.policy().Enabled = false
}

// EnableAutoAccept is `runner autoaccept on`: a policy with lists keeps
// them; one never filled in starts from DefaultAutoAccept.
func (c *Config) EnableAutoAccept(installed []string) {
	if c.AutoAccept == nil || (len(c.AutoAccept.Repos) == 0 && len(c.AutoAccept.Agents) == 0 && len(c.AutoAccept.Modes) == 0) {
		requireStepUp := c.AutoAccept == nil || c.AutoAccept.RequireStepUp
		p := c.DefaultAutoAccept(installed)
		p.RequireStepUp = requireStepUp
		c.AutoAccept, c.legacyAutoAccept = &p, false
		return
	}
	c.policy().Enabled = true
}

// SetStepUpRequired is `runner autoaccept step-up required|optional`.
func (c *Config) SetStepUpRequired(required bool) { c.policy().RequireStepUp = required }

// SetAllowShell is `runner autoaccept allow-shell on|off`.
func (c *Config) SetAllowShell(on bool) { c.policy().AllowShell = on }

// SetAllowRemoteTyping is `runner autoaccept remote-typing on|off`.
func (c *Config) SetAllowRemoteTyping(on bool) { c.policy().AllowRemoteTyping = on }

// bashTool matches Bash, or Bash(<pattern>), as one entry of a Claude Code
// --allowedTools list.
var bashTool = regexp.MustCompile(`(^|[\s,])Bash($|[\s,(])`)

// ShellAccess says why job's agent, under this runner's permission settings,
// may run any shell command it likes: "" when it cannot. Auto-accept leaves
// such runs to a person unless the owner said otherwise (AllowShell).
func (c *Config) ShellAccess(job runnerwire.Job) string {
	mode := c.PermissionModeFor(job.Agent)
	readOnly := job.Mode != runnerwire.ModeInteractive && (job.Kind == runnerwire.JobReview || job.Kind == runnerwire.JobPlan)
	if readOnly {
		mode, _ = reviewPermission(c, job.Agent)
	}
	switch job.Agent {
	case "codex":
		if mode == "danger-full-access" {
			return "codexSandbox danger-full-access"
		}
	case "gemini":
		if mode == "yolo" {
			return "geminiApprovalMode yolo"
		}
	case "opencode":
		if mode == "full" {
			return "opencodePermission full"
		}
	case "antigravity":
		if mode == "full" || mode == "auto" {
			return "antigravityMode " + mode
		}
	case "claude":
		if readOnly {
			// Reviews and plans run with Bash disallowed (claudeReadOnlyTools),
			// which wins over any allow.
			return ""
		}
		if mode == "bypassPermissions" || mode == "auto" {
			return "permissionMode " + mode
		}
		for _, tool := range c.AllowedTools {
			if bashTool.MatchString(strings.TrimSpace(tool)) {
				return "allowedTools " + tool
			}
		}
	}
	return ""
}

// ResolveRepo finds one of this runner's repositories by ID, name (when only
// one has it) or path.
func (c *Config) ResolveRepo(arg string) (runnerwire.Repo, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return runnerwire.Repo{}, fmt.Errorf("no repository given")
	}
	for _, repo := range c.Repos {
		if repo.ID == arg {
			return repo, nil
		}
	}
	match := -1
	for i, repo := range c.Repos {
		if strings.EqualFold(repo.Name, arg) {
			if match >= 0 {
				return runnerwire.Repo{}, fmt.Errorf("several repositories are named %q; use its id", arg)
			}
			match = i
		}
	}
	if match >= 0 {
		return c.Repos[match], nil
	}
	if abs, err := filepath.Abs(arg); err == nil {
		id := RepoID(abs)
		for _, repo := range c.Repos {
			if repo.ID == id {
				return repo, nil
			}
		}
	}
	return runnerwire.Repo{}, fmt.Errorf("%q is not one of this runner's repositories (see nodevas runner repo list)", arg)
}

// AddAutoAcceptRepo lists a repository of this runner for auto-accept.
func (c *Config) AddAutoAcceptRepo(arg string) (runnerwire.Repo, error) {
	repo, err := c.ResolveRepo(arg)
	if err != nil {
		return repo, err
	}
	p := c.policy()
	if !slices.Contains(p.Repos, repo.ID) {
		if len(p.Repos) >= runnerwire.MaxRepos {
			return repo, fmt.Errorf("the auto-accept list holds at most %d repositories", runnerwire.MaxRepos)
		}
		p.Repos = append(p.Repos, repo.ID)
	}
	return repo, nil
}

// RemoveAutoAcceptRepo takes a repository off the auto-accept list. An ID
// left behind by a repository already removed from the runner is matched
// as it is.
func (c *Config) RemoveAutoAcceptRepo(arg string) (string, error) {
	id := strings.TrimSpace(arg)
	if repo, err := c.ResolveRepo(arg); err == nil {
		id = repo.ID
	}
	p := c.policy()
	i := slices.Index(p.Repos, id)
	if i < 0 {
		return "", fmt.Errorf("%q is not on the auto-accept list", arg)
	}
	p.Repos = slices.Delete(p.Repos, i, i+1)
	return id, nil
}

// forgetRepo drops a repository removed from the runner from the
// auto-accept list too, so re-adding it later does not silently re-list it.
func (c *Config) forgetRepo(id string) {
	if c.AutoAccept == nil {
		return
	}
	c.AutoAccept.Repos = slices.DeleteFunc(c.AutoAccept.Repos, func(r string) bool { return r == id })
}

// AddAutoAcceptAgent lists an agent kind for auto-accept.
func (c *Config) AddAutoAcceptAgent(kind string) (string, error) {
	kind, err := oneOf("agent", strings.TrimSpace(kind), Kinds)
	if err != nil {
		return "", err
	}
	p := c.policy()
	if !slices.Contains(p.Agents, kind) {
		p.Agents = append(p.Agents, kind)
	}
	return kind, nil
}

// RemoveAutoAcceptAgent takes an agent kind off the auto-accept list.
func (c *Config) RemoveAutoAcceptAgent(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	p := c.policy()
	i := slices.Index(p.Agents, kind)
	if i < 0 {
		return "", fmt.Errorf("%q is not on the auto-accept list", kind)
	}
	p.Agents = slices.Delete(p.Agents, i, i+1)
	return kind, nil
}

// parseModeName maps headless|interactive to the wire's mode.
func parseModeName(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ModeNameHeadless:
		return runnerwire.ModeHeadless, nil
	case ModeNameInteractive:
		return runnerwire.ModeInteractive, nil
	}
	return "", fmt.Errorf("mode must be %s or %s", ModeNameHeadless, ModeNameInteractive)
}

// ModeName is a wire mode as the CLI names it.
func ModeName(mode string) string {
	if mode == runnerwire.ModeHeadless {
		return ModeNameHeadless
	}
	return mode
}

// AddAutoAcceptMode lists a mode (headless|interactive) for auto-accept.
func (c *Config) AddAutoAcceptMode(name string) (string, error) {
	mode, err := parseModeName(name)
	if err != nil {
		return "", err
	}
	p := c.policy()
	if len(p.Modes) == 0 {
		// Empty meant headless only; keep that meaning explicit.
		p.Modes = []string{runnerwire.ModeHeadless}
	}
	if !slices.Contains(p.Modes, mode) {
		p.Modes = append(p.Modes, mode)
	}
	return ModeName(mode), nil
}

// RemoveAutoAcceptMode takes a mode off the auto-accept list. The last one
// stays: an empty list means headless only, so "none" is `autoaccept off`.
func (c *Config) RemoveAutoAcceptMode(name string) (string, error) {
	mode, err := parseModeName(name)
	if err != nil {
		return "", err
	}
	p := c.policy()
	modes := allowedModes(*p)
	i := slices.Index(modes, mode)
	if i < 0 {
		return "", fmt.Errorf("%s is not on the auto-accept list", ModeName(mode))
	}
	if len(modes) == 1 {
		return "", fmt.Errorf("%s is the only mode left; to auto-accept nothing, use nodevas runner autoaccept off", ModeName(mode))
	}
	p.Modes = slices.Delete(slices.Clone(modes), i, i+1)
	return ModeName(mode), nil
}

// allowedModes is the policy's modes with the empty-means-headless rule applied.
func allowedModes(p runnerwire.AutoAcceptPolicy) []string {
	if len(p.Modes) == 0 {
		return []string{runnerwire.ModeHeadless}
	}
	return p.Modes
}

// AutoAcceptModeNames is the policy's modes as the CLI names them.
func AutoAcceptModeNames(p runnerwire.AutoAcceptPolicy) []string {
	var names []string
	for _, m := range allowedModes(p) {
		names = append(names, ModeName(m))
	}
	return names
}

// Reasons auto-accept gives for not applying.
const (
	ReasonAutoAcceptOff = "auto-accept is off on this runner"
	ReasonNeedsStepUp   = "auto-accept needs a recent step-up on the hub"
)

// AutoAcceptDecision decides whether policy covers offer: on, stepped up (or
// step-up waived), and the repository, agent and mode all listed. A no comes
// with every condition that failed, each naming what was missing.
func AutoAcceptDecision(p runnerwire.AutoAcceptPolicy, offer Offer) (bool, string) {
	if !p.Enabled {
		return false, ReasonAutoAcceptOff
	}
	job := offer.Job
	var missing []string
	if p.RequireStepUp && !job.SteppedUp {
		missing = append(missing, ReasonNeedsStepUp)
	}
	if !slices.Contains(p.Repos, job.RepoID) {
		name := offer.Repo.Name
		if name == "" {
			name = job.RepoID
		}
		missing = append(missing, fmt.Sprintf("repo %s is not on this runner's auto-accept list", name))
	}
	if !slices.Contains(p.Agents, job.Agent) {
		missing = append(missing, fmt.Sprintf("agent %s is not on this runner's auto-accept list", job.Agent))
	}
	if !slices.Contains(allowedModes(p), job.Mode) {
		missing = append(missing, fmt.Sprintf("%s runs are not on this runner's auto-accept list", ModeName(job.Mode)))
	}
	if offer.Shell != "" && !p.AllowShell {
		missing = append(missing, fmt.Sprintf("agent %s may run any shell command here (%s); "+
			"auto-accept leaves that to a person (nodevas runner autoaccept allow-shell on)", job.Agent, offer.Shell))
	}
	if len(missing) > 0 {
		return false, strings.Join(missing, "; ")
	}
	why := "dispatcher stepped up"
	if !job.SteppedUp {
		why = "step-up not required"
	}
	return true, fmt.Sprintf("%s; repo %s, agent %s, %s are on the auto-accept list",
		why, offer.Repo.Name, job.Agent, ModeName(job.Mode))
}
