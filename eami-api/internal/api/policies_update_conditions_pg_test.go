// policies_update_conditions_pg_test.go -- eami-api/internal/api
//
// Real-Postgres regression test for a real, pre-existing bug found live
// while verifying B-210's workspace-scoped policy editing:
// UpsertPolicyCondition (internal/store/policies.sql.go) has always done
// `INSERT INTO policy_conditions (...) ON CONFLICT (policy_id) DO UPDATE
// ...`, but policy_conditions.policy_id never had a UNIQUE constraint
// (only a plain index) -- so any real PATCH that includes `conditions`
// 500'd with a real Postgres error (42P10, "no unique or exclusion
// constraint matching the ON CONFLICT specification"), on BOTH the
// org-wide PATCH /v1/gateway/policies/{policyId} and the workspace-scoped
// PATCH /v1/workspaces/{workspaceId}/policies/{policyId} -- confirmed live
// against the real running container before writing the fix (migration
// 000023_policy_conditions_unique_policy_id). PolicyPanel.tsx's real form
// always sends `conditions`, so this broke every real edit through the UI,
// not just an edge case -- reused workspaceTestEnv (workspaces_pg_test.go)
// exactly, same as the other B-210 test files.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestUpdatePolicy_RealDB_WithConditions -v
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/eami/api/internal/store"
)

func TestUpdatePolicy_RealDB_WithConditions_OrgWide(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "update-conditions-org")
	admin := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, admin, orgID, "admin@update-conditions.test", "admin")

	q := store.New(env.pool)
	pol, err := q.CreatePolicy(ctx, store.CreatePolicyParams{
		OrgID: orgID, Name: "org-wide-update-test", Priority: 5, Action: "deny", Status: "active",
	})
	if err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	resp := env.do(t, http.MethodPatch, "/v1/gateway/policies/"+pol.ID.String(), adminTok, map[string]any{
		"name": "org-wide-update-test (edited)", "priority": 5, "action": "deny", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"some_tool"}},
	})
	body := readWSBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with conditions: expected 200, got %d: %s -- this is the exact real bug (42P10) this migration fixes", resp.StatusCode, body)
	}
	var got struct {
		Name       string `json:"name"`
		Conditions struct {
			ToolNames []string `json:"tool_names"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "org-wide-update-test (edited)" {
		t.Fatalf("name = %q, want the edited name", got.Name)
	}
	if len(got.Conditions.ToolNames) != 1 || got.Conditions.ToolNames[0] != "some_tool" {
		t.Fatalf("conditions.tool_names = %v, want [\"some_tool\"]", got.Conditions.ToolNames)
	}

	// A second PATCH with conditions must also succeed -- proves the
	// UNIQUE constraint's ON CONFLICT DO UPDATE path (not just the first
	// INSERT-shaped call) works correctly too.
	resp2 := env.do(t, http.MethodPatch, "/v1/gateway/policies/"+pol.ID.String(), adminTok, map[string]any{
		"name": "org-wide-update-test (edited again)", "priority": 5, "action": "deny", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"another_tool"}},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second PATCH with conditions: expected 200, got %d: %s", resp2.StatusCode, readWSBody(resp2))
	}
}

func TestUpdatePolicy_RealDB_WithConditions_WorkspaceScoped(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "update-conditions-ws")
	wsID := env.seedWorkspace(t, ctx, orgID, "Update Conditions Workspace")
	admin := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, admin, wsID, "workspace_admin")
	adminTok := env.token(t, admin, orgID, "wsadmin@update-conditions.test", "operator")

	createResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", adminTok, map[string]any{
		"name": "ws-update-test", "priority": 5, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"initial_tool"}},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createResp.StatusCode, readWSBody(createResp))
	}
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	// The real bug: creating with conditions goes through a plain INSERT
	// (works fine, ON CONFLICT is never exercised); it's the SECOND write
	// -- an update, hitting the ON CONFLICT DO UPDATE branch -- that 500'd.
	resp := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsID.String()+"/policies/"+created.ID, adminTok, map[string]any{
		"name": "ws-update-test (edited)", "priority": 5, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"edited_tool"}},
	})
	body := readWSBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with conditions: expected 200, got %d: %s -- this is the exact real bug (42P10) this migration fixes", resp.StatusCode, body)
	}
	var got struct {
		Name          string  `json:"name"`
		WorkspaceName *string `json:"workspace_name"`
		Conditions    struct {
			ToolNames []string `json:"tool_names"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "ws-update-test (edited)" {
		t.Fatalf("name = %q, want the edited name", got.Name)
	}
	if got.WorkspaceName == nil || *got.WorkspaceName != "Update Conditions Workspace" {
		t.Fatalf("workspace_name = %v, want the real workspace name", got.WorkspaceName)
	}
	if len(got.Conditions.ToolNames) != 1 || got.Conditions.ToolNames[0] != "edited_tool" {
		t.Fatalf("conditions.tool_names = %v, want [\"edited_tool\"]", got.Conditions.ToolNames)
	}
}
