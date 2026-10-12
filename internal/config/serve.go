// Package config owns configuration that controls how Nodevas itself runs.
// Project content remains in graph.yaml and is parsed by internal/engine.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
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

// AbuseConfig controls rate limiting, burst budgets, and concurrency gates.
type AbuseConfig struct {
	PreAuthGlobalRate  float64 `yaml:"pre_auth_global_rate"`
	PreAuthGlobalBurst float64 `yaml:"pre_auth_global_burst"`
	PreAuthIPRate      float64 `yaml:"pre_auth_ip_rate"`
	PreAuthIPBurst     float64 `yaml:"pre_auth_ip_burst"`

	ReadGlobalRate  float64 `yaml:"read_global_rate"`
	ReadGlobalBurst float64 `yaml:"read_global_burst"`
	ReadIPRate      float64 `yaml:"read_ip_rate"`
	ReadIPBurst     float64 `yaml:"read_ip_burst"`
	ReadActorRate   float64 `yaml:"read_actor_rate"`
	ReadActorBurst  float64 `yaml:"read_actor_burst"`

	WriteGlobalRate  float64 `yaml:"write_global_rate"`
	WriteGlobalBurst float64 `yaml:"write_global_burst"`
	WriteIPRate      float64 `yaml:"write_ip_rate"`
	WriteIPBurst     float64 `yaml:"write_ip_burst"`
	WriteActorRate   float64 `yaml:"write_actor_rate"`
	WriteActorBurst  float64 `yaml:"write_actor_burst"`

	ExpensiveGlobalRate  float64 `yaml:"expensive_global_rate"`
	ExpensiveGlobalBurst float64 `yaml:"expensive_global_burst"`
	ExpensiveIPRate      float64 `yaml:"expensive_ip_rate"`
	ExpensiveIPBurst     float64 `yaml:"expensive_ip_burst"`
	ExpensiveActorRate   float64 `yaml:"expensive_actor_rate"`
	ExpensiveActorBurst  float64 `yaml:"expensive_actor_burst"`

	HeavyGlobalRate  float64 `yaml:"heavy_global_rate"`
	HeavyGlobalBurst float64 `yaml:"heavy_global_burst"`
	HeavyIPRate      float64 `yaml:"heavy_ip_rate"`
	HeavyIPBurst     float64 `yaml:"heavy_ip_burst"`
	HeavyActorRate   float64 `yaml:"heavy_actor_rate"`
	HeavyActorBurst  float64 `yaml:"heavy_actor_burst"`

	WSUpgradeGlobalRate  float64 `yaml:"ws_upgrade_global_rate"`
	WSUpgradeGlobalBurst float64 `yaml:"ws_upgrade_global_burst"`
	WSUpgradeIPRate      float64 `yaml:"ws_upgrade_ip_rate"`
	WSUpgradeIPBurst     float64 `yaml:"ws_upgrade_ip_burst"`
	WSUpgradeActorRate   float64 `yaml:"ws_upgrade_actor_rate"`
	WSUpgradeActorBurst  float64 `yaml:"ws_upgrade_actor_burst"`

	MaxConcurrentHeavy  int `yaml:"max_concurrent_heavy"`
	MaxConcurrentDOCX   int `yaml:"max_concurrent_docx"`
	MaxConcurrentRemote int `yaml:"max_concurrent_remote"`
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
	Abuse          AbuseConfig   `yaml:"abuse"`
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
		Abuse: AbuseConfig{
			PreAuthGlobalRate:  200,
			PreAuthGlobalBurst: 400,
			PreAuthIPRate:      40,
			PreAuthIPBurst:     80,

			ReadGlobalRate:  300,
			ReadGlobalBurst: 600,
			ReadIPRate:      60,
			ReadIPBurst:     120,
			ReadActorRate:   40,
			ReadActorBurst:  80,

			WriteGlobalRate:  150,
			WriteGlobalBurst: 300,
			WriteIPRate:      40,
			WriteIPBurst:     80,
			WriteActorRate:   30,
			WriteActorBurst:  60,

			ExpensiveGlobalRate:  20,
			ExpensiveGlobalBurst: 40,
			ExpensiveIPRate:      8,
			ExpensiveIPBurst:     24,
			ExpensiveActorRate:   4,
			ExpensiveActorBurst:  16,

			HeavyGlobalRate:  10,
			HeavyGlobalBurst: 30,
			HeavyIPRate:      6,
			HeavyIPBurst:     18,
			HeavyActorRate:   4,
			HeavyActorBurst:  16,

			WSUpgradeGlobalRate:  40,
			WSUpgradeGlobalBurst: 80,
			WSUpgradeIPRate:      10,
			WSUpgradeIPBurst:     20,
			WSUpgradeActorRate:   5,
			WSUpgradeActorBurst:  10,

			MaxConcurrentHeavy:  4,
			MaxConcurrentDOCX:   2,
			MaxConcurrentRemote: 2,
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
		{"NODEVAS_SERVE_ABUSE_MAX_CONCURRENT_HEAVY", func(value int) { cfg.Abuse.MaxConcurrentHeavy = value }},
		{"NODEVAS_SERVE_ABUSE_MAX_CONCURRENT_DOCX", func(value int) { cfg.Abuse.MaxConcurrentDOCX = value }},
		{"NODEVAS_SERVE_ABUSE_MAX_CONCURRENT_REMOTE", func(value int) { cfg.Abuse.MaxConcurrentRemote = value }},
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

	floats := []struct {
		name string
		set  func(float64)
	}{
		{"NODEVAS_SERVE_ABUSE_PRE_AUTH_GLOBAL_RATE", func(value float64) { cfg.Abuse.PreAuthGlobalRate = value }},
		{"NODEVAS_SERVE_ABUSE_PRE_AUTH_GLOBAL_BURST", func(value float64) { cfg.Abuse.PreAuthGlobalBurst = value }},
		{"NODEVAS_SERVE_ABUSE_PRE_AUTH_IP_RATE", func(value float64) { cfg.Abuse.PreAuthIPRate = value }},
		{"NODEVAS_SERVE_ABUSE_PRE_AUTH_IP_BURST", func(value float64) { cfg.Abuse.PreAuthIPBurst = value }},
		{"NODEVAS_SERVE_ABUSE_READ_GLOBAL_RATE", func(value float64) { cfg.Abuse.ReadGlobalRate = value }},
		{"NODEVAS_SERVE_ABUSE_READ_GLOBAL_BURST", func(value float64) { cfg.Abuse.ReadGlobalBurst = value }},
		{"NODEVAS_SERVE_ABUSE_READ_IP_RATE", func(value float64) { cfg.Abuse.ReadIPRate = value }},
		{"NODEVAS_SERVE_ABUSE_READ_IP_BURST", func(value float64) { cfg.Abuse.ReadIPBurst = value }},
		{"NODEVAS_SERVE_ABUSE_READ_ACTOR_RATE", func(value float64) { cfg.Abuse.ReadActorRate = value }},
		{"NODEVAS_SERVE_ABUSE_READ_ACTOR_BURST", func(value float64) { cfg.Abuse.ReadActorBurst = value }},
		{"NODEVAS_SERVE_ABUSE_WRITE_GLOBAL_RATE", func(value float64) { cfg.Abuse.WriteGlobalRate = value }},
		{"NODEVAS_SERVE_ABUSE_WRITE_GLOBAL_BURST", func(value float64) { cfg.Abuse.WriteGlobalBurst = value }},
		{"NODEVAS_SERVE_ABUSE_WRITE_IP_RATE", func(value float64) { cfg.Abuse.WriteIPRate = value }},
		{"NODEVAS_SERVE_ABUSE_WRITE_IP_BURST", func(value float64) { cfg.Abuse.WriteIPBurst = value }},
		{"NODEVAS_SERVE_ABUSE_WRITE_ACTOR_RATE", func(value float64) { cfg.Abuse.WriteActorRate = value }},
		{"NODEVAS_SERVE_ABUSE_WRITE_ACTOR_BURST", func(value float64) { cfg.Abuse.WriteActorBurst = value }},
		{"NODEVAS_SERVE_ABUSE_EXPENSIVE_GLOBAL_RATE", func(value float64) { cfg.Abuse.ExpensiveGlobalRate = value }},
		{"NODEVAS_SERVE_ABUSE_EXPENSIVE_GLOBAL_BURST", func(value float64) { cfg.Abuse.ExpensiveGlobalBurst = value }},
		{"NODEVAS_SERVE_ABUSE_EXPENSIVE_IP_RATE", func(value float64) { cfg.Abuse.ExpensiveIPRate = value }},
		{"NODEVAS_SERVE_ABUSE_EXPENSIVE_IP_BURST", func(value float64) { cfg.Abuse.ExpensiveIPBurst = value }},
		{"NODEVAS_SERVE_ABUSE_EXPENSIVE_ACTOR_RATE", func(value float64) { cfg.Abuse.ExpensiveActorRate = value }},
		{"NODEVAS_SERVE_ABUSE_EXPENSIVE_ACTOR_BURST", func(value float64) { cfg.Abuse.ExpensiveActorBurst = value }},
		{"NODEVAS_SERVE_ABUSE_HEAVY_GLOBAL_RATE", func(value float64) { cfg.Abuse.HeavyGlobalRate = value }},
		{"NODEVAS_SERVE_ABUSE_HEAVY_GLOBAL_BURST", func(value float64) { cfg.Abuse.HeavyGlobalBurst = value }},
		{"NODEVAS_SERVE_ABUSE_HEAVY_IP_RATE", func(value float64) { cfg.Abuse.HeavyIPRate = value }},
		{"NODEVAS_SERVE_ABUSE_HEAVY_IP_BURST", func(value float64) { cfg.Abuse.HeavyIPBurst = value }},
		{"NODEVAS_SERVE_ABUSE_HEAVY_ACTOR_RATE", func(value float64) { cfg.Abuse.HeavyActorRate = value }},
		{"NODEVAS_SERVE_ABUSE_HEAVY_ACTOR_BURST", func(value float64) { cfg.Abuse.HeavyActorBurst = value }},
		{"NODEVAS_SERVE_ABUSE_WS_UPGRADE_GLOBAL_RATE", func(value float64) { cfg.Abuse.WSUpgradeGlobalRate = value }},
		{"NODEVAS_SERVE_ABUSE_WS_UPGRADE_GLOBAL_BURST", func(value float64) { cfg.Abuse.WSUpgradeGlobalBurst = value }},
		{"NODEVAS_SERVE_ABUSE_WS_UPGRADE_IP_RATE", func(value float64) { cfg.Abuse.WSUpgradeIPRate = value }},
		{"NODEVAS_SERVE_ABUSE_WS_UPGRADE_IP_BURST", func(value float64) { cfg.Abuse.WSUpgradeIPBurst = value }},
		{"NODEVAS_SERVE_ABUSE_WS_UPGRADE_ACTOR_RATE", func(value float64) { cfg.Abuse.WSUpgradeActorRate = value }},
		{"NODEVAS_SERVE_ABUSE_WS_UPGRADE_ACTOR_BURST", func(value float64) { cfg.Abuse.WSUpgradeActorBurst = value }},
	}
	for _, field := range floats {
		value, ok := lookup(field.name)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return fmt.Errorf("%s must be a floating-point number: %w", field.name, err)
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

	rates := []struct {
		name string
		val  float64
	}{
		{"pre_auth_global_rate", c.Abuse.PreAuthGlobalRate},
		{"pre_auth_ip_rate", c.Abuse.PreAuthIPRate},
		{"read_global_rate", c.Abuse.ReadGlobalRate},
		{"read_ip_rate", c.Abuse.ReadIPRate},
		{"read_actor_rate", c.Abuse.ReadActorRate},
		{"write_global_rate", c.Abuse.WriteGlobalRate},
		{"write_ip_rate", c.Abuse.WriteIPRate},
		{"write_actor_rate", c.Abuse.WriteActorRate},
		{"expensive_global_rate", c.Abuse.ExpensiveGlobalRate},
		{"expensive_ip_rate", c.Abuse.ExpensiveIPRate},
		{"expensive_actor_rate", c.Abuse.ExpensiveActorRate},
		{"heavy_global_rate", c.Abuse.HeavyGlobalRate},
		{"heavy_ip_rate", c.Abuse.HeavyIPRate},
		{"heavy_actor_rate", c.Abuse.HeavyActorRate},
		{"ws_upgrade_global_rate", c.Abuse.WSUpgradeGlobalRate},
		{"ws_upgrade_ip_rate", c.Abuse.WSUpgradeIPRate},
		{"ws_upgrade_actor_rate", c.Abuse.WSUpgradeActorRate},
	}
	for _, r := range rates {
		if math.IsNaN(r.val) || math.IsInf(r.val, 0) || r.val < 0.1 || r.val > 1000000 {
			return fmt.Errorf("abuse rate limit %s must be a finite number between 0.1 and 1000000", r.name)
		}
	}

	bursts := []struct {
		name string
		val  float64
	}{
		{"pre_auth_global_burst", c.Abuse.PreAuthGlobalBurst},
		{"pre_auth_ip_burst", c.Abuse.PreAuthIPBurst},
		{"read_global_burst", c.Abuse.ReadGlobalBurst},
		{"read_ip_burst", c.Abuse.ReadIPBurst},
		{"read_actor_burst", c.Abuse.ReadActorBurst},
		{"write_global_burst", c.Abuse.WriteGlobalBurst},
		{"write_ip_burst", c.Abuse.WriteIPBurst},
		{"write_actor_burst", c.Abuse.WriteActorBurst},
		{"expensive_global_burst", c.Abuse.ExpensiveGlobalBurst},
		{"expensive_ip_burst", c.Abuse.ExpensiveIPBurst},
		{"expensive_actor_burst", c.Abuse.ExpensiveActorBurst},
		{"heavy_global_burst", c.Abuse.HeavyGlobalBurst},
		{"heavy_ip_burst", c.Abuse.HeavyIPBurst},
		{"heavy_actor_burst", c.Abuse.HeavyActorBurst},
		{"ws_upgrade_global_burst", c.Abuse.WSUpgradeGlobalBurst},
		{"ws_upgrade_ip_burst", c.Abuse.WSUpgradeIPBurst},
		{"ws_upgrade_actor_burst", c.Abuse.WSUpgradeActorBurst},
	}
	for _, b := range bursts {
		if math.IsNaN(b.val) || math.IsInf(b.val, 0) || b.val < 1.0 || b.val > 1000000 {
			return fmt.Errorf("abuse burst limit %s must be a finite number between 1.0 and 1000000", b.name)
		}
	}

	if c.Abuse.MaxConcurrentHeavy < 1 || c.Abuse.MaxConcurrentHeavy > 64 ||
		c.Abuse.MaxConcurrentDOCX < 1 || c.Abuse.MaxConcurrentDOCX > 64 ||
		c.Abuse.MaxConcurrentRemote < 1 || c.Abuse.MaxConcurrentRemote > 64 {
		return errors.New("abuse max concurrent parameters must be between 1 and 64")
	}
	return nil
}
