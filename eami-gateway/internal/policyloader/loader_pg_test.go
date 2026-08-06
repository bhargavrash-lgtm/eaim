// loader_pg_test.go -- eami-gateway/internal/policyloader
//
// Integration test for Loader.Load's real DB round trip of
// policy_conditions.tool_server_ids (B-044/migration 009), against a REAL
// Postgres, following the pattern established throughout this session
// (TEST_DATABASE_URL/POSTGRES_PASSWORD fallback, throwaway fixtures via
// t.Cleanup).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/policyloader/... -v
package policyloader

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	policy "github.com/eami/policy"
)

func loaderTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/127.0.0.1:5432 layout) to run policyloader integration tests against a real Postgres")
	}
	return "postgresql://eami_app:" + pw + "@127.0.0.1:5432/eami"
}

// TestLoad_ToolServerIDs_RealDBRoundTrip proves AC2's DB-level wiring: a
// policy rule authored with tool_server_ids in Postgres is correctly
// scanned into Conditions.ToolServerIDs by queryRules, and the resulting
// evaluator honors it -- matches a call resolved to that specific
// gateway_tools.id, does not match a different one or an unresolved call.
func TestLoad_ToolServerIDs_RealDBRoundTrip(t *testing.T) {
	dsn := loaderTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "policyloader-test-"+orgID.String()[:8], "policyloader-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)
	})

	targetServerID := uuid.New().String()

	policyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, name, priority, action, status)
		VALUES ($1, $2, $3, 1, 'deny', 'active')
	`, policyID, orgID, "policyloader-test-rule"); err != nil {
		t.Fatalf("insert policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_server_ids)
		VALUES ($1, $2, $3)
	`, uuid.New(), policyID, []string{targetServerID}); err != nil {
		t.Fatalf("insert policy_conditions: %v", err)
	}

	loader := New(pool)
	if err := loader.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ev := loader.Evaluator()

	matching, err := ev.Evaluate(ctx, policy.ActionContext{ToolServerID: targetServerID})
	if err != nil {
		t.Fatalf("Evaluate (matching): %v", err)
	}
	if matching.Action != policy.ActionDeny {
		t.Errorf("Evaluate with matching ToolServerID: Action = %q, want %q", matching.Action, policy.ActionDeny)
	}

	nonMatching, err := ev.Evaluate(ctx, policy.ActionContext{ToolServerID: uuid.New().String()})
	if err != nil {
		t.Fatalf("Evaluate (non-matching): %v", err)
	}
	if nonMatching.Action == policy.ActionDeny {
		t.Errorf("Evaluate with a different ToolServerID: Action = %q, want default (not deny)", nonMatching.Action)
	}

	unresolved, err := ev.Evaluate(ctx, policy.ActionContext{ToolServerID: ""})
	if err != nil {
		t.Fatalf("Evaluate (unresolved): %v", err)
	}
	if unresolved.Action == policy.ActionDeny {
		t.Errorf("Evaluate with an unresolved (empty) ToolServerID: Action = %q, want default (not deny)", unresolved.Action)
	}
}
