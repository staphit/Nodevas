package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// copiedTable is one table CopyToPostgres moves. Every column listed is TEXT on
// both engines; generated keys (audit_events.id, accounts.rowid) are left for
// the target to assign, in the source's order so they keep meaning the same
// thing.
type copiedTable struct {
	name    string
	columns []string
	order   string
}

// copiedTables is everything operational. search_documents and search_fts are
// absent: the index stays in the SQLite file (see DB.Local).
var copiedTables = []copiedTable{
	{"accounts", []string{"id", "name", "role", "password_hash", "pin_hash", "email", "created_at"}, "rowid"},
	{"settings", []string{"key", "value", "updated_at"}, "key"},
	{"sessions", []string{"token_hash", "user_id", "user_name", "user_role", "revision", "created_at", "expires_at"}, "created_at"},
	{"audit_events", []string{"at", "project", "actor_id", "actor_name", "action", "target", "client_ip", "detail"}, "id"},
}

// CopyToPostgres copies a workspace's SQLite data into a PostgreSQL database
// in one transaction, and returns how many rows each table received.
//
// A target that already holds rows is refused unless replace is set, in which
// case those rows are deleted first: copying on top of a live server would
// merge two account lists, and which "admin" wins is not something to settle
// by accident.
func CopyToPostgres(ctx context.Context, src, dst *DB, replace bool) (map[string]int, error) {
	if src.Backend() != SQLite || dst.Backend() != Postgres {
		return nil, errors.New("copy goes from a SQLite workspace database to a PostgreSQL one")
	}
	counts := map[string]int{}
	err := dst.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range copiedTables {
			var existing int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table.name).Scan(&existing); err != nil {
				return fmt.Errorf("count %s: %w", table.name, err)
			}
			if existing > 0 && !replace {
				return fmt.Errorf("the PostgreSQL database already has %d %s rows; pass --replace to overwrite them",
					existing, table.name)
			}
		}
		if replace {
			names := make([]string, len(copiedTables))
			for i, table := range copiedTables {
				names[i] = table.name
			}
			if _, err := tx.ExecContext(ctx,
				`TRUNCATE `+strings.Join(names, ", ")+` RESTART IDENTITY`); err != nil {
				return fmt.Errorf("clear target: %w", err)
			}
		}
		for _, table := range copiedTables {
			n, err := copyTable(ctx, src, tx, table)
			if err != nil {
				return fmt.Errorf("copy %s: %w", table.name, err)
			}
			counts[table.name] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

func copyTable(ctx context.Context, src *DB, tx *sql.Tx, table copiedTable) (int, error) {
	columns := strings.Join(table.columns, ", ")
	rows, err := src.QueryContext(ctx,
		`SELECT `+columns+` FROM `+table.name+` ORDER BY `+table.order)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	insert := `INSERT INTO ` + table.name + ` (` + columns + `) VALUES (` +
		strings.TrimSuffix(strings.Repeat("?, ", len(table.columns)), ", ") + `)`
	values := make([]string, len(table.columns))
	targets := make([]any, len(table.columns))
	for i := range values {
		targets[i] = &values[i]
	}
	args := make([]any, len(values))
	n := 0
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return n, err
		}
		for i, value := range values {
			// PostgreSQL text cannot hold NUL, which SQLite text can.
			args[i] = strings.ReplaceAll(value, "\x00", "")
		}
		if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}
