// workflow_step_params_test.go -- eami-api/internal/api
// Real-Postgres integration tests for Multi-Hop Workflows Brief 2's
// static per-step parameters endpoint (B-059). Follows workflows_test.go's
// established real-Postgres/real-HTTP conventions exactly.
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

func seedWorkflowWithStep(t *testing.T, ctx context.Context, q *store.Queries, orgID, toolID uuid.UUID) uuid.UUID {
	t.Helper()
	wf, err := q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: orgID, Name: "params-test-workflow", Status: "draft"})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if err := q.InsertWorkflowStep(ctx, uuid.New(), wf.ID, 0, toolID, "query", nil); err != nil {
		t.Fatalf("InsertWorkflowStep: %v", err)
	}
	steps, err := q.ListWorkflowSteps(ctx, wf.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("ListWorkflowSteps: %v (len=%d)", err, len(steps))
	}
	return steps[0].ID
}

// TestWorkflowStepParams_RealDB_PutThenGet_RoundTrips proves the store-
// level UPSERT/GET pair works against real Postgres, including the
// "GET on a step with no params yet returns {}, not 404" case.
func TestWorkflowStepParams_RealDB_PutThenGet_RoundTrips(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- must run AFTER seedTestOrg's
	// t.Cleanup-registered DELETE (a plain defer here would close the pool
	// first, silently no-op-ing the cleanup -- the exact B-056-class bug
	// already found and fixed once this session in workflows_test.go).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "wsp-roundtrip")
	toolID := seedTestTool(t, ctx, q, orgID, "wsp-tool")
	stepID := seedWorkflowWithStep(t, ctx, q, orgID, toolID)

	// No params configured yet: GetWorkflowStepParams returns pgx.ErrNoRows,
	// which the handler (not tested at this store level) turns into {}.
	if _, err := q.GetWorkflowStepParams(ctx, stepID, orgID); err != pgx.ErrNoRows {
		t.Errorf("GetWorkflowStepParams before any PUT: err = %v, want pgx.ErrNoRows", err)
	}

	raw, err := q.UpsertWorkflowStepParams(ctx, stepID, orgID, []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("UpsertWorkflowStepParams: %v", err)
	}
	var got map[string]any
	json.Unmarshal(raw, &got)
	if got["a"] != float64(1) {
		t.Errorf("UpsertWorkflowStepParams returned %s, want {\"a\":1}", raw)
	}

	// Full replace, not merge: a second PUT with a different shape
	// completely replaces the first.
	raw2, err := q.UpsertWorkflowStepParams(ctx, stepID, orgID, []byte(`{"b":2}`))
	if err != nil {
		t.Fatalf("UpsertWorkflowStepParams (replace): %v", err)
	}
	var got2 map[string]any
	json.Unmarshal(raw2, &got2)
	if _, stillThere := got2["a"]; stillThere {
		t.Errorf("second PUT = %s, old key \"a\" should be gone (full replace, not merge)", raw2)
	}
	if got2["b"] != float64(2) {
		t.Errorf("second PUT = %s, want {\"b\":2}", raw2)
	}

	rawGet, err := q.GetWorkflowStepParams(ctx, stepID, orgID)
	if err != nil {
		t.Fatalf("GetWorkflowStepParams after PUT: %v", err)
	}
	var gotGet map[string]any
	json.Unmarshal(rawGet, &gotGet)
	if gotGet["b"] != float64(2) {
		t.Errorf("GET after PUT = %s, want {\"b\":2}", rawGet)
	}
}

// TestWorkflowStepParams_RealDB_CrossOrg_Rejected proves the org-ownership
// guard: org B cannot read or write org A's step params.
func TestWorkflowStepParams_RealDB_CrossOrg_Rejected(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- must run AFTER seedTestOrg's
	// t.Cleanup-registered DELETE (a plain defer here would close the pool
	// first, silently no-op-ing the cleanup -- the exact B-056-class bug
	// already found and fixed once this session in workflows_test.go).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgA := seedTestOrg(t, ctx, pool, "wsp-crossorg-a")
	orgB := seedTestOrg(t, ctx, pool, "wsp-crossorg-b")
	toolID := seedTestTool(t, ctx, q, orgA, "wsp-crossorg-tool")
	stepID := seedWorkflowWithStep(t, ctx, q, orgA, toolID)

	belongs, err := q.WorkflowStepBelongsToOrg(ctx, stepID, orgB)
	if err != nil {
		t.Fatalf("WorkflowStepBelongsToOrg: %v", err)
	}
	if belongs {
		t.Error("WorkflowStepBelongsToOrg(org A's step, org B) = true, want false")
	}

	if _, err := q.GetWorkflowStepParams(ctx, stepID, orgB); err != pgx.ErrNoRows {
		t.Errorf("GetWorkflowStepParams(orgB) err = %v, want pgx.ErrNoRows", err)
	}
	if _, err := q.UpsertWorkflowStepParams(ctx, stepID, orgB, []byte(`{"hijack":true}`)); err != pgx.ErrNoRows {
		t.Errorf("UpsertWorkflowStepParams(orgB) err = %v, want pgx.ErrNoRows (no row written)", err)
	}

	// Confirm nothing was actually written for org B's attempt.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_step_params WHERE workflow_step_id = $1`, stepID).Scan(&count); err != nil {
		t.Fatalf("count workflow_step_params: %v", err)
	}
	if count != 0 {
		t.Errorf("workflow_step_params row count after a rejected cross-org PUT = %d, want 0", count)
	}
}

// TestWorkflowStepParams_HTTP_PutRejectsNonObjectBody proves the handler-
// level validation: params must be a JSON object (they become
// mcp.ActionContext.Parameters, a map, at dispatch time).
func TestWorkflowStepParams_HTTP_PutRejectsNonObjectBody(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- must run AFTER seedTestOrg's
	// t.Cleanup-registered DELETE (a plain defer here would close the pool
	// first, silently no-op-ing the cleanup -- the exact B-056-class bug
	// already found and fixed once this session in workflows_test.go).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "wsp-http-nonobj")
	toolID := seedTestTool(t, ctx, q, orgID, "wsp-http-tool")
	stepID := seedWorkflowWithStep(t, ctx, q, orgID, toolID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	token, _, err := authSvc.IssueAccessToken(uuid.New(), orgID, "admin@wsp-test.example", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/v1/gateway/workflow-steps/"+stepID.String()+"/params", bytes.NewReader([]byte(`[1,2,3]`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT params: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-object params body", resp.StatusCode)
	}
}
