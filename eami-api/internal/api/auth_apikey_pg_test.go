// auth_apikey_pg_test.go -- eami-api/internal/api
// Real-Postgres integration tests for CreateAPIKey's new agent_id/expires_at
// fields (B-098) -- previously the api_keys.expires_at column existed but
// was never accepted at the API layer, and agent_id didn't exist at all.
// Follows tools_update_pg_test.go/workflows_test.go's established
// TEST_DATABASE_URL/POSTGRES_PASSWORD convention and t.Cleanup-only pool
// lifecycle (CLAUDE.md's mandatory pattern).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestCreateAPIKey_RealDB -v
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// TestCreateAPIKey_RealDB_PersistsAgentIDAndExpiresAt proves the store-level
// round trip: a key created with both fields set is readable back via
// GetAPIKeyByHash (the same lookup eami-gateway's IssueHandler performs)
// with both fields intact.
func TestCreateAPIKey_RealDB_PersistsAgentIDAndExpiresAt(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "b098-apikey-store-test")
	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "b098-apikey-agent", Model: "claude-sonnet-5", Owner: "test@example.com", Scope: "read:test", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("seed test agent: %v", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	keyHash := "b098-store-test-hash-" + uuid.New().String()
	k, err := q.CreateAPIKey(ctx, store.CreateAPIKeyParams{
		OrgID:     orgID,
		Name:      "b098-store-test-key",
		KeyHash:   keyHash,
		Prefix:    "eami_k_test_",
		Scopes:    []string{},
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
		AgentID:   pgtype.UUID{Bytes: agent.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !k.AgentID.Valid || uuid.UUID(k.AgentID.Bytes) != agent.ID {
		t.Errorf("AgentID = %+v, want valid, matching %s", k.AgentID, agent.ID)
	}
	if !k.ExpiresAt.Valid || !k.ExpiresAt.Time.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %+v, want valid, equal to %s", k.ExpiresAt, expiresAt)
	}

	// GetAPIKeyByHash is the same lookup eami-gateway's IssueHandler
	// performs -- proves the round trip end to end, not just CreateAPIKey's
	// own RETURNING clause.
	got, err := q.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		t.Fatalf("GetAPIKeyByHash: %v", err)
	}
	if !got.AgentID.Valid || uuid.UUID(got.AgentID.Bytes) != agent.ID {
		t.Errorf("GetAPIKeyByHash AgentID = %+v, want valid, matching %s", got.AgentID, agent.ID)
	}
	if !got.ExpiresAt.Valid || !got.ExpiresAt.Time.Equal(expiresAt) {
		t.Errorf("GetAPIKeyByHash ExpiresAt = %+v, want valid, equal to %s", got.ExpiresAt, expiresAt)
	}
}

// TestGetAPIKeyByHash_RealDB_ExcludesExpiredKey proves the other code-review
// finding fix: GetAPIKeyByHash now filters out an expired key, matching
// eami-gateway's own pgAPIKeyValidator.ValidateAPIKey behavior for the
// identical row -- previously this query only checked revoked, so an
// expired-but-unrevoked key still matched it.
func TestGetAPIKeyByHash_RealDB_ExcludesExpiredKey(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "b098-apikey-expired-lookup-test")

	keyHash := "b098-expired-lookup-hash-" + uuid.New().String()
	if _, err := q.CreateAPIKey(ctx, store.CreateAPIKeyParams{
		OrgID:     orgID,
		Name:      "b098-expired-lookup-key",
		KeyHash:   keyHash,
		Prefix:    "eami_k_test_",
		Scopes:    []string{},
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if _, err := q.GetAPIKeyByHash(ctx, keyHash); err == nil {
		t.Fatal("expected GetAPIKeyByHash to exclude an expired key, got a row")
	}
}

// TestCreateAPIKey_HTTP_RejectsSuspendedAgent proves the code-review
// finding fix: binding a new key to an agent that already exists in the
// caller's own org but is suspended/revoked is rejected up front (400),
// rather than silently minting a key that could never authorize issuance.
func TestCreateAPIKey_HTTP_RejectsSuspendedAgent(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "b098-apikey-suspended-test")
	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "b098-apikey-suspended-agent", Model: "claude-sonnet-5", Owner: "test@example.com", Scope: "read:test", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("seed test agent: %v", err)
	}
	if _, err := q.UpdateAgent(ctx, store.UpdateAgentParams{
		ID: agent.ID, OrgID: orgID, Status: pgtype.Text{String: "suspended", Valid: true},
	}); err != nil {
		t.Fatalf("suspend test agent: %v", err)
	}

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	userID := seedTestUser(t, ctx, pool, orgID)
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@suspended-test.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name":     "suspended-agent-attempt",
		"agent_id": agent.ID.String(),
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/auth/api-keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/auth/api-keys: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, respBody)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE org_id = $1`, orgID).Scan(&count); err != nil {
		t.Fatalf("count api_keys: %v", err)
	}
	if count != 0 {
		t.Errorf("api_keys created for a suspended agent_id = %d, want 0", count)
	}
}

// TestCreateAPIKey_HTTP_RejectsAgentIDFromDifferentOrg proves the actual
// validation this brief added: an agent_id belonging to a DIFFERENT org
// than the caller's own is rejected (400), not silently bound -- otherwise
// a key could end up scoped to another org's agent. Mirrors
// TestWorkflows_HTTP_CreateWorkflow_RejectsCrossOrgToolReference's shape.
func TestCreateAPIKey_HTTP_RejectsAgentIDFromDifferentOrg(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgA := seedTestOrg(t, ctx, pool, "b098-apikey-http-a")
	orgB := seedTestOrg(t, ctx, pool, "b098-apikey-http-b")
	agentB, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgB, Name: "b098-apikey-agent-b", Model: "claude-sonnet-5", Owner: "test@example.com", Scope: "read:test", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("seed org B test agent: %v", err)
	}

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	userA := seedTestUser(t, ctx, pool, orgA)
	token, _, err := authSvc.IssueAccessToken(userA, orgA, "admin@org-a.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name":     "cross-org-attempt",
		"agent_id": agentB.ID.String(),
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/auth/api-keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/auth/api-keys: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE org_id = $1`, orgA).Scan(&count); err != nil {
		t.Fatalf("count api_keys: %v", err)
	}
	if count != 0 {
		t.Errorf("api_keys created for org A after a rejected cross-org agent_id = %d, want 0", count)
	}
}

// TestCreateAPIKey_HTTP_AgentIDAndExpiresAt_RoundTrip proves the full HTTP
// path end to end: a same-org agent_id and a date-only expires_at are both
// accepted and both come back correctly in the response.
func TestCreateAPIKey_HTTP_AgentIDAndExpiresAt_RoundTrip(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "b098-apikey-http-roundtrip")
	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "b098-apikey-roundtrip-agent", Model: "claude-sonnet-5", Owner: "test@example.com", Scope: "read:test", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("seed test agent: %v", err)
	}

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	userID := seedTestUser(t, ctx, pool, orgID)
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@roundtrip.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	expiresDate := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	body, _ := json.Marshal(map[string]any{
		"name":       "roundtrip-key",
		"agent_id":   agent.ID.String(),
		"expires_at": expiresDate,
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/auth/api-keys", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/auth/api-keys: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201, body = %s", resp.StatusCode, respBody)
	}

	var out api.CreateAPIKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Key == "" {
		t.Error("expected a non-empty raw key in the response")
	}
	if out.Meta.AgentID == nil || *out.Meta.AgentID != agent.ID.String() {
		t.Errorf("Meta.AgentID = %v, want %s", out.Meta.AgentID, agent.ID)
	}
	if out.Meta.ExpiresAt == nil {
		t.Fatal("expected a non-nil Meta.ExpiresAt")
	}
	if got := out.Meta.ExpiresAt.Format("2006-01-02"); got != expiresDate {
		t.Errorf("Meta.ExpiresAt date = %q, want %q", got, expiresDate)
	}

	var gotAgentID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT agent_id FROM api_keys WHERE id = $1`, out.Meta.ID).Scan(&gotAgentID); err != nil {
		t.Fatalf("query persisted agent_id: %v", err)
	}
	if gotAgentID != agent.ID {
		t.Errorf("persisted agent_id = %s, want %s", gotAgentID, agent.ID)
	}
}
