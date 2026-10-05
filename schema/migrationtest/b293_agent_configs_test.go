// b293_agent_configs_test.go — schema/migrationtest
// B-293: migration 000027 adds agent_configs.model_file_size_mb (default
// 100, range-checked) and turns any '{}' enabled_scanners -- which older
// agents read as "all scanners" -- into the full list, because an empty
// list now means "no scanners". Run against a throwaway database migrated
// to 26, seeded, migrated to 27 and back down.
package migrationtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrate_B293_ModelFileSizeAndEmptyScannerBackfill(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)
	if err := m.Migrate(26); err != nil {
		t.Fatalf("migrate to 26: %v", err)
	}

	exec(t, c, dbName, `INSERT INTO orgs (name, slug) VALUES ('B293 Org', 'b293-org')`)
	for _, name := range []string{"empty-list", "custom"} {
		exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, $1, 'm', 'qa', 'test' FROM orgs WHERE slug = 'b293-org'`, name)
	}
	exec(t, c, dbName, `UPDATE agent_configs SET enabled_scanners = '{}' WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'empty-list')`)
	exec(t, c, dbName, `UPDATE agent_configs SET enabled_scanners = ARRAY['gpu','browser'] WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'custom')`)

	scanners := func(agent string) string {
		t.Helper()
		return scalar[string](t, c, dbName,
			`SELECT array_to_string(ac.enabled_scanners, '|') FROM agent_configs ac JOIN gateway_agents g ON g.id = ac.agent_id WHERE g.name = $1`, agent)
	}

	if err := m.Migrate(27); err != nil {
		t.Fatalf("migrate to 27: %v", err)
	}
	if got := scanners("empty-list"); got != allTen {
		t.Fatalf("'{}' row = %q, want the full list (it meant 'all' before)", got)
	}
	if got := scanners("custom"); got != "gpu|browser" {
		t.Fatalf("custom row changed: %q", got)
	}
	if got := scalar[int32](t, c, dbName,
		`SELECT model_file_size_mb FROM agent_configs ac JOIN gateway_agents g ON g.id = ac.agent_id WHERE g.name = 'custom'`); got != 100 {
		t.Fatalf("model_file_size_mb default = %d, want 100 (the agent's own default)", got)
	}
	// The range check holds.
	conn, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, v := range []int{0, 100001} {
		if _, err := conn.Exec(context.Background(), `UPDATE agent_configs SET model_file_size_mb = $1`, v); err == nil {
			t.Fatalf("model_file_size_mb = %d was accepted", v)
		}
	}

	// Rollback drops the column; the backfill stays (it means the same thing to every agent).
	if err := m.Migrate(26); err != nil {
		t.Fatalf("migrate down to 26: %v", err)
	}
	if got := scalar[int64](t, c, dbName,
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'agent_configs' AND column_name = 'model_file_size_mb'`); got != 0 {
		t.Fatal("model_file_size_mb still present after rollback")
	}
	if got := scanners("empty-list"); got != allTen {
		t.Fatalf("after rollback = %q", got)
	}
}
