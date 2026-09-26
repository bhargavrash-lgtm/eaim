package migrationtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// cmdbMigrationVersion is 000024_cmdb_classification. Pinned rather than
// derived from latestMigrationVersion so these tests keep exercising 24's own
// up/down even after later migrations land.
const cmdbMigrationVersion = 24

// connectMigrationDB uses the simple protocol so no cached statement plan
// outlives the schema changes the up/down migrations make mid-test.
func connectMigrationDB(t *testing.T, c pgConn, dbName string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable&default_query_exec_mode=simple_protocol", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestCMDBClassificationMigration_RealPostgres(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)
	if err := m.Migrate(cmdbMigrationVersion - 1); err != nil {
		t.Fatalf("apply pre-CMDB migrations: %v", err)
	}

	ctx := context.Background()
	conn := connectMigrationDB(t, c, dbName)

	var existingOrg string
	if err := conn.QueryRow(ctx, `INSERT INTO orgs(name,slug) VALUES('Existing','cmdb-existing') RETURNING id`).Scan(&existingOrg); err != nil {
		t.Fatal(err)
	}
	var endpointID, agentID string
	if err := conn.QueryRow(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname) VALUES($1,'seed-agent','seed-host') RETURNING id`, existingOrg).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope) VALUES($1,'seed-governed','model','qa','test') RETURNING id`, existingOrg).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO gateway_tools(org_id,name,type,auth_type) VALUES($1,'seed-tool','mcp','api_key')`, existingOrg); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(cmdbMigrationVersion); err != nil {
		t.Fatalf("apply CMDB migration: %v", err)
	}
	assertCMDBDefaults(t, conn, existingOrg)

	var futureOrg string
	if err := conn.QueryRow(ctx, `INSERT INTO orgs(name,slug) VALUES('Future','cmdb-future') RETURNING id`).Scan(&futureOrg); err != nil {
		t.Fatal(err)
	}
	assertCMDBDefaults(t, conn, futureOrg)
	if _, err := conn.Exec(ctx, `SELECT seed_default_ci_taxonomy($1)`, futureOrg); err != nil {
		t.Fatalf("idempotent reseed: %v", err)
	}
	assertCMDBDefaults(t, conn, futureOrg)

	var explicitEndpointType string
	if err := conn.QueryRow(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name) SELECT $1,id,'endpoint','Laptop','' FROM ci_categories WHERE org_id=$1 AND normalized_name='end-user compute' RETURNING id`, existingOrg).Scan(&explicitEndpointType); err != nil {
		t.Fatal(err)
	}
	// Type names are unique org-wide, across asset kinds, after normalization.
	assertPgError(t, "23505", "ci_types_org_id_normalized_name_key", func() error {
		_, err := conn.Exec(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name) SELECT $1,id,'agent','  LAPTOP  ','' FROM ci_categories WHERE org_id=$1 AND normalized_name='ai systems'`, existingOrg)
		return err
	})
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, explicitEndpointType, endpointID); err != nil {
		t.Fatalf("explicit assignment: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=NULL WHERE id=$1`, endpointID); err != nil {
		t.Fatalf("reset assignment: %v", err)
	}

	var agentDefault, existingEndpointDefault, otherEndpointDefault, futureEndpointCategory string
	if err := conn.QueryRow(ctx, `SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='agent' AND is_default`, existingOrg).Scan(&agentDefault); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='endpoint' AND is_default`, existingOrg).Scan(&existingEndpointDefault); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='endpoint' AND is_default`, futureOrg).Scan(&otherEndpointDefault); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT id FROM ci_categories WHERE org_id=$1 AND normalized_name='end-user compute'`, futureOrg).Scan(&futureEndpointCategory); err != nil {
		t.Fatal(err)
	}

	// Kind mismatch and cross-org assignment are rejected by the
	// check_asset_ci_type_kind trigger (it runs before the composite FK).
	const kindTriggerMsg = "CI type does not belong to asset organization and kind"
	assertPgError(t, "23514", kindTriggerMsg, func() error {
		_, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, agentDefault, endpointID)
		return err
	})
	assertPgError(t, "23514", kindTriggerMsg, func() error {
		_, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, otherEndpointDefault, endpointID)
		return err
	})

	// A second default is stopped by the partial unique index...
	assertPgError(t, "23505", "ci_types_one_default_per_kind", func() error {
		_, err := conn.Exec(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name,is_default) SELECT $1,id,'endpoint','Other default','',TRUE FROM ci_categories WHERE org_id=$1 LIMIT 1`, existingOrg)
		return err
	})
	// ...while zero defaults is stopped by the deferred require_ci_default
	// constraint trigger, which only fires at COMMIT.
	const oneDefaultMsg = "exactly one default CI type is required"
	assertPgError(t, "23514", oneDefaultMsg, func() error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `UPDATE ci_types SET is_default=FALSE WHERE id=$1`, existingEndpointDefault); err != nil {
			t.Fatalf("unsetting the default must be allowed mid-transaction (deferred check): %v", err)
		}
		return tx.Commit(ctx)
	})
	assertPgError(t, "23514", oneDefaultMsg, func() error {
		_, err := conn.Exec(ctx, `DELETE FROM ci_types WHERE id=$1`, existingEndpointDefault)
		return err
	})

	// Scope immutability comes from keep_ci_type_scope_immutable. Moving
	// org_id together with a category_id in the destination org satisfies the
	// composite FK, so only the trigger stops this cross-tenant move.
	assertPgError(t, "23514", "asset_kind is immutable", func() error {
		_, err := conn.Exec(ctx, `UPDATE ci_types SET asset_kind='tool' WHERE id=$1`, explicitEndpointType)
		return err
	})
	assertPgError(t, "23514", "org_id is immutable", func() error {
		_, err := conn.Exec(ctx, `UPDATE ci_types SET org_id=$1 WHERE id=$2`, futureOrg, explicitEndpointType)
		return err
	})
	assertPgError(t, "23514", "org_id is immutable", func() error {
		_, err := conn.Exec(ctx, `UPDATE ci_types SET org_id=$1, category_id=$2 WHERE id=$3`, futureOrg, futureEndpointCategory, explicitEndpointType)
		return err
	})
	assertPgError(t, "23514", "org_id is immutable", func() error {
		_, err := conn.Exec(ctx, `UPDATE ci_types SET org_id=$1, category_id=$2, name='Moved default' WHERE id=$3`, futureOrg, futureEndpointCategory, existingEndpointDefault)
		return err
	})

	// An in-use type cannot be deleted (composite FK, ON DELETE RESTRICT).
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, explicitEndpointType, endpointID); err != nil {
		t.Fatal(err)
	}
	assertPgError(t, "23503", "endpoints_ci_type_org_fk", func() error {
		_, err := conn.Exec(ctx, `DELETE FROM ci_types WHERE id=$1`, explicitEndpointType)
		return err
	})

	// Deleting an org whose assets are explicitly classified still succeeds:
	// the RESTRICT FKs sit alongside the orgs cascades and must not block them.
	if _, err := conn.Exec(ctx, `UPDATE gateway_agents SET ci_type_id=$1 WHERE id=$2`, agentDefault, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM orgs WHERE id=$1`, existingOrg); err != nil {
		t.Fatalf("delete org with explicitly classified endpoint and agent: %v", err)
	}
	var leftover int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM ci_types WHERE org_id=$1) + (SELECT count(*) FROM ci_categories WHERE org_id=$1) + (SELECT count(*) FROM endpoints WHERE org_id=$1) + (SELECT count(*) FROM gateway_agents WHERE org_id=$1)`, existingOrg).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("org delete left %d CMDB/asset rows behind", leftover)
	}
	assertCMDBDefaults(t, conn, futureOrg)
}

// T2: 000024's down migration actually runs, removes every object the up
// created (the schema returns to exactly the version-23 shape), keeps
// pre-existing inventory, and a re-up succeeds and reseeds existing orgs.
func TestCMDBClassificationMigration_DownRestoresPreviousSchemaAndReUpWorks_RealPostgres(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)
	if err := m.Migrate(cmdbMigrationVersion - 1); err != nil {
		t.Fatalf("apply pre-CMDB migrations: %v", err)
	}
	ctx := context.Background()
	conn := connectMigrationDB(t, c, dbName)

	before := schemaFingerprint(t, conn)
	if strings.Contains(before, "ci_type") {
		t.Fatal("version 23 schema unexpectedly already contains CMDB objects")
	}

	var orgID string
	if err := conn.QueryRow(ctx, `INSERT INTO orgs(name,slug) VALUES('Rollback','cmdb-rollback') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname) VALUES($1,'rb-agent','rb-host')`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope) VALUES($1,'rb-governed','model','qa','test')`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO gateway_tools(org_id,name,type,auth_type) VALUES($1,'rb-tool','mcp','api_key')`, orgID); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(cmdbMigrationVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	up := schemaFingerprint(t, conn)
	for _, want := range []string{"tbl:ci_categories", "tbl:ci_types", "col:endpoints.ci_type_id", "fn:seed_default_ci_taxonomy", "trg:orgs:CREATE TRIGGER trg_orgs_seed_default_ci_taxonomy"} {
		if !strings.Contains(up, want) {
			t.Fatalf("after up, schema is missing %s", want)
		}
	}
	assertCMDBDefaults(t, conn, orgID)
	// Explicit assignments exist at rollback time, so the down must drop
	// referencing columns/FKs before the tables they reference.
	if _, err := conn.Exec(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name) SELECT $1,id,'endpoint','Laptop','' FROM ci_categories WHERE org_id=$1 AND normalized_name='end-user compute'`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=(SELECT id FROM ci_types WHERE org_id=$1 AND normalized_name='laptop') WHERE org_id=$1`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE gateway_agents SET ci_type_id=(SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='agent' AND is_default) WHERE org_id=$1`, orgID); err != nil {
		t.Fatal(err)
	}

	if err := m.Migrate(cmdbMigrationVersion - 1); err != nil {
		t.Fatalf("down (000024_cmdb_classification.down.sql): %v", err)
	}
	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("read version after down: %v", err)
	}
	if version != cmdbMigrationVersion-1 || dirty {
		t.Fatalf("after down: version=%d dirty=%v, want %d clean", version, dirty, cmdbMigrationVersion-1)
	}
	if after := schemaFingerprint(t, conn); after != before {
		t.Fatalf("down did not restore the version-23 schema exactly:\n%s", fingerprintDiff(before, after))
	}
	var hostname string
	var agents, tools int
	if err := conn.QueryRow(ctx, `SELECT (SELECT hostname FROM endpoints WHERE org_id=$1), (SELECT count(*) FROM gateway_agents WHERE org_id=$1), (SELECT count(*) FROM gateway_tools WHERE org_id=$1)`, orgID).Scan(&hostname, &agents, &tools); err != nil {
		t.Fatal(err)
	}
	if hostname != "rb-host" || agents != 1 || tools != 1 {
		t.Fatalf("down lost inventory: hostname=%q agents=%d tools=%d", hostname, agents, tools)
	}

	if err := m.Migrate(cmdbMigrationVersion); err != nil {
		t.Fatalf("re-up after down: %v", err)
	}
	if reUp := schemaFingerprint(t, conn); reUp != up {
		t.Fatalf("re-up schema differs from the first up:\n%s", fingerprintDiff(up, reUp))
	}
	assertCMDBDefaults(t, conn, orgID)
	var explicit int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM endpoints WHERE org_id=$1 AND ci_type_id IS NOT NULL) + (SELECT count(*) FROM gateway_agents WHERE org_id=$1 AND ci_type_id IS NOT NULL)`, orgID).Scan(&explicit); err != nil {
		t.Fatal(err)
	}
	if explicit != 0 {
		t.Fatalf("re-up resurrected %d explicit assignments; the down is documented to discard them", explicit)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("up to latest after round trip: %v", err)
	}
}

func assertCMDBDefaults(t *testing.T, conn *pgx.Conn, orgID string) {
	t.Helper()
	var categories, defaults int
	if err := conn.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM ci_categories WHERE org_id=$1), (SELECT count(*) FROM ci_types WHERE org_id=$1 AND is_default)`, orgID).Scan(&categories, &defaults); err != nil {
		t.Fatal(err)
	}
	if categories != 3 || defaults != 3 {
		t.Fatalf("org %s categories/defaults=%d/%d want 3/3", orgID, categories, defaults)
	}
}

// schemaFingerprint lists every public-schema table, column (with type,
// nullability and default), constraint definition, index definition, user
// trigger definition and function (signature plus a hash of its body), one
// per line, sorted.
func schemaFingerprint(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var fp string
	err := conn.QueryRow(context.Background(), `
SELECT string_agg(x, E'\n' ORDER BY x) FROM (
  SELECT 'tbl:' || tablename FROM pg_tables WHERE schemaname = 'public'
  UNION ALL SELECT 'col:' || table_name || '.' || column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
    FROM information_schema.columns WHERE table_schema = 'public'
  UNION ALL SELECT 'con:' || conrelid::regclass::text || ':' || conname || ':' || pg_get_constraintdef(oid)
    FROM pg_constraint WHERE connamespace = 'public'::regnamespace
  UNION ALL SELECT 'idx:' || tablename || ':' || indexdef FROM pg_indexes WHERE schemaname = 'public'
  UNION ALL SELECT 'trg:' || tgrelid::regclass::text || ':' || pg_get_triggerdef(oid) FROM pg_trigger
    WHERE NOT tgisinternal AND tgrelid IN (SELECT oid FROM pg_class WHERE relnamespace = 'public'::regnamespace)
  UNION ALL SELECT 'fn:' || p.oid::regprocedure::text || ':' || md5(p.prosrc) FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace
) s(x)`).Scan(&fp)
	if err != nil {
		t.Fatalf("schema fingerprint: %v", err)
	}
	return fp
}

func fingerprintDiff(want, got string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	w, g := in(want), in(got)
	var out []string
	for l := range w {
		if !g[l] {
			out = append(out, "  missing: "+l)
		}
	}
	for l := range g {
		if !w[l] {
			out = append(out, "  extra:   "+l)
		}
	}
	return strings.Join(out, "\n")
}

// assertPgError requires a PostgreSQL error with exactly this SQLSTATE whose
// constraint name or message contains `detail`, so a case cannot pass because
// some other protection (or an unrelated error) happened to fire.
func assertPgError(t *testing.T, code, detail string, fn func() error) {
	t.Helper()
	err := fn()
	if err == nil {
		t.Fatalf("expected PostgreSQL error %s (%s), got success", code, detail)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected pg error %s (%s), got %T: %v", code, detail, err, err)
	}
	if pgErr.Code != code || !(pgErr.ConstraintName == detail || strings.Contains(pgErr.Message, detail)) {
		t.Fatalf("expected SQLSTATE %s matching %q, got %s constraint=%q message=%q", code, detail, pgErr.Code, pgErr.ConstraintName, pgErr.Message)
	}
}
