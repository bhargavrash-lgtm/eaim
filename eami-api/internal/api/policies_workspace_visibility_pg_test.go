// policies_workspace_visibility_pg_test.go -- eami-api/internal/api
//
// Real-Postgres regression test closing the DESIGN_SYSTEM.md §7.3 gap:
// GET /v1/gateway/policies (the org-wide list PoliciesPage.tsx actually
// renders) never selected workspace_id at all, so a workspace-scoped
// policy created via B-209's real API rendered indistinguishably from an
// org-wide floor policy on that page -- same row shape, no signal of
// which is which. Reuses workspaceTestEnv (workspaces_pg_test.go) exactly,
// same as workspace_policies_pg_test.go already does.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestListPolicies_RealDB_WorkspaceVisibility -v
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/eami/api/internal/store"
)

func TestListPolicies_RealDB_WorkspaceVisibility(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "policy-vis")
	wsID := env.seedWorkspace(t, ctx, orgID, "Policy Visibility Workspace")

	admin := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, admin, orgID, "admin@policy-vis.test", "admin")

	// AC2 regression case: a plain org-floor policy, seeded exactly the
	// way every policy predating tonight's Workspaces work looks --
	// workspace_id NULL, nothing new about it at all.
	q := store.New(env.pool)
	floor, err := q.CreatePolicy(ctx, store.CreatePolicyParams{
		OrgID: orgID, Name: "policy-vis-floor", Priority: 900, Action: "deny", Status: "active",
	})
	if err != nil {
		t.Fatalf("seed floor policy: %v", err)
	}
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM policies WHERE id = $1`, floor.ID) })

	// The real, live case this brief closes: a workspace-scoped policy
	// created via the real B-209 API, not a hand-written SQL insert.
	createResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/policies", adminTok, map[string]any{
		"name": "policy-vis-scoped", "priority": 5, "action": "allow", "alert": false, "status": "active",
		"conditions": map[string]any{"tool_names": []string{"vis-tool"}},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create workspace policy: status = %d, want 201", createResp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(createResp.Body).Decode(&created)
	createResp.Body.Close()

	// The endpoint under test: the plain org-wide list, exactly what
	// PoliciesPage.tsx calls.
	listResp := env.do(t, http.MethodGet, "/v1/gateway/policies", adminTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list policies: status = %d, want 200", listResp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID            string  `json:"id"`
			Name          string  `json:"name"`
			WorkspaceID   *string `json:"workspace_id"`
			WorkspaceName *string `json:"workspace_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	listResp.Body.Close()

	var sawFloor, sawScoped bool
	for _, p := range list.Data {
		switch p.ID {
		case floor.ID.String():
			sawFloor = true
			if p.WorkspaceID != nil || p.WorkspaceName != nil {
				t.Fatalf("org-floor policy must have nil workspace_id/workspace_name, got id=%v name=%v", p.WorkspaceID, p.WorkspaceName)
			}
		case created.ID:
			sawScoped = true
			if p.WorkspaceID == nil || *p.WorkspaceID != wsID.String() {
				t.Fatalf("workspace-scoped policy: workspace_id = %v, want %q", p.WorkspaceID, wsID.String())
			}
			if p.WorkspaceName == nil || *p.WorkspaceName != "Policy Visibility Workspace" {
				t.Fatalf("workspace-scoped policy: workspace_name = %v, want %q", p.WorkspaceName, "Policy Visibility Workspace")
			}
		}
	}
	if !sawFloor {
		t.Fatalf("seeded org-floor policy never appeared in the list")
	}
	if !sawScoped {
		t.Fatalf("created workspace-scoped policy never appeared in the list")
	}
}
