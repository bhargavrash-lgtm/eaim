package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// B-233: an org admin's requireWorkspaceRole bypass ran before any org
// check, and the membership UPDATE/DELETE filtered only by user and
// workspace, so an org-A admin holding org B's workspace and user UUIDs
// could promote, demote or remove org B's members. Same adversarial shape
// as B-141/B-232: real attacker org, real victim org, the attempt must be
// rejected with a non-revealing 404, and the victim's stored membership
// must be unchanged (read straight from Postgres).

func membershipRole(t *testing.T, env *workspaceTestEnv, ctx context.Context, userID, workspaceID uuid.UUID) string {
	t.Helper()
	var role string
	err := env.pool.QueryRow(ctx, `SELECT coalesce((SELECT role FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2), '<none>')`, userID, workspaceID).Scan(&role)
	if err != nil {
		t.Fatal(err)
	}
	return role
}

func TestWorkspaceMember_CrossOrgMutationRejected_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b233-attacker")
	orgB := seedTestOrg(t, ctx, env.pool, "b233-victim")
	attacker := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "admin@b233-a.test", "admin")

	wsB := env.seedWorkspace(t, ctx, orgB, "B233 victim workspace")
	victimMember := seedTestUser(t, ctx, env.pool, orgB)
	env.seedMembership(t, ctx, victimMember, wsB, "workspace_member")
	victimAdmin := seedTestUser(t, ctx, env.pool, orgB)
	env.seedMembership(t, ctx, victimAdmin, wsB, "workspace_admin")

	// The non-revealing baseline: a workspace id that exists nowhere.
	baseline := expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/workspaces/%s/members/%s", uuid.New(), victimMember), attacker, map[string]any{"role": "workspace_admin"}), http.StatusNotFound, "PATCH member of a nonexistent workspace")
	// Pinned to requireWorkspaceRole's own ownership-check response, so a
	// regression where the handler (not the middleware) rejects is caught.
	if !strings.Contains(baseline, "workspace not found") {
		t.Fatalf("nonexistent-workspace 404 body %q, want requireWorkspaceRole's \"workspace not found\"", baseline)
	}

	cases := []struct {
		name, method string
		user         uuid.UUID
		body         any
	}{
		{"promote existing member", http.MethodPatch, victimMember, map[string]any{"role": "workspace_admin"}},
		{"demote existing workspace_admin", http.MethodPatch, victimAdmin, map[string]any{"role": "workspace_member"}},
		{"role change for nonexistent user", http.MethodPatch, uuid.New(), map[string]any{"role": "workspace_admin"}},
		{"remove existing member", http.MethodDelete, victimMember, nil},
		{"remove existing workspace_admin", http.MethodDelete, victimAdmin, nil},
		{"remove nonexistent user", http.MethodDelete, uuid.New(), nil},
	}
	for _, c := range cases {
		resp := env.do(t, c.method, fmt.Sprintf("/v1/workspaces/%s/members/%s", wsB, c.user), attacker, c.body)
		got := expectCMDBStatus(t, resp, http.StatusNotFound, "cross-org "+c.name)
		if got != baseline {
			t.Fatalf("cross-org %s: 404 body %q differs from the nonexistent-workspace 404 %q (existence oracle, or rejected by a layer other than the ownership check)", c.name, got, baseline)
		}
	}

	if r := membershipRole(t, env, ctx, victimMember, wsB); r != "workspace_member" {
		t.Fatalf("REGRESSION B-233: victim member's role is %q after cross-org attempts, want workspace_member", r)
	}
	if r := membershipRole(t, env, ctx, victimAdmin, wsB); r != "workspace_admin" {
		t.Fatalf("REGRESSION B-233: victim workspace_admin's role is %q after cross-org attempts, want workspace_admin", r)
	}
	// Every other route behind requireWorkspaceRole is refused identically
	// (security review Low): the ownership check guards the whole group.
	var nameBefore string
	if err := env.pool.QueryRow(ctx, `SELECT name FROM workspaces WHERE id=$1`, wsB).Scan(&nameBefore); err != nil {
		t.Fatal(err)
	}
	ws := fmt.Sprintf("/v1/workspaces/%s", wsB)
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, ws + "/members", nil},
		{http.MethodPatch, ws, map[string]any{"name": "pwned"}},
		{http.MethodPost, ws + "/members", map[string]any{"user_id": victimMember.String(), "role": "workspace_admin"}},
		{http.MethodGet, ws + "/policies", nil},
		{http.MethodPost, ws + "/policies", map[string]any{"name": "b233-cross-org", "action": "deny", "priority": 5, "conditions": map[string]any{}}},
		{http.MethodPatch, ws + "/policies/" + uuid.NewString(), map[string]any{"status": "disabled"}},
		{http.MethodDelete, ws + "/policies/" + uuid.NewString(), nil},
	} {
		got := expectCMDBStatus(t, env.do(t, rt.method, rt.path, attacker, rt.body), http.StatusNotFound, "cross-org "+rt.method+" "+rt.path)
		if got != baseline {
			t.Fatalf("cross-org %s %s: 404 body %q differs from the nonexistent-workspace 404 %q", rt.method, rt.path, got, baseline)
		}
	}
	var nameAfter string
	var wsPolicies int
	if err := env.pool.QueryRow(ctx, `SELECT name, (SELECT count(*) FROM policies WHERE workspace_id=$1) FROM workspaces WHERE id=$1`, wsB).Scan(&nameAfter, &wsPolicies); err != nil {
		t.Fatal(err)
	}
	if nameAfter != nameBefore || wsPolicies != 0 {
		t.Fatalf("REGRESSION B-233: cross-org requests changed org B's workspace: name %q -> %q, workspace policies=%d", nameBefore, nameAfter, wsPolicies)
	}
}

func TestWorkspaceMember_SameOrgManagementStillWorks_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b233-same-org")
	adminID := seedTestUser(t, ctx, env.pool, orgID)
	admin := env.token(t, adminID, orgID, "admin@b233.test", "admin")
	ws := env.seedWorkspace(t, ctx, orgID, "B233 own workspace")
	member := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, member, ws, "workspace_member")
	// A non-admin workspace_admin of this workspace manages it too.
	wsAdminID := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, wsAdminID, ws, "workspace_admin")
	wsAdmin := env.token(t, wsAdminID, orgID, "wsadmin@b233.test", "operator")

	path := fmt.Sprintf("/v1/workspaces/%s/members/%s", ws, member)
	expectCMDBStatus(t, env.do(t, http.MethodPatch, path, admin, map[string]any{"role": "workspace_admin"}), http.StatusNoContent, "org admin promotes own-org member")
	if r := membershipRole(t, env, ctx, member, ws); r != "workspace_admin" {
		t.Fatalf("promotion not persisted: %q", r)
	}
	expectCMDBStatus(t, env.do(t, http.MethodPatch, path, wsAdmin, map[string]any{"role": "workspace_member"}), http.StatusNoContent, "workspace_admin demotes own member")
	if r := membershipRole(t, env, ctx, member, ws); r != "workspace_member" {
		t.Fatalf("demotion not persisted: %q", r)
	}
	body := expectCMDBStatus(t, env.do(t, http.MethodPatch, fmt.Sprintf("/v1/workspaces/%s/members/%s", ws, uuid.New()), admin, map[string]any{"role": "workspace_admin"}), http.StatusNotFound, "own workspace, nonexistent member")
	if body == "" {
		t.Fatal("expected a not_found body")
	}
	expectCMDBStatus(t, env.do(t, http.MethodDelete, path, admin, nil), http.StatusNoContent, "org admin removes own-org member")
	if r := membershipRole(t, env, ctx, member, ws); r != "<none>" {
		t.Fatalf("removal not persisted: %q", r)
	}
}

// Layer 2 in isolation: the store writes themselves are org-scoped, so even
// a caller that skipped requireWorkspaceRole could not touch another org's
// membership. The HTTP test above never reaches this layer (the middleware
// rejects first), so this is the test that catches its removal.
func TestWorkspaceMemberStore_OrgScopedWrites_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b233-store-a")
	orgB := seedTestOrg(t, ctx, env.pool, "b233-store-b")
	wsB := env.seedWorkspace(t, ctx, orgB, "B233 store workspace")
	member := seedTestUser(t, ctx, env.pool, orgB)
	env.seedMembership(t, ctx, member, wsB, "workspace_member")
	q := store.New(env.pool)

	if n, err := q.UpdateWorkspaceMemberRole(ctx, orgA, wsB, member, "workspace_admin"); err != nil || n != 0 {
		t.Fatalf("wrong-org role update: n=%d err=%v, want 0 rows", n, err)
	}
	if n, err := q.RemoveWorkspaceMember(ctx, orgA, wsB, member); err != nil || n != 0 {
		t.Fatalf("wrong-org removal: n=%d err=%v, want 0 rows", n, err)
	}
	if r := membershipRole(t, env, ctx, member, wsB); r != "workspace_member" {
		t.Fatalf("wrong-org store writes changed the membership: %q", r)
	}
	if n, err := q.UpdateWorkspaceMemberRole(ctx, orgB, wsB, member, "workspace_admin"); err != nil || n != 1 {
		t.Fatalf("right-org role update: n=%d err=%v, want 1 row", n, err)
	}
	if n, err := q.RemoveWorkspaceMember(ctx, orgB, wsB, member); err != nil || n != 1 {
		t.Fatalf("right-org removal: n=%d err=%v, want 1 row", n, err)
	}
}
