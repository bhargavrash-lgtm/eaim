// tools_action_paths_pg_test.go -- eami-api/internal/api
// Real-Postgres integration test for gateway_tools.action_paths (B-046),
// following tools_update_pg_test.go's established TEST_DATABASE_URL/
// POSTGRES_PASSWORD convention. This proves the store layer's JSONB
// COALESCE semantics actually hold against real Postgres, not just a fake
// store: an omitted action_paths on UPDATE leaves the stored value
// untouched, and CreateTool/UpdateTool round-trip the real bytes.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestActionPaths_RealDB -v
package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/store"
)

// TestActionPaths_RealDB_PersistedOnCreate proves CreateTool's action_paths
// bind round-trips through real Postgres JSONB exactly as submitted.
func TestActionPaths_RealDB_PersistedOnCreate(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "action-paths-test-"+orgID.String()[:8], "action-paths-test-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	actionPaths := []byte(`{"list":{"path":"/v1/list","method":"GET"}}`)
	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "action-paths-tool", Type: "rest_api", AuthType: "api_key",
		BaseURL:     strPtrTest("https://example.com"),
		ActionPaths: actionPaths,
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	var got map[string]struct {
		Path   string `json:"path"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(created.ActionPaths, &got); err != nil {
		t.Fatalf("CreateTool's returned ActionPaths must be valid JSON: %v (raw: %s)", err, created.ActionPaths)
	}
	if got["list"].Path != "/v1/list" || got["list"].Method != "GET" {
		t.Errorf("ActionPaths[list] = %+v, want {/v1/list GET}", got["list"])
	}
}

// TestActionPaths_RealDB_PreservedWhenOmittedOnUpdate proves the same
// COALESCE discipline already established for credentials_encrypted/
// mcp_args (tools_update_pg_test.go) applies to action_paths too: a
// name-only update must leave real, previously-stored mappings untouched.
func TestActionPaths_RealDB_PreservedWhenOmittedOnUpdate(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "action-paths-test2-"+orgID.String()[:8], "action-paths-test2-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	origActionPaths := []byte(`{"list":{"path":"/v1/list","method":"GET"}}`)
	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "before-edit", Type: "rest_api", AuthType: "api_key",
		BaseURL:     strPtrTest("https://example.com"),
		ActionPaths: origActionPaths,
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	newName := "after-edit"
	updated, err := q.UpdateTool(ctx, store.UpdateToolParams{
		ID: created.ID, OrgID: orgID,
		Name:        &newName,
		ActionPaths: nil, // omitted, per the handler's contract
	})
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}

	var got map[string]struct {
		Path   string `json:"path"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(updated.ActionPaths, &got); err != nil {
		t.Fatalf("ActionPaths after a name-only edit must still be valid JSON: %v (raw: %s)", err, updated.ActionPaths)
	}
	if got["list"].Path != "/v1/list" || got["list"].Method != "GET" {
		t.Errorf("ActionPaths after a name-only edit = %+v, want unchanged {list: {/v1/list GET}}", got)
	}
}

// TestActionPaths_RealDB_ClearedWhenExplicitlyEmpty proves the other half:
// an explicit []byte("{}") (the handler's "admin cleared all mappings"
// signal) really does overwrite, via COALESCE, a real previously-stored
// non-empty value.
func TestActionPaths_RealDB_ClearedWhenExplicitlyEmpty(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "action-paths-test3-"+orgID.String()[:8], "action-paths-test3-"+orgID.String()); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)

	created, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: "clear-me", Type: "rest_api", AuthType: "api_key",
		BaseURL:     strPtrTest("https://example.com"),
		ActionPaths: []byte(`{"list":{"path":"/v1/list","method":"GET"}}`),
	})
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	updated, err := q.UpdateTool(ctx, store.UpdateToolParams{
		ID: created.ID, OrgID: orgID,
		ActionPaths: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("UpdateTool: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(updated.ActionPaths, &got); err != nil {
		t.Fatalf("ActionPaths after an explicit clear must still be valid JSON: %v (raw: %s)", err, updated.ActionPaths)
	}
	if len(got) != 0 {
		t.Errorf("ActionPaths after an explicit {} update = %v, want empty", got)
	}
}
