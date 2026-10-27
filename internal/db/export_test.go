package db

import (
	"context"
	"database/sql"
	"fmt"
)

// PGMigrationNames is the PostgreSQL chain's step names, in order.
func PGMigrationNames() []string {
	names := make([]string, 0, len(pgMigrations))
	for _, step := range pgMigrations {
		names = append(names, step.name)
	}
	return names
}

// ApplyPGMigrationsThrough brings an empty database to the schema it had at
// step last, recording each step as migrate does: the shape of a server that
// last ran an older release.
func ApplyPGMigrationsThrough(ctx context.Context, handle *sql.DB, last string) error {
	if _, err := handle.ExecContext(ctx, pgSchemaMigrations); err != nil {
		return err
	}
	for _, step := range pgMigrations {
		if _, err := handle.ExecContext(ctx, step.stmt); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		if _, err := handle.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES ($1, $2)`, step.name, Now()); err != nil {
			return err
		}
		if step.name == last {
			return nil
		}
	}
	return fmt.Errorf("no step %s", last)
}
