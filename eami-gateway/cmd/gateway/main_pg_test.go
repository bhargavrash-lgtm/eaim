// main_pg_test.go -- cmd/gateway
//
// Integration tests for resolveDynamicTool (B-044) against a REAL
// Postgres, following the pattern established throughout this session.
// resolveDynamicTool is dispatch's routing decision extracted into a
// named, testable function specifically so this coverage doesn't require
// standing up the full MCP/SSE/dispatch machinery -- see its own doc
// comment in main.go.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestResolveDynamicTool -v
package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/testdb"
	"github.com/eami/gateway/internal/toolrouter"
)

type mainTestEnv struct {
	pool  *pgxpool.Pool
	orgID uuid.UUID
}

// newMainTestEnv (B-122/B-140) provisions a fresh, isolated throwaway
// database per test via internal/testdb -- see that package's doc comment
// for why. The org this env seeds is real, but scoped to this test's own
// throwaway database, so no per-org DELETE cleanup is needed any more: the
// whole database is dropped by testdb.NewThrowawayPool's own t.Cleanup.
func newMainTestEnv(t *testing.T) *mainTestEnv {
	t.Helper()
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "gateway-main-test-"+orgID.String()[:8], "gateway-main-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}

	return &mainTestEnv{pool: pool, orgID: orgID}
}

func (e *mainTestEnv) insertTool(t *testing.T, orgID uuid.UUID, name, toolType string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type)
		VALUES ($1, $2, $3, $4, 'api_key')
	`, uuid.New(), orgID, name, toolType); err != nil {
		t.Fatalf("insert gateway_tools row: %v", err)
	}
}

// TestResolveDynamicTool_RestAPI_ReturnsRow proves AC1's routing decision:
// a registered rest_api tool resolves to a non-nil row.
func TestResolveDynamicTool_RestAPI_ReturnsRow(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "my-rest-tool", "rest_api")

	row := resolveDynamicTool(context.Background(), toolrouter.New(env.pool, nil), env.orgID.String(), "my-rest-tool")
	if row == nil {
		t.Fatal("expected a resolved row for a registered rest_api tool, got nil")
	}
	if row.Type != "rest_api" {
		t.Errorf("Type = %q, want rest_api", row.Type)
	}
}

// TestResolveDynamicTool_UnregisteredName_ReturnsNil proves AC5/AC6's
// fallback design: a tool name with no matching gateway_tools row at all
// falls back to the static path (nil), not a rejection -- see main.go's
// resolveDynamicTool doc comment and BACKLOG.md B-044 for why.
func TestResolveDynamicTool_UnregisteredName_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)

	row := resolveDynamicTool(context.Background(), toolrouter.New(env.pool, nil), env.orgID.String(), "never-registered")
	if row != nil {
		t.Errorf("expected nil (fallback to static) for an unregistered tool name, got %+v", row)
	}
}

// TestResolveDynamicTool_McpType_ReturnsNil and
// TestResolveDynamicTool_DatabaseType_ReturnsNil prove AC6: mcp/database-
// type gateway_tools rows are out of scope this brief and must fall back
// to the static path exactly like an unregistered name, not be treated as
// "found" for dynamic dispatch purposes.
func TestResolveDynamicTool_McpType_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "my-mcp-tool", "mcp")

	row := resolveDynamicTool(context.Background(), toolrouter.New(env.pool, nil), env.orgID.String(), "my-mcp-tool")
	if row != nil {
		t.Errorf("expected nil (fallback to static) for an mcp-type tool, got %+v", row)
	}
}

func TestResolveDynamicTool_DatabaseType_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "my-db-tool", "database")

	row := resolveDynamicTool(context.Background(), toolrouter.New(env.pool, nil), env.orgID.String(), "my-db-tool")
	if row != nil {
		t.Errorf("expected nil (fallback to static) for a database-type tool, got %+v", row)
	}
}

// TestResolveDynamicTool_CrossOrg_ReturnsNil proves AC4 at the dispatch
// decision level (toolrouter's own TestResolve_CrossOrg_NotFound proves it
// at the Resolve layer directly): an agent from one org must never
// dynamically route to a different org's identically-named tool.
func TestResolveDynamicTool_CrossOrg_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "shared-name", "rest_api")

	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "gateway-main-test-other-"+otherOrgID.String()[:8], "gateway-main-test-other-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)
	})

	row := resolveDynamicTool(context.Background(), toolrouter.New(env.pool, nil), otherOrgID.String(), "shared-name")
	if row != nil {
		t.Errorf("expected nil for a cross-org lookup (org isolation), got %+v", row)
	}
}

// TestResolveDynamicTool_EmptyOrgOrTool_ReturnsNil is a defensive guard --
// resolveDynamicTool must not attempt a DB lookup with an empty org/tool
// (which would be a malformed ActionContext, not a real routing decision).
func TestResolveDynamicTool_EmptyOrgOrTool_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	tr := toolrouter.New(env.pool, nil)

	if row := resolveDynamicTool(context.Background(), tr, "", "some-tool"); row != nil {
		t.Errorf("expected nil for empty orgID, got %+v", row)
	}
	if row := resolveDynamicTool(context.Background(), tr, env.orgID.String(), ""); row != nil {
		t.Errorf("expected nil for empty tool name, got %+v", row)
	}
}
