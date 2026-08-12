// migrate_test.go — schema/migrationtest
// Real-Postgres integration tests for the golang-migrate-based migration
// runner (B-051). Each test creates its own throwaway database on the
// real Postgres server (the connecting user has CREATEDB -- confirmed,
// not assumed) so fresh-install and already-seeded-data scenarios can be
// tested against genuinely different starting states, rather than reusing
// one shared always-current schema the way this repo's other _pg_test.go
// files do for application-logic tests.
//
// Run: TEST_DATABASE_URL or POSTGRES_PASSWORD must point at a real,
// reachable Postgres server (same convention as the rest of this repo's
// real-Postgres tests). go test ./... from this directory.
package migrationtest

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
)

// latestMigrationVersion derives the expected final schema_migrations
// version from the actual *.up.sql files present in schema/migrations-v2,
// instead of a hardcoded number. This test suite has now broken CI twice
// for the identical preventable reason (B-055 added migration 3, B-058
// added migration 6, both times a hardcoded "want" version went stale) --
// deriving it from the same files golang-migrate itself reads means the
// two can never drift: whatever's actually on disk IS what "correct"
// means, by construction. Assumes sequential, gapless numbering (000001,
// 000002, ...), which is this repo's own established convention for
// migrations-v2 (confirmed by listing the directory, not assumed) -- a
// real gap or duplicate would make golang-migrate itself fail to apply
// migrations correctly, a different and more informative failure than a
// silently-wrong expected version here.
func latestMigrationVersion(t *testing.T) uint {
	t.Helper()
	abs, err := filepath.Abs("../migrations-v2")
	if err != nil {
		t.Fatalf("resolve migrations-v2 path: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(abs, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations-v2/*.up.sql: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no *.up.sql files found in schema/migrations-v2 -- migrationsSourceURL path likely wrong")
	}
	return uint(len(matches))
}

// adminConnString returns a plain postgres:// connection string to the
// real server's "postgres" maintenance database (used only to CREATE/DROP
// the throwaway per-test databases), and pgHost/pgUser/pgPass split out
// for building per-test pgx5:// migrate URLs. Mirrors the
// TEST_DATABASE_URL / POSTGRES_PASSWORD fallback convention already
// established elsewhere in this repo (e.g.
// eami-api/internal/api/tools_update_pg_test.go).
type pgConn struct {
	host, user, pass string
}

func testPgConn(t *testing.T) pgConn {
	t.Helper()
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" && os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL or POSTGRES_PASSWORD not set -- skipping real-Postgres migration tests")
	}
	// 127.0.0.1, not localhost -- this machine resolves localhost to ::1
	// first and Docker Desktop's IPv6 port-forwarding silently drops
	// traffic, a known quirk documented in CONTEXT.md.
	return pgConn{host: "127.0.0.1:5432", user: "eami_app", pass: pw}
}

func (c pgConn) adminURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s/postgres?sslmode=disable", c.user, c.pass, c.host)
}

// migrateURL returns a pgx5:// connection string (the scheme golang-migrate's
// pgx/v5 driver registers itself under) for the named database.
func (c pgConn) migrateURL(dbName string) string {
	return fmt.Sprintf("pgx5://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName)
}

// migrationsSourceURL resolves the file:// URL for schema/migrations-v2,
// relative to this test file's own location so it works regardless of
// the caller's working directory -- the exact files docker-compose's
// migrate service uses, not a copy.
func migrationsSourceURL(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../migrations-v2")
	if err != nil {
		t.Fatalf("resolve migrations-v2 path: %v", err)
	}
	return "file://" + filepath.ToSlash(abs)
}

// newThrowawayDB creates a fresh, uniquely-named database on the same
// server, returns its name, and registers cleanup to drop it.
func newThrowawayDB(t *testing.T, c pgConn) string {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), c.adminURL())
	if err != nil {
		t.Fatalf("connect to admin DB: %v", err)
	}
	defer conn.Close(context.Background())

	dbName := fmt.Sprintf("migrationtest_%d_%d", os.Getpid(), rand.Int63())
	// CREATE DATABASE cannot run inside a transaction block; a plain
	// (non-pooled, non-Begin'd) pgx.Conn.Exec runs it outside one.
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(`CREATE DATABASE %q`, dbName)); err != nil {
		t.Fatalf("create throwaway database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cc, err := pgx.Connect(context.Background(), c.adminURL())
		if err != nil {
			// t.Errorf (not Logf) -- a cleanup failure here means dbName is
			// leaked on the real server, which should fail the test run,
			// not just log quietly. t.Cleanup funcs may still report
			// failures after the main test body has returned.
			t.Errorf("cleanup: connect to admin DB (throwaway database %s may be leaked): %v", dbName, err)
			return
		}
		defer cc.Close(context.Background())
		_, _ = cc.Exec(context.Background(),
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`,
			dbName)
		if _, err := cc.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, dbName)); err != nil {
			t.Errorf("cleanup: drop throwaway database %s (leaked): %v", dbName, err)
		}
	})
	return dbName
}

func openMigrator(t *testing.T, c pgConn, dbName string) *migrate.Migrate {
	t.Helper()
	m, err := migrate.New(migrationsSourceURL(t), c.migrateURL(dbName))
	if err != nil {
		t.Fatalf("open migrator for %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			t.Logf("cleanup: close migrator source: %v", srcErr)
		}
		if dbErr != nil {
			t.Logf("cleanup: close migrator database: %v", dbErr)
		}
	})
	return m
}

// tableNames returns every table in the public schema, sorted, for
// comparing two databases' final structure.
func tableNames(t *testing.T, c pgConn, dbName string) []string {
	t.Helper()
	conn, err := pgx.Connect(context.Background(),
		fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatalf("connect to %s: %v", dbName, err)
	}
	defer conn.Close(context.Background())

	rows, err := conn.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables in %s: %v", dbName, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, n)
	}
	return names
}

func scalar[T any](t *testing.T, c pgConn, dbName, query string, args ...any) T {
	t.Helper()
	conn, err := pgx.Connect(context.Background(),
		fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatalf("connect to %s: %v", dbName, err)
	}
	defer conn.Close(context.Background())

	var v T
	if err := conn.QueryRow(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("scalar query %q against %s: %v", query, dbName, err)
	}
	return v
}

func exec(t *testing.T, c pgConn, dbName, stmt string, args ...any) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(),
		fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatalf("connect to %s: %v", dbName, err)
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(context.Background(), stmt, args...); err != nil {
		t.Fatalf("exec %q against %s: %v", stmt, dbName, err)
	}
}

// ─── AC1: applies correctly to a live database with existing data ────────

func TestMigrate_ExistingSeededDatabase_AppliesNewMigrationWithoutDataLoss(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)

	// Get to "already seeded, one migration behind" by running only the
	// baseline (version 1), then inserting real-looking data -- mirroring
	// exactly what this session did against the real dev database.
	m := openMigrator(t, c, dbName)
	if err := m.Steps(1); err != nil {
		t.Fatalf("apply baseline migration: %v", err)
	}

	exec(t, c, dbName,
		`INSERT INTO orgs (name, slug) VALUES ('Acme Corp', 'acme-corp')`)
	orgCountBefore := scalar[int](t, c, dbName, `SELECT count(*) FROM orgs`)
	if orgCountBefore != 1 {
		t.Fatalf("setup: expected 1 org before migrating, got %d", orgCountBefore)
	}

	// Now apply the pending migration against this already-seeded database.
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("apply pending migration to seeded database: %v", err)
	}

	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if dirty {
		t.Fatal("database left dirty after migration")
	}
	if want := latestMigrationVersion(t); version != want {
		t.Fatalf("version = %d, want %d", version, want)
	}

	// Existing data survived untouched.
	orgCountAfter := scalar[int](t, c, dbName, `SELECT count(*) FROM orgs`)
	if orgCountAfter != 1 {
		t.Fatalf("org count after migration = %d, want 1 (existing data must survive)", orgCountAfter)
	}
	orgName := scalar[string](t, c, dbName, `SELECT name FROM orgs WHERE slug = 'acme-corp'`)
	if orgName != "Acme Corp" {
		t.Fatalf("org name after migration = %q, want %q", orgName, "Acme Corp")
	}

	// And the new migration's effect is actually present.
	comment := scalar[string](t, c, dbName, `SELECT obj_description('orgs'::regclass)`)
	if comment == "" {
		t.Fatal("expected orgs table comment to be set by migration 000002, got empty")
	}
}

// ─── AC2: fresh install and incremental-apply end in identical final state ─

func TestMigrate_FreshPathAndIncrementalPath_ProduceIdenticalFinalSchema(t *testing.T) {
	c := testPgConn(t)

	// Fresh path: one Up() call against a brand-new empty database applies
	// every migration in one shot -- what a new Model A appliance's first
	// boot does.
	freshDB := newThrowawayDB(t, c)
	freshMigrator := openMigrator(t, c, freshDB)
	if err := freshMigrator.Up(); err != nil {
		t.Fatalf("fresh path: Up(): %v", err)
	}

	// Incremental path: apply one migration at a time via separate Steps()
	// calls -- what a real production upgrade looks like, one release at
	// a time, not all at once. Loop count derived from the real migration
	// file count (see latestMigrationVersion) instead of a fixed number of
	// hand-written Steps(1) calls -- the previous hardcoded-5-calls version
	// silently stopped one migration short the moment migration 6 (B-058)
	// was added, since Go doesn't warn about a loop that just doesn't run
	// enough iterations.
	incrementalDB := newThrowawayDB(t, c)
	incrementalMigrator := openMigrator(t, c, incrementalDB)
	want := latestMigrationVersion(t)
	for i := uint(1); i <= want; i++ {
		if err := incrementalMigrator.Steps(1); err != nil {
			t.Fatalf("incremental path: apply migration %d: %v", i, err)
		}
	}

	freshTables := tableNames(t, c, freshDB)
	incrementalTables := tableNames(t, c, incrementalDB)

	if len(freshTables) != len(incrementalTables) {
		t.Fatalf("table count differs: fresh=%d incremental=%d", len(freshTables), len(incrementalTables))
	}
	for i := range freshTables {
		if freshTables[i] != incrementalTables[i] {
			t.Fatalf("table set differs at index %d: fresh=%q incremental=%q", i, freshTables[i], incrementalTables[i])
		}
	}

	freshComment := scalar[string](t, c, freshDB, `SELECT obj_description('orgs'::regclass)`)
	incrementalComment := scalar[string](t, c, incrementalDB, `SELECT obj_description('orgs'::regclass)`)
	if freshComment != incrementalComment {
		t.Fatalf("orgs table comment differs: fresh=%q incremental=%q", freshComment, incrementalComment)
	}

	freshModelCount := scalar[int](t, c, freshDB, `SELECT count(*) FROM model_pricing`)
	incrementalModelCount := scalar[int](t, c, incrementalDB, `SELECT count(*) FROM model_pricing`)
	if freshModelCount != incrementalModelCount {
		t.Fatalf("model_pricing row count differs: fresh=%d incremental=%d", freshModelCount, incrementalModelCount)
	}
}

// ─── AC3: applying the same migration twice doesn't corrupt or duplicate ──

func TestMigrate_RunTwice_NoErrorNoDuplication(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)

	if err := m.Up(); err != nil {
		t.Fatalf("first Up(): %v", err)
	}
	modelCountAfterFirst := scalar[int](t, c, dbName, `SELECT count(*) FROM model_pricing`)
	tableCountAfterFirst := len(tableNames(t, c, dbName))

	// Second Up() via the tool: migrate's own schema_migrations bookkeeping
	// should make this a clean no-op, not an error.
	err := m.Up()
	if err != nil && err != migrate.ErrNoChange {
		t.Fatalf("second Up(): expected nil or ErrNoChange, got %v", err)
	}

	modelCountAfterSecond := scalar[int](t, c, dbName, `SELECT count(*) FROM model_pricing`)
	tableCountAfterSecond := len(tableNames(t, c, dbName))

	if modelCountAfterSecond != modelCountAfterFirst {
		t.Fatalf("model_pricing row count changed on re-run: %d -> %d (seed data duplicated)",
			modelCountAfterFirst, modelCountAfterSecond)
	}
	if tableCountAfterSecond != tableCountAfterFirst {
		t.Fatalf("table count changed on re-run: %d -> %d", tableCountAfterFirst, tableCountAfterSecond)
	}

	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if dirty {
		t.Fatal("database left dirty after re-running Up()")
	}
	if want := latestMigrationVersion(t); version != want {
		t.Fatalf("version = %d, want %d", version, want)
	}
}

// ─── Fresh-database sanity check: matches schema.sql's expected shape ─────

func TestMigrate_FreshDatabase_MatchesExpectedSchema(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)

	if err := m.Up(); err != nil {
		t.Fatalf("Up(): %v", err)
	}

	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if want := latestMigrationVersion(t); dirty || version != want {
		t.Fatalf("version=%d dirty=%v, want version=%d dirty=false", version, dirty, want)
	}

	// Spot-check a handful of tables spanning the whole schema, not just
	// the baseline's early tables -- proves the entire 640-line baseline
	// applied, not just its first few statements before a silent partial
	// failure. setup_tokens (B-055, migration 000003) and workflows/
	// workflow_steps (B-058, migration 000006) included so this check
	// keeps pace with the latest migration, not just the baseline.
	for _, table := range []string{
		"orgs", "users", "gateway_agents", "policies", "gateway_tools",
		"approval_requests", "audit_log", "token_usage", "episodes",
		"alert_rules", "discovered_endpoints", "agent_configs", "paste_events",
		"setup_tokens", "workflows", "workflow_steps",
	} {
		exists := scalar[bool](t, c, dbName,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
			table)
		if !exists {
			t.Errorf("expected table %q to exist after migrating, it doesn't", table)
		}
	}

	modelCount := scalar[int](t, c, dbName, `SELECT count(*) FROM model_pricing`)
	if modelCount == 0 {
		t.Error("model_pricing seed data missing after fresh migrate")
	}
}
