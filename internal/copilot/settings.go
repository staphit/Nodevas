package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"nodevas/internal/db"
)

// settingsKey is the row in the settings table that holds the administrator's
// overrides, as one small JSON object: the settings table is a key/value
// store, and these values are only ever read and written together.
const settingsKey = "copilot.settings"

// Bounds on what an administrator may store. Generous for any real value,
// small enough that the settings row stays a setting.
const (
	maxModelBytes   = 200
	maxBaseURLBytes = 2048
	maxAPIKeyBytes  = 4096
)

// ErrInvalidSettings wraps every refusal of a settings update, so the HTTP
// layer can answer 400 without matching on text.
var ErrInvalidSettings = errors.New("invalid copilot settings")

// KeyStore holds the provider API key outside the database. *secrets.Store
// satisfies it: the key is encrypted with the machine key, so a database
// backup is never a usable credential.
type KeyStore interface {
	Load() ([]byte, error)
	Save(plain []byte) error
	Clear() error
}

// Defaults are what the server was started with: nodevas.yaml, then the
// environment. Administrator settings override them at runtime.
type Defaults struct {
	Enabled     bool
	Provider    string
	Model       string
	BaseURL     string
	Effort      string
	AllowBypass bool
	// APIKey is NODEVAS_COPILOT_API_KEY, which applies whatever the provider.
	APIKey string
	// VendorKeys are the vendors' own variables (ANTHROPIC_API_KEY,
	// OPENAI_API_KEY) by provider kind, used when nothing more specific is
	// set. Keyed by kind so switching provider picks up the matching key.
	VendorKeys map[string]string
}

// Settings are the effective, non-secret copilot settings.
type Settings struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	BaseURL     string `json:"baseUrl"`
	Effort      string `json:"effort"`
	AllowBypass bool   `json:"allowBypass"`
}

// overrides is what an administrator saved. An empty string means "not
// overridden", so clearing a field returns it to the server's default.
type overrides struct {
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	BaseURL     string `json:"baseUrl,omitempty"`
	Effort      string `json:"effort,omitempty"`
	AllowBypass *bool  `json:"allowBypass,omitempty"`
}

// SettingsUpdate is a partial update from the settings API. A nil field is
// left as it is; an empty string reverts it to the server default (for
// APIKey: forgets the stored key).
type SettingsUpdate struct {
	Provider    *string `json:"provider,omitempty"`
	Model       *string `json:"model,omitempty"`
	BaseURL     *string `json:"baseUrl,omitempty"`
	Effort      *string `json:"effort,omitempty"`
	AllowBypass *bool   `json:"allowBypass,omitempty"`
	APIKey      *string `json:"apiKey,omitempty"`
}

// SettingsView is what the admin settings API returns. The key itself is
// write-only: the view says whether there is one and where it comes from.
type SettingsView struct {
	Settings
	Enabled      bool   `json:"enabled"`
	HasAPIKey    bool   `json:"hasApiKey"`
	APIKeySource string `json:"apiKeySource,omitempty"` // stored | environment
}

// settingsStore resolves defaults, overrides and the key.
type settingsStore struct {
	database     *db.DB
	keys         KeyStore
	defaults     Defaults
	defaultModel func(kind string) string
}

func (s *settingsStore) loadOverrides(ctx context.Context) (overrides, error) {
	var saved overrides
	if s.database == nil {
		return saved, nil
	}
	raw, err := s.database.Setting(ctx, settingsKey, "")
	if err != nil || raw == "" {
		return saved, err
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		// A row somebody broke by hand must not take the copilot down with
		// it; the defaults still describe a working setup.
		return overrides{}, nil
	}
	return saved, nil
}

// effective merges the defaults with the overrides. A model or base URL from
// the configuration only applies to the provider it was configured for: after
// an administrator switches vendor, a Claude model name sent to an OpenAI
// endpoint would only fail.
func (s *settingsStore) effective(saved overrides) Settings {
	settings := Settings{
		Provider:    firstNonEmpty(saved.Provider, s.defaults.Provider, "anthropic"),
		Effort:      firstNonEmpty(saved.Effort, s.defaults.Effort, "low"),
		AllowBypass: s.defaults.AllowBypass,
	}
	sameVendor := settings.Provider == s.defaults.Provider
	settings.Model = saved.Model
	if settings.Model == "" && sameVendor {
		settings.Model = s.defaults.Model
	}
	if settings.Model == "" && s.defaultModel != nil {
		settings.Model = s.defaultModel(settings.Provider)
	}
	settings.BaseURL = saved.BaseURL
	if settings.BaseURL == "" && sameVendor {
		settings.BaseURL = s.defaults.BaseURL
	}
	if saved.AllowBypass != nil {
		settings.AllowBypass = *saved.AllowBypass
	}
	return settings
}

// resolve returns the effective settings and the API key with its source.
func (s *settingsStore) resolve(ctx context.Context) (Settings, string, string, error) {
	saved, err := s.loadOverrides(ctx)
	if err != nil {
		return Settings{}, "", "", err
	}
	settings := s.effective(saved)
	if s.keys != nil {
		stored, err := s.keys.Load()
		if err != nil {
			return settings, "", "", fmt.Errorf("copilot API key: %w", err)
		}
		if key := strings.TrimSpace(string(stored)); key != "" {
			return settings, key, "stored", nil
		}
	}
	if key := strings.TrimSpace(s.defaults.APIKey); key != "" {
		return settings, key, "environment", nil
	}
	if key := strings.TrimSpace(s.defaults.VendorKeys[settings.Provider]); key != "" {
		return settings, key, "environment", nil
	}
	return settings, "", "", nil
}

func (s *settingsStore) view(ctx context.Context) (SettingsView, error) {
	settings, key, source, err := s.resolve(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	return SettingsView{
		Settings: settings, Enabled: s.defaults.Enabled,
		HasAPIKey: key != "", APIKeySource: source,
	}, nil
}

func (s *settingsStore) update(ctx context.Context, update SettingsUpdate) error {
	if s.database == nil {
		return errors.New("copilot settings need the workspace database")
	}
	saved, err := s.loadOverrides(ctx)
	if err != nil {
		return err
	}
	if update.Provider != nil {
		value := strings.TrimSpace(*update.Provider)
		if value != "" && value != "anthropic" && value != "openai" {
			return fmt.Errorf("%w: provider must be anthropic or openai", ErrInvalidSettings)
		}
		saved.Provider = value
	}
	if update.Model != nil {
		value := strings.TrimSpace(*update.Model)
		if len(value) > maxModelBytes || hasControl(value) {
			return fmt.Errorf("%w: model is not a model name", ErrInvalidSettings)
		}
		saved.Model = value
	}
	if update.BaseURL != nil {
		value := strings.TrimSpace(*update.BaseURL)
		if value != "" {
			parsed, err := url.Parse(value)
			if err != nil || len(value) > maxBaseURLBytes ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return fmt.Errorf("%w: baseUrl must be an http or https URL", ErrInvalidSettings)
			}
		}
		saved.BaseURL = value
	}
	if update.Effort != nil {
		value := strings.TrimSpace(*update.Effort)
		switch value {
		case "", "low", "medium", "high":
		default:
			return fmt.Errorf("%w: effort must be low, medium or high", ErrInvalidSettings)
		}
		saved.Effort = value
	}
	if update.AllowBypass != nil {
		value := *update.AllowBypass
		saved.AllowBypass = &value
	}
	if update.APIKey != nil {
		key := strings.TrimSpace(*update.APIKey)
		if len(key) > maxAPIKeyBytes || hasControl(key) {
			return fmt.Errorf("%w: apiKey is not an API key", ErrInvalidSettings)
		}
		if s.keys == nil {
			return errors.New("no secrets store is configured for the copilot API key")
		}
		// The key is written before the settings row: a provider switch that
		// lands without its key would leave the copilot calling the new
		// vendor with the old vendor's credential.
		if key == "" {
			err = s.keys.Clear()
		} else {
			err = s.keys.Save([]byte(key))
		}
		if err != nil {
			return fmt.Errorf("copilot API key: %w", err)
		}
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return s.database.SetSetting(ctx, settingsKey, string(encoded))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
