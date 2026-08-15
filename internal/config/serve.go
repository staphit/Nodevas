// Package config owns configuration that controls how Nodevas itself runs.
// Project content remains in graph.yaml and is parsed by internal/engine.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultFileName is the optional workspace-level server configuration file.
const DefaultFileName = "nodevas.yaml"

// SMTPConfig contains the non-secret SMTP connection settings. The password
// deliberately stays outside this file and is read from NODEVAS_SMTP_PASSWORD
// by the serve command.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	From     string `yaml:"from"`
	Security string `yaml:"security"`
}

// LoggingConfig controls the process and HTTP log output.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// ServeConfig contains settings for the nodevas serve command. CLI flags are
// applied by the command after this value has been loaded, so a flag always
// overrides the file and environment values.
type ServeConfig struct {
	Port           int           `yaml:"port"`
	Listen         string        `yaml:"listen"`
	Hostname       string        `yaml:"hostname"`
	BehindProxy    bool          `yaml:"behind_proxy"`
	TrustedProxy   string        `yaml:"trusted_proxy"`
	TLSCert        string        `yaml:"tls_cert"`
	TLSKey         string        `yaml:"tls_key"`
	AllowPlaintext bool          `yaml:"allow_plaintext"`
	MaxActiveUsers int           `yaml:"max_active_users"`
	SMTP           SMTPConfig    `yaml:"smtp"`
	Logging        LoggingConfig `yaml:"logging"`
}

// DefaultServeConfig returns the same defaults that the serve command used
// before configuration files were supported.
func DefaultServeConfig() ServeConfig {
	return ServeConfig{
		Port:         5666,
		Listen:       "127.0.0.1",
		TrustedProxy: "127.0.0.1/32,::1/128",
		SMTP: SMTPConfig{
			Port:     587,
			Security: "starttls",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// LoadServeConfig loads a YAML server configuration. If optional is true, a
// missing file is treated as an empty configuration and the normal defaults
// are returned. An explicitly supplied --config path is loaded with optional
// set to false.
func LoadServeConfig(path string, optional bool) (ServeConfig, error) {
	cfg := DefaultServeConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read server config %q: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("server config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return cfg, fmt.Errorf("server config %q: multiple YAML documents are not supported", path)
		}
		return cfg, fmt.Errorf("server config %q: %w", path, err)
	}
	return cfg, nil
}

// ApplyEnvironment applies the documented NODEVAS_SERVE_* overrides. The
// lookup function is injected so parsing stays deterministic and testable.
func ApplyEnvironment(cfg *ServeConfig, lookup func(string) (string, bool)) error {
	if cfg == nil {
		return errors.New("server config is nil")
	}
	if lookup == nil {
		return errors.New("environment lookup is nil")
	}

	stringsToSet := []struct {
		name string
		set  func(string)
	}{
		{"NODEVAS_SERVE_LISTEN", func(value string) { cfg.Listen = value }},
		{"NODEVAS_SERVE_HOSTNAME", func(value string) { cfg.Hostname = value }},
		{"NODEVAS_SERVE_TRUSTED_PROXY", func(value string) { cfg.TrustedProxy = value }},
		{"NODEVAS_SERVE_TLS_CERT", func(value string) { cfg.TLSCert = value }},
		{"NODEVAS_SERVE_TLS_KEY", func(value string) { cfg.TLSKey = value }},
		{"NODEVAS_SERVE_SMTP_HOST", func(value string) { cfg.SMTP.Host = value }},
		{"NODEVAS_SERVE_SMTP_USER", func(value string) { cfg.SMTP.User = value }},
		{"NODEVAS_SERVE_SMTP_FROM", func(value string) { cfg.SMTP.From = value }},
		{"NODEVAS_SERVE_SMTP_SECURITY", func(value string) { cfg.SMTP.Security = value }},
		{"NODEVAS_SERVE_LOG_LEVEL", func(value string) { cfg.Logging.Level = value }},
		{"NODEVAS_SERVE_LOG_FORMAT", func(value string) { cfg.Logging.Format = value }},
	}
	for _, field := range stringsToSet {
		if value, ok := lookup(field.name); ok && strings.TrimSpace(value) != "" {
			field.set(strings.TrimSpace(value))
		}
	}

	ints := []struct {
		name string
		set  func(int)
	}{
		{"NODEVAS_SERVE_PORT", func(value int) { cfg.Port = value }},
		{"NODEVAS_SERVE_MAX_ACTIVE_USERS", func(value int) { cfg.MaxActiveUsers = value }},
		{"NODEVAS_SERVE_SMTP_PORT", func(value int) { cfg.SMTP.Port = value }},
	}
	for _, field := range ints {
		value, ok := lookup(field.name)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s must be an integer: %w", field.name, err)
		}
		field.set(parsed)
	}

	bools := []struct {
		name string
		set  func(bool)
	}{
		{"NODEVAS_SERVE_BEHIND_PROXY", func(value bool) { cfg.BehindProxy = value }},
		{"NODEVAS_SERVE_ALLOW_PLAINTEXT", func(value bool) { cfg.AllowPlaintext = value }},
	}
	for _, field := range bools {
		value, ok := lookup(field.name)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s must be a boolean: %w", field.name, err)
		}
		field.set(parsed)
	}
	return nil
}

// Validate checks values that are independent of the network topology. The
// serve command performs the additional TLS and hostname checks because those
// depend on its existing host-classification helpers.
func (c ServeConfig) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if c.MaxActiveUsers < 0 {
		return errors.New("max-active-users cannot be negative")
	}
	if c.SMTP.Port < 1 || c.SMTP.Port > 65535 {
		return errors.New("smtp port must be between 1 and 65535")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls_cert and tls_key must be given together")
	}
	return nil
}
