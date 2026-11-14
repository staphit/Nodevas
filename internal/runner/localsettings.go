package runner

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"nodevas/internal/runnerwire"
)

// The built-in runner's settings in the app ("This computer", 這台電腦;
// runnerwire/settings.go): what each agent may do, whether every run is
// asked about, which full-access agents' automatic runs start unasked, and
// the pull request options. Only the built-in runner takes them from the
// hub; any other runner refuses them (runnerwire.SettingsErrLocalOnly), since
// a team member changes their PC's settings on that PC.
//
// A change applies without a restart: the config is cloned, changed, saved
// (read-modify-write of the file, as maxConcurrent is) and swapped in whole
// (Client.cfg). Runs offered from then on take the new settings; a run
// already going keeps the config it was offered under (run.cfg).

// permissionModes is, per agent kind, the CLI's own mode for each level:
// read only, edit in its own copy, run any command.
var permissionModes = map[string][3]string{
	"claude":      {"plan", "acceptEdits", "bypassPermissions"},
	"codex":       {"read-only", "workspace-write", "danger-full-access"},
	"gemini":      {"default", "auto_edit", "yolo"},
	"opencode":    {"read-only", "edit", "full"},
	"antigravity": {"plan", "accept-edits", "full"},
}

// permissionLevels are the levels in permissionModes' order.
var permissionLevels = [3]string{runnerwire.PermRead, runnerwire.PermEdit, runnerwire.PermFull}

// permissionKeys names each kind's setting, as `nodevas runner set` does.
var permissionKeys = map[string]string{
	"claude": "permissionMode", "codex": "codexSandbox", "gemini": "geminiApprovalMode",
	"opencode": "opencodePermission", "antigravity": "antigravityMode",
}

// PermissionLevel is how freely agent kind may act under cfg, as a level
// (runnerwire.Perm*), and the exact setting behind it. A mode that maps to no
// level (Claude Code's auto, manual or dontAsk, agy's auto, or Bash in
// Claude Code's allowedTools below full) is runnerwire.PermCustom.
func PermissionLevel(cfg *Config, kind string) (level, detail string) {
	mode := cfg.PermissionModeFor(kind)
	detail = permissionKeys[kind] + "=" + mode
	level = runnerwire.PermCustom
	for i, m := range permissionModes[kind] {
		if m == mode {
			level = permissionLevels[i]
		}
	}
	if kind == "claude" && level != runnerwire.PermFull {
		for _, tool := range cfg.AllowedTools {
			if bashTool.MatchString(strings.TrimSpace(tool)) {
				return runnerwire.PermCustom, detail + ", allowedTools " + tool
			}
		}
	}
	return level, detail
}

// setPermissionLevel sets agent kind's mode for level. Read and edit take
// Bash out of Claude Code's allowedTools too: left there, it would let the
// agent run any command whatever its mode says.
func (c *Config) setPermissionLevel(kind, level string) error {
	modes, ok := permissionModes[kind]
	if !ok {
		return fmt.Errorf("unknown agent kind %q", kind)
	}
	i := slices.Index(permissionLevels[:], level)
	if i < 0 {
		return fmt.Errorf("permission level must be %s, %s or %s", runnerwire.PermRead, runnerwire.PermEdit, runnerwire.PermFull)
	}
	mode := modes[i]
	switch kind {
	case "claude":
		c.PermissionMode = mode
		if level != runnerwire.PermFull {
			c.AllowedTools = slices.DeleteFunc(slices.Clone(c.AllowedTools), func(tool string) bool {
				return bashTool.MatchString(strings.TrimSpace(tool))
			})
			if len(c.AllowedTools) == 0 {
				c.AllowedTools = nil
			}
		}
	case "codex":
		c.CodexSandbox = mode
	case "gemini":
		c.GeminiApprovalMode = mode
	case "opencode":
		c.OpenCodePermission = mode
	case "antigravity":
		c.AntigravityMode = mode
	}
	return nil
}

// setShellWithoutAsking adds kind to ShellWithoutAsking, or takes it off.
func (c *Config) setShellWithoutAsking(kind string, on bool) {
	list := slices.DeleteFunc(slices.Clone(c.ShellWithoutAsking), func(k string) bool { return k == kind })
	if on {
		list = append(list, kind)
	}
	if len(list) == 0 {
		list = nil
	}
	c.ShellWithoutAsking = list
}

// localSettings is Info.Local for cfg, saved at path.
func localSettings(cfg *Config, path string) *runnerwire.LocalSettings {
	local := &runnerwire.LocalSettings{
		Permission:         map[string]string{},
		PermissionDetail:   map[string]string{},
		AskEveryRun:        cfg.AskEveryRun,
		ShellWithoutAsking: append([]string{}, cfg.ShellWithoutAsking...),
		AutoPR:             cfg.AutoPROn(),
		PRDraft:            cfg.PRDraft,
		ConfigPath:         path,
	}
	for _, kind := range Kinds {
		local.Permission[kind], local.PermissionDetail[kind] = PermissionLevel(cfg, kind)
	}
	return local
}

// builtInCaps are the capabilities only the built-in runner has: it takes
// the app's answers to its asks, and repo admin.
var builtInCaps = []string{runnerwire.CapAppAccept, runnerwire.CapRepoAdmin}

// refreshInfo is info, a hello, with what the runner's settings say now:
// its repos, run limit, auto-accept policy and, for the built-in runner, its
// capabilities and Info.Local. It runs no agent CLI, so it is quick enough
// for a hello sent while the hub waits for an answer.
func (c *Client) refreshInfo(info runnerwire.Info) runnerwire.Info {
	cfg := c.cfg()
	info.Repos = helloRepos(cfg)
	info.MaxConcurrent = c.MaxConcurrentRuns()
	policy := cfg.AutoAcceptPolicy()
	info.AutoAccept = &policy
	info.MaxConcurrentCeiling = 0
	if cfg.RemoteSettingsOn() {
		info.MaxConcurrentCeiling = cfg.MaxConcurrentCeilingOrDefault()
	}
	caps := slices.DeleteFunc(slices.Clone(info.Capabilities), func(cp string) bool { return slices.Contains(builtInCaps, cp) })
	info.Local = nil
	if c.BuiltIn() {
		caps = append(caps, builtInCaps...)
		info.Local = localSettings(cfg, c.ConfigPath)
	}
	info.Capabilities = caps
	return info
}

// updateConfig changes the runner's settings with edit: on a clone of the
// config in effect, swapped in whole, and in the settings file read fresh,
// so a `nodevas runner set` made since the runner started is kept. edit must
// be safe to run on both. An error from edit on the clone changes nothing;
// one after that leaves the change in effect until the runner stops, not
// saved. The caller holds settingsMu.
func (c *Client) updateConfig(edit func(*Config) error) (persisted bool, err error) {
	next := c.cfg().Clone()
	if err := edit(next); err != nil {
		return false, err
	}
	c.live.Store(next)
	if c.ConfigPath == "" {
		return false, nil
	}
	onDisk, err := LoadConfig(c.ConfigPath)
	if err != nil {
		return false, err
	}
	if onDisk.LegacyAutoAccept() {
		// Writing it back would drop the old autoAccept's agents; the CLI
		// migrates it first (nodevas runner status).
		return false, fmt.Errorf("%s still has the old autoAccept setting; run nodevas runner status once to update it", c.ConfigPath)
	}
	if err := edit(onDisk); err != nil {
		return false, err
	}
	if err := SaveConfig(c.ConfigPath, onDisk); err != nil {
		return false, err
	}
	return true, nil
}

// errNotSaved marks an updateConfig failure after the change took effect.
var errNotSaved = errors.New("applied until the runner stops; not saved")

// localRequest reports whether req sets anything but maxConcurrent.
func localRequest(req runnerwire.SettingsRequest) bool {
	return req.Permission != nil || req.AskEveryRun != nil || req.ShellWithoutAsking != nil ||
		req.AutoPR != nil || req.PRDraft != nil || req.Redetect
}

// checkLocalRequest vets req's local fields before anything applies.
func checkLocalRequest(req runnerwire.SettingsRequest) error {
	for kind, level := range req.Permission {
		if _, ok := permissionModes[kind]; !ok {
			return fmt.Errorf("unknown agent kind %q", kind)
		}
		if !slices.Contains(permissionLevels[:], level) {
			return fmt.Errorf("%s: permission level must be %s, %s or %s", kind, runnerwire.PermRead, runnerwire.PermEdit, runnerwire.PermFull)
		}
	}
	for kind := range req.ShellWithoutAsking {
		if !slices.Contains(Kinds, kind) {
			return fmt.Errorf("unknown agent kind %q", kind)
		}
	}
	return nil
}

// editLocal is the change req's local fields make.
func editLocal(req runnerwire.SettingsRequest) func(*Config) error {
	return func(cfg *Config) error {
		for _, kind := range sortedKeys(req.Permission) {
			if err := cfg.setPermissionLevel(kind, req.Permission[kind]); err != nil {
				return err
			}
		}
		if req.AskEveryRun != nil {
			cfg.AskEveryRun = *req.AskEveryRun
		}
		for _, kind := range sortedKeys(req.ShellWithoutAsking) {
			cfg.setShellWithoutAsking(kind, req.ShellWithoutAsking[kind])
		}
		if req.AutoPR != nil {
			on := *req.AutoPR
			cfg.AutoPR = &on
		}
		if req.PRDraft != nil {
			cfg.PRDraft = *req.PRDraft
		}
		return nil
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// allowShellWithoutAsking is the owner's "don't ask again for this agent"
// from the app: kind's automatic runs start unasked from now on, saved.
func (c *Client) allowShellWithoutAsking(kind string) {
	c.settingsMu.Lock()
	persisted, err := c.updateConfig(func(cfg *Config) error {
		cfg.setShellWithoutAsking(kind, true)
		return nil
	})
	c.settingsMu.Unlock()
	switch {
	case err != nil:
		c.logf("automatic runs of %s now start without asking; %v: %v", kind, errNotSaved, err)
	case persisted:
		c.logf("automatic runs of %s now start without asking (you said so in Nodevas); saved in %s", kind, c.ConfigPath)
	default:
		c.logf("automatic runs of %s now start without asking (you said so in Nodevas), until the runner stops", kind)
	}
	c.changedInfo()
}

// builtInMigratedNote is said once, on the first start as the built-in
// runner.
const builtInMigratedNote = "Runs you start in Nodevas now begin right away on this computer (no y needed). " +
	"To be asked first: Settings → Agents → This computer → Ask me before every run starts " +
	"(設定 → Agent → 這台電腦 → 每個工作開始前都先問我)."

// migrateBuiltIn runs on the first start as the built-in runner: an
// autoAccept.allowShell the owner had set becomes shellWithoutAsking for the
// agents installed here that it covered (auto-accept on, the agent on its
// list): allowShell never let any other agent start unasked. The rest of
// autoAccept stays in the file, unused while the runner is the built-in one,
// and applies again if it ever is not.
func (c *Client) migrateBuiltIn(agents []runnerwire.Agent) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	cfg := c.cfg()
	if cfg.BuiltInMigrated {
		return
	}
	var kinds []string
	if policy := cfg.AutoAccept; policy != nil && policy.Enabled && policy.AllowShell {
		for _, agent := range agents {
			if agent.Installed && slices.Contains(policy.Agents, agent.Kind) {
				kinds = append(kinds, agent.Kind)
			}
		}
	}
	_, err := c.updateConfig(func(cfg *Config) error {
		for _, kind := range kinds {
			if !slices.Contains(cfg.ShellWithoutAsking, kind) {
				cfg.ShellWithoutAsking = append(cfg.ShellWithoutAsking, kind)
			}
		}
		cfg.BuiltInMigrated = true
		return nil
	})
	c.logf("%s", builtInMigratedNote)
	if len(kinds) > 0 {
		c.logf("autoAccept allowShell carried over: automatic runs of %s start without asking", strings.Join(kinds, ", "))
	}
	if err != nil {
		c.logf("%v: %v", errNotSaved, err)
	}
}
