package migrationtest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCMDBClassificationMigration_RealPostgres(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)
	if err := m.Steps(int(latestMigrationVersion(t) - 1)); err != nil {
		t.Fatalf("apply pre-CMDB migrations: %v", err)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	var existingOrg string
	if err := conn.QueryRow(ctx, `INSERT INTO orgs(name,slug) VALUES('Existing','cmdb-existing') RETURNING id`).Scan(&existingOrg); err != nil {
		t.Fatal(err)
	}
	var endpointID, agentID, toolID string
	if err := conn.QueryRow(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname) VALUES($1,'seed-agent','seed-host') RETURNING id`, existingOrg).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope) VALUES($1,'seed-governed','model','qa','test') RETURNING id`, existingOrg).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO gateway_tools(org_id,name,type,auth_type) VALUES($1,'seed-tool','mcp','api_key') RETURNING id`, existingOrg).Scan(&toolID); err != nil {
		t.Fatal(err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("apply CMDB migration: %v", err)
	}
	assertDefaults := func(orgID string) {
		t.Helper()
		var categories, defaults int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM ci_categories WHERE org_id=$1`, orgID).Scan(&categories); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM ci_types WHERE org_id=$1 AND is_default`, orgID).Scan(&defaults); err != nil {
			t.Fatal(err)
		}
		if categories != 3 || defaults != 3 {
			t.Fatalf("org %s categories/defaults=%d/%d want 3/3", orgID, categories, defaults)
		}
	}
	assertDefaults(existingOrg)

	var futureOrg string
	if err := conn.QueryRow(ctx, `INSERT INTO orgs(name,slug) VALUES('Future','cmdb-future') RETURNING id`).Scan(&futureOrg); err != nil {
		t.Fatal(err)
	}
	assertDefaults(futureOrg)
	if _, err := conn.Exec(ctx, `SELECT seed_default_ci_taxonomy($1)`, futureOrg); err != nil {
		t.Fatalf("idempotent reseed: %v", err)
	}
	assertDefaults(futureOrg)

	var explicitEndpointType string
	if err := conn.QueryRow(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name) SELECT $1,id,'endpoint','Laptop','' FROM ci_categories WHERE org_id=$1 AND normalized_name='end-user compute' RETURNING id`, existingOrg).Scan(&explicitEndpointType); err != nil {
		t.Fatal(err)
	}
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name) SELECT $1,id,'agent','  LAPTOP  ','' FROM ci_categories WHERE org_id=$1 AND normalized_name='ai systems'`, existingOrg)
		return err
	})
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, explicitEndpointType, endpointID); err != nil {
		t.Fatalf("explicit assignment: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=NULL WHERE id=$1`, endpointID); err != nil {
		t.Fatalf("reset assignment: %v", err)
	}

	var agentDefault string
	if err := conn.QueryRow(ctx, `SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='agent' AND is_default`, existingOrg).Scan(&agentDefault); err != nil {
		t.Fatal(err)
	}
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, agentDefault, endpointID)
		return err
	})
	var otherEndpointDefault string
	if err := conn.QueryRow(ctx, `SELECT id FROM ci_types WHERE org_id=$1 AND asset_kind='endpoint' AND is_default`, futureOrg).Scan(&otherEndpointDefault); err != nil {
		t.Fatal(err)
	}
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, otherEndpointDefault, endpointID)
		return err
	})
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `INSERT INTO ci_types(org_id,category_id,asset_kind,name,normalized_name,is_default) SELECT $1,id,'endpoint','Other default','',TRUE FROM ci_categories WHERE org_id=$1 LIMIT 1`, existingOrg)
		return err
	})
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `UPDATE ci_types SET asset_kind='tool' WHERE id=$1`, explicitEndpointType)
		return err
	})
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `UPDATE ci_types SET org_id=$1 WHERE id=$2`, futureOrg, explicitEndpointType)
		return err
	})
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `DELETE FROM ci_types WHERE org_id=$1 AND asset_kind='endpoint' AND is_default`, existingOrg)
		return err
	})
	if _, err := conn.Exec(ctx, `UPDATE endpoints SET ci_type_id=$1 WHERE id=$2`, explicitEndpointType, endpointID); err != nil {
		t.Fatal(err)
	}
	assertConstraintError(t, func() error {
		_, err := conn.Exec(ctx, `DELETE FROM ci_types WHERE id=$1`, explicitEndpointType)
		return err
	})
	_ = agentID
	_ = toolID
}

func assertConstraintError(t *testing.T, fn func() error) {
	t.Helper()
	err := fn()
	if err == nil {
		t.Fatal("expected PostgreSQL constraint failure")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected pg error, got %T: %v", err, err)
	}
}
