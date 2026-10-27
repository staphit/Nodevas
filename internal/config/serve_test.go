package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadServeConfigUsesDefaultsWhenOptionalFileIsMissing(t *testing.T) {
	cfg, err := LoadServeConfig(filepath.Join(t.TempDir(), "nodevas.yaml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defaults := DefaultServeConfig()
	if cfg != defaults {
		t.Fatalf("config = %+v, want defaults %+v", cfg, defaults)
	}
}

func TestLoadServeConfigParsesNestedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodevas.yaml")
	data := []byte(`
port: 8443
listen: 0.0.0.0
hostname: nodevas.example.com
behind_proxy: true
trusted_proxy: 10.0.0.0/8
tls_cert: cert.pem
tls_key: key.pem
allow_plaintext: false
max_active_users: 12
smtp:
  host: smtp.example.com
  port: 465
  user: mailer
  from: Nodevas <no-reply@example.com>
  security: implicit
logging:
  level: warn
  format: text
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadServeConfig(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8443 || cfg.Listen != "0.0.0.0" || !cfg.BehindProxy || cfg.MaxActiveUsers != 12 {
		t.Fatalf("top-level config = %+v", cfg)
	}
	if cfg.SMTP.Host != "smtp.example.com" || cfg.SMTP.Port != 465 || cfg.SMTP.Security != "implicit" {
		t.Fatalf("smtp config = %+v", cfg.SMTP)
	}
	if cfg.Logging.Level != "warn" || cfg.Logging.Format != "text" {
		t.Fatalf("logging config = %+v", cfg.Logging)
	}
}

func TestLoadServeConfigRejectsUnknownAndMultipleDocuments(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "unknown.yaml")
	if err := os.WriteFile(unknown, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServeConfig(unknown, false); err == nil || !strings.Contains(err.Error(), "field unknown") {
		t.Fatalf("unknown field error = %v", err)
	}

	multiple := filepath.Join(t.TempDir(), "multiple.yaml")
	if err := os.WriteFile(multiple, []byte("port: 1\n---\nport: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServeConfig(multiple, false); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("multiple document error = %v", err)
	}
}

func TestApplyEnvironmentOverridesValues(t *testing.T) {
	values := map[string]string{
		"NODEVAS_SERVE_PORT":             "9000",
		"NODEVAS_SERVE_BEHIND_PROXY":     "true",
		"NODEVAS_SERVE_MAX_ACTIVE_USERS": "4",
		"NODEVAS_SERVE_SMTP_SECURITY":    "implicit",
		"NODEVAS_SERVE_LOG_FORMAT":       "text",
		"NODEVAS_SERVE_TRUSTED_PROXY":    "10.0.0.1/32",
	}
	cfg := DefaultServeConfig()
	if err := ApplyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9000 || !cfg.BehindProxy || cfg.MaxActiveUsers != 4 ||
		cfg.SMTP.Security != "implicit" || cfg.Logging.Format != "text" ||
		cfg.TrustedProxy != "10.0.0.1/32" {
		t.Fatalf("environment overrides not applied: %+v", cfg)
	}
}

func TestApplyEnvironmentRejectsInvalidValues(t *testing.T) {
	for key, value := range map[string]string{
		"NODEVAS_SERVE_PORT":         "six thousand",
		"NODEVAS_SERVE_BEHIND_PROXY": "sometimes",
	} {
		cfg := DefaultServeConfig()
		err := ApplyEnvironment(&cfg, func(candidate string) (string, bool) {
			if candidate == key {
				return value, true
			}
			return "", false
		})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s error = %v", key, err)
		}
	}
}

func TestServeConfigValidate(t *testing.T) {
	cfg := DefaultServeConfig()
	cfg.MaxActiveUsers = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative max-active-users was accepted")
	}
	cfg = DefaultServeConfig()
	cfg.TLSCert = "cert.pem"
	if err := cfg.Validate(); err == nil {
		t.Fatal("a certificate without a key was accepted")
	}
	cfg = DefaultServeConfig()
	cfg.Abuse.ReadGlobalRate = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero abuse rate was accepted")
	}
	cfg = DefaultServeConfig()
	cfg.Abuse.MaxConcurrentHeavy = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero max concurrent was accepted")
	}
}

func TestApplyEnvironmentAbuseOverrides(t *testing.T) {
	values := map[string]string{
		"NODEVAS_SERVE_ABUSE_READ_GLOBAL_RATE":     "500.5",
		"NODEVAS_SERVE_ABUSE_MAX_CONCURRENT_HEAVY": "8",
	}
	cfg := DefaultServeConfig()
	if err := ApplyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Abuse.ReadGlobalRate != 500.5 || cfg.Abuse.MaxConcurrentHeavy != 8 {
		t.Fatalf("abuse environment overrides not applied: %+v", cfg.Abuse)
	}
}

func TestCopilotConfigFromYAMLAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodevas.yaml")
	data := []byte(`
copilot:
  enabled: false
  provider: openai
  model: llama3.1
  base_url: http://127.0.0.1:11434/v1
  effort: medium
  allow_bypass: true
  max_tool_calls_per_turn: 10
  agent_role: worker
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadServeConfig(path, false)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Copilot
	if got.Enabled || got.Provider != "openai" || got.Model != "llama3.1" ||
		got.BaseURL != "http://127.0.0.1:11434/v1" || got.Effort != "medium" ||
		got.AllowBypass == nil || !*got.AllowBypass || got.MaxToolCallsPerTurn != 10 ||
		got.AgentRole != "worker" {
		t.Fatalf("copilot config = %+v", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid copilot config refused: %v", err)
	}

	values := map[string]string{
		"NODEVAS_COPILOT_PROVIDER": "anthropic",
		"NODEVAS_COPILOT_MODEL":    "claude-opus-5-5",
		"NODEVAS_COPILOT_BASE_URL": "https://proxy.example",
		"NODEVAS_COPILOT_ENABLED":  "true",
	}
	if err := ApplyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	got = cfg.Copilot
	if !got.Enabled || got.Provider != "anthropic" || got.Model != "claude-opus-5-5" ||
		got.BaseURL != "https://proxy.example" {
		t.Fatalf("copilot environment overrides not applied: %+v", got)
	}
}

func TestCopilotConfigDefaultsAndValidation(t *testing.T) {
	defaults := DefaultServeConfig().Copilot
	if !defaults.Enabled || defaults.Provider != "auto" || defaults.Effort != "low" ||
		defaults.MaxToolCallsPerTurn != 25 || defaults.AgentRole != "orchestrator" ||
		defaults.AllowBypass != nil {
		t.Fatalf("copilot defaults = %+v", defaults)
	}
	for name, mutate := range map[string]func(*CopilotConfig){
		"provider": func(c *CopilotConfig) { c.Provider = "gemini" },
		"effort":   func(c *CopilotConfig) { c.Effort = "max" },
		"calls":    func(c *CopilotConfig) { c.MaxToolCallsPerTurn = 0 },
		"role":     func(c *CopilotConfig) { c.AgentRole = "admin" },
	} {
		cfg := DefaultServeConfig()
		mutate(&cfg.Copilot)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: invalid copilot config accepted", name)
		}
	}
}

func TestAccessConfigFromYAMLAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodevas.yaml")
	data := []byte("access:\n  team_domain: team.cloudflareaccess.com\n  aud: tag-1\n  required: true\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadServeConfig(path, false)
	if err != nil {
		t.Fatal(err)
	}
	want := AccessConfig{TeamDomain: "team.cloudflareaccess.com", AUD: "tag-1", Required: true}
	if cfg.Access != want || !cfg.Access.Enabled() {
		t.Fatalf("access = %+v, want %+v", cfg.Access, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{
		"NODEVAS_SERVE_ACCESS_TEAM_DOMAIN": "other.cloudflareaccess.com",
		"NODEVAS_SERVE_ACCESS_AUD":         "tag-2",
		"NODEVAS_SERVE_ACCESS_REQUIRED":    "false",
	}
	if err := ApplyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	want = AccessConfig{TeamDomain: "other.cloudflareaccess.com", AUD: "tag-2"}
	if cfg.Access != want {
		t.Fatalf("access after env = %+v, want %+v", cfg.Access, want)
	}

	bad := map[string]string{"NODEVAS_SERVE_ACCESS_REQUIRED": "maybe"}
	if err := ApplyEnvironment(&cfg, func(key string) (string, bool) {
		value, ok := bad[key]
		return value, ok
	}); err == nil {
		t.Fatal("non-boolean NODEVAS_SERVE_ACCESS_REQUIRED accepted")
	}
}

func TestAccessConfigValidate(t *testing.T) {
	if DefaultServeConfig().Access.Enabled() {
		t.Fatal("access enabled by default")
	}
	for _, bad := range []AccessConfig{
		{TeamDomain: "team.cloudflareaccess.com"},
		{AUD: "tag"},
		{Required: true},
	} {
		cfg := DefaultServeConfig()
		cfg.Access = bad
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	cfg := DefaultServeConfig()
	cfg.Access = AccessConfig{TeamDomain: "team.cloudflareaccess.com", AUD: "tag"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("audit-mode access refused: %v", err)
	}
}
