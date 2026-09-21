// loader_workspace_pg_test.go -- eami-gateway/internal/policyloader
//
// Integration test for Loader.Load's real DB round trip of
// policies.workspace_id (B-207/migration 000021), against a REAL Postgres,
// following the same pattern as loader_pg_test.go
// (TEST_DATABASE_URL/POSTGRES_PASSWORD fallback, throwaway fixtures via
// internal/testdb).
//
// This closes the gap between the pure-Go proof in
// eami-policy/workspace_test.go (comparator + matchesRule correctness in
// isolation) and the real SQL scanning/ordering this package owns -- a
// bug in queryRules' column list, scan order, or ORDER BY clause would
// not be caught by the pure-Go tests at all, since those construct
// policy.Rule values directly rather than going through a real query.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/policyloader/... -v -run Workspace
package policyloader

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/testdb"
	policy "github.com/eami/policy"
)

// TestLoad_WorkspaceID_RealDBRoundTrip proves the org-floor-cannot-be-
// overridden guarantee holds end-to-end through a REAL Postgres row: a
// global (workspace_id NULL) deny policy and a workspace-scoped allow
// policy for the same tool are both authored directly in Postgres, with
// the workspace policy's raw priority number deliberately LOWER than the
// global policy's (the same adversarial shape as
// eami-policy/workspace_test.go's pure-Go regression test) -- proving the
// real SQL ORDER BY + scan + Go-side re-sort, taken together, still
// produce the correct final evaluation order.
func TestLoad_WorkspaceID_RealDBRoundTrip(t *testing.T) {
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "b207-ws-test-"+orgID.String()[:8], "b207-ws-test-"+orgID.String()); err != nil {
		t.Fatalf("insert test org: %v", err)
	}

	groupID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`,
		groupID, orgID, "b207-ws-test-group"); err != nil {
		t.Fatalf("insert test group: %v", err)
	}
	workspaceID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, org_id, group_id, name) VALUES ($1, $2, $3, $4)`,
		workspaceID, orgID, groupID, "b207-ws-test-workspace"); err != nil {
		t.Fatalf("insert test workspace: %v", err)
	}

	// Global (workspace_id NULL) deny, HIGH priority number (500 -- would
	// sort LAST under a priority-only scheme).
	globalPolicyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, workspace_id, name, priority, action, status)
		VALUES ($1, $2, NULL, $3, 500, 'deny', 'active')
	`, globalPolicyID, orgID, "b207-org-floor-deny"); err != nil {
		t.Fatalf("insert global policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_names) VALUES ($1, $2, $3)
	`, uuid.New(), globalPolicyID, []string{"shared-tool"}); err != nil {
		t.Fatalf("insert global policy conditions: %v", err)
	}

	// Workspace-scoped allow, LOW priority number (1 -- would sort FIRST
	// under a priority-only scheme, incorrectly outranking the floor).
	wsPolicyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, workspace_id, name, priority, action, status)
		VALUES ($1, $2, $3, $4, 1, 'allow', 'active')
	`, wsPolicyID, orgID, workspaceID, "b207-workspace-allow-attempt"); err != nil {
		t.Fatalf("insert workspace policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_names) VALUES ($1, $2, $3)
	`, uuid.New(), wsPolicyID, []string{"shared-tool"}); err != nil {
		t.Fatalf("insert workspace policy conditions: %v", err)
	}

	loader := New(pool)
	if err := loader.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ev := loader.Evaluator()

	// The workspace's own dispatch: the org floor must win, even though
	// the workspace's own conflicting rule has a numerically lower
	// priority. This is the real, DB-backed proof of the mechanism the
	// pure-Go test already proved in isolation.
	d, err := ev.Evaluate(ctx, policy.ActionContext{
		OrgID: orgID.String(), WorkspaceID: workspaceID.String(), ToolName: "shared-tool",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Action != policy.ActionDeny || d.PolicyID == nil || *d.PolicyID != globalPolicyID.String() {
		t.Errorf("REGRESSION: workspace policy (priority 1) overrode the org floor (priority 500) through "+
			"a real DB round trip -- got action=%q policyID=%v, want deny/%s. If this fails, check "+
			"queryRules' ORDER BY / column scan order, not just the pure-Go comparator.",
			d.Action, d.PolicyID, globalPolicyID)
	}

	// A no-workspace dispatch in the same org must also see the floor
	// (obviously), and must never see the workspace-scoped allow.
	dNoWS, err := ev.Evaluate(ctx, policy.ActionContext{
		OrgID: orgID.String(), WorkspaceID: "", ToolName: "shared-tool",
	})
	if err != nil {
		t.Fatalf("Evaluate (no workspace): %v", err)
	}
	if dNoWS.Action != policy.ActionDeny || dNoWS.PolicyID == nil || *dNoWS.PolicyID != globalPolicyID.String() {
		t.Errorf("no-workspace dispatch: got action=%q policyID=%v, want deny/%s",
			dNoWS.Action, dNoWS.PolicyID, globalPolicyID)
	}

	// A different workspace in the same org must never see ws-a's own
	// allow rule (cross-workspace leak check, real-DB-backed).
	otherWorkspaceID := uuid.New()
	dOtherWS, err := ev.Evaluate(ctx, policy.ActionContext{
		OrgID: orgID.String(), WorkspaceID: otherWorkspaceID.String(), ToolName: "shared-tool",
	})
	if err != nil {
		t.Fatalf("Evaluate (other workspace): %v", err)
	}
	if dOtherWS.Action != policy.ActionDeny || dOtherWS.PolicyID == nil || *dOtherWS.PolicyID != globalPolicyID.String() {
		t.Errorf("other-workspace dispatch: got action=%q policyID=%v, want deny/%s (the org floor; "+
			"never the first workspace's own allow rule)", dOtherWS.Action, dOtherWS.PolicyID, globalPolicyID)
	}
}

// TestDeleteWorkspace_CascadesItsOwnScopedPolicies is the real-DB
// regression test for a HIGH-severity finding from this brief's own
// mandatory security review, caught and fixed before shipping: the first
// version of migration 000021 used ON DELETE SET NULL for
// policies.workspace_id, which would have silently PROMOTED every policy
// scoped to a deleted workspace into an org-wide floor policy (NULL
// workspace_id is the evaluator's own signal for "global, evaluated
// first, ahead of every workspace-scoped rule" -- not a benign "no
// membership" the way it is for gateway_agents/endpoints). Fixed to ON
// DELETE CASCADE: a workspace-scoped policy has no meaning once its
// workspace is gone, so it's removed instead of silently widened in
// scope. This test proves the row is genuinely gone, not merely
// unaffected -- a regression back to SET NULL would leave the row
// present with workspace_id NULL, which this test's row-count assertion
// would catch either way.
func TestDeleteWorkspace_CascadesItsOwnScopedPolicies(t *testing.T) {
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, "b207-cascade-test-"+orgID.String()[:8], "b207-cascade-test-"+orgID.String()); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	groupID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`,
		groupID, orgID, "b207-cascade-group"); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	workspaceID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, org_id, group_id, name) VALUES ($1, $2, $3, $4)`,
		workspaceID, orgID, groupID, "b207-cascade-workspace"); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	policyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, workspace_id, name, priority, action, status)
		VALUES ($1, $2, $3, $4, 1, 'allow', 'active')
	`, policyID, orgID, workspaceID, "b207-cascade-workspace-allow"); err != nil {
		t.Fatalf("insert workspace-scoped policy: %v", err)
	}

	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM policies WHERE id = $1`, policyID).Scan(&before); err != nil {
		t.Fatalf("count before delete: %v", err)
	}
	if before != 1 {
		t.Fatalf("setup failed: policy row not found before delete")
	}

	if _, err := pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM policies WHERE id = $1`, policyID).Scan(&after); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if after != 0 {
		t.Errorf("REGRESSION: workspace-scoped policy survived its own workspace's deletion (count=%d, want 0) -- "+
			"if this policy now has workspace_id=NULL instead of being deleted, the ON DELETE CASCADE fix was "+
			"reverted back to SET NULL, silently promoting it to an org-wide floor policy", after)
	}
}

// TestPolicyInsert_CrossOrgWorkspaceID_Rejected is the real-DB regression
// test for a MEDIUM-severity finding from the same security review: a
// trigger enforcing that a policy's workspace_id must belong to the SAME
// org_id as the policy row itself, closing a defense-in-depth gap in the
// same class as this codebase's two prior real cross-tenant incidents
// (B-128, B-141) -- a plain FK to workspaces(id) alone can't express that
// composite (org_id, workspace_id) invariant.
func TestPolicyInsert_CrossOrgWorkspaceID_Rejected(t *testing.T) {
	pool := testdb.NewThrowawayPool(t)
	ctx := context.Background()

	orgA := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgA, "b207-xorg-a-"+orgA.String()[:8], "b207-xorg-a-"+orgA.String()); err != nil {
		t.Fatalf("insert org A: %v", err)
	}
	orgB := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgB, "b207-xorg-b-"+orgB.String()[:8], "b207-xorg-b-"+orgB.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}
	groupID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO groups (id, org_id, name) VALUES ($1, $2, $3)`,
		groupID, orgA, "b207-xorg-group"); err != nil {
		t.Fatalf("insert group (org A): %v", err)
	}
	workspaceA := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, org_id, group_id, name) VALUES ($1, $2, $3, $4)`,
		workspaceA, orgA, groupID, "b207-xorg-workspace-a"); err != nil {
		t.Fatalf("insert workspace A: %v", err)
	}

	// A policy row claiming org B, but pointing at org A's workspace --
	// must be rejected by the trigger, not silently accepted.
	_, err := pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, workspace_id, name, priority, action, status)
		VALUES ($1, $2, $3, $4, 1, 'allow', 'active')
	`, uuid.New(), orgB, workspaceA, "b207-xorg-cross-attempt")
	if err == nil {
		t.Fatal("REGRESSION: a policy with org_id=orgB and workspace_id belonging to orgA was accepted -- " +
			"the check_workspace_org_match trigger did not fire or was removed")
	}
}
