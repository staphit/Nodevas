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
}
