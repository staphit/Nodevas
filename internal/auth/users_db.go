// Account-table initialization and administrator repair.

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"nodevas/internal/identity"
)

// EnsureAdmin promotes the oldest account when the table has no administrator.
// NewUserStore and callers that wrap an existing database call it once during
// startup so a hand-edited account table cannot lock everyone out.
func (u *UserStore) EnsureAdmin() error {
	if !u.ready() {
		return nil
	}
	ctx := context.Background()
	return u.database.Tx(ctx, func(tx *sql.Tx) error {
		return ensureAdmin(ctx, tx)
	})
}

// ensureAdmin repairs a table with no administrator without changing account
// data or importing any external representation.
func ensureAdmin(ctx context.Context, tx *sql.Tx) error {
	var admins int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE role = ?`, string(identity.RoleAdmin)).Scan(&admins); err != nil {
		return fmt.Errorf("count administrators: %w", err)
	}
	if admins > 0 {
		return nil
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM accounts ORDER BY rowid LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read accounts: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET role = ? WHERE id = ?`, string(identity.RoleAdmin), id); err != nil {
		return fmt.Errorf("promote administrator: %w", err)
	}
	return nil
}
