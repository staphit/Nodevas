package testpg

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Column is one column of a public table, as the catalog describes it.
type Column struct {
	Name     string
	Type     string // information_schema data_type
	Nullable bool
	Default  bool
	Identity string // "ALWAYS", "BY DEFAULT" or ""
	Allowed  []string
	RefTable string
	RefCol   string
}

// Table is a public base table and its columns in ordinal order.
type Table struct {
	Name    string
	Columns []Column
	Parents []string
}

var checkInList = regexp.MustCompile(`\((\w+)\s*=\s*ANY\s*\(\(?ARRAY\[(.*?)\]`)
var quoted = regexp.MustCompile(`'((?:[^']|'')*)'`)

// Tables reads every public base table except schema_migrations, parents
// before children (foreign keys), then by name.
func Tables(t testing.TB, handle *sql.DB) []Table {
	t.Helper()
	byName := map[string]*Table{}
	rows, err := handle.Query(`
SELECT c.table_name, c.column_name, c.data_type, c.is_nullable = 'YES',
       c.column_default IS NOT NULL, coalesce(c.identity_generation, '')
FROM information_schema.columns c
JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE' AND c.table_name <> 'schema_migrations'
ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	for rows.Next() {
		var table string
		var column Column
		if err := rows.Scan(&table, &column.Name, &column.Type, &column.Nullable, &column.Default, &column.Identity); err != nil {
			t.Fatal(err)
		}
		if byName[table] == nil {
			byName[table] = &Table{Name: table}
		}
		byName[table].Columns = append(byName[table].Columns, column)
	}
	rows.Close()

	constraints, err := handle.Query(`
SELECT cl.relname, co.contype::text, pg_get_constraintdef(co.oid),
       coalesce(rc.relname, ''), coalesce((SELECT attname FROM pg_attribute WHERE attrelid = co.confrelid AND attnum = co.confkey[1]), ''),
       coalesce((SELECT attname FROM pg_attribute WHERE attrelid = co.conrelid AND attnum = co.conkey[1]), '')
FROM pg_constraint co
JOIN pg_class cl ON cl.oid = co.conrelid
JOIN pg_namespace n ON n.oid = cl.relnamespace AND n.nspname = 'public'
LEFT JOIN pg_class rc ON rc.oid = co.confrelid
WHERE co.contype IN ('c', 'f')`)
	if err != nil {
		t.Fatalf("constraints: %v", err)
	}
	for constraints.Next() {
		var table, kind, def, refTable, refCol, col string
		if err := constraints.Scan(&table, &kind, &def, &refTable, &refCol, &col); err != nil {
			t.Fatal(err)
		}
		entry := byName[table]
		if entry == nil {
			continue
		}
		for i := range entry.Columns {
			column := &entry.Columns[i]
			switch kind {
			case "f":
				if column.Name == col {
					column.RefTable, column.RefCol = refTable, refCol
					entry.Parents = append(entry.Parents, refTable)
				}
			case "c":
				match := checkInList.FindStringSubmatch(def)
				if match != nil && match[1] == column.Name {
					for _, value := range quoted.FindAllStringSubmatch(match[2], -1) {
						column.Allowed = append(column.Allowed, strings.ReplaceAll(value[1], "''", "'"))
					}
				}
			}
		}
	}
	constraints.Close()

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	var ordered []Table
	done := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if done[name] || byName[name] == nil {
			return
		}
		done[name] = true
		for _, parent := range byName[name].Parents {
			if parent != name {
				visit(parent)
			}
		}
		ordered = append(ordered, *byName[name])
	}
	for _, name := range names {
		visit(name)
	}
	return ordered
}

// SeedEveryTable inserts n rows into every public table, with foreign keys
// pointing at the parents' rows and CHECK ... IN columns taking an allowed
// value, and returns the row counts it inserted.
func SeedEveryTable(t testing.TB, handle *sql.DB, n int) map[string]int {
	t.Helper()
	inserted := map[string][]map[string]any{}
	counts := map[string]int{}
	for _, table := range Tables(t, handle) {
		for i := 0; i < n; i++ {
			row := map[string]any{}
			var names, marks []string
			var args []any
			for _, column := range table.Columns {
				if column.Identity == "ALWAYS" {
					continue
				}
				var value any
				switch {
				case column.RefTable != "":
					parent := inserted[column.RefTable]
					if len(parent) == 0 {
						t.Fatalf("%s.%s references empty %s", table.Name, column.Name, column.RefTable)
					}
					value = parent[i%len(parent)][column.RefCol]
					if value == nil {
						var got any
						_ = handle.QueryRow(fmt.Sprintf(`SELECT %s FROM %s ORDER BY 1 OFFSET %d LIMIT 1`,
							column.RefCol, column.RefTable, i%len(parent))).Scan(&got)
						value = got
					}
				case len(column.Allowed) > 0:
					value = column.Allowed[(i+1)%len(column.Allowed)]
				default:
					value = synth(table.Name, column, i)
				}
				row[column.Name] = value
				names = append(names, `"`+column.Name+`"`)
				marks = append(marks, fmt.Sprintf("$%d", len(args)+1))
				args = append(args, value)
			}
			statement := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, table.Name,
				strings.Join(names, ", "), strings.Join(marks, ", "))
			if _, err := handle.Exec(statement, args...); err != nil {
				t.Fatalf("seed %s: %v", table.Name, err)
			}
			inserted[table.Name] = append(inserted[table.Name], row)
			counts[table.Name]++
		}
	}
	return counts
}

func synth(table string, column Column, i int) any {
	switch column.Type {
	case "integer", "bigint", "smallint":
		return int64(i + 1)
	case "double precision", "real", "numeric":
		return float64(i) + 0.5
	case "boolean":
		return i%2 == 0
	case "json", "jsonb":
		return fmt.Sprintf(`{"row": %d}`, i)
	}
	return fmt.Sprintf("%s.%s.%d", table, column.Name, i)
}

// Counts returns every public table's row count.
func Counts(t testing.TB, handle *sql.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range Tables(t, handle) {
		var n int
		if err := handle.QueryRow(`SELECT count(*) FROM ` + table.Name).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table.Name, err)
		}
		out[table.Name] = n
	}
	return out
}
