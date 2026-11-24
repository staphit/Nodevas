package db_test

// The PostgreSQL-only database: one engine, one migration chain, no SQLite
// file and no fallback. These run on internal/testpg (NODEVAS_TEST_DATABASE_URL
// or the embedded server).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"nodevas/internal/db"
	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

func rawHandle(t *testing.T, url string) *sql.DB {
	t.Helper()
	handle, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	return handle
}

func appliedNames(t *testing.T, handle *sql.DB) []string {
	t.Helper()
	rows, err := handle.Query(`SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func TestUnreachableServerFailsAfterWait(t *testing.T) {
	root := t.TempDir()
	t.Setenv(db.URLEnv, "postgres://nodevas:hunter2-secret@127.0.0.1:1/nodevas?sslmode=disable&connect_timeout=1")
	t.Setenv(db.URLFileEnv, "")
	t.Setenv(db.WaitEnv, "0s")
	started := time.Now()
	handle, err := db.OpenRoot(root)
	if err == nil {
		handle.Close()
		t.Fatal("an unreachable server opened: something fell back")
	}
	if !errors.Is(err, db.ErrUnreachable) {
		t.Fatalf("OpenRoot = %v, want ErrUnreachable", err)
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Fatal("the error echoes the password")
	}
	if time.Since(started) > 20*time.Second {
		t.Fatalf("a zero wait took %s", time.Since(started))
	}
	if _, err := os.Stat(db.PathFor(root)); !os.IsNotExist(err) {
		t.Fatalf("a SQLite file was created at %s: %v", db.PathFor(root), err)
	}
}

// Without a URL, OpenRoot would start (or attach to) a real embedded cluster;
// under `go test` that is always a mistake, and the message says what to do.
func TestOpenRootWithoutURLInTestsRefuses(t *testing.T) {
	t.Setenv(db.URLEnv, "")
	t.Setenv(db.URLFileEnv, "")
	handle, err := db.OpenRoot(t.TempDir())
	if err == nil {
		handle.Close()
		t.Fatal("OpenRoot started a database under go test")
	}
	if !strings.Contains(err.Error(), "testpg") {
		t.Fatalf("OpenRoot = %v; the refusal should point at internal/testpg", err)
	}
}

func TestMigrationNamesUniqueAndOrdered(t *testing.T) {
	names := db.PGMigrationNames()
	shape := regexp.MustCompile(`^\d{4}_[a-z0-9_]+$`)
	seen := map[string]bool{}
	for i, name := range names {
		if !shape.MatchString(name) {
			t.Errorf("step %q is not NNNN_name", name)
		}
		// 0036 is the one number two branches each took (the agent
		// workspace's run_workspace, the theme packs' theme_packs) before
		// they met; databases record steps by name, so both names stay.
		if seen[name[:4]] && name[:4] != "0036" {
			t.Errorf("number %s used twice", name[:4])
		}
		seen[name[:4]] = true
		if i > 0 && names[i-1] >= name {
			t.Errorf("%s comes after %s", name, names[i-1])
		}
	}
	// The NAS records steps by name; these are the ones it already has, and
	// they must never be renamed or renumbered.
	for _, kept := range []string{"0001_accounts", "0002_audit", "0003_settings", "0005_sessions", "0024_run_handoff", "0025_agent_roles"} {
		if !slices.Contains(names, kept) {
			t.Errorf("step %s is gone: the NAS database has it recorded", kept)
		}
	}
	// The newest steps, in order: a step is only ever appended.
	want := []string{
		"0026_search_index", "0027_legacy_imports", "0028_agent_analytics", "0029_search_gin_fastupdate",
		"0030_agent_ask_kinds", "0031_agent_ask_run_key", "0032_agent_ask_input_truncated",
		"0033_permission_prompt_action_details", "0034_agent_message_hub_notice", "0035_run_changes_patch_pruned",
		"0036_run_workspace", "0036_theme_packs", "0037_global_runs", "0038_run_origin", "0039_account_preferences",
		"0040_copilot_entry_data", "0041_plan_run_drafts", "0042_run_failure_code",
		"0043_run_continue", "0044_run_groups", "0045_run_coordination",
	}
	if n := len(names); n < len(want) || !slices.Equal(names[n-len(want):], want) {
		t.Fatalf("chain ends %v, want ... %v", names[max(0, n-len(want)-1):], want)
	}
}

// The NAS database last ran master (through 0024). Upgrading must apply the
// rest in place and keep every row.
func TestUpgradeInPlaceFrom0024(t *testing.T) {
	ctx := context.Background()
	url := testpg.NewEmptyURL(t)
	handle := rawHandle(t, url)
	if err := db.ApplyPGMigrationsThrough(ctx, handle, "0024_run_handoff"); err != nil {
		t.Fatalf("apply through 0024: %v", err)
	}
	seeded := testpg.SeedEveryTable(t, handle, 3)
	if len(seeded) < 25 {
		t.Fatalf("seeded only %d tables", len(seeded))
	}

	upgraded, err := db.OpenPostgres(url, 0)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	upgraded.Close()

	got := appliedNames(t, handle)
	want := db.PGMigrationNames()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("schema_migrations = %v\nwant %v", got, want)
	}
	for _, name := range []string{"0025_agent_roles", "0026_search_index", "0027_legacy_imports"} {
		if !slices.Contains(got, name) {
			t.Errorf("%s was not applied in place", name)
		}
	}
	counts := testpg.Counts(t, handle)
	for table, n := range seeded {
		if counts[table] != n {
			t.Errorf("%s has %d rows after the upgrade, had %d", table, counts[table], n)
		}
	}
}

type schemaShape struct {
	columns []string
	indexes []string
}

func shape(t *testing.T, handle *sql.DB) schemaShape {
	t.Helper()
	var out schemaShape
	collect := func(query string, into *[]string) {
		rows, err := handle.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			*into = append(*into, line)
		}
	}
	collect(`SELECT table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' ||
	                coalesce(column_default, '') || ' ' || coalesce(collation_name, '')
	         FROM information_schema.columns WHERE table_schema = 'public' ORDER BY 1`, &out.columns)
	collect(`SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' ORDER BY 1`, &out.indexes)
	return out
}

func TestFreshSchemaEqualsUpgradedSchema(t *testing.T) {
	ctx := context.Background()
	freshURL := testpg.NewEmptyURL(t)
	fresh, err := db.OpenPostgres(freshURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Close()

	upgradedURL := testpg.NewEmptyURL(t)
	handle := rawHandle(t, upgradedURL)
	if err := db.ApplyPGMigrationsThrough(ctx, handle, "0024_run_handoff"); err != nil {
		t.Fatal(err)
	}
	upgraded, err := db.OpenPostgres(upgradedURL, 0)
	if err != nil {
		t.Fatal(err)
	}
	upgraded.Close()

	a, b := shape(t, rawHandle(t, freshURL)), shape(t, handle)
	if strings.Join(a.columns, "\n") != strings.Join(b.columns, "\n") {
		t.Errorf("columns differ:\nfresh:\n%s\nupgraded:\n%s", strings.Join(a.columns, "\n"), strings.Join(b.columns, "\n"))
	}
	if strings.Join(a.indexes, "\n") != strings.Join(b.indexes, "\n") {
		t.Errorf("indexes differ:\nfresh:\n%s\nupgraded:\n%s", strings.Join(a.indexes, "\n"), strings.Join(b.indexes, "\n"))
	}
	for _, table := range []string{"search_documents", "legacy_imports"} {
		if !slices.ContainsFunc(a.columns, func(line string) bool { return strings.HasPrefix(line, table+".") }) {
			t.Errorf("fresh schema has no %s", table)
		}
	}
}

func TestConcurrentMigrateIsSerialized(t *testing.T) {
	url := testpg.NewEmptyURL(t)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle, err := db.OpenPostgres(url, 0)
			if err == nil {
				handle.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("open %d: %v", i, err)
		}
	}
	if got := appliedNames(t, rawHandle(t, url)); len(got) != len(db.PGMigrationNames()) {
		t.Fatalf("%d steps recorded, chain has %d", len(got), len(db.PGMigrationNames()))
	}
}

func TestReadPoolCannotWrite(t *testing.T) {
	database := testpg.New(t)
	if _, err := database.Reader().Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', 'now')`); err == nil {
		t.Fatal("the read pool wrote a row")
	}
}

// Search reindexing has its own small pool: a write transaction held open on
// the main writer (an audit append, a session touch) must not stall it.
func TestSearchPoolDoesNotBlockMainWriter(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('held', 'v', 'now')`); err != nil {
		t.Fatal(err)
	}
	search := database.Search()
	if search == database.Writer() {
		t.Fatal("Search() is the main writer")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := search.ExecContext(waitCtx, `SELECT 1`); err != nil {
		t.Fatalf("the search pool waited on the main writer: %v", err)
	}
	stats := search.Stats()
	if stats.MaxOpenConnections != 2 {
		t.Fatalf("search pool max open = %d, want 2", stats.MaxOpenConnections)
	}
}

func TestIsUniqueViolationNamesConstraint(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	insert := func(name string) error {
		_, err := database.ExecContext(ctx,
			`INSERT INTO accounts (id, name, role, created_at) VALUES (?, ?, 'member', ?)`,
			"id-"+name, name, db.Now())
		return err
	}
	if err := insert("Alice"); err != nil {
		t.Fatal(err)
	}
	err := insert("ALICE")
	if !db.IsUniqueViolation(err, "") || !db.IsUniqueViolation(err, "accounts_name_nocase") {
		t.Fatalf("IsUniqueViolation(%v) = false", err)
	}
	if db.IsUniqueViolation(err, "accounts_email_nocase") {
		t.Fatal("matched the wrong constraint")
	}
	// SQLite's message shape is no longer recognised: there is no SQLite.
	if db.IsUniqueViolation(fmt.Errorf("UNIQUE constraint failed: accounts.email"), "") {
		t.Fatal("IsUniqueViolation still matches SQLite error text")
	}
	if db.IsUniqueViolation(nil, "") {
		t.Fatal("nil is a violation")
	}
}

// ---------- ported from the SQLite suite ----------

func TestSchemaObjectsExist(t *testing.T) {
	database := testpg.New(t)
	rows, err := database.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		tables[name] = true
	}
	for _, want := range []string{
		"schema_migrations", "accounts", "audit_events", "settings", "sessions", "copilot_conversations",
		"runs", "run_events", "project_members", "run_role_settings", "search_documents", "legacy_imports",
	} {
		if !tables[want] {
			t.Errorf("missing table %s", want)
		}
	}
	for _, gone := range []string{"search_fts", "search_content"} {
		if tables[gone] {
			t.Errorf("SQLite-only table %s exists", gone)
		}
	}
}

// 0036_run_workspace: a run's workspace outcome, and its workspace actions
// with at most one waiting per run.
func TestRunWorkspaceSchema(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at)
VALUES ('r1', 'p', 'n', 'rn', 'o', 'claude', 'repo', 'succeeded', 'o', '2026-11-13T00:00:00.000000Z')`); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	var workspace, commit string
	if err := database.QueryRowContext(ctx, `SELECT workspace, workspace_commit FROM runs WHERE id = 'r1'`).Scan(&workspace, &commit); err != nil ||
		workspace != "" || commit != "" {
		t.Fatalf("defaults = %q %q, %v", workspace, commit, err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE runs SET workspace = 'gone' WHERE id = 'r1'`); err == nil {
		t.Fatal("workspace 'gone' accepted")
	}
	insert := func(id, action, status string) error {
		_, err := database.ExecContext(ctx, `INSERT INTO run_workspace_actions (id, run_id, action, status, requested_by, created_at)
VALUES (?, 'r1', ?, ?, 'o', '2026-11-13T00:00:00.000000Z')`, id, action, status)
		return err
	}
	if err := insert("a1", "merge_local", "pending"); err != nil {
		t.Fatal(err)
	}
	if err := insert("a2", "discard", "pending"); !db.IsUniqueViolation(err, "") {
		t.Fatalf("a second pending action: %v", err)
	}
	if err := insert("a3", "discard", "done"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]string{{"push", "pending"}, {"discard", "maybe"}} {
		if err := insert("a4", bad[0], bad[1]); err == nil {
			t.Errorf("action %q status %q accepted", bad[0], bad[1])
		}
	}
}

// 0037_global_runs: a global run has no project, node, pipeline, retry,
// kind or role, and a valid branch slug; a node's run is untouched.
func TestGlobalRunsSchema(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	insert := func(id, project, node, slug, extra string, global bool) error {
		_, err := database.ExecContext(ctx, `INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, is_global, branch_slug, no_pr, kind)
VALUES (?, ?, ?, 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-14T00:00:00.000000Z', ?, ?, TRUE, ?)`, id, project, node, global, slug, extra)
		return err
	}
	if err := insert("r1", "p", "n", "", "", false); err != nil {
		t.Fatalf("a node's run: %v", err)
	}
	var global, noPR bool
	var slug string
	if err := database.QueryRowContext(ctx, `SELECT is_global, branch_slug, no_pr FROM runs WHERE id = 'r1'`).Scan(&global, &slug, &noPR); err != nil ||
		global || slug != "" || !noPR {
		t.Fatalf("node run = %v %q %v, %v", global, slug, noPR, err)
	}
	if err := insert("g1", "", "", "fix-it", "", true); err != nil {
		t.Fatalf("a global run: %v", err)
	}
	for name, bad := range map[string][4]string{
		"project":   {"p", "", "fix-it", ""},
		"node":      {"", "n", "fix-it", ""},
		"no slug":   {"", "", "", ""},
		"kind":      {"", "", "fix-it", "review"},
		"bad slug":  {"", "", "Fix_It", ""},
		"dash slug": {"", "", "fix--it", ""},
		"long slug": {"", "", strings.Repeat("a", 41), ""},
	} {
		if err := insert("g-"+strings.ReplaceAll(name, " ", "-"), bad[0], bad[1], bad[2], bad[3], true); err == nil {
			t.Errorf("global run with %s accepted", name)
		}
	}
	if err := insert("r2", "p", "n", "Bad", "", false); err == nil {
		t.Error("a node run with an invalid slug accepted")
	}
	for _, column := range []string{"pipeline_id", "retry_of", "role"} {
		if _, err := database.ExecContext(ctx, `UPDATE runs SET `+column+` = 'x' WHERE id = 'g1'`); err == nil {
			t.Errorf("global run with %s accepted", column)
		}
	}
}

// 0038_run_origin: a run's origin is one of the known starters or unknown
// (rows from before, which a runner treats as automatic), and a decline
// code is free text the dispatcher clamps.
func TestRunOriginSchema(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `INSERT INTO runs (id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at)
VALUES ('r1', 'p', 'n', 'rn', 'o', 'claude', 'repo', 'declined', 'o', '2026-11-14T00:00:00.000000Z')`); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	var origin, code string
	if err := database.QueryRowContext(ctx, `SELECT origin, decline_code FROM runs WHERE id = 'r1'`).Scan(&origin, &code); err != nil ||
		origin != "" || code != "" {
		t.Fatalf("defaults = %q %q, %v", origin, code, err)
	}
	for _, good := range []string{"person", "autopilot", "pipeline", "handoff", ""} {
		if _, err := database.ExecContext(ctx, `UPDATE runs SET origin = ? WHERE id = 'r1'`, good); err != nil {
			t.Errorf("origin %q refused: %v", good, err)
		}
	}
	for _, bad := range []string{"robot", "Person"} {
		if _, err := database.ExecContext(ctx, `UPDATE runs SET origin = ? WHERE id = 'r1'`, bad); err == nil {
			t.Errorf("origin %q accepted", bad)
		}
	}
	if _, err := database.ExecContext(ctx, `UPDATE runs SET decline_code = 'declined_in_app' WHERE id = 'r1'`); err != nil {
		t.Fatalf("decline code: %v", err)
	}
}

// 0039_account_preferences: one value per account and key, keys compared
// byte by byte, and the same key kept apart for each account.
func TestAccountPreferencesSchema(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	insert := func(account, key string) error {
		_, err := database.ExecContext(ctx, `INSERT INTO account_preferences (account_id, key, value, updated_at)
VALUES (?, ?, '"dark"', '2026-11-14T00:00:00.000000Z')`, account, key)
		return err
	}
	for _, row := range [][2]string{{"local", "theme"}, {"local", "Theme"}, {"acct-1", "theme"}} {
		if err := insert(row[0], row[1]); err != nil {
			t.Fatalf("insert %v: %v", row, err)
		}
	}
	if err := insert("local", "theme"); !db.IsUniqueViolation(err, "") {
		t.Fatalf("a second value for one account's key: %v", err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	if got, err := database.Setting(ctx, "a.b", "fallback"); err != nil || got != "fallback" {
		t.Fatalf("unset = %q, %v", got, err)
	}
	for _, value := range []string{"one", "two"} {
		if err := database.SetSetting(ctx, "a.b", value); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := database.Setting(ctx, "a.b", ""); err != nil || got != "two" {
		t.Fatalf("Setting = %q, %v", got, err)
	}
	var n int
	_ = database.QueryRow(`SELECT count(*) FROM settings WHERE key = 'a.b'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d rows for one key", n)
	}
}

func TestAccountRoleCheckAndCaseInsensitiveEmail(t *testing.T) {
	database := testpg.New(t)
	if _, err := database.Exec(`INSERT INTO accounts (id, name, role, created_at) VALUES ('x', 'x', 'root', 'now')`); err == nil {
		t.Fatal("role 'root' accepted")
	}
	if _, err := database.Exec(`INSERT INTO accounts (id, name, role, email, created_at) VALUES ('a', 'a', 'admin', 'Ann@Example.com', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO accounts (id, name, role, email, created_at) VALUES ('b', 'b', 'member', 'ann@example.COM', 'now')`); !db.IsUniqueViolation(err, "") {
		t.Fatalf("a second account with the same email in another case: %v", err)
	}
	var id string
	if err := database.QueryRow(`SELECT id FROM accounts WHERE email = ? COLLATE NOCASE`, "ANN@EXAMPLE.COM").Scan(&id); err != nil || id != "a" {
		t.Fatalf("case-insensitive lookup = %q, %v", id, err)
	}
}

func TestTxRollsBackOnErrorAndPanic(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	failed := errors.New("boom")
	err := database.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('e', 'v', 'now')`); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("Tx = %v", err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = database.Tx(ctx, func(tx *sql.Tx) error {
			_, _ = tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('p', 'v', 'now')`)
			panic("boom")
		})
	}()
	var n int
	_ = database.QueryRow(`SELECT count(*) FROM settings WHERE key IN ('e', 'p')`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows survived a rolled-back transaction", n)
	}
	// The single writer is usable afterwards.
	if err := database.SetSetting(ctx, "after", "ok"); err != nil {
		t.Fatalf("writer after a panic: %v", err)
	}
}

func TestMigrationsAddContinueAndGroups(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	insertRun := func(id, status, startedAt, continueOf, workRoot, endReason string) error {
		_, err := database.ExecContext(ctx, `INSERT INTO runs
			(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at,
			 started_at, is_global, branch_slug, continue_of, work_root, view_only, reset_at, end_reason, push_refused, group_id)
			VALUES (?, '', '', 'rn', 'o', 'claude', 'repo', ?, 'o', '2026-11-18T00:00:00.000000Z',
			 ?, TRUE, 'fix-it', ?, ?, TRUE, '2026-11-18T01:00:00Z', ?, TRUE, 'g1')`,
			id, status, startedAt, continueOf, workRoot, endReason)
		return err
	}
	if err := insertRun("root1", "succeeded", "2026-11-18T00:00:01Z", "", "", "ended"); err != nil {
		t.Fatalf("insert root: %v", err)
	}
	var resetAt, endReason, groupID string
	var viewOnly, pushRefused bool
	if err := database.QueryRowContext(ctx,
		`SELECT view_only, reset_at, end_reason, push_refused, group_id FROM runs WHERE id = 'root1'`).
		Scan(&viewOnly, &resetAt, &endReason, &pushRefused, &groupID); err != nil ||
		!viewOnly || resetAt != "2026-11-18T01:00:00Z" || endReason != "ended" || !pushRefused || groupID != "g1" {
		t.Fatalf("read back = %v %q %q %v %q, %v", viewOnly, resetAt, endReason, pushRefused, groupID, err)
	}
	// Invalid end_reason rejected.
	if err := insertRun("bad-end", "succeeded", "", "", "", "timeout"); err == nil {
		t.Fatal("invalid end_reason accepted")
	}
	// Valid quiet / quiet_waiting accepted.
	if err := insertRun("q1", "succeeded", "", "", "", "quiet"); err != nil {
		t.Fatalf("quiet: %v", err)
	}
	if err := insertRun("q2", "succeeded", "", "", "", "quiet_waiting"); err != nil {
		t.Fatalf("quiet_waiting: %v", err)
	}
	// One live continuation per prior; withdrawn (cancelled or declined with started_at='') frees the slot.
	if err := insertRun("f-withdrawn-cancel", "cancelled", "", "root1", "root1", ""); err != nil {
		t.Fatalf("withdrawn cancel: %v", err)
	}
	if err := insertRun("f-withdrawn-decline", "declined", "", "root1", "root1", ""); err != nil {
		t.Fatalf("withdrawn decline: %v", err)
	}
	if err := insertRun("f-live", "queued", "", "root1", "root1", ""); err != nil {
		t.Fatalf("live follow-up: %v", err)
	}
	if err := insertRun("f-second", "queued", "", "root1", "root1", ""); !db.IsUniqueViolation(err, "runs_one_continuation") {
		t.Fatalf("second live continuation err = %v, want runs_one_continuation", err)
	}
	// run_groups table works.
	if _, err := database.ExecContext(ctx, `INSERT INTO run_groups
		(id, owner_id, runner_id, created_by, created_by_name, title, agent, model, no_pr, repo_count, held_until, created_at)
		VALUES ('g1', 'o', 'rn', 'o', 'ann', 'Upgrade deps', 'claude', 'opus', FALSE, 3, '2026-11-18T02:00:00Z', '2026-11-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert run_groups: %v", err)
	}
}

func TestMigrationsAddCoordination(t *testing.T) {
	database := testpg.New(t)
	ctx := context.Background()
	hexKey := "0123456789abcdef0123456789abcdef"

	// Layer 1 columns + checks.
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, after_run, busy_choice, repo_key)
		VALUES ('r1', 'p', 'n1', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:00Z', 'r0', 'queue', ?)`, hexKey); err != nil {
		t.Fatalf("insert layer 1 run: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE runs SET busy_choice = 'skip' WHERE id = 'r1'`); err == nil {
		t.Fatal("invalid busy_choice 'skip' accepted on runs")
	}
	if _, err := database.ExecContext(ctx, `UPDATE runs SET repo_key = 'not-hex' WHERE id = 'r1'`); err == nil {
		t.Fatal("invalid repo_key accepted")
	}

	// Layer 2 tables.
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at)
		VALUES ('r2', 'p', 'n1', 'rn', 'o', 'claude', 'repo', 'running', 'o', '2026-11-18T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_touched_files (run_id, path, add_only, at) VALUES ('r1', 'src/a.go', TRUE, '2026-11-18T00:00:02Z')`); err != nil {
		t.Fatalf("insert run_touched_files: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_overlaps (run_a, run_b, paths, path_count, strong_count, first_at, updated_at)
		VALUES ('r1', 'r2', '["src/a.go"]', 1, 1, '2026-11-18T00:00:02Z', '2026-11-18T00:00:02Z')`); err != nil {
		t.Fatalf("insert run_overlaps: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_overlaps (run_a, run_b, first_at, updated_at)
		VALUES ('r2', 'r1', '2026-11-18T00:00:02Z', '2026-11-18T00:00:02Z')`); err == nil {
		t.Fatal("run_overlaps accepted run_a >= run_b")
	}

	// Layer 3: run_contests + runs_contest_shape + runs_global_shape.
	if _, err := database.ExecContext(ctx, `INSERT INTO run_contests
		(id, owner_id, created_by, project, node_id, is_global, title, entry_count, state, created_at)
		VALUES ('c1', 'o', 'o', 'p', 'n1', FALSE, 'Compare', 2, 'running', '2026-11-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert node contest: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_contests
		(id, owner_id, created_by, project, node_id, is_global, title, entry_count, state, created_at)
		VALUES ('c-bad', 'o', 'o', 'p', 'n1', TRUE, 'Bad', 2, 'running', '2026-11-18T00:00:00Z')`); err == nil {
		t.Fatal("global contest with project/node accepted")
	}
	// Headless entry row (mode='') inserts; interactive entry refused; retry_of refused.
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, mode, kind, contest_id, contest_label)
		VALUES ('ca', 'p', 'n1', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:03Z', '', '', 'c1', 'A')`); err != nil {
		t.Fatalf("contest entry mode='': %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, mode, kind, contest_id, contest_label)
		VALUES ('cb-int', 'p', 'n1', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:04Z', 'interactive', '', 'c1', 'B')`); err == nil {
		t.Fatal("interactive contest entry accepted")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, mode, kind, retry_of, contest_id, contest_label)
		VALUES ('cb-retry', 'p', 'n1', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:04Z', '', '', 'ca', 'c1', 'B')`); err == nil {
		t.Fatal("contest entry with retry_of accepted")
	}
	// Reviewer row of kind 'compare' with label 'J' inserts (node and global).
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, mode, kind, contest_id, contest_label)
		VALUES ('cj', 'p', 'n1', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:05Z', '', 'compare', 'c1', 'J')`); err != nil {
		t.Fatalf("node compare reviewer: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, is_global, branch_slug, mode, kind, contest_id, contest_label)
		VALUES ('gcj', '', '', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:06Z', TRUE, 'cmp', '', 'compare', 'c2', 'J')`); err != nil {
		t.Fatalf("global compare reviewer with contest: %v", err)
	}
	// Global compare without contest_id refused by runs_global_shape.
	if _, err := database.ExecContext(ctx, `INSERT INTO runs
		(id, project, node_id, runner_id, owner_id, agent, repo_id, status, created_by, created_at, is_global, branch_slug, mode, kind)
		VALUES ('gc-bad', '', '', 'rn', 'o', 'claude', 'repo', 'queued', 'o', '2026-11-18T00:00:07Z', TRUE, 'cmp', '', 'compare')`); err == nil {
		t.Fatal("global compare without contest accepted")
	}

	// open_pr in run_workspace_actions action CHECK (and verify real constraint replaced 0036's inline check).
	var constraintCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint
		WHERE conrelid = 'run_workspace_actions'::regclass AND contype = 'c'
		  AND conname = 'run_workspace_actions_action_check'`).Scan(&constraintCount); err != nil || constraintCount != 1 {
		t.Fatalf("run_workspace_actions_action_check count = %d, %v", constraintCount, err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_workspace_actions
		(id, run_id, action, status, requested_by, created_at)
		VALUES ('wa-pr', 'ca', 'open_pr', 'pending', 'o', '2026-11-18T00:00:08Z')`); err != nil {
		t.Fatalf("insert open_pr workspace action: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO run_workspace_actions
		(id, run_id, action, status, requested_by, created_at)
		VALUES ('wa-bad', 'ca', 'force_push', 'done', 'o', '2026-11-18T00:00:09Z')`); err == nil {
		t.Fatal("invalid workspace action accepted")
	}
}
