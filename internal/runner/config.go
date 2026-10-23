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
	"strings"

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
