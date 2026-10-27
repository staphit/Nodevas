package analytics

import (
	"context"
	"errors"

	"nodevas/internal/db"
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
)

// LoadSettings is owner's settings for project, the defaults when unset.
func LoadSettings(ctx context.Context, database *db.DB, ownerID, project string) (Settings, error) {
	// TODO(green): implement.
	return Settings{}, nil
}

// SaveSettings validates and stores owner's settings for s.Project.
func SaveSettings(ctx context.Context, database *db.DB, ownerID string, s Settings) error {
	// TODO(green): implement.
	return errors.New("not implemented")
}
