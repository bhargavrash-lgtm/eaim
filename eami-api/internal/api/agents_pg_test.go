// agents_pg_test.go -- eami-api/internal/api
// Real-Postgres integration test for B-074: gateway_agents' pre-existing
// UNIQUE (org_id, name) constraint (confirmed live against the running DB
// before this brief -- not new) must surface as a clean 409 from the
// CreateAgent HTTP handler, not an opaque 500 with a leaked driver error
// string. Follows workflows_test.go's toolsUpdateTestDSN/seedTestOrg/
// api.NewServer HTTP-level convention exactly.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestCreateAgent_RealDB -v
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// TestCreateAgent_RealDB_DuplicateNameInSameOrg_Returns409NotDuplicate proves
// AC1: a second agent-create attempt with a name already used in the same
// org is rejected with a clean 409, and the first row is left completely
// untouched (no duplicate row, no corruption of the original).
func TestCreateAgent_RealDB_DuplicateNameInSameOrg_Returns409NotDuplicate(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- see workflows_test.go's identical
	// comment / BACKLOG.md B-056 for why: t.Cleanup runs LIFO, strictly
	// after this function's own defers, so a plain defer pool.Close() here
	// would close the pool before the org-delete t.Cleanup registered by
	// seedTestOrg below ever got a chance to run.
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "agents-dup")
	// gateway_agents.created_by is FK-constrained to users(id) -- a random
	// uuid.New() JWT subject (no seeded row) would fail every insert with
	// an FK violation, not the unique-violation this test targets.
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@agents-dup.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	payload := map[string]any{
		"name":      "duplicate-name-agent",
		"model":     "claude-sonnet-4-6",
		"owner":     "qa-team",
		"scope":     "Run read-only queries against the CRM for ticket triage",
		"risk_tier": "low",
	}
	post := func() *http.Response {
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/gateway/agents", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/gateway/agents: %v", err)
		}
		return resp
	}

	// First create: must succeed.
	resp1 := post()
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create: status = %d, want 201", resp1.StatusCode)
	}
	var first api.AgentResp
	if err := json.NewDecoder(resp1.Body).Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	// Second create, same org + same name: must be a clean 409, never a 500.
	resp2 := post()
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate create: status = %d, want 409", resp2.StatusCode)
	}
	var errResp api.ErrorResponse
	if err := json.NewDecoder(resp2.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if errResp.Code != "conflict" {
		t.Errorf("error code = %q, want %q", errResp.Code, "conflict")
	}
	if errResp.Message == "" {
		t.Error("conflict response must carry a non-empty, human-readable message")
	}

	// Exactly one row for (org_id, name) -- the first row, untouched.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gateway_agents WHERE org_id = $1 AND name = $2`,
		orgID, "duplicate-name-agent",
	).Scan(&count); err != nil {
		t.Fatalf("count gateway_agents: %v", err)
	}
	if count != 1 {
		t.Errorf("gateway_agents rows for (org_id, name) = %d, want 1 (first row must be untouched, no duplicate)", count)
	}

	var storedModel string
	if err := pool.QueryRow(ctx,
		`SELECT model FROM gateway_agents WHERE id = $1`, uuidFromString(t, first.ID),
	).Scan(&storedModel); err != nil {
		t.Fatalf("re-fetch first row: %v", err)
	}
	if storedModel != "claude-sonnet-4-6" {
		t.Errorf("first row's model = %q after rejected duplicate, want unchanged %q", storedModel, "claude-sonnet-4-6")
	}
}

func uuidFromString(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}
