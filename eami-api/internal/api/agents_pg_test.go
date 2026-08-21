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

// TestDeleteAgent_RealDB_WithRealHistory_Returns409NotRawError reproduces
// B-077's exact original failure live against real Postgres: an agent with
// a real episode referencing it could not be deleted at all before this
// brief -- the FK violation (episodes.agent_id is NO ACTION, deliberately
// not cascaded) surfaced as a raw 500 with a leaked driver error string.
// Proves the fix (AC3), that create/suspend/delete each leave a real
// agent_lifecycle_events row (AC4), and that suspend remains a working
// alternative to a blocked delete.
func TestDeleteAgent_RealDB_WithRealHistory_Returns409NotRawError(t *testing.T) {
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
	orgID := seedTestOrg(t, ctx, pool, "agents-b077")
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@agents-b077.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	doReq := func(method, path string, body any) *http.Response {
		var reader *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, ts.URL+path, reader)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// AC1 + AC4 (created): a real agent, created through the real handler.
	createResp := doReq(http.MethodPost, "/v1/gateway/agents", map[string]any{
		"name": "b077-history-agent", "model": "claude-sonnet-4-6", "owner": "qa-team",
		"scope": "Reproduce B-077's real-history deletion scenario", "risk_tier": "low",
	})
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", createResp.StatusCode)
	}
	var agent api.AgentResp
	if err := json.NewDecoder(createResp.Body).Decode(&agent); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	agentID := uuidFromString(t, agent.ID)

	assertLifecycleEvent := func(eventType string) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM agent_lifecycle_events WHERE org_id = $1 AND agent_id = $2 AND event_type = $3`,
			orgID, agentID, eventType,
		).Scan(&count); err != nil {
			t.Fatalf("count agent_lifecycle_events(%s): %v", eventType, err)
		}
		if count != 1 {
			t.Errorf("agent_lifecycle_events rows for event_type=%q = %d, want exactly 1 (AC4: every lifecycle action must be traceable)", eventType, count)
		}
	}
	assertLifecycleEvent("created")

	// B-077's exact real-history scenario: a real episode row referencing
	// this agent -- the same class of row (episodes/approval_requests/
	// workflow_runs) whose NO ACTION FK originally 500'd DeleteAgent.
	if _, err := pool.Exec(ctx,
		`INSERT INTO episodes (org_id, agent_id, agent_name, task, outcome) VALUES ($1, $2, $3, 'B-077 repro task', 'success')`,
		orgID, agentID, agent.Name,
	); err != nil {
		t.Fatalf("seed real episode: %v", err)
	}

	// AC3: delete must now be a clean 409, never a raw 500.
	deleteResp := doReq(http.MethodDelete, "/v1/gateway/agents/"+agent.ID, nil)
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusConflict {
		t.Fatalf("delete with real history: status = %d, want 409 (not a raw 500 -- this is B-077's exact original bug)", deleteResp.StatusCode)
	}
	var errResp api.ErrorResponse
	if err := json.NewDecoder(deleteResp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if errResp.Code != "conflict" {
		t.Errorf("error code = %q, want %q", errResp.Code, "conflict")
	}
	if errResp.Message == "" {
		t.Error("blocked-delete response must carry a non-empty, actionable message")
	}

	// Agent must still exist -- the blocked delete must not have partially
	// applied anything.
	var stillExists int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gateway_agents WHERE id = $1`, agentID).Scan(&stillExists); err != nil {
		t.Fatalf("count gateway_agents: %v", err)
	}
	if stillExists != 1 {
		t.Errorf("gateway_agents row after blocked delete = %d, want 1 (must still exist)", stillExists)
	}
	// No spurious "deleted" lifecycle row for a delete that didn't happen.
	var deletedEventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_lifecycle_events WHERE org_id = $1 AND agent_id = $2 AND event_type = 'deleted'`,
		orgID, agentID,
	).Scan(&deletedEventCount); err != nil {
		t.Fatalf("count deleted events: %v", err)
	}
	if deletedEventCount != 0 {
		t.Errorf("agent_lifecycle_events has %d 'deleted' rows after a BLOCKED delete, want 0", deletedEventCount)
	}

	// AC2 + AC4 (suspended): suspend must still work as the real
	// alternative to a blocked hard delete.
	suspendResp := doReq(http.MethodPatch, "/v1/gateway/agents/"+agent.ID, map[string]any{"status": "suspended"})
	defer suspendResp.Body.Close()
	if suspendResp.StatusCode != http.StatusOK {
		t.Fatalf("suspend: status = %d, want 200", suspendResp.StatusCode)
	}
	var suspended api.AgentResp
	if err := json.NewDecoder(suspendResp.Body).Decode(&suspended); err != nil {
		t.Fatalf("decode suspend response: %v", err)
	}
	if suspended.Status != "suspended" {
		t.Errorf("status after suspend = %q, want %q", suspended.Status, "suspended")
	}
	assertLifecycleEvent("suspended")
}

func uuidFromString(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}
