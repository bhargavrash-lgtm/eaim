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
	if err := txQ.InsertWorkflowStep(ctx, uuid.New(), wf.ID, 0, toolA, "query", nil); err != nil {
		t.Fatalf("InsertWorkflowStep 0: %v", err)
	}
	if err := txQ.InsertWorkflowStep(ctx, uuid.New(), wf.ID, 1, toolB, "notify", nil); err != nil {
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
	if err := txQ2.InsertWorkflowStep(ctx, uuid.New(), wf.ID, 0, toolB, "notify", nil); err != nil {
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
	if err := q.InsertWorkflowStep(ctx, uuid.New(), wf.ID, 0, toolA, "query", nil); err != nil {
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
	if err := q.InsertWorkflowStep(ctx, uuid.New(), wf.ID, 0, toolID, "query", nil); err != nil {
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

// ─── B-063: output->input mapping -- step id stability ─────────────────────

// httpWorkflowTestEnv bundles a real running server + real Postgres pool +
// a real admin token for one org, shared by the B-063 HTTP-level tests
// below (all need the same real Create->Update round trip through the
// actual JSON DTO layer, not just store.Queries called directly, since
// what's being proven is specifically that a step's id/input_mapping
// survive the wire round trip a real admin's save would go through).
type httpWorkflowTestEnv struct {
	pool  *pgxpool.Pool
	q     *store.Queries
	orgID uuid.UUID
	base  string
	token string
}

func newHTTPWorkflowTestEnv(t *testing.T, label string) *httpWorkflowTestEnv {
	t.Helper()
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, registered first -- see every other test in this file's
	// identical comment on why a plain defer here would reproduce B-056.
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, label)
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// A real seeded user, not uuid.New() -- workflows.created_by is a real
	// FK to users(id); CreateWorkflow's INSERT would otherwise fail every
	// test that actually reaches it (the existing cross-org test never
	// notices this because it's rejected by validateWorkflowSteps first).
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@"+label+".test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	return &httpWorkflowTestEnv{pool: pool, q: q, orgID: orgID, base: ts.URL, token: token}
}

func (e *httpWorkflowTestEnv) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.base+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var parsed map[string]any
	if resp.ContentLength != 0 {
		_ = json.NewDecoder(resp.Body).Decode(&parsed)
	}
	return resp, parsed
}

// TestWorkflows_HTTP_UpdateWorkflow_ReorderPreservesStepIDs proves AC2:
// a pure reorder (same 2 steps, swapped order, both echoing their real
// ids back) through the real Create->Update HTTP round trip preserves
// each step's own workflow_steps.id -- not fresh ids minted for both
// rows, which is what would happen without B-063's id-reuse fix (the
// pre-B-063 behavior: DeleteWorkflowSteps + InsertWorkflowStep always
// minted brand-new ids on every steps-array save).
func TestWorkflows_HTTP_UpdateWorkflow_ReorderPreservesStepIDs(t *testing.T) {
	env := newHTTPWorkflowTestEnv(t, "wf-b063-reorder")
	ctx := context.Background()
	toolA := seedTestTool(t, ctx, env.q, env.orgID, "connector-a")
	toolB := seedTestTool(t, ctx, env.q, env.orgID, "connector-b")

	resp, created := env.do(t, http.MethodPost, "/v1/gateway/workflows", map[string]any{
		"name": "reorder-test",
		"steps": []map[string]any{
			{"gateway_tool_id": toolA.String(), "action": "query"},
			{"gateway_tool_id": toolB.String(), "action": "notify"},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", resp.StatusCode, created)
	}
	createdSteps, _ := created["steps"].([]any)
	if len(createdSteps) != 2 {
		t.Fatalf("created steps = %+v, want 2", createdSteps)
	}
	step0 := createdSteps[0].(map[string]any)
	step1 := createdSteps[1].(map[string]any)
	idA, idB := step0["id"].(string), step1["id"].(string)
	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("expected two distinct real step ids, got %q and %q", idA, idB)
	}
	wfID := created["id"].(string)

	// Reorder: same two steps, same real ids echoed back, swapped order.
	resp2, updated := env.do(t, http.MethodPatch, "/v1/gateway/workflows/"+wfID, map[string]any{
		"steps": []map[string]any{
			{"id": idB, "gateway_tool_id": toolB.String(), "action": "notify"},
			{"id": idA, "gateway_tool_id": toolA.String(), "action": "query"},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %+v", resp2.StatusCode, updated)
	}
	updatedSteps, _ := updated["steps"].([]any)
	if len(updatedSteps) != 2 {
		t.Fatalf("updated steps = %+v, want 2", updatedSteps)
	}
	newStep0 := updatedSteps[0].(map[string]any)
	newStep1 := updatedSteps[1].(map[string]any)
	if newStep0["id"] != idB {
		t.Errorf("after reorder, position 0 id = %v, want the original step B id %q (preserved, not regenerated)", newStep0["id"], idB)
	}
	if newStep1["id"] != idA {
		t.Errorf("after reorder, position 1 id = %v, want the original step A id %q (preserved, not regenerated)", newStep1["id"], idA)
	}
}

// TestWorkflows_HTTP_UpdateWorkflow_InputMappingSurvivesUnrelatedEdit
// proves the id-stability finding's actual severity was real and is
// fixed: before B-063, ANY steps-array save wiped input_mapping outright
// (InsertWorkflowStep never had a parameter for it), not just a reorder.
// Here an unrelated field (name) changes while the step's own id and
// input_mapping are echoed back unchanged -- input_mapping must survive.
func TestWorkflows_HTTP_UpdateWorkflow_InputMappingSurvivesUnrelatedEdit(t *testing.T) {
	env := newHTTPWorkflowTestEnv(t, "wf-b063-mapping")
	ctx := context.Background()
	toolA := seedTestTool(t, ctx, env.q, env.orgID, "connector-a")
	fromStep := uuid.New().String() // shape-valid; doesn't need to resolve to a real step for this DTO round-trip proof

	resp, created := env.do(t, http.MethodPost, "/v1/gateway/workflows", map[string]any{
		"name": "mapping-test",
		"steps": []map[string]any{
			{
				"gateway_tool_id": toolA.String(),
				"action":          "notify",
				"input_mapping": map[string]any{
					"contact_id": map[string]any{"from_step": fromStep, "path": "contact.id"},
				},
			},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", resp.StatusCode, created)
	}
	createdSteps, _ := created["steps"].([]any)
	step0 := createdSteps[0].(map[string]any)
	stepID := step0["id"].(string)
	mapping0, _ := step0["input_mapping"].(map[string]any)
	if mapping0 == nil {
		t.Fatalf("created step's input_mapping = %+v, want it present", step0)
	}
	wfID := created["id"].(string)

	// Unrelated edit: rename the workflow, echo the step back with its
	// real id AND the same input_mapping (full-replace semantics require
	// resending it, matching action_paths' own established convention --
	// this call proves that convention actually preserves the data, not
	// that omitting it would).
	resp2, updated := env.do(t, http.MethodPatch, "/v1/gateway/workflows/"+wfID, map[string]any{
		"name": "mapping-test-renamed",
		"steps": []map[string]any{
			{
				"id":              stepID,
				"gateway_tool_id": toolA.String(),
				"action":          "notify",
				"input_mapping": map[string]any{
					"contact_id": map[string]any{"from_step": fromStep, "path": "contact.id"},
				},
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %+v", resp2.StatusCode, updated)
	}
	if updated["name"] != "mapping-test-renamed" {
		t.Errorf("name = %v, want the rename to have applied", updated["name"])
	}
	updatedSteps, _ := updated["steps"].([]any)
	newStep0 := updatedSteps[0].(map[string]any)
	if newStep0["id"] != stepID {
		t.Errorf("step id = %v, want preserved %q", newStep0["id"], stepID)
	}
	mapping1, _ := newStep0["input_mapping"].(map[string]any)
	entry, _ := mapping1["contact_id"].(map[string]any)
	if entry == nil || entry["from_step"] != fromStep || entry["path"] != "contact.id" {
		t.Fatalf("input_mapping after an unrelated field edit = %+v, want it to survive byte-identical (this is the bug the id-stability finding described: pre-fix, InsertWorkflowStep had no parameter for input_mapping at all, so ANY steps-array save silently wiped it)", newStep0["input_mapping"])
	}
}

// TestWorkflows_HTTP_UpdateWorkflow_EchoedIDFromDifferentWorkflow_NeverReused
// is the security-review-anticipated guard: resolveStepIDs' id-reuse
// lookup is scoped to `WHERE workflow_id = $1` (via ListWorkflowSteps on
// the workflow actually being updated) -- submitting another workflow's
// real step id must never let a request "steal" or reuse it. The
// submitted id should be silently treated as unrecognized (a fresh id
// minted), and the OTHER workflow's own step must be completely
// unaffected.
func TestWorkflows_HTTP_UpdateWorkflow_EchoedIDFromDifferentWorkflow_NeverReused(t *testing.T) {
	env := newHTTPWorkflowTestEnv(t, "wf-b063-idsteal")
	ctx := context.Background()
	toolA := seedTestTool(t, ctx, env.q, env.orgID, "connector-a")

	// Workflow A: the "victim" whose real step id we'll try to reuse.
	respA, createdA := env.do(t, http.MethodPost, "/v1/gateway/workflows", map[string]any{
		"name":  "victim-workflow",
		"steps": []map[string]any{{"gateway_tool_id": toolA.String(), "action": "query"}},
	})
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("create A status = %d, body = %+v", respA.StatusCode, createdA)
	}
	victimStepID := createdA["steps"].([]any)[0].(map[string]any)["id"].(string)
	victimWorkflowID := createdA["id"].(string)

	// Workflow B: the attacker-controlled request, echoing A's real step id.
	respB, createdB := env.do(t, http.MethodPost, "/v1/gateway/workflows", map[string]any{
		"name":  "attacker-workflow",
		"steps": []map[string]any{{"gateway_tool_id": toolA.String(), "action": "notify"}},
	})
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("create B status = %d, body = %+v", respB.StatusCode, createdB)
	}
	workflowBID := createdB["id"].(string)

	respB2, updatedB := env.do(t, http.MethodPatch, "/v1/gateway/workflows/"+workflowBID, map[string]any{
		"steps": []map[string]any{
			{"id": victimStepID, "gateway_tool_id": toolA.String(), "action": "hijacked"},
		},
	})
	if respB2.StatusCode != http.StatusOK {
		t.Fatalf("update B status = %d, body = %+v", respB2.StatusCode, updatedB)
	}
	newStepID := updatedB["steps"].([]any)[0].(map[string]any)["id"].(string)
	if newStepID == victimStepID {
		t.Fatalf("workflow B's step reused workflow A's real step id %q -- id-reuse lookup is not properly scoped to the workflow being updated", victimStepID)
	}

	// Workflow A's own step must be completely untouched.
	victimSteps, err := env.q.ListWorkflowSteps(ctx, uuid.MustParse(victimWorkflowID))
	if err != nil {
		t.Fatalf("ListWorkflowSteps (victim): %v", err)
	}
	if len(victimSteps) != 1 || victimSteps[0].ID.String() != victimStepID || victimSteps[0].Action != "query" {
		t.Fatalf("victim workflow's step was affected by workflow B's request: %+v", victimSteps)
	}
}

// TestWorkflows_HTTP_UpdateWorkflow_DuplicateEchoedID_Returns400NotRaw500
// is a regression guard (code-review finding): the same real step id
// echoed on two different submitted steps would otherwise let
// resolveStepIDs assign it to both, and the second INSERT would hit
// workflow_steps' primary-key uniqueness (23505) -- a code
// isForeignKeyViolation doesn't recognize -- surfacing as an opaque 500
// with a raw Postgres error string. validateWorkflowSteps must catch
// this itself and return a clean 400.
func TestWorkflows_HTTP_UpdateWorkflow_DuplicateEchoedID_Returns400NotRaw500(t *testing.T) {
	env := newHTTPWorkflowTestEnv(t, "wf-b063-dupid")
	ctx := context.Background()
	toolA := seedTestTool(t, ctx, env.q, env.orgID, "connector-a")
	toolB := seedTestTool(t, ctx, env.q, env.orgID, "connector-b")

	resp, created := env.do(t, http.MethodPost, "/v1/gateway/workflows", map[string]any{
		"name": "dup-id-test",
		"steps": []map[string]any{
			{"gateway_tool_id": toolA.String(), "action": "query"},
			{"gateway_tool_id": toolB.String(), "action": "notify"},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", resp.StatusCode, created)
	}
	stepID := created["steps"].([]any)[0].(map[string]any)["id"].(string)
	wfID := created["id"].(string)

	resp2, body := env.do(t, http.MethodPatch, "/v1/gateway/workflows/"+wfID, map[string]any{
		"steps": []map[string]any{
			{"id": stepID, "gateway_tool_id": toolA.String(), "action": "query"},
			{"id": stepID, "gateway_tool_id": toolB.String(), "action": "notify"}, // same id, second step
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %+v, want 400 (not a raw 500 from a unique-constraint violation)", resp2.StatusCode, body)
	}
	if code, _ := body["code"].(string); code != "bad_request" {
		t.Errorf("error code = %q, want bad_request", code)
	}
}

// TestWorkflows_HTTP_UpdateWorkflow_StaticParamsSurviveReusedStepID is a
// regression guard found live during B-063's own verification (not part
// of the originally approved plan): workflow_step_params.workflow_step_id
// has ON DELETE CASCADE (migration 000007), so DeleteWorkflowSteps'
// unconditional delete wipes a step's static params even when that same
// step's id is reused a moment later by InsertWorkflowStep -- the
// identical class of bug the id-stability finding already fixed for
// input_mapping, just on the sibling static-params table. Proves an
// unrelated sibling-step edit doesn't silently zero out a step's own
// previously-set static params.
func TestWorkflows_HTTP_UpdateWorkflow_StaticParamsSurviveReusedStepID(t *testing.T) {
	env := newHTTPWorkflowTestEnv(t, "wf-b063-paramscarry")
	ctx := context.Background()
	toolA := seedTestTool(t, ctx, env.q, env.orgID, "connector-a")
	toolB := seedTestTool(t, ctx, env.q, env.orgID, "connector-b")

	resp, created := env.do(t, http.MethodPost, "/v1/gateway/workflows", map[string]any{
		"name": "params-carry-test",
		"steps": []map[string]any{
			{"gateway_tool_id": toolA.String(), "action": "query"},
			{"gateway_tool_id": toolB.String(), "action": "notify"},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", resp.StatusCode, created)
	}
	createdSteps := created["steps"].([]any)
	step0ID := createdSteps[0].(map[string]any)["id"].(string)
	step1ID := createdSteps[1].(map[string]any)["id"].(string)
	wfID := created["id"].(string)

	// Set static params on step 0, via the real B-059 endpoint.
	if _, err := env.q.UpsertWorkflowStepParams(ctx, uuid.MustParse(step0ID), env.orgID, []byte(`{"q":"original"}`)); err != nil {
		t.Fatalf("UpsertWorkflowStepParams: %v", err)
	}

	// Unrelated edit: only step 1's action changes; step 0 is echoed back
	// with its real id and otherwise identical -- exactly what a real
	// admin's edit panel submit does when only one row was touched.
	resp2, updated := env.do(t, http.MethodPatch, "/v1/gateway/workflows/"+wfID, map[string]any{
		"steps": []map[string]any{
			{"id": step0ID, "gateway_tool_id": toolA.String(), "action": "query"},
			{"id": step1ID, "gateway_tool_id": toolB.String(), "action": "notify-changed"},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %+v", resp2.StatusCode, updated)
	}
	newStep0ID := updated["steps"].([]any)[0].(map[string]any)["id"].(string)
	if newStep0ID != step0ID {
		t.Fatalf("step 0 id = %v, want preserved %q", newStep0ID, step0ID)
	}

	raw, err := env.q.GetWorkflowStepParams(ctx, uuid.MustParse(step0ID), env.orgID)
	if err != nil {
		t.Fatalf("GetWorkflowStepParams after unrelated sibling-step edit: %v", err)
	}
	var params map[string]string
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("unmarshal carried params: %v", err)
	}
	if params["q"] != "original" {
		t.Fatalf("step 0 static params after unrelated edit = %s, want {\"q\":\"original\"} preserved (this is the bug: DeleteWorkflowSteps cascades workflow_step_params even when the step id is reused)", raw)
	}
}
