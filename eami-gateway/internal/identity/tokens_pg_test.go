// tokens_pg_test.go -- eami-gateway/internal/identity
//
// Integration tests for the DB-backed revocation store against a REAL
// Postgres, deliberately deviating from this package's usual
// fake/file-store test convention (see tokens_test.go): the bug this file
// exists to catch -- an INSERT/SELECT referencing a column
// (revoked_ai_tokens.expires_at) that has never existed on the real table,
// and an INSERT omitting the real NOT NULL agent_id column -- is exactly
// the class of bug a fake store can never surface, since a fake has no
// real schema to violate. Follows the same pattern as
// internal/approval/router_pg_test.go (B-039).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/identity/... -run TestDBRevocation -v
package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/gateway/internal/testdb"
)

// ─── shared test env ────────────────────────────────────────────────────────

// identityTestEnv wires a real *pgxpool.Pool against a real Postgres and a
// throwaway org + gateway_agents row for revoked_ai_tokens.agent_id's FK
// target.
type identityTestEnv struct {
	pool    *pgxpool.Pool
	orgID   uuid.UUID
	agentID uuid.UUID
}

// newIdentityTestEnv (B-122/B-140) provisions a fresh, isolated throwaway
// database per test via internal/testdb -- see that package's doc comment
// for why. No per-org DELETE cleanup is needed any more: the whole
// database is dropped by testdb.NewThrowawayPool's own t.Cleanup.
func newIdentityTestEnv(t *testing.T) *identityTestEnv {
	t.Helper()
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "identity-token-test-"+orgID.String()[:8], "identity-token-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}

	agentID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, agentID, orgID, "identity-token-test-agent", "claude-sonnet-5", "test@example.com", "read:test"); err != nil {
		t.Fatalf("insert test agent: %v", err)
	}

	return &identityTestEnv{pool: pool, orgID: orgID, agentID: agentID}
}

// ─── dbRevocationStore: real schema round trip ────────────────────────────────

// TestDBRevocation_Save_WritesRealRow proves save() no longer fails against
// the real revoked_ai_tokens schema (jti, agent_id, revoked_at, reason --
// no expires_at) and that the row it writes matches what loadAll expects
// to read back.
func TestDBRevocation_Save_WritesRealRow(t *testing.T) {
	env := newIdentityTestEnv(t)
	store := &dbRevocationStore{pool: env.pool}
	jti := "test-jti-" + uuid.New().String()[:8]

	if err := store.save(context.Background(), jti, env.agentID.String()); err != nil {
		t.Fatalf("save: %v", err)
	}

	var gotAgentID uuid.UUID
	err := env.pool.QueryRow(context.Background(),
		`SELECT agent_id FROM revoked_ai_tokens WHERE jti = $1`, jti).Scan(&gotAgentID)
	if err != nil {
		t.Fatalf("row not found after save: %v", err)
	}
	if gotAgentID != env.agentID {
		t.Errorf("agent_id: got %v want %v", gotAgentID, env.agentID)
	}
}

// TestDBRevocation_LoadAll_ReturnsSavedJTIs proves loadAll()'s query
// actually executes against the real table (no more "column expires_at
// does not exist") and returns everything saved, since revocation here is
// permanent (no expiry column exists on this table at all).
func TestDBRevocation_LoadAll_ReturnsSavedJTIs(t *testing.T) {
	env := newIdentityTestEnv(t)
	store := &dbRevocationStore{pool: env.pool}
	jti := "test-jti-" + uuid.New().String()[:8]

	if err := store.save(context.Background(), jti, env.agentID.String()); err != nil {
		t.Fatalf("save: %v", err)
	}

	jtis, err := store.loadAll(context.Background())
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	found := false
	for _, got := range jtis {
		if got == jti {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("loadAll did not return saved jti %q; got %v", jti, jtis)
	}
}

// ─── Manager: restart-simulation, real Postgres ───────────────────────────────

// TestManagerWithDB_Validate_RevokedToken_SurvivesRestart mirrors
// tokens_test.go's fake-store TestManager_Validate_RevokedToken_SurvivesRestart,
// but backed by NewManagerWithDB against a real pool -- the scenario the
// fake/file-store version can never catch, since only the DB-backed path
// was ever broken.
func TestManagerWithDB_Validate_RevokedToken_SurvivesRestart(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"

	// Manager A issues and revokes a token. AgentID is deliberately issued
	// in the real "agent:<name>" subject shape (internal/registry's
	// convention, matching what internal/mcp and internal/episode actually
	// see in production), not a raw UUID -- Claims.Subject is NEVER a
	// gateway_agents.id UUID directly. Revoke's agentID must be the
	// resolved UUID, so this test resolves it itself the same way a real
	// caller (a future POST /v1/gateway/tokens/{token}/revoke handler)
	// would have to.
	mA, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB (A): %v", err)
	}
	resp, err := mA.Issue(IssueRequest{AgentID: "agent:identity-token-test-agent", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claimsA, err := mA.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revocation Validate: %v", err)
	}
	if claimsA.Subject == env.agentID.String() {
		t.Fatal("test setup bug: Subject should be the \"agent:<name>\" form, not already a UUID")
	}
	if err := mA.Revoke(claimsA.ID, env.agentID.String()); err != nil { // the resolved UUID, not claimsA.Subject
		t.Fatalf("Revoke: %v", err)
	}

	// Simulate a gateway restart: new Manager B, same key file, same DB.
	mB, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB (B): %v", err)
	}

	_, err = mB.Validate(resp.Token)
	if err == nil {
		t.Fatal("expected error: token revoked before restart was accepted by a fresh Manager")
	}
}

// TestNewManagerWithDB_HydrationFailure_IsFatal proves a hydration failure
// (e.g. an unreachable database at startup) is returned as an error --
// never a silent WARN-and-proceed-with-an-empty-list, which is the exact
// failure mode B-041 closes.
func TestNewManagerWithDB_HydrationFailure_IsFatal(t *testing.T) {
	// A pool pointed at a port nothing listens on: any query against it
	// fails with a connection error, exercising loadAll's error path
	// without depending on a specific SQL/schema mismatch.
	badPool, err := pgxpool.New(context.Background(), "postgresql://eami_app:wrong@127.0.0.1:1/eami")
	if err != nil {
		t.Fatalf("pgxpool.New (lazy, should not itself fail): %v", err)
	}
	defer badPool.Close()

	keyPath := t.TempDir() + "/gateway.key"
	_, err = NewManagerWithDB(keyPath, 300, "eami-gateway", badPool)
	if err == nil {
		t.Fatal("expected NewManagerWithDB to return an error when hydration fails, got nil")
	}
}
