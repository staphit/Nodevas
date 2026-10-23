package agentsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// NetworkConfig is the per-user agent network settings file. It lives with
// the user's config, not in any repo or workspace: which folders stay off the
// network is a decision about this machine, and a repo must not be able to
// opt itself back in.
type NetworkConfig struct {
	// ExcludeFolders are absolute paths. An agent session whose working
	// directory is one of them, or inside one, does not join the network:
	// its hooks report nothing and its `nodevas mcp` sends no heartbeat.
	ExcludeFolders []string `json:"excludeFolders"`
}

// NetworkConfigPath is <user config dir>/nodevas/agent-network.json.
func NetworkConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nodevas", "agent-network.json"), nil
}

// LoadNetworkConfig reads path. A missing file is an empty config, not an
// error: nothing excluded is the default.
func LoadNetworkConfig(path string) (NetworkConfig, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NetworkConfig{}, nil
	}
	if err != nil {
		return NetworkConfig{}, err
	}
	var cfg NetworkConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return NetworkConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// SaveNetworkConfig writes cfg to path, creating its folder.
func SaveNetworkConfig(path string, cfg NetworkConfig) error {
	if cfg.ExcludeFolders == nil {
		cfg.ExcludeFolders = []string{}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// foldCase says whether paths compare case-insensitively here. Windows file
// names do; macOS's usually do too, but not always, and a false match there
// would hide a session the person meant to show.
var foldCase = runtime.GOOS == "windows"

func normalizeFolder(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("a folder path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func samePath(a, b string) bool {
	if foldCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isInside reports whether dir is folder or below it. Both are cleaned
// absolute paths.
func isInside(dir, folder string) bool {
	if samePath(dir, folder) {
		return true
	}
	prefix := folder
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if len(dir) <= len(prefix) {
		return false
	}
	return samePath(dir[:len(prefix)], prefix)
}

// IsInside reports whether path is folder or below it, comparing as this
// machine's file system does (case-insensitively on Windows). Both are made
// absolute and cleaned first; neither is resolved through symlinks, which is
// the caller's to do when it matters.
func IsInside(path, folder string) bool {
	p, err := normalizeFolder(path)
	if err != nil {
		return false
	}
	f, err := normalizeFolder(folder)
	if err != nil {
		return false
	}
	return isInside(p, f)
}

// Excluded returns the excluded folder dir is in, or "" when dir is on the
// network.
func (cfg NetworkConfig) Excluded(dir string) string {
	dir, err := normalizeFolder(dir)
	if err != nil {
		return ""
	}
	for _, folder := range cfg.ExcludeFolders {
		clean, err := normalizeFolder(folder)
		if err != nil || !filepath.IsAbs(strings.TrimSpace(folder)) {
			// A relative entry would mean a different folder from every
			// working directory; it is ignored rather than guessed at.
			continue
		}
		if isInside(dir, clean) {
			return clean
		}
	}
	return ""
}

// AddExclude adds path (made absolute). It reports false when the folder was
// already listed.
func (cfg *NetworkConfig) AddExclude(path string) (string, bool, error) {
	clean, err := normalizeFolder(path)
	if err != nil {
		return "", false, err
	}
	if slices.ContainsFunc(cfg.ExcludeFolders, func(f string) bool { return samePath(filepath.Clean(f), clean) }) {
		return clean, false, nil
	}
	cfg.ExcludeFolders = append(cfg.ExcludeFolders, clean)
	return clean, true, nil
}

// RemoveExclude drops path. It reports false when the folder was not listed.
func (cfg *NetworkConfig) RemoveExclude(path string) (string, bool, error) {
	clean, err := normalizeFolder(path)
	if err != nil {
		return "", false, err
	}
	before := len(cfg.ExcludeFolders)
	cfg.ExcludeFolders = slices.DeleteFunc(cfg.ExcludeFolders, func(f string) bool {
		return samePath(filepath.Clean(f), clean)
	})
	return clean, len(cfg.ExcludeFolders) != before, nil
}

// ExcludedFolder loads the config at path (NetworkConfigPath when empty) and
// returns the excluded folder dir is in, or "". A config that cannot be read
// leaves the session on the network and returns the error, for the caller to
// report where it can (`nodevas mcp` has stderr; a hook has nowhere): every
// agent on the machine going dark over one typo is the worse failure.
func ExcludedFolder(path, dir string) (string, error) {
	if path == "" {
		var err error
		if path, err = NetworkConfigPath(); err != nil {
			return "", err
		}
	}
	cfg, err := LoadNetworkConfig(path)
	if err != nil {
		return "", err
	}
	return cfg.Excluded(dir), nil
}
