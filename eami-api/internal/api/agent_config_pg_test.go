package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/store"
)

// B-232 (H-1): PUT /v1/gateway/agents/{id}/config only checked org ownership
// when the agent had no config row -- and every agent always has one
// (trg_agent_configs_default) -- so any admin/operator could overwrite
// another org's agent scanner config by id. Same adversarial shape as
// B-141's cross-org tests: a real attacker org, a real victim org, the
// attempt must be rejected, and the victim's stored row must be unchanged.

type agentConfigRow struct {
	interval  int32
	paths     string
	maxBytes  int32
	scanners  string
	updatedAt string
}

// readAgentConfigRow reads the victim's row straight from Postgres (not via
// the API under test). exists=false when there is no row.
func readAgentConfigRow(t *testing.T, env *workspaceTestEnv, ctx context.Context, agentID uuid.UUID) (agentConfigRow, bool) {
	t.Helper()
	var r agentConfigRow
	var n int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM agent_configs WHERE agent_id=$1`, agentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return r, false
	}
	if err := env.pool.QueryRow(ctx, `SELECT scan_interval_seconds, array_to_string(model_scan_paths,'|'), max_report_size_bytes, array_to_string(enabled_scanners,'|'), updated_at::text FROM agent_configs WHERE agent_id=$1`, agentID).
		Scan(&r.interval, &r.paths, &r.maxBytes, &r.scanners, &r.updatedAt); err != nil {
		t.Fatal(err)
	}
	return r, true
}

func seedConfigAgent(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO gateway_agents(org_id,name,model,owner,scope) VALUES($1,$2,'model','qa','test') RETURNING id`, orgID, name).Scan(&id); err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
	return id
}

func configPutBody() map[string]any {
	return map[string]any{
		"scan_interval_seconds": 61,
		"model_scan_paths":      []string{"/attacker"},
		"max_report_size_bytes": 1048576,
		"enabled_scanners":      []string{"browser"},
	}
}

func TestAgentConfig_CrossOrgWriteRejected_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b232-attacker")
	orgB := seedTestOrg(t, ctx, env.pool, "b232-victim")
	attackerAdmin := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "admin@b232-a.test", "admin")
	attackerOperator := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "operator@b232-a.test", "operator")

	victimWithRow := seedConfigAgent(t, env, ctx, orgB, "b232-victim-with-row")
	victimNoRow := seedConfigAgent(t, env, ctx, orgB, "b232-victim-no-row")
	// The trigger seeds a row for every agent; remove one to exercise the
	// "no config row yet" branch too.
	if _, err := env.pool.Exec(ctx, `DELETE FROM agent_configs WHERE agent_id=$1`, victimNoRow); err != nil {
		t.Fatal(err)
	}
	// Give the victim a non-default config so an overwrite is unmistakable.
	if _, err := env.pool.Exec(ctx, `UPDATE agent_configs SET scan_interval_seconds=4321, model_scan_paths=ARRAY['/victim'], enabled_scanners=ARRAY['models'] WHERE agent_id=$1`, victimWithRow); err != nil {
		t.Fatal(err)
	}
	beforeWith, ok := readAgentConfigRow(t, env, ctx, victimWithRow)
	if !ok {
		t.Fatal("setup: victim config row missing")
	}
	if _, ok := readAgentConfigRow(t, env, ctx, victimNoRow); ok {
		t.Fatal("setup: victimNoRow still has a config row")
	}

	nonexistentBody := ""
	{
		resp := env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", uuid.New()), attackerAdmin, configPutBody())
		nonexistentBody = expectCMDBStatus(t, resp, http.StatusNotFound, "PUT config for a nonexistent agent")
	}

	for _, tc := range []struct {
		name  string
		token string
		agent uuid.UUID
	}{
		{"admin, victim HAS a config row (the broken branch)", attackerAdmin, victimWithRow},
		{"operator, victim HAS a config row", attackerOperator, victimWithRow},
		{"admin, victim has NO config row", attackerAdmin, victimNoRow},
		{"operator, victim has NO config row", attackerOperator, victimNoRow},
	} {
		resp := env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", tc.agent), tc.token, configPutBody())
		body := expectCMDBStatus(t, resp, http.StatusNotFound, "cross-org PUT config: "+tc.name)
		// Same status and body as a nonexistent id: no existence oracle.
		if body != nonexistentBody {
			t.Fatalf("%s: 404 body %q differs from nonexistent-agent 404 %q (existence oracle)", tc.name, body, nonexistentBody)
		}
		if strings.Contains(body, "4321") || strings.Contains(body, "/victim") {
			t.Fatalf("%s: response leaked victim config: %s", tc.name, body)
		}
	}

	afterWith, ok := readAgentConfigRow(t, env, ctx, victimWithRow)
	if !ok || afterWith != beforeWith {
		t.Fatalf("REGRESSION B-232: victim config row changed by a cross-org PUT: before=%+v after=%+v", beforeWith, afterWith)
	}
	if _, ok := readAgentConfigRow(t, env, ctx, victimNoRow); ok {
		t.Fatal("REGRESSION B-232: a cross-org PUT created a config row for the victim's agent")
	}

	// Reads are org-checked too (pre-existing, pinned here alongside).
	resp := env.do(t, http.MethodGet, fmt.Sprintf("/v1/gateway/agents/%s/config", victimWithRow), attackerAdmin, nil)
	expectCMDBStatus(t, resp, http.StatusNotFound, "cross-org GET config")
}

func TestAgentConfig_SameOrgConfigureStillWorks_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b232-same-org")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@b232.test", "admin")
	operator := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "operator@b232.test", "operator")
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@b232.test", "viewer")
	withRow := seedConfigAgent(t, env, ctx, orgID, "b232-own-with-row")
	noRow := seedConfigAgent(t, env, ctx, orgID, "b232-own-no-row")
	if _, err := env.pool.Exec(ctx, `DELETE FROM agent_configs WHERE agent_id=$1`, noRow); err != nil {
		t.Fatal(err)
	}

	// Full update by admin, existing row.
	body := expectCMDBStatus(t, env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", withRow), admin, configPutBody()), http.StatusOK, "same-org admin PUT (row exists)")
	var got struct {
		ScanIntervalSeconds int32    `json:"scan_interval_seconds"`
		EnabledScanners     []string `json:"enabled_scanners"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.ScanIntervalSeconds != 61 {
		t.Fatalf("same-org PUT response %s, want scan_interval_seconds 61", body)
	}
	if r, _ := readAgentConfigRow(t, env, ctx, withRow); r.interval != 61 || r.paths != "/attacker" || r.scanners != "browser" {
		t.Fatalf("same-org PUT not persisted: %+v", r)
	}

	// Partial update by operator merges onto the stored row.
	expectCMDBStatus(t, env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", withRow), operator, map[string]any{"scan_interval_seconds": 900}), http.StatusOK, "same-org operator partial PUT")
	if r, _ := readAgentConfigRow(t, env, ctx, withRow); r.interval != 900 || r.paths != "/attacker" || r.scanners != "browser" {
		t.Fatalf("partial PUT did not merge onto the stored row: %+v", r)
	}

	// No row yet: defaults merged with the request, and the row is created.
	expectCMDBStatus(t, env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", noRow), admin, map[string]any{"scan_interval_seconds": 120}), http.StatusOK, "same-org PUT (no row yet)")
	if r, ok := readAgentConfigRow(t, env, ctx, noRow); !ok || r.interval != 120 || r.maxBytes != 5242880 {
		t.Fatalf("same-org PUT with no row: exists=%v row=%+v, want interval 120 and default max bytes", ok, r)
	}

	// Viewers remain read-only (route-level RBAC, unchanged).
	expectCMDBStatus(t, env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", withRow), viewer, configPutBody()), http.StatusForbidden, "viewer PUT config")
	expectCMDBStatus(t, env.do(t, http.MethodGet, fmt.Sprintf("/v1/gateway/agents/%s/config", withRow), viewer, nil), http.StatusOK, "viewer GET config")
}

// The HTTP tests above are always stopped by the handler's ownership check,
// so they never reach UpsertAgentConfig's own org scoping. This exercises
// that second layer directly: called with the wrong org, the org-scoped
// INSERT ... SELECT must write nothing (row exists or not) and return
// pgx.ErrNoRows; with the right org it must still upsert.
func TestUpsertAgentConfig_StoreLevelOrgScoping_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b232-store-a")
	orgB := seedTestOrg(t, ctx, env.pool, "b232-store-b")
	withRow := seedConfigAgent(t, env, ctx, orgB, "b232-store-with-row")
	noRow := seedConfigAgent(t, env, ctx, orgB, "b232-store-no-row")
	if _, err := env.pool.Exec(ctx, `DELETE FROM agent_configs WHERE agent_id=$1`, noRow); err != nil {
		t.Fatal(err)
	}
	before, _ := readAgentConfigRow(t, env, ctx, withRow)
	q := store.New(env.pool)
	params := func(org, agent uuid.UUID) store.UpsertAgentConfigParams {
		return store.UpsertAgentConfigParams{OrgID: org, AgentID: agent, ScanIntervalSeconds: 61,
			ModelScanPaths: []string{"/attacker"}, MaxReportSizeBytes: 1048576, EnabledScanners: []string{"browser"}}
	}
	for _, agent := range []uuid.UUID{withRow, noRow} {
		if _, err := q.UpsertAgentConfig(ctx, params(orgA, agent)); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("store upsert with the wrong org for agent %s: err=%v, want pgx.ErrNoRows", agent, err)
		}
	}
	if after, ok := readAgentConfigRow(t, env, ctx, withRow); !ok || after != before {
		t.Fatalf("store-level wrong-org upsert changed the victim row: before=%+v after=%+v", before, after)
	}
	if _, ok := readAgentConfigRow(t, env, ctx, noRow); ok {
		t.Fatal("store-level wrong-org upsert created a config row")
	}
	cfg, err := q.UpsertAgentConfig(ctx, params(orgB, withRow))
	if err != nil || cfg.ScanIntervalSeconds != 61 {
		t.Fatalf("store upsert with the right org: cfg=%+v err=%v", cfg, err)
	}
}
