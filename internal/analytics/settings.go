package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nodevas/internal/db"
	"nodevas/internal/engine"
)

// Settings are an owner's analytics settings for one project ("" the
// owner's default).
type Settings struct {
	Project        string            `json:"project"`
	AutoRouting    bool              `json:"autoRouting"`
	ExplorationPct int               `json:"explorationPct"`
	RequiredTiers  map[string]string `json:"requiredTiers"`
}

// DefaultExplorationPct and MaxExplorationPct bound exploration.
const (
	DefaultExplorationPct = 10
	MaxExplorationPct     = 50
	// maxRequiredTiers caps the per-role overrides one row holds.
	maxRequiredTiers = 64
)

// ErrInvalid marks settings the caller can fix.
var ErrInvalid = errors.New("invalid argument")

// DefaultSettings are the settings with no row stored.
func DefaultSettings(project string) Settings {
	return Settings{Project: project, ExplorationPct: DefaultExplorationPct, RequiredTiers: map[string]string{}}
}

// LoadSettings is owner's settings for project, the defaults when unset:
// the project's row, else the owner's default row (project ""), else the
// built-in defaults.
func LoadSettings(ctx context.Context, database *db.DB, ownerID, project string) (Settings, error) {
	for _, p := range []string{project, ""} {
		s, found, err := loadSettingsRow(ctx, database, ownerID, p)
		if err != nil {
			return DefaultSettings(project), err
		}
		if found {
			s.Project = project
			return s, nil
		}
		if project == "" {
			break
		}
	}
	return DefaultSettings(project), nil
}

func loadSettingsRow(ctx context.Context, database *db.DB, ownerID, project string) (Settings, bool, error) {
	s := DefaultSettings(project)
	var pct int64
	var tiers string
	err := database.QueryRowContext(ctx, `
SELECT auto_routing, exploration_pct, required_tiers FROM analytics_settings WHERE owner_id = ? AND project = ?`,
		ownerID, project).Scan(&s.AutoRouting, &pct, &tiers)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	s.ExplorationPct = int(pct)
	if json.Unmarshal([]byte(tiers), &s.RequiredTiers) != nil || s.RequiredTiers == nil {
		s.RequiredTiers = map[string]string{}
	}
	return s, true, nil
}

// ValidateSettings checks settings: exploration 0 to 50, valid role ids and
// tier names.
func ValidateSettings(s Settings) error {
	if s.ExplorationPct < 0 || s.ExplorationPct > MaxExplorationPct {
		return fmt.Errorf("%w: explorationPct is 0 to %d", ErrInvalid, MaxExplorationPct)
	}
	if len(s.RequiredTiers) > maxRequiredTiers {
		return fmt.Errorf("%w: at most %d required tiers", ErrInvalid, maxRequiredTiers)
	}
	for role, tier := range s.RequiredTiers {
		if !engine.ValidRoleID(role) {
			return fmt.Errorf("%w: unknown role %q", ErrInvalid, role)
		}
		if TierRank(tier) < 0 {
			return fmt.Errorf("%w: tier %q is not senior, mid or probation", ErrInvalid, tier)
		}
	}
	return nil
}

// SaveSettings validates and stores owner's settings for s.Project.
func SaveSettings(ctx context.Context, database *db.DB, ownerID string, s Settings) error {
	if ownerID == "" {
		return fmt.Errorf("%w: no owner", ErrInvalid)
	}
	if err := ValidateSettings(s); err != nil {
		return err
	}
	tiers := s.RequiredTiers
	if tiers == nil {
		tiers = map[string]string{}
	}
	raw, err := json.Marshal(tiers)
	if err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, `
INSERT INTO analytics_settings (owner_id, project, auto_routing, exploration_pct, required_tiers, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (owner_id, project) DO UPDATE SET
    auto_routing = excluded.auto_routing, exploration_pct = excluded.exploration_pct,
    required_tiers = excluded.required_tiers, updated_at = excluded.updated_at`,
		ownerID, s.Project, s.AutoRouting, s.ExplorationPct, string(raw), formatStamp(time.Now()))
	return err
}
