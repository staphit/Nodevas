package db

import "testing"

func TestRebindNumbersPlaceholdersOutsideQuotesAndComments(t *testing.T) {
	cases := map[string]string{
		`SELECT 1`:                              `SELECT 1`,
		`SELECT a FROM t WHERE b = ? AND c = ?`: `SELECT a FROM t WHERE b = $1 AND c = $2`,
		`SELECT '?' , "?" FROM t WHERE x = ?`:   `SELECT '?' , "?" FROM t WHERE x = $1`,
		`SELECT 'it''s ?' WHERE x = ?`:          `SELECT 'it''s ?' WHERE x = $1`,
		"-- why?\nSELECT ? -- and?\n, ?":        "-- why?\nSELECT $1 -- and?\n, $2",
		`action LIKE ? ESCAPE '\'`:              `action LIKE $1 ESCAPE '\'`,
		`LIMIT ? OFFSET ?`:                      `LIMIT $1 OFFSET $2`,
		`SELECT 'unterminated ?`:                `SELECT 'unterminated ?`,
	}
	for in, want := range cases {
		if got := rebind(in); got != want {
			t.Errorf("rebind(%q) = %q, want %q", in, got, want)
		}
	}
}
