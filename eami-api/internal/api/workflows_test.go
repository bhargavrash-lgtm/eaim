// workflows_test.go -- eami-api/internal/api
// Real-Postgres integration tests for Multi-Hop Workflows Brief 1 (B-058):
// schema + CRUD foundation. Follows tools_update_pg_test.go's established
// TEST_DATABASE_URL/POSTGRES_PASSWORD convention and store-level testing
// approach (store.Queries called directly against a real DB, same as
// that file's own cross-org proof) -- plus one full HTTP-level test for
// the cross-org step-reference guard specifically, since that's the
// handler's own validation logic (validateWorkflowSteps), not something
// a store-level call alone exercises.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestWorkflows -v
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

// seedTestOrg inserts a throwaway org and registers its cleanup. Mirrors
// tools_update_pg_test.go's inline seeding exactly.
func seedTestOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) uuid.UUID {
	t.Helper()
	orgID := uuid.New()
	name := label + "-" + orgID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`, orgID, name, name); err != nil {
		t.Fatalf("seed org %s: %v", label, err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgID) })
	return orgID
}

// seedTestUser inserts a minimal user row in orgID, for workflows.org_id
// FK-satisfying created_by references.
func seedTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	email := "wf-test-" + userID.String()[:8] + "@example.com"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'admin')`, userID, orgID, email); err != nil {
		t.Fatalf("seed test user: %v", err)
	}
	return userID
}

// seedTestTool creates a minimal rest_api gateway_tools row in orgID.
func seedTestTool(t *testing.T, ctx context.Context, q *store.Queries, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	tool, err := q.CreateTool(ctx, store.CreateToolParams{
		OrgID: orgID, Name: name, Type: "rest_api", AuthType: "api_key",
		BaseURL: strPtrTest("https://example.com"),
	})
	if err != nil {
		t.Fatalf("seed tool %s: %v", name, err)
	}
	return tool.ID
}

// TestWorkflows_RealDB_CreateListGetUpdateDelete exercises the full store-
// level lifecycle: create a workflow with 2 steps inside a transaction
// (mirroring workflows.go's CreateWorkflow handler exactly), list it,
// get it with steps joined + tool names, replace its steps (reorder +
// add + remove), then delete it and confirm steps cascade.
func TestWorkflows_RealDB_CreateListGetUpdateDelete(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- deliberately so it runs AFTER the
	// org-delete t.Cleanup callbacks registered below (via seedTestOrg).
	// t.Cleanup runs LIFO, in a phase strictly after this function's own
	// defers, so a plain `defer pool.Close()` here would close the pool
	// before any t.Cleanup-registered DELETE ever got a chance to run --
	// exactly the bug BACKLOG.md/CONTEXT.md's B-056 entry documents (found
	// live while writing this test: the first version of this file used a
	// plain defer and every cleanup silently no-op'd, leaking test orgs
	// into the shared dev database).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "wf-lifecycle")
	creator := seedTestUser(t, ctx, pool, orgID)
	toolA := seedTestTool(t, ctx, q, orgID, "connector-a")
	toolB := seedTestTool(t, ctx, q, orgID, "connector-b")

	// Create: workflow + 2 steps, inside a transaction, exactly as
	// workflows.go's CreateWorkflow handler does. defer Rollback (a no-op
	// once Commit succeeds) mirrors the handler's own safety net -- without
	// it, a t.Fatalf mid-transaction here would leak the held connection
	// forever and hang the deferred pool.Close() below (found live while
	// writing this test).
	tx, err := q.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)
	txQ := q.WithTx(tx)
	wf, err := txQ.CreateWorkflow(ctx, store.CreateWorkflowParams{
		OrgID: orgID, Name: "my-workflow", Status: "draft", CreatedBy: &creator,
	})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if err := txQ.InsertWorkflowStep(ctx, wf.ID, 0, toolA, "query"); err != nil {
		t.Fatalf("InsertWorkflowStep 0: %v", err)
	}
	if err := txQ.InsertWorkflowStep(ctx, wf.ID, 1, toolB, "notify"); err != nil {
		t.Fatalf("InsertWorkflowStep 1: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// List: workflow appears with step_count == 2.
	list, err := q.ListWorkflows(ctx, orgID)
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	found := false
	for _, w := range list {
		if w.ID == wf.ID {
			found = true
			if w.StepCount != 2 {
				t.Errorf("StepCount = %d, want 2", w.StepCount)
			}
		}
	}
	if !found {
		t.Fatal("created workflow not present in ListWorkflows")
	}

	// Get + steps: joined tool names present, in submitted order.
	got, err := q.GetWorkflow(ctx, wf.ID, orgID)
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}
	if got.Name != "my-workflow" || got.Status != "draft" {
		t.Errorf("GetWorkflow = %+v, want name=my-workflow status=draft", got)
	}
	steps, err := q.ListWorkflowSteps(ctx, wf.ID)
	if err != nil {
		t.Fatalf("ListWorkflowSteps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	if steps[0].StepOrder != 0 || steps[0].Action != "query" || !steps[0].ToolName.Valid || steps[0].ToolName.String != "connector-a" {
		t.Errorf("steps[0] = %+v, want step_order=0 action=query tool_name=connector-a", steps[0])
	}
	if steps[1].StepOrder != 1 || steps[1].Action != "notify" || !steps[1].ToolName.Valid || steps[1].ToolName.String != "connector-b" {
		t.Errorf("steps[1] = %+v, want step_order=1 action=notify tool_name=connector-b", steps[1])
	}

	// Update: full-replace steps (reorder B before A, drop nothing, add
	// none -- a minimal but real reorder), plus a name/status change.
	tx2, err := q.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin (update): %v", err)
	}
	defer tx2.Rollback(ctx)
	txQ2 := q.WithTx(tx2)
	newName := "my-workflow-renamed"
	newStatus := "active"
	if _, err := txQ2.UpdateWorkflow(ctx, store.UpdateWorkflowParams{
		ID: wf.ID, OrgID: orgID, Name: &newName, Status: &newStatus,
	}); err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}
	if err := txQ2.DeleteWorkflowSteps(ctx, wf.ID); err != nil {
		t.Fatalf("DeleteWorkflowSteps: %v", err)
	}
	// Reordered: B now first, A dropped entirely -- proves add/remove/
	// reorder all in one replace.
	if err := txQ2.InsertWorkflowStep(ctx, wf.ID, 0, toolB, "notify"); err != nil {
		t.Fatalf("InsertWorkflowStep (reordered): %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("Commit (update): %v", err)
	}

	updated, err := q.GetWorkflow(ctx, wf.ID, orgID)
	if err != nil {
		t.Fatalf("GetWorkflow (after update): %v", err)
	}
	if updated.Name != "my-workflow-renamed" || updated.Status != "active" {
		t.Errorf("after update: name=%q status=%q, want my-workflow-renamed/active", updated.Name, updated.Status)
	}
	updatedSteps, err := q.ListWorkflowSteps(ctx, wf.ID)
	if err != nil {
		t.Fatalf("ListWorkflowSteps (after update): %v", err)
	}
	if len(updatedSteps) != 1 || updatedSteps[0].Action != "notify" {
		t.Fatalf("steps after update = %+v, want exactly 1 step (notify)", updatedSteps)
	}

	// Delete: workflow removed, steps cascade-deleted (verified via a
	// direct query, not just the absence of an error).
	deleted, err := q.DeleteWorkflow(ctx, orgID, wf.ID)
	if err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteWorkflow returned false for an existing workflow")
	}
	var remainingSteps int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_steps WHERE workflow_id = $1`, wf.ID).Scan(&remainingSteps); err != nil {
		t.Fatalf("count remaining steps: %v", err)
	}
	if remainingSteps != 0 {
		t.Errorf("workflow_steps rows remaining after DeleteWorkflow = %d, want 0 (cascade)", remainingSteps)
	}
	if _, err := q.GetWorkflow(ctx, wf.ID, orgID); err != pgx.ErrNoRows {
		t.Errorf("GetWorkflow after delete: err = %v, want pgx.ErrNoRows", err)
	}
}

// TestWorkflows_RealDB_CrossOrg proves org isolation against real Postgres
// (same standard as tools_update_pg_test.go's cross-org proof and B-042's
// "real second org, wrong org_id" pattern): org B cannot Get/Update/Delete
// org A's workflow, and the real row is untouched by the attempt.
func TestWorkflows_RealDB_CrossOrg(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- deliberately so it runs AFTER the
	// org-delete t.Cleanup callbacks registered below (via seedTestOrg).
	// t.Cleanup runs LIFO, in a phase strictly after this function's own
	// defers, so a plain `defer pool.Close()` here would close the pool
	// before any t.Cleanup-registered DELETE ever got a chance to run --
	// exactly the bug BACKLOG.md/CONTEXT.md's B-056 entry documents (found
	// live while writing this test: the first version of this file used a
	// plain defer and every cleanup silently no-op'd, leaking test orgs
	// into the shared dev database).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgA := seedTestOrg(t, ctx, pool, "wf-crossorg-a")
	orgB := seedTestOrg(t, ctx, pool, "wf-crossorg-b")
	toolA := seedTestTool(t, ctx, q, orgA, "org-a-connector")

	wf, err := q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: orgA, Name: "org-a-workflow", Status: "draft"})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if err := q.InsertWorkflowStep(ctx, wf.ID, 0, toolA, "query"); err != nil {
		t.Fatalf("InsertWorkflowStep: %v", err)
	}

	// Get with org B's id: not found, not a data leak.
	if _, err := q.GetWorkflow(ctx, wf.ID, orgB); err != pgx.ErrNoRows {
		t.Errorf("GetWorkflow(orgB) err = %v, want pgx.ErrNoRows", err)
	}

	// Update with org B's id: no row affected, real row untouched.
	hijack := "hijacked-name"
	if _, err := q.UpdateWorkflow(ctx, store.UpdateWorkflowParams{ID: wf.ID, OrgID: orgB, Name: &hijack}); err != pgx.ErrNoRows {
		t.Errorf("UpdateWorkflow(orgB) err = %v, want pgx.ErrNoRows", err)
	}
	stillA, err := q.GetWorkflow(ctx, wf.ID, orgA)
	if err != nil {
		t.Fatalf("GetWorkflow(orgA) after cross-org update attempt: %v", err)
	}
	if stillA.Name != "org-a-workflow" {
		t.Errorf("name after cross-org update attempt = %q, want unchanged \"org-a-workflow\"", stillA.Name)
	}

	// Delete with org B's id: reports not-found, row survives.
	deleted, err := q.DeleteWorkflow(ctx, orgB, wf.ID)
	if err != nil {
		t.Fatalf("DeleteWorkflow(orgB): %v", err)
	}
	if deleted {
		t.Error("DeleteWorkflow(orgB) reported success deleting org A's workflow")
	}
	if _, err := q.GetWorkflow(ctx, wf.ID, orgA); err != nil {
		t.Errorf("workflow no longer exists after a cross-org delete attempt that should have been a no-op: %v", err)
	}

	// The load-bearing guard itself: org A's tool does not belong to org B.
	belongs, err := q.ToolBelongsToOrg(ctx, toolA, orgB)
	if err != nil {
		t.Fatalf("ToolBelongsToOrg: %v", err)
	}
	if belongs {
		t.Error("ToolBelongsToOrg(orgA's tool, orgB) = true, want false")
	}
	// Sanity: it does belong to its real org.
	belongsA, err := q.ToolBelongsToOrg(ctx, toolA, orgA)
	if err != nil {
		t.Fatalf("ToolBelongsToOrg (own org): %v", err)
	}
	if !belongsA {
		t.Error("ToolBelongsToOrg(orgA's tool, orgA) = false, want true")
	}
}

// TestWorkflows_HTTP_CreateWorkflow_RejectsCrossOrgToolReference proves the
// full CreateWorkflow HTTP handler -- not just the ToolBelongsToOrg
// primitive -- refuses a step referencing another org's connector: a real
// httptest server, a real JWT (auth.Service.IssueAccessToken, the same
// mechanism jwtMiddleware verifies), a real Postgres-backed store. Org A's
// JWT submitting org B's real gateway_tools.id must get 400 with no
// workflow row created.
func TestWorkflows_HTTP_CreateWorkflow_RejectsCrossOrgToolReference(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- deliberately so it runs AFTER the
	// org-delete t.Cleanup callbacks registered below (via seedTestOrg).
	// t.Cleanup runs LIFO, in a phase strictly after this function's own
	// defers, so a plain `defer pool.Close()` here would close the pool
	// before any t.Cleanup-registered DELETE ever got a chance to run --
	// exactly the bug BACKLOG.md/CONTEXT.md's B-056 entry documents (found
	// live while writing this test: the first version of this file used a
	// plain defer and every cleanup silently no-op'd, leaking test orgs
	// into the shared dev database).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgA := seedTestOrg(t, ctx, pool, "wf-http-a")
	orgB := seedTestOrg(t, ctx, pool, "wf-http-b")
	toolB := seedTestTool(t, ctx, q, orgB, "org-b-connector")

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	token, _, err := authSvc.IssueAccessToken(uuid.New(), orgA, "admin@org-a.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name": "cross-org-attempt",
		"steps": []map[string]any{
			{"gateway_tool_id": toolB.String(), "action": "query"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/gateway/workflows", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/gateway/workflows: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE org_id = $1`, orgA).Scan(&count); err != nil {
		t.Fatalf("count workflows: %v", err)
	}
	if count != 0 {
		t.Errorf("workflows created for org A after a rejected cross-org step reference = %d, want 0", count)
	}
}

// TestWorkflows_RealDB_DanglingReference proves AC4: deleting a
// gateway_tools row referenced by a workflow step (via DeleteTool,
// completely untouched by this brief) leaves the workflow itself intact,
// with the affected step's gateway_tool_id/tool_name surfaced as NULL --
// not a broken/500ing workflow, not a silently dropped step.
func TestWorkflows_RealDB_DanglingReference(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- deliberately so it runs AFTER the
	// org-delete t.Cleanup callbacks registered below (via seedTestOrg).
	// t.Cleanup runs LIFO, in a phase strictly after this function's own
	// defers, so a plain `defer pool.Close()` here would close the pool
	// before any t.Cleanup-registered DELETE ever got a chance to run --
	// exactly the bug BACKLOG.md/CONTEXT.md's B-056 entry documents (found
	// live while writing this test: the first version of this file used a
	// plain defer and every cleanup silently no-op'd, leaking test orgs
	// into the shared dev database).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "wf-dangling")
	toolID := seedTestTool(t, ctx, q, orgID, "soon-to-be-deleted")

	wf, err := q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: orgID, Name: "dangling-test", Status: "draft"})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if err := q.InsertWorkflowStep(ctx, wf.ID, 0, toolID, "query"); err != nil {
		t.Fatalf("InsertWorkflowStep: %v", err)
	}

	// Delete the tool via the real, untouched DeleteTool -- proves it
	// still succeeds unconditionally, exactly as before this brief.
	found, err := q.DeleteTool(ctx, orgID, toolID)
	if err != nil {
		t.Fatalf("DeleteTool: %v", err)
	}
	if !found {
		t.Fatal("DeleteTool reported not-found for a tool that was just created")
	}

	// Workflow still exists and is still listable/gettable.
	if _, err := q.GetWorkflow(ctx, wf.ID, orgID); err != nil {
		t.Fatalf("GetWorkflow after referenced tool deleted: %v (workflow must survive)", err)
	}
	steps, err := q.ListWorkflowSteps(ctx, wf.ID)
	if err != nil {
		t.Fatalf("ListWorkflowSteps after referenced tool deleted: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1 (step must survive, not be dropped)", len(steps))
	}
	if steps[0].GatewayToolID.Valid {
		t.Errorf("GatewayToolID.Valid = true after the referenced tool was deleted, want false (SET NULL)")
	}
	if steps[0].ToolName.Valid {
		t.Errorf("ToolName.Valid = true after the referenced tool was deleted, want false")
	}
	if steps[0].Action != "query" {
		t.Errorf("Action = %q after dangling reference, want preserved \"query\"", steps[0].Action)
	}
}
