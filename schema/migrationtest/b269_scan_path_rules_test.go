// b269_scan_path_rules_test.go — schema/migrationtest
// B-269 Slice 0: migration 000028 makes new agents default to no model
// paths (S4) and lets endpoint_model_files store 'gpt4all' and 'scan_path'
// (S2). Down restores both, relabelling the new source values 'unknown'
// first so the old CHECK holds.
package migrationtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrate_B269_Slice0_DefaultsAndModelSources(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)
	if err := m.Migrate(27); err != nil {
		t.Fatalf("migrate to 27: %v", err)
	}
	exec(t, c, dbName, `INSERT INTO orgs (name, slug) VALUES ('B269 Org', 'b269-org')`)
	exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, 'old-agent', 'm', 'qa', 'test' FROM orgs WHERE slug = 'b269-org'`)
	paths := func(agent string) string {
		t.Helper()
		return scalar[string](t, c, dbName, `SELECT array_to_string(ac.model_scan_paths, '|') FROM agent_configs ac JOIN gateway_agents g ON g.id = ac.agent_id WHERE g.name = $1`, agent)
	}
	if got := paths("old-agent"); got == "" {
		t.Fatal("setup: version 27 default should still be the whole-profile paths")
	}

	if err := m.Migrate(28); err != nil {
		t.Fatalf("migrate to 28: %v", err)
	}
	exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, 'new-agent', 'm', 'qa', 'test' FROM orgs WHERE slug = 'b269-org'`)
	if got := paths("new-agent"); got != "" {
		t.Fatalf("new agent's default paths = %q, want none (S4)", got)
	}
	if got := paths("old-agent"); got == "" {
		t.Fatal("an existing row's paths were changed (they must stay; B-296 cleans them)")
	}

	exec(t, c, dbName, `INSERT INTO endpoints (org_id, agent_id, hostname) SELECT id, 'ep', 'ep' FROM orgs WHERE slug = 'b269-org'`)
	for _, src := range []string{"gpt4all", "scan_path", "lmstudio"} {
		exec(t, c, dbName, `INSERT INTO endpoint_model_files (endpoint_id, name, source, detected_at) SELECT id, $1, $1, now() FROM endpoints WHERE agent_id = 'ep'`, src)
	}
	conn, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.user, c.pass, c.host, dbName))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `INSERT INTO endpoint_model_files (endpoint_id, name, source, detected_at) SELECT id, 'x', 'lm_studio', now() FROM endpoints WHERE agent_id = 'ep'`); err == nil {
		t.Fatal("the CHECK accepted an unlisted source")
	}

	if err := m.Migrate(27); err != nil {
		t.Fatalf("migrate down to 27: %v", err)
	}
	if got := scalar[int64](t, c, dbName, `SELECT count(*) FROM endpoint_model_files WHERE source IN ('gpt4all','scan_path')`); got != 0 {
		t.Fatalf("new source values left after rollback: %d", got)
	}
	if got := scalar[int64](t, c, dbName, `SELECT count(*) FROM endpoint_model_files WHERE source = 'unknown'`); got != 2 {
		t.Fatalf("rows relabelled 'unknown' = %d, want 2", got)
	}
	exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, 'after-down', 'm', 'qa', 'test' FROM orgs WHERE slug = 'b269-org'`)
	if got := paths("after-down"); got == "" {
		t.Fatal("rollback didn't restore the old default")
	}
}
