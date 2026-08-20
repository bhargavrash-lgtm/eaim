// router_pg_test.go -- eami-gateway/internal/aiprovider
//
// Integration tests for Router.Resolve against a REAL Postgres, mirroring
// toolrouter/router_pg_test.go's established pattern exactly (same
// TEST_DATABASE_URL/POSTGRES_PASSWORD fallback, same throwaway-org
// fixtures cleaned up via t.Cleanup).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/aiprovider/... -v
package aiprovider

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func aiproviderTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/127.0.0.1:5432 layout) to run aiprovider integration tests against a real Postgres")
	}
	return "postgresql://eami_app:" + pw + "@127.0.0.1:5432/eami"
}

type aiproviderTestEnv struct {
	pool  *pgxpool.Pool
	orgID uuid.UUID
}

func newAIProviderTestEnv(t *testing.T) *aiproviderTestEnv {
	t.Helper()
	dsn := aiproviderTestDSN(t)
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
		orgID, "aiprovider-test-"+orgID.String()[:8], "aiprovider-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID)
	})

	return &aiproviderTestEnv{pool: pool, orgID: orgID}
}

func (e *aiproviderTestEnv) insertConnector(t *testing.T, orgID uuid.UUID, name, provider, auditMode string, credsEncrypted []byte) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode, credentials_encrypted)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, $5, $6)
	`, uuid.New(), orgID, name, provider, auditMode, credsEncrypted); err != nil {
		t.Fatalf("insert ai_provider gateway_tools row: %v", err)
	}
}

func (e *aiproviderTestEnv) insertNonProviderTool(t *testing.T, orgID uuid.UUID, name, toolType string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type)
		VALUES ($1, $2, $3, $4, 'api_key')
	`, uuid.New(), orgID, name, toolType); err != nil {
		t.Fatalf("insert gateway_tools row: %v", err)
	}
}

// ─── Resolve ────────────────────────────────────────────────────────────────

func TestResolve_FindsAIProviderConnector(t *testing.T) {
	env := newAIProviderTestEnv(t)
	env.insertConnector(t, env.orgID, "claude", "claude", "full", nil)

	r := New(env.pool, nil, nil)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if row.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", row.Provider)
	}
	if row.AuditMode != "full" {
		t.Errorf("AuditMode = %q, want full", row.AuditMode)
	}
	// data_handling_designation (B-078) was never explicitly set on this
	// insert -- proves Resolve reads back the real DB DEFAULT ('unknown'),
	// not a Go-side zero-value coincidence.
	if row.DataHandling != "unknown" {
		t.Errorf("DataHandling = %q, want the DB default \"unknown\"", row.DataHandling)
	}
}

// TestResolve_DataHandlingDesignation_RoundTrips proves B-078 AC1/AC2's
// prerequisite: a connector with a real, explicitly-set designation
// resolves it correctly, not just the default case above.
func TestResolve_DataHandlingDesignation_RoundTrips(t *testing.T) {
	env := newAIProviderTestEnv(t)
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode, data_handling_designation)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', 'claude', 'full', 'zero_retention')
	`, uuid.New(), env.orgID, "claude-zdr"); err != nil {
		t.Fatalf("insert connector with explicit designation: %v", err)
	}

	r := New(env.pool, nil, nil)
	row, err := r.Resolve(context.Background(), env.orgID.String(), "claude-zdr")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if row.DataHandling != "zero_retention" {
		t.Errorf("DataHandling = %q, want zero_retention", row.DataHandling)
	}
}

func TestResolve_NotFound_ReturnsErrNotFound(t *testing.T) {
	env := newAIProviderTestEnv(t)
	r := New(env.pool, nil, nil)

	_, err := r.Resolve(context.Background(), env.orgID.String(), "never-registered")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestResolve_RestAPIRow_ReturnsErrNotFound proves Resolve is genuinely
// type-scoped in its own SQL (type='ai_provider'), not a shared lookup
// with a post-hoc filter -- a rest_api row with the same name in the same
// org is invisible to this resolver.
func TestResolve_RestAPIRow_ReturnsErrNotFound(t *testing.T) {
	env := newAIProviderTestEnv(t)
	env.insertNonProviderTool(t, env.orgID, "shared-name", "rest_api")

	r := New(env.pool, nil, nil)
	_, err := r.Resolve(context.Background(), env.orgID.String(), "shared-name")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestResolve_CrossOrg_NotFound(t *testing.T) {
	env := newAIProviderTestEnv(t)
	env.insertConnector(t, env.orgID, "claude", "claude", "full", nil)

	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "aiprovider-test-other-"+otherOrgID.String()[:8], "aiprovider-test-other-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)
	})

	r := New(env.pool, nil, nil)
	_, err := r.Resolve(context.Background(), otherOrgID.String(), "claude")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (org isolation)", err)
	}
}
