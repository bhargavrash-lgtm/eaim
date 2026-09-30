// b271_agent_configs_test.go — schema/migrationtest
// B-271: migration 000025 makes all 10 scanners the agent_configs default and
// backfills the 4 never-offered names (ai_processes, gpu, python_envs,
// nodejs_ai) into existing rows, keeping every other choice in each row. Run
// against a throwaway database migrated to 24, seeded, then migrated to 25
// (and back down), so the backfill never touches a shared database.
package migrationtest

import (
	"testing"
)

const allTen = "ai_apps|models|mcp_servers|cloud_clients|network_activity|browser|ai_processes|gpu|python_envs|nodejs_ai"

func TestMigrate_B271_AgentConfigsAllScannersDefaultAndBackfill(t *testing.T) {
	c := testPgConn(t)
	dbName := newThrowawayDB(t, c)
	m := openMigrator(t, c, dbName)
	if err := m.Migrate(24); err != nil {
		t.Fatalf("migrate to 24: %v", err)
	}

	scanners := func(agent string) string {
		t.Helper()
		return scalar[string](t, c, dbName,
			`SELECT array_to_string(ac.enabled_scanners, '|') FROM agent_configs ac JOIN gateway_agents g ON g.id = ac.agent_id WHERE g.name = $1`, agent)
	}
	updatedAt := func(agent string) string {
		t.Helper()
		return scalar[string](t, c, dbName,
			`SELECT ac.updated_at::text FROM agent_configs ac JOIN gateway_agents g ON g.id = ac.agent_id WHERE g.name = $1`, agent)
	}

	exec(t, c, dbName, `INSERT INTO orgs (name, slug) VALUES ('B271 Org', 'b271-org')`)
	for _, name := range []string{"old-default", "demo-no-models", "custom-partial", "already-all", "empty-means-all"} {
		// trg_agent_configs_default seeds each agent's row with the column default.
		exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, $1, 'm', 'qa', 'test' FROM orgs WHERE slug = 'b271-org'`, name)
	}
	if got, want := scanners("old-default"), "ai_apps|models|mcp_servers|cloud_clients|network_activity|browser"; got != want {
		t.Fatalf("setup: version-24 default = %q, want the old 6-name default %q", got, want)
	}
	// The real demo endpoint's shape: the old default minus "models" (B-194's mitigation).
	exec(t, c, dbName, `UPDATE agent_configs SET enabled_scanners = ARRAY['ai_apps','mcp_servers','cloud_clients','network_activity','browser'] WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'demo-no-models')`)
	// A row that already has one of the four (only possible through the raw API).
	exec(t, c, dbName, `UPDATE agent_configs SET enabled_scanners = ARRAY['gpu','browser'] WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'custom-partial')`)
	// A row that already has all four, in a custom order: must be left untouched.
	exec(t, c, dbName, `UPDATE agent_configs SET enabled_scanners = ARRAY['nodejs_ai','gpu','python_envs','ai_processes','ai_apps'], updated_at = '2026-01-01T00:00:00Z' WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'already-all')`)
	alreadyAllUpdatedAt := updatedAt("already-all")
	// An empty list means "all scanners" to the agent; the backfill must not
	// narrow it to a 4-name allow-list.
	exec(t, c, dbName, `UPDATE agent_configs SET enabled_scanners = '{}' WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'empty-means-all')`)

	if err := m.Migrate(25); err != nil {
		t.Fatalf("migrate to 25: %v", err)
	}

	for _, tc := range []struct{ agent, want string }{
		{"old-default", allTen},
		{"demo-no-models", "ai_apps|mcp_servers|cloud_clients|network_activity|browser|ai_processes|gpu|python_envs|nodejs_ai"},
		{"custom-partial", "gpu|browser|ai_processes|python_envs|nodejs_ai"},
		{"already-all", "nodejs_ai|gpu|python_envs|ai_processes|ai_apps"},
		{"empty-means-all", ""},
	} {
		if got := scanners(tc.agent); got != tc.want {
			t.Errorf("after 000025, %s scanners = %q, want %q", tc.agent, got, tc.want)
		}
	}
	if got := updatedAt("already-all"); got != alreadyAllUpdatedAt {
		t.Errorf("a row that already had all four was rewritten: updated_at %s -> %s", alreadyAllUpdatedAt, got)
	}
	if got := scalar[bool](t, c, dbName, `SELECT 'models' = ANY(enabled_scanners) FROM agent_configs WHERE agent_id = (SELECT id FROM gateway_agents WHERE name = 'demo-no-models')`); got {
		t.Error("backfill re-enabled models on the B-194-mitigated row")
	}

	// A governed agent created after the migration gets all 10 from the trigger.
	exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, 'new-after-25', 'm', 'qa', 'test' FROM orgs WHERE slug = 'b271-org'`)
	if got := scanners("new-after-25"); got != allTen {
		t.Fatalf("new agent's default scanners = %q, want all ten %q", got, allTen)
	}

	// Down reverts the default only; the backfill is deliberately kept.
	if err := m.Migrate(24); err != nil {
		t.Fatalf("migrate down to 24: %v", err)
	}
	exec(t, c, dbName, `INSERT INTO gateway_agents (org_id, name, model, owner, scope) SELECT id, 'new-after-down', 'm', 'qa', 'test' FROM orgs WHERE slug = 'b271-org'`)
	if got, want := scanners("new-after-down"), "ai_apps|models|mcp_servers|cloud_clients|network_activity|browser"; got != want {
		t.Errorf("after down, new agent's default = %q, want the old 6 %q", got, want)
	}
	if got := scanners("old-default"); got != allTen {
		t.Errorf("down stripped the backfill: old-default = %q, want %q", got, allTen)
	}
}
