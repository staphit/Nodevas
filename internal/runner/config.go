// Package runner is the client half of the workstation: the `nodevas runner`
// daemon that runs on the owner's own machine, keeps an outbound connection to
// the hub and tells it which agent CLIs, models and repositories it has.
//
// This is the only side that starts processes. In W0 that is the agent CLIs'
// --version and git, nothing else; the hub never asks for a command, it only
// ever reads what the runner reports (see internal/runnerwire).
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"nodevas/internal/runner/adapter"
	"nodevas/internal/runnerwire"
)

// Config is the runner's settings file. It holds the runner token and the
// Access service-token secret, so it is written owner-only and never printed.
type Config struct {
	// Hub is the base URL of the Nodevas server, e.g. https://nodevas.example.com.
	Hub   string `json:"hub"`
	Token string `json:"token,omitempty"`
	// AccessClientID and AccessClientSecret are a Cloudflare Access service
	// token, for a hub behind Access. The edge checks them; the hub never does.
	AccessClientID     string `json:"accessClientId,omitempty"`
	AccessClientSecret string `json:"accessClientSecret,omitempty"`
	// Name is an optional label for log lines; the hub's name for the runner
	// is the one it was created under.
	Name string `json:"name,omitempty"`
	// Repos are the repositories the owner allowed runs in, added with
	// `nodevas runner repo add` on this machine and nowhere else.
	Repos []runnerwire.Repo `json:"repos,omitempty"`
	// Models replaces the built-in model list for an agent kind.
	Models map[string][]runnerwire.Model `json:"models,omitempty"`

	// Run settings, from W1 on; zero values mean the defaults below. They live
	// here and not in the job because how freely an agent may act on this
	// machine is its owner's call, never the hub's.

	// MaxConcurrent is how many runs (being asked about or running) at once.
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
	// RunTimeoutMinutes stops a run that goes on longer.
	RunTimeoutMinutes int `json:"runTimeoutMinutes,omitempty"`
	// DataDir holds run worktrees, under DataDir/worktrees/<run id>.
	DataDir string `json:"dataDir,omitempty"`
	// PermissionMode is Claude Code's --permission-mode for every run.
	PermissionMode string `json:"permissionMode,omitempty"`
	// AllowedTools is passed to Claude Code as --allowedTools, e.g. "Bash(go test *)".
	AllowedTools []string `json:"allowedTools,omitempty"`
	// CodexSandbox is Codex's --sandbox for every run.
	CodexSandbox string `json:"codexSandbox,omitempty"`
	// CodexIgnoreUserConfig runs Codex without the owner's config.toml (their
	// MCP servers, hooks and default model); its sign-in is still used.
	CodexIgnoreUserConfig bool `json:"codexIgnoreUserConfig,omitempty"`
	// GeminiApprovalMode is Gemini CLI's --approval-mode for every run.
	GeminiApprovalMode string `json:"geminiApprovalMode,omitempty"`
	// AutoAccept is the auto-accept allowlist (W6; autoaccept.go): which
	// offers are taken without asking at the terminal. nil means off. W1's
	// boolean form is migrated on load.
	AutoAccept *runnerwire.AutoAcceptPolicy `json:"autoAccept,omitempty"`
	// legacyAutoAccept: the file had W1's `"autoAccept": true`, and
	// MigrateLegacyAutoAccept has not filled in the agents yet.
	legacyAutoAccept bool

	// Pull requests, from W3 on.

	// AutoPR pushes a run's commit and opens a pull request with the owner's
	// gh login; nil means the default, on.
	AutoPR *bool `json:"autoPR,omitempty"`
	// PRDraft opens pull requests as drafts. A failed run's PR is always one.
	PRDraft bool `json:"prDraft,omitempty"`
	// PRLabels replaces the default labels; nil means DefaultPRLabels, an
	// empty list means none. "{model}" and "{agent}" are filled in per run.
	PRLabels *[]string `json:"prLabels,omitempty"`
	// PRPollSeconds is how often open pull requests are checked with gh.
	PRPollSeconds int `json:"prPollSeconds,omitempty"`

	// Remote merge, from W4 on.

	// RemoteMerge lets someone who approves a run's diff on the hub have this
	// runner merge its PR (with the owner's gh login); nil means the default,
	// on. Off, every approval is refused.
	RemoteMerge *bool `json:"remoteMerge,omitempty"`
	// MergeMethod is how an approved PR is merged: squash (the default),
	// merge or rebase. The hub's request never chooses.
	MergeMethod string `json:"mergeMethod,omitempty"`
	// DeleteBranchOnMerge deletes the PR's branch on GitHub once merged.
	DeleteBranchOnMerge bool `json:"deleteBranchOnMerge,omitempty"`
}

// Defaults and bounds for the run settings.
const (
	DefaultMaxConcurrent     = 1
	MaxMaxConcurrent         = 16
	DefaultRunTimeoutMinutes = 60
	MaxRunTimeoutMinutes     = 24 * 60
	DefaultPRPollSeconds     = 120
	MinPRPollSeconds         = 30
	MaxPRPollSeconds         = 24 * 60 * 60
)

// DefaultPRLabels are a run's pull request labels unless the owner set others.
var DefaultPRLabels = []string{"nodevas", "model:{model}"}

// MergeMethods are the merge methods gh pr merge takes, the default first.
var MergeMethods = []string{"squash", "merge", "rebase"}

// RemoteMergeOn is RemoteMerge with its default (on) applied.
func (c *Config) RemoteMergeOn() bool { return c.RemoteMerge == nil || *c.RemoteMerge }

// MergeMethodOrDefault is MergeMethod with its default applied; anything
// unknown (a hand-edited file) falls back to squash.
func (c *Config) MergeMethodOrDefault() string {
	if slices.Contains(MergeMethods, c.MergeMethod) {
		return c.MergeMethod
	}
	return MergeMethods[0]
}

// AutoPROn is AutoPR with its default (on) applied.
func (c *Config) AutoPROn() bool { return c.AutoPR == nil || *c.AutoPR }

// PRLabelTemplates is PRLabels with its default applied, placeholders unfilled.
func (c *Config) PRLabelTemplates() []string {
	if c.PRLabels == nil {
		return append([]string(nil), DefaultPRLabels...)
	}
	return append([]string(nil), (*c.PRLabels)...)
}

// PRLabelsFor is the labels for one run's pull request: {model} and {agent}
// filled in, "default" standing for the CLI's own default model.
func (c *Config) PRLabelsFor(agent, model string) []string {
	if model == "" {
		model = "default"
	}
	var labels []string
	for _, label := range c.PRLabelTemplates() {
		label = strings.ReplaceAll(label, "{model}", model)
		label = strings.ReplaceAll(label, "{agent}", agent)
		if label = strings.TrimSpace(label); label != "" && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return labels
}

// PRPollInterval is PRPollSeconds with its default and bounds applied.
func (c *Config) PRPollInterval() time.Duration {
	seconds := c.PRPollSeconds
	if seconds <= 0 {
		seconds = DefaultPRPollSeconds
	}
	return time.Duration(min(max(seconds, MinPRPollSeconds), MaxPRPollSeconds)) * time.Second
}

// MaxConcurrentRuns is MaxConcurrent with its default applied.
func (c *Config) MaxConcurrentRuns() int {
	if c.MaxConcurrent <= 0 {
		return DefaultMaxConcurrent
	}
	return min(c.MaxConcurrent, MaxMaxConcurrent)
}

// RunTimeout is RunTimeoutMinutes with its default applied.
func (c *Config) RunTimeout() time.Duration {
	minutes := c.RunTimeoutMinutes
	if minutes <= 0 {
		minutes = DefaultRunTimeoutMinutes
	}
	return time.Duration(min(minutes, MaxRunTimeoutMinutes)) * time.Minute
}

// RunTimeoutMins is the timeout in minutes, for messages.
func (c *Config) RunTimeoutMins() int { return int(c.RunTimeout() / time.Minute) }

// RunDataDir is DataDir with its default applied: next to the settings, in the
// OS's user config directory.
func (c *Config) RunDataDir() (string, error) {
	if strings.TrimSpace(c.DataDir) != "" {
		return c.DataDir, nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config directory: %w", err)
	}
	return filepath.Join(root, "nodevas", "runner-data"), nil
}

// ClaudePermissionMode is PermissionMode with its default applied.
func (c *Config) ClaudePermissionMode() string {
	if c.PermissionMode == "" {
		return adapter.DefaultClaudePermissionMode
	}
	return c.PermissionMode
}

// CodexSandboxMode is CodexSandbox with its default applied.
func (c *Config) CodexSandboxMode() string {
	if c.CodexSandbox == "" {
		return adapter.DefaultCodexSandbox
	}
	return c.CodexSandbox
}

// GeminiApproval is GeminiApprovalMode with its default applied.
func (c *Config) GeminiApproval() string {
	if c.GeminiApprovalMode == "" {
		return adapter.DefaultGeminiApprovalMode
	}
	return c.GeminiApprovalMode
}

// PermissionModeFor is the owner's permission setting for an agent kind, as
// adapter.RunEnv.PermissionMode takes it.
func (c *Config) PermissionModeFor(kind string) string {
	switch kind {
	case "codex":
		return c.CodexSandboxMode()
	case "gemini":
		return c.GeminiApproval()
	}
	return c.ClaudePermissionMode()
}

// SettingKeys are the keys `nodevas runner set` takes, in display order.
var SettingKeys = []string{"maxConcurrent", "runTimeoutMinutes", "dataDir", "permissionMode", "allowedTools",
	"codexSandbox", "codexIgnoreUserConfig", "geminiApprovalMode", "autoAccept",
	"autoPR", "prDraft", "prLabels", "prPollSeconds", "remoteMerge", "mergeMethod", "deleteBranchOnMerge"}

// Set changes one run setting from its command-line form. "default" resets
// any of them. allowedTools takes one tool per value, or one value split at
// top-level commas ("Edit,Bash(git diff, git log)" is two tools); "none"
// clears it. prLabels takes labels one per value or comma-separated; "none"
// means no labels.
func (c *Config) Set(key string, values []string) error {
	canonical := ""
	for _, k := range SettingKeys {
		if strings.EqualFold(k, key) {
			canonical = k
		}
	}
	if canonical == "" {
		return fmt.Errorf("unknown setting %q; settings are %s", key, strings.Join(SettingKeys, ", "))
	}
	if len(values) == 0 {
		return fmt.Errorf("%s needs a value", canonical)
	}
	if canonical != "allowedTools" && canonical != "prLabels" && len(values) != 1 {
		return fmt.Errorf("%s takes one value", canonical)
	}
	value := strings.TrimSpace(values[0])
	reset := strings.EqualFold(value, "default") && len(values) == 1
	switch canonical {
	case "maxConcurrent":
		if reset {
			c.MaxConcurrent = 0
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > MaxMaxConcurrent {
			return fmt.Errorf("maxConcurrent must be a number from 1 to %d", MaxMaxConcurrent)
		}
		c.MaxConcurrent = n
	case "runTimeoutMinutes":
		if reset {
			c.RunTimeoutMinutes = 0
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > MaxRunTimeoutMinutes {
			return fmt.Errorf("runTimeoutMinutes must be a number from 1 to %d", MaxRunTimeoutMinutes)
		}
		c.RunTimeoutMinutes = n
	case "dataDir":
		if reset {
			c.DataDir = ""
			return nil
		}
		abs, err := filepath.Abs(value)
		if err != nil || value == "" {
			return fmt.Errorf("dataDir %q is not a usable path", value)
		}
		c.DataDir = abs
	case "permissionMode":
		if reset {
			c.PermissionMode = ""
			return nil
		}
		mode, err := oneOf(canonical, value, adapter.ClaudePermissionModes)
		if err != nil {
			return err
		}
		c.PermissionMode = mode
	case "codexSandbox":
		if reset {
			c.CodexSandbox = ""
			return nil
		}
		mode, err := oneOf(canonical, value, adapter.CodexSandboxModes)
		if err != nil {
			return err
		}
		c.CodexSandbox = mode
	case "codexIgnoreUserConfig":
		if reset {
			c.CodexIgnoreUserConfig = false
			return nil
		}
		on, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("codexIgnoreUserConfig must be true or false")
		}
		c.CodexIgnoreUserConfig = on
	case "geminiApprovalMode":
		if reset {
			c.GeminiApprovalMode = ""
			return nil
		}
		mode, err := oneOf(canonical, value, adapter.GeminiApprovalModes)
		if err != nil {
			return err
		}
		c.GeminiApprovalMode = mode
	case "allowedTools":
		if reset || (len(values) == 1 && (strings.EqualFold(value, "none") || value == "")) {
			c.AllowedTools = nil
			return nil
		}
		var tools []string
		if len(values) == 1 {
			tools = splitTools(value)
		} else {
			for _, v := range values {
				if v = strings.TrimSpace(v); v != "" {
					tools = append(tools, v)
				}
			}
		}
		if len(tools) == 0 {
			return errors.New("allowedTools: no tools given")
		}
		c.AllowedTools = tools
	case "autoAccept":
		if reset {
			c.AutoAccept, c.legacyAutoAccept = nil, false
			return nil
		}
		on, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("autoAccept must be true or false")
		}
		// Without detection at hand every kind is listed; the CLI calls
		// SetAutoAccept itself with the agents installed here.
		c.SetAutoAccept(on, Kinds)
	case "autoPR":
		if reset {
			c.AutoPR = nil
			return nil
		}
		on, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("autoPR must be true or false")
		}
		c.AutoPR = &on
	case "prDraft":
		if reset {
			c.PRDraft = false
			return nil
		}
		on, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("prDraft must be true or false")
		}
		c.PRDraft = on
	case "prLabels":
		if reset {
			c.PRLabels = nil
			return nil
		}
		labels := []string{}
		if !(len(values) == 1 && (strings.EqualFold(value, "none") || value == "")) {
			for _, v := range values {
				for _, label := range strings.Split(v, ",") {
					if label = strings.TrimSpace(label); label == "" {
						continue
					}
					if len(label) > 50 {
						return fmt.Errorf("label %q is longer than GitHub allows (50)", label)
					}
					labels = append(labels, label)
				}
			}
			if len(labels) == 0 {
				return errors.New("prLabels: no labels given")
			}
		}
		c.PRLabels = &labels
	case "prPollSeconds":
		if reset {
			c.PRPollSeconds = 0
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < MinPRPollSeconds || n > MaxPRPollSeconds {
			return fmt.Errorf("prPollSeconds must be a number from %d to %d", MinPRPollSeconds, MaxPRPollSeconds)
		}
		c.PRPollSeconds = n
	case "remoteMerge":
		if reset {
			c.RemoteMerge = nil
			return nil
		}
		on, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("remoteMerge must be true or false")
		}
		c.RemoteMerge = &on
	case "mergeMethod":
		if reset {
			c.MergeMethod = ""
			return nil
		}
		method, err := oneOf(canonical, value, MergeMethods)
		if err != nil {
			return err
		}
		c.MergeMethod = method
	case "deleteBranchOnMerge":
		if reset {
			c.DeleteBranchOnMerge = false
			return nil
		}
		on, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("deleteBranchOnMerge must be true or false")
		}
		c.DeleteBranchOnMerge = on
	}
	return nil
}

// oneOf matches value to one of choices, ignoring case, and returns the
// choice as the CLI spells it.
func oneOf(key, value string, choices []string) (string, error) {
	for _, choice := range choices {
		if strings.EqualFold(choice, value) {
			return choice, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s", key, strings.Join(choices, ", "))
}

// splitTools splits at commas outside parentheses, so a rule like
// "Bash(git diff, git log)" stays one tool.
func splitTools(s string) []string {
	var tools []string
	depth, start := 0, 0
	flush := func(end int) {
		if tool := strings.TrimSpace(s[start:end]); tool != "" {
			tools = append(tools, tool)
		}
	}
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return tools
}

// DefaultConfigPath is where the runner keeps its settings: next to the rest
// of Nodevas's per-user state, in the OS's user config directory.
func DefaultConfigPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config directory: %w", err)
	}
	return filepath.Join(root, "nodevas", "runner.json"), nil
}

// LoadConfig reads the settings file. A file that does not exist yet is an
// empty configuration, not an error: `runner login` is what creates it.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// SaveConfig writes the settings file owner-only (0600; on Windows the mode is
// best effort and the file inherits the user profile's ACL). It writes a
// temporary file and renames it, so a crash never leaves half a token behind.
func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".runner-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	// Windows honours only the read-only bit; the profile directory's ACL is
	// the protection there, so a failure here is not worth stopping for.
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

// ValidateHub normalises the hub URL. It must be https, except on loopback
// where a local `nodevas serve` speaks plain http: the runner token is a
// bearer credential and must not cross a network in the clear.
func ValidateHub(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("hub URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("hub URL %q is not an absolute URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("hub URL %q must not carry credentials, a query or a fragment", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", fmt.Errorf("hub URL %q: plain http is only allowed for a hub on this machine; use https", raw)
		}
	default:
		return "", fmt.Errorf("hub URL %q must be https", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateToken checks the shape of a runner token, so a session cookie or an
// Access secret pasted by mistake is caught here rather than as a 401 later.
func ValidateToken(token string) error {
	if !strings.HasPrefix(token, runnerwire.TokenPrefix) || len(token) <= len(runnerwire.TokenPrefix)+16 {
		return fmt.Errorf("that is not a runner token: runner tokens start with %q (create one on the Runners page)",
			runnerwire.TokenPrefix)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("runner token must not contain whitespace")
	}
	return nil
}
