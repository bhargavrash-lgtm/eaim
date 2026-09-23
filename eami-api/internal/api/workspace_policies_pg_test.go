// workspace_policies_pg_test.go -- eami-api/internal/api
//
// Real-Postgres integration tests for B-197 increment 4: workspace-scoped
// policy CRUD (workspace_policies.go). Follows workspaces_pg_test.go's own
// workspaceTestEnv/seedWorkspace/seedMembership/token/do helpers exactly --
// this is the same access-control severity class (B-128/B-141/B-207/B-208),
// re-using the established real-HTTP-server + real-JWT + real-Postgres
// harness rather than inventing a new one.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestWorkspacePolic -v
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestCreateWorkspacePolicy -v
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	policy "github.com/eami/policy"
)

// ── CRUD lifecycle ───────────────────────────────────────────────────────────────

// TestWorkspacePolicies_RealDB_CRUDLifecycle covers the happy path end to
// end: create (workspace_admin), list (includes both this workspace's own
// policy AND a separately-seeded org floor policy -- DESIGN_SYSTEM.md
// §7.3's visibility distinction), update, delete.
func TestWorkspacePolicies_RealDB_CRUDLifecycle(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-crud")
	wsID := env.seedWorkspace(t, ctx, orgID, "WSP CRUD Workspace")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsID, "workspace_admin")
	adminTok := env.token(t, admin, orgID, "admin@wsp-crud.test", "operator") // org role deliberately not "admin"

	// A separately-seeded org-wide floor policy (workspace_id NULL),
	// belonging to the same org -- must show up in this workspace's list.
	floorID := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, workspace_id, name, priority, action, status)
		VALUES ($1, $2, NULL, $3, 999, 'deny', 'active')
	`, floorID, orgID, "wsp-crud-floor"); err != nil {
		t.Fatalf("seed floor policy: %v", err)
	}
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM policies WHERE id = $1`, floorID) })

	// Create.
	createResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", adminTok, map[string]any{
		"name": "WSP Create Test", "priority": 5, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"wsp-tool"}},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201: %s", createResp.StatusCode, readWSBody(createResp))
	}
	var created struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspace_id"`
		Priority    int32  `json:"priority"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()
	if created.WorkspaceID != wsID.String() {
		t.Fatalf("create: workspace_id = %q, want %q", created.WorkspaceID, wsID.String())
	}

	// List: must include BOTH the workspace's own policy and the org floor.
	listResp := env.do(t, http.MethodGet, "/v1/workspaces/"+wsID.String()+"/policies", adminTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want 200: %s", listResp.StatusCode, readWSBody(listResp))
	}
	var list struct {
		Data []struct {
			ID            string  `json:"id"`
			WorkspaceID   *string `json:"workspace_id"`
			WorkspaceName *string `json:"workspace_name"`
		} `json:"data"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	listResp.Body.Close()
	var sawOwn, sawFloor bool
	for _, p := range list.Data {
		if p.ID == created.ID {
			sawOwn = true
			// B-210 regression: ListWorkspacePolicies originally selected
			// workspace_id only, never the workspace's real name -- found
			// live (WorkspacePoliciesPage.tsx's ScopeBadge rendered "Global
			// floor" for a genuinely workspace-scoped policy) and fixed.
			if p.WorkspaceName == nil || *p.WorkspaceName != "WSP CRUD Workspace" {
				t.Fatalf("own policy in list has wrong/missing workspace_name: %v, want %q", p.WorkspaceName, "WSP CRUD Workspace")
			}
		}
		if p.ID == floorID.String() {
			sawFloor = true
			if p.WorkspaceID != nil {
				t.Fatalf("floor policy in list has non-nil workspace_id: %v", *p.WorkspaceID)
			}
			if p.WorkspaceName != nil {
				t.Fatalf("floor policy in list has non-nil workspace_name: %v", *p.WorkspaceName)
			}
		}
	}
	if !sawOwn || !sawFloor {
		t.Fatalf("list missing expected rows: sawOwn=%v sawFloor=%v, data=%+v", sawOwn, sawFloor, list.Data)
	}

	// Update.
	updResp := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsID.String()+"/policies/"+created.ID, adminTok,
		map[string]any{"priority": 6})
	if updResp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, want 200: %s", updResp.StatusCode, readWSBody(updResp))
	}
	var updated struct{ Priority int32 }
	json.NewDecoder(updResp.Body).Decode(&updated)
	updResp.Body.Close()
	if updated.Priority != 6 {
		t.Fatalf("update: priority = %d, want 6", updated.Priority)
	}

	// Delete.
	delResp := env.do(t, http.MethodDelete, "/v1/workspaces/"+wsID.String()+"/policies/"+created.ID, adminTok, nil)
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204: %s", delResp.StatusCode, readWSBody(delResp))
	}
	delResp.Body.Close()
	var count int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM policies WHERE id = $1`, created.ID).Scan(&count)
	if count != 0 {
		t.Fatalf("policy row survived delete")
	}
}

// TestCreateWorkspacePolicy_RealDB_DuplicatePriority_Returns409 proves Part
// A #4's design decision actually holds against the real DEFERRABLE
// constraint (policies_org_workspace_priority_key, migration 000021): a
// second policy at the same priority within the SAME workspace is rejected
// as a clean 409, not a raw 500 -- worth a real assertion given this exact
// codebase's own prior false alarm around DEFERRABLE constraints only
// firing at COMMIT, not per-statement (B-207's own investigation notes).
// A single autocommit INSERT's implicit transaction commits synchronously
// with the statement, so the deferred check still surfaces to this
// caller -- confirmed here, not just assumed from that precedent.
func TestCreateWorkspacePolicy_RealDB_DuplicatePriority_Returns409(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-duppriority")
	wsID := env.seedWorkspace(t, ctx, orgID, "WSP Dup Priority WS")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsID, "workspace_admin")
	tok := env.token(t, admin, orgID, "admin@wsp-duppriority.test", "operator")

	first := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", tok, map[string]any{
		"name": "first", "priority": 42, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"t"}},
	})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create: status = %d, want 201: %s", first.StatusCode, readWSBody(first))
	}
	first.Body.Close()

	second := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", tok, map[string]any{
		"name": "second", "priority": 42, "action": "deny", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"t2"}},
	})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("REGRESSION: duplicate priority within the same workspace: status = %d, want 409: %s",
			second.StatusCode, readWSBody(second))
	}
	second.Body.Close()

	var count int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM policies WHERE workspace_id = $1 AND priority = 42`, wsID).Scan(&count)
	if count != 1 {
		t.Fatalf("REGRESSION: expected exactly 1 policy at priority 42 in this workspace after the 409, got %d", count)
	}
}

// TestUpdateDeleteWorkspacePolicy_RealDB_WrongWorkspace_NotFound proves the
// PATCH/DELETE handlers' explicit workspace_id = $3 filter: a policyId
// that is real, and belongs to the SAME org, but a DIFFERENT workspace,
// 404s rather than being modifiable/deletable -- policies.go's own
// UpdatePolicy/DeletePolicy only filter by id+org_id, which is exactly
// the gap this brief's Part A investigation flagged and this new pair of
// handlers closes.
func TestUpdateDeleteWorkspacePolicy_RealDB_WrongWorkspace_NotFound(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-wrongws")
	wsA := env.seedWorkspace(t, ctx, orgID, "WSP Wrong WS A")
	wsB := env.seedWorkspace(t, ctx, orgID, "WSP Wrong WS B")

	adminOfBoth := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, adminOfBoth, wsA, "workspace_admin")
	env.seedMembership(t, ctx, adminOfBoth, wsB, "workspace_admin")
	tok := env.token(t, adminOfBoth, orgID, "both@wsp-wrongws.test", "operator")

	// A real policy that belongs to wsA.
	createResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsA.String()+"/policies", tok, map[string]any{
		"name": "A's policy", "priority": 1, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"t"}},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create in A: status = %d: %s", createResp.StatusCode, readWSBody(createResp))
	}
	var created struct{ ID string }
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	// Same admin (of BOTH A and B), attempting to PATCH A's policy through
	// B's route -- must 404, even though they're a legitimate
	// workspace_admin of B.
	updResp := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsB.String()+"/policies/"+created.ID, tok,
		map[string]any{"priority": 2})
	if updResp.StatusCode != http.StatusNotFound {
		t.Fatalf("REGRESSION: update A's policy via B's route: status = %d, want 404: %s", updResp.StatusCode, readWSBody(updResp))
	}
	updResp.Body.Close()

	delResp := env.do(t, http.MethodDelete, "/v1/workspaces/"+wsB.String()+"/policies/"+created.ID, tok, nil)
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("REGRESSION: delete A's policy via B's route: status = %d, want 404: %s", delResp.StatusCode, readWSBody(delResp))
	}
	delResp.Body.Close()

	var priority int32
	env.pool.QueryRow(ctx, `SELECT priority FROM policies WHERE id = $1`, created.ID).Scan(&priority)
	if priority != 1 {
		t.Fatalf("REGRESSION: A's policy priority changed to %d despite the 404s", priority)
	}
}

// ── Mandatory adversarial test 1 ─────────────────────────────────────────────────
// A client-supplied workspace_id in the POST body (targeting a DIFFERENT,
// real workspace) is silently ignored -- the created policy is scoped
// exclusively to the route's own {workspaceId}, never the body's.
func TestCreateWorkspacePolicy_RealDB_BodyWorkspaceIDIgnored_ServerResolvedFromRoute(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-bodyid")
	wsA := env.seedWorkspace(t, ctx, orgID, "WSP BodyID A")
	wsB := env.seedWorkspace(t, ctx, orgID, "WSP BodyID B")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsA, "workspace_admin")
	tok := env.token(t, admin, orgID, "admin@wsp-bodyid.test", "operator")

	// The malicious body: a real, different workspace's ID smuggled in as
	// "workspace_id". PolicyCreateRequest (types.go) has no such field --
	// json.Decode silently drops it, so this must have zero effect.
	resp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsA.String()+"/policies", tok, map[string]any{
		"name": "Body ID Attack", "priority": 1, "action": "allow", "alert": false, "status": "active",
		"workspace_id": wsB.String(),
		"conditions":   map[string]any{"tool_names": []string{"t"}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201: %s", resp.StatusCode, readWSBody(resp))
	}
	var created struct {
		ID          string  `json:"id"`
		WorkspaceID *string `json:"workspace_id"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	if created.WorkspaceID == nil || *created.WorkspaceID != wsA.String() {
		t.Fatalf("REGRESSION: response workspace_id = %v, want %q (the route param, never the body's wsB)",
			created.WorkspaceID, wsA.String())
	}

	var dbWorkspaceID uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT workspace_id FROM policies WHERE id = $1`, created.ID).Scan(&dbWorkspaceID); err != nil {
		t.Fatalf("read back workspace_id: %v", err)
	}
	if dbWorkspaceID != wsA {
		t.Fatalf("REGRESSION: real DB row has workspace_id = %s, want %s (route param) -- the body's "+
			"workspace_id (%s) leaked through instead", dbWorkspaceID, wsA, wsB)
	}
}

// A body attempting to submit workspace_id: null explicitly (not just
// omitted) -- same guarantee, different JSON shape of the same attack.
func TestCreateWorkspacePolicy_RealDB_BodyWorkspaceIDNull_StillServerResolved(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-bodynull")
	wsA := env.seedWorkspace(t, ctx, orgID, "WSP BodyNull A")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsA, "workspace_admin")
	tok := env.token(t, admin, orgID, "admin@wsp-bodynull.test", "operator")

	resp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsA.String()+"/policies", tok, map[string]any{
		"name": "Body Null Attack", "priority": 1, "action": "allow", "alert": false, "status": "active",
		"workspace_id": nil,
		"conditions":   map[string]any{"tool_names": []string{"t"}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201: %s", resp.StatusCode, readWSBody(resp))
	}
	var created struct{ ID string }
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	var dbWorkspaceID uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT workspace_id FROM policies WHERE id = $1`, created.ID).Scan(&dbWorkspaceID); err != nil {
		t.Fatalf("REGRESSION: workspace_id read back as NULL (or read failed: %v) -- a body-supplied "+
			"\"workspace_id\": null must never promote a workspace-route-created policy to an org-wide floor policy", err)
	}
	if dbWorkspaceID != wsA {
		t.Fatalf("REGRESSION: workspace_id = %s, want %s", dbWorkspaceID, wsA)
	}
}

// ── Mandatory adversarial test 2 ─────────────────────────────────────────────────
// A workspace_admin of Workspace A cannot create a policy under Workspace
// B's route (re-confirms requireWorkspaceRole, established B-208, applies
// correctly to this new route too).
func TestCreateWorkspacePolicy_RealDB_WorkspaceAdminOfDifferentWorkspace_Rejected(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-crossws")
	wsA := env.seedWorkspace(t, ctx, orgID, "WSP CrossWS A")
	wsB := env.seedWorkspace(t, ctx, orgID, "WSP CrossWS B")

	adminOfA := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, adminOfA, wsA, "workspace_admin")
	tok := env.token(t, adminOfA, orgID, "admin-a@wsp-crossws.test", "operator")

	resp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsB.String()+"/policies", tok, map[string]any{
		"name": "Should Be Rejected", "priority": 1, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"t"}},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: status = %d, want 403 -- workspace_admin of A must not create a policy under B's route: %s",
			resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	var count int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM policies WHERE workspace_id = $1`, wsB).Scan(&count)
	if count != 0 {
		t.Fatalf("REGRESSION: a policy was actually created under Workspace B despite the 403")
	}
}

// ── Mandatory adversarial test 3 ─────────────────────────────────────────────────
// A workspace-scoped policy created via the REAL new HTTP endpoint is
// proven, via the REAL eami-policy evaluator (not a DB query), to rank
// below every org-floor policy -- an actual dispatch test exercising this
// handler's own INSERT end to end through B-207's composite sort-key
// mechanism, not a re-confirmation of policyloader's own isolated test
// (eami-gateway/internal/policyloader/loader_workspace_pg_test.go)
// against a hand-written SQL insert.
//
// eami-policy is dependency-free (its own go.mod: "No external
// dependencies -- structural evaluation uses stdlib only"), so importing
// it directly here is architecturally free -- no new heavy cross-module
// coupling, unlike importing eami-gateway or eami-api's own router into
// the other direction would be.
func TestCreateWorkspacePolicy_RealDB_Evaluator_OrgFloorStillWins(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-evalfloor")
	wsID := env.seedWorkspace(t, ctx, orgID, "WSP Eval Floor WS")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsID, "workspace_admin")
	tok := env.token(t, admin, orgID, "admin@wsp-evalfloor.test", "operator")

	// The org-wide floor: a DENY at a numerically HIGH priority (500 --
	// would sort LAST under a naive priority-only scheme).
	floorID := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policies (id, org_id, workspace_id, name, priority, action, status)
		VALUES ($1, $2, NULL, $3, 500, 'deny', 'active')
	`, floorID, orgID, "wsp-evalfloor-deny"); err != nil {
		t.Fatalf("seed floor policy: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO policy_conditions (id, policy_id, tool_names) VALUES ($1, $2, $3)
	`, uuid.New(), floorID, []string{"eval-shared-tool"}); err != nil {
		t.Fatalf("seed floor conditions: %v", err)
	}
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM policies WHERE id = $1`, floorID) })

	// The workspace-scoped policy, created via the REAL new endpoint (not
	// a hand-written SQL insert) -- an ALLOW at a numerically LOW priority
	// (1 -- would sort FIRST under a naive scheme, incorrectly outranking
	// the floor if this handler's own INSERT got workspace_id wrong).
	createResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", tok, map[string]any{
		"name": "wsp-evalfloor-allow-attempt", "priority": 1, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"eval-shared-tool"}},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create workspace policy: status = %d, want 201: %s", createResp.StatusCode, readWSBody(createResp))
	}
	var created struct{ ID string }
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	// Read back EVERY policy for this org exactly as policyloader's own
	// queryRules would (join + workspace_id), and feed them into the REAL
	// eami-policy evaluator -- the actual library code B-207 built, not a
	// SQL-side re-implementation of its ordering rule.
	rows, err := env.pool.Query(ctx, `
		SELECT p.id, COALESCE(p.workspace_id::text, ''), p.priority, p.action, pc.tool_names
		FROM policies p LEFT JOIN policy_conditions pc ON pc.policy_id = p.id
		WHERE p.org_id = $1
	`, orgID)
	if err != nil {
		t.Fatalf("read back policies: %v", err)
	}
	defer rows.Close()

	var rules []policy.Rule
	for rows.Next() {
		var id, wsIDStr, action string
		var priority int32
		var toolNames []string
		if err := rows.Scan(&id, &wsIDStr, &priority, &action, &toolNames); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rules = append(rules, policy.Rule{
			ID: id, OrgID: orgID.String(), WorkspaceID: wsIDStr, Priority: int(priority), Action: action,
			Conditions: policy.Conditions{ToolNames: toolNames},
		})
	}
	if len(rules) != 2 {
		t.Fatalf("expected exactly 2 real policy rows (floor + workspace), got %d", len(rules))
	}

	ev := policy.NewEvaluator(rules)
	d, err := ev.Evaluate(ctx, policy.ActionContext{
		OrgID: orgID.String(), WorkspaceID: wsID.String(), ToolName: "eval-shared-tool",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Action != policy.ActionDeny || d.PolicyID == nil || *d.PolicyID != floorID.String() {
		t.Errorf("REGRESSION: the workspace-scoped policy (priority 1, created via the real HTTP endpoint) "+
			"overrode the org floor (priority 500) -- got action=%q policyID=%v, want deny/%s. If this "+
			"fails, check CreateWorkspacePolicy's own INSERT column list/values, not just the evaluator's "+
			"comparator (already separately proven correct by B-207's own tests).",
			d.Action, d.PolicyID, floorID)
	}

	// Sanity: a dispatch with NO workspace in the same org must also see
	// the floor and never the workspace-scoped allow just created.
	dNoWS, err := ev.Evaluate(ctx, policy.ActionContext{OrgID: orgID.String(), WorkspaceID: "", ToolName: "eval-shared-tool"})
	if err != nil {
		t.Fatalf("Evaluate (no workspace): %v", err)
	}
	if dNoWS.Action != policy.ActionDeny || dNoWS.PolicyID == nil || *dNoWS.PolicyID != floorID.String() {
		t.Errorf("no-workspace dispatch: got action=%q policyID=%v, want deny/%s", dNoWS.Action, dNoWS.PolicyID, floorID)
	}
	_ = created // used above only to confirm the create call itself succeeded
}

// ── Mandatory adversarial test 4 ─────────────────────────────────────────────────
// A workspace_member (not workspace_admin) can view but cannot create or
// modify policies in their own workspace.
func TestWorkspacePolicy_RealDB_MemberCannotWrite_CanRead(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "wsp-memberro")
	wsID := env.seedWorkspace(t, ctx, orgID, "WSP Member RO WS")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsID, "workspace_admin")
	adminTok := env.token(t, admin, orgID, "admin@wsp-memberro.test", "operator")

	member := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, member, wsID, "workspace_member")
	memberTok := env.token(t, member, orgID, "member@wsp-memberro.test", "operator")

	// A real policy, created by the admin, for the member to attempt to
	// modify below.
	createResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", adminTok, map[string]any{
		"name": "Member RO Target", "priority": 1, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"t"}},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("setup create: status = %d: %s", createResp.StatusCode, readWSBody(createResp))
	}
	var created struct{ ID string }
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	// Member cannot create.
	createByMember := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", memberTok, map[string]any{
		"name": "Member Create Attempt", "priority": 2, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"t"}},
	})
	if createByMember.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: workspace_member create: status = %d, want 403: %s", createByMember.StatusCode, readWSBody(createByMember))
	}
	createByMember.Body.Close()

	// Member cannot update.
	updByMember := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsID.String()+"/policies/"+created.ID, memberTok,
		map[string]any{"priority": 99})
	if updByMember.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: workspace_member update: status = %d, want 403: %s", updByMember.StatusCode, readWSBody(updByMember))
	}
	updByMember.Body.Close()

	// Member cannot delete.
	delByMember := env.do(t, http.MethodDelete, "/v1/workspaces/"+wsID.String()+"/policies/"+created.ID, memberTok, nil)
	if delByMember.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: workspace_member delete: status = %d, want 403: %s", delByMember.StatusCode, readWSBody(delByMember))
	}
	delByMember.Body.Close()

	// Member CAN read (list).
	listResp := env.do(t, http.MethodGet, "/v1/workspaces/"+wsID.String()+"/policies", memberTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("REGRESSION: workspace_member list: status = %d, want 200: %s", listResp.StatusCode, readWSBody(listResp))
	}
	var list struct {
		Data []struct{ ID string } `json:"data"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	listResp.Body.Close()
	found := false
	for _, p := range list.Data {
		if p.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("workspace_member's own list did not include the workspace's real policy: %+v", list.Data)
	}

	// Confirm nothing actually changed despite the attempts.
	var priority int32
	env.pool.QueryRow(ctx, `SELECT priority FROM policies WHERE id = $1`, created.ID).Scan(&priority)
	if priority != 1 {
		t.Fatalf("REGRESSION: policy priority changed to %d despite every write attempt being 403'd", priority)
	}
}
