// workspaces_pg_test.go -- eami-api/internal/api
//
// Real-Postgres integration tests for B-197 increment 3: Workspace CRUD,
// workspace membership management, and requireWorkspaceRole. Follows
// workflows_test.go's seedTestOrg/seedTestUser + tools_update_pg_test.go's
// toolsUpdateTestDSN convention exactly (established, reused pattern --
// see agents_pg_test.go for the same shape with real HTTP + JWT).
//
// Same severity class as B-128/B-141/B-207 -- every one of this file's
// adversarial tests proves a real access-control boundary against a real
// running server + real database, not a mocked/unit-level assertion.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestWorkspace -v
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestRequireWorkspaceRole -v
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestAddWorkspaceMember -v
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// ── shared test env ─────────────────────────────────────────────────────────────

type workspaceTestEnv struct {
	pool    *pgxpool.Pool
	authSvc *auth.Service
	http    *http.Client
	url     string
}

func newWorkspaceTestEnv(t *testing.T) *workspaceTestEnv {
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
	// t.Cleanup, not a plain defer -- registered before any per-row DELETE
	// t.Cleanup this test file's seed helpers add (CLAUDE.md's mandatory
	// real-Postgres pool lifecycle rule).
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &workspaceTestEnv{pool: pool, authSvc: authSvc, http: ts.Client(), url: ts.URL}
}

// seedWorkspace inserts a real groups + workspaces row directly (bypassing
// the API, for direct test-setup control) and registers cleanup. Mirrors
// the real IS-A relationship CreateWorkspace's own handler builds.
func (e *workspaceTestEnv) seedWorkspace(t *testing.T, ctx context.Context, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var groupID uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO groups (org_id, name) VALUES ($1, $2) RETURNING id`,
		orgID, name+"-group").Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	var wsID uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO workspaces (org_id, group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		orgID, groupID, name).Scan(&wsID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM groups WHERE id = $1`, groupID) })
	return wsID
}

func (e *workspaceTestEnv) seedMembership(t *testing.T, ctx context.Context, userID, workspaceID uuid.UUID, role string) {
	t.Helper()
	if _, err := e.pool.Exec(ctx, `INSERT INTO workspace_memberships (user_id, workspace_id, role) VALUES ($1, $2, $3)`,
		userID, workspaceID, role); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

func (e *workspaceTestEnv) token(t *testing.T, userID, orgID uuid.UUID, email, role string) string {
	t.Helper()
	tok, _, err := e.authSvc.IssueAccessToken(userID, orgID, email, role)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

func (e *workspaceTestEnv) do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.url+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func readWSBody(resp *http.Response) string {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// ── CRUD lifecycle ───────────────────────────────────────────────────────────────

func TestWorkspaces_RealDB_CreateListGetUpdateDelete(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "ws-crud")
	adminID := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, adminID, orgID, "admin@ws-crud.test", "admin")

	// Create.
	createResp := env.do(t, http.MethodPost, "/v1/workspaces", adminTok, map[string]string{"name": "Engineering"})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201: %s", createResp.StatusCode, readWSBody(createResp))
	}
	var created struct {
		ID      string `json:"id"`
		GroupID string `json:"group_id"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	createResp.Body.Close()
	if created.ID == "" || created.GroupID == "" || created.Name != "Engineering" {
		t.Fatalf("unexpected create response: %+v", created)
	}
	// Confirm the creator was auto-granted workspace_admin (security-review
	// finding: without this, a brand-new workspace starts with zero
	// members) -- a real row, not just a claimed side effect.
	var creatorRole string
	env.pool.QueryRow(ctx, `SELECT role FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2`,
		adminID, created.ID).Scan(&creatorRole)
	if creatorRole != "workspace_admin" {
		t.Fatalf("REGRESSION: creator's own membership role = %q, want workspace_admin -- CreateWorkspace "+
			"must auto-grant the creator, not leave the workspace ownerless", creatorRole)
	}

	// Confirm the real IS-A relationship: a genuine groups row backs it.
	var groupCount int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM groups WHERE id = $1`, created.GroupID).Scan(&groupCount)
	if groupCount != 1 {
		t.Fatalf("expected a real backing groups row, found %d", groupCount)
	}

	// List.
	listResp := env.do(t, http.MethodGet, "/v1/workspaces", adminTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want 200: %s", listResp.StatusCode, readWSBody(listResp))
	}
	var list struct {
		Data []struct{ ID, Name string } `json:"data"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	listResp.Body.Close()
	found := false
	for _, w := range list.Data {
		if w.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created workspace not found in list: %+v", list.Data)
	}

	// Get.
	getResp := env.do(t, http.MethodGet, "/v1/workspaces/"+created.ID, adminTok, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, want 200: %s", getResp.StatusCode, readWSBody(getResp))
	}
	getResp.Body.Close()

	// Update (admin bypass, no membership row needed).
	updateResp := env.do(t, http.MethodPatch, "/v1/workspaces/"+created.ID, adminTok, map[string]string{"name": "Engineering (renamed)"})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, want 200: %s", updateResp.StatusCode, readWSBody(updateResp))
	}
	var updated struct{ Name string }
	json.NewDecoder(updateResp.Body).Decode(&updated)
	updateResp.Body.Close()
	if updated.Name != "Engineering (renamed)" {
		t.Fatalf("update: name = %q, want %q", updated.Name, "Engineering (renamed)")
	}

	// Delete.
	delResp := env.do(t, http.MethodDelete, "/v1/workspaces/"+created.ID, adminTok, nil)
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204: %s", delResp.StatusCode, readWSBody(delResp))
	}
	delResp.Body.Close()

	// Confirm the real cascade: both the workspace AND its backing group are gone.
	getAfter := env.do(t, http.MethodGet, "/v1/workspaces/"+created.ID, adminTok, nil)
	if getAfter.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404: %s", getAfter.StatusCode, readWSBody(getAfter))
	}
	getAfter.Body.Close()
	env.pool.QueryRow(ctx, `SELECT count(*) FROM groups WHERE id = $1`, created.GroupID).Scan(&groupCount)
	if groupCount != 0 {
		t.Fatalf("REGRESSION: backing groups row survived DeleteWorkspace, want 0, got %d -- the cascade chain is broken", groupCount)
	}
}

// ── Mandatory adversarial test 1 ─────────────────────────────────────────────────
// A user with NO row in workspace_memberships cannot access the workspace,
// even holding a normally-sufficient org-level role (operator -- which
// passes requireRole("admin","operator") elsewhere in this app, but must
// NOT bypass requireWorkspaceRole the way "admin" deliberately does).
func TestRequireWorkspaceRole_RealDB_NoMembershipRow_Forbidden(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "ws-nomember")
	wsID := env.seedWorkspace(t, ctx, orgID, "Restricted WS")

	operatorID := seedTestUser(t, ctx, env.pool, orgID)
	operatorTok := env.token(t, operatorID, orgID, "operator@ws-nomember.test", "operator")

	// No workspace_memberships row seeded for operatorID at all.
	resp := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsID.String(), operatorTok, map[string]string{"name": "Hijacked"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: status = %d, want 403 -- an operator with no workspace_memberships row "+
			"must not be able to act on a workspace: %s", resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	// Confirm nothing actually changed.
	var name string
	env.pool.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, wsID).Scan(&name)
	if name != "Restricted WS" {
		t.Fatalf("REGRESSION: workspace name changed to %q despite the 403", name)
	}
}

// ── Mandatory adversarial test 2 ─────────────────────────────────────────────────
// A workspace_admin of Workspace A cannot manage membership in Workspace B.
func TestRequireWorkspaceRole_RealDB_WorkspaceAdminOfDifferentWorkspace_CannotManageOther(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "ws-crossws")
	wsA := env.seedWorkspace(t, ctx, orgID, "Workspace A")
	wsB := env.seedWorkspace(t, ctx, orgID, "Workspace B")

	adminOfA := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, adminOfA, wsA, "workspace_admin")
	adminOfATok := env.token(t, adminOfA, orgID, "admin-a@ws-crossws.test", "operator") // org role deliberately NOT "admin"

	victim := seedTestUser(t, ctx, env.pool, orgID)

	// adminOfA is genuinely workspace_admin of A -- sanity-check that works first.
	okResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsA.String()+"/members", adminOfATok,
		map[string]string{"user_id": victim.String(), "role": "workspace_member"})
	if okResp.StatusCode != http.StatusCreated {
		t.Fatalf("sanity check: adding a member to A (own workspace) failed, status = %d: %s", okResp.StatusCode, readWSBody(okResp))
	}
	okResp.Body.Close()

	// The real adversarial case: same token, same user, attempting the
	// IDENTICAL action against Workspace B, where they hold no role at all.
	victim2 := seedTestUser(t, ctx, env.pool, orgID)
	badResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsB.String()+"/members", adminOfATok,
		map[string]string{"user_id": victim2.String(), "role": "workspace_member"})
	if badResp.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: status = %d, want 403 -- workspace_admin of A must not manage membership in B: %s",
			badResp.StatusCode, readWSBody(badResp))
	}
	badResp.Body.Close()

	var count int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM workspace_memberships WHERE workspace_id = $1 AND user_id = $2`, wsB, victim2).Scan(&count)
	if count != 0 {
		t.Fatalf("REGRESSION: victim2 was actually added to Workspace B despite the 403")
	}
}

// ── Mandatory adversarial test 3 ─────────────────────────────────────────────────
// Mirrors B-141's own cross-org identity test shape: a client-supplied
// workspace_id belonging to a DIFFERENT org than the caller's own
// token-asserted org must be rejected, not trusted -- even if the caller
// genuinely holds a workspace_admin row for a SAME-NAMED or coincidentally
// real workspace in their own org, the specific ID in the URL must resolve
// against the real org boundary, server-side, every time.
func TestRequireWorkspaceRole_RealDB_CrossOrgWorkspaceID_Rejected(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()

	orgA := seedTestOrg(t, ctx, env.pool, "ws-xorg-a")
	orgB := seedTestOrg(t, ctx, env.pool, "ws-xorg-b")
	wsInOrgB := env.seedWorkspace(t, ctx, orgB, "Org B's Workspace")

	// A user who is a real admin -- but of Org A, not Org B. Their JWT
	// legitimately claims OrgID=orgA (server-set at login, never forgeable
	// by the client) -- the middleware must resolve wsInOrgB against
	// THAT claimed org, not just check "does this workspace_id exist
	// anywhere."
	userA := seedTestUser(t, ctx, env.pool, orgA)
	tokA := env.token(t, userA, orgA, "admin@ws-xorg-a.test", "admin")

	resp := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsInOrgB.String(), tokA, map[string]string{"name": "Hijacked From Org A"})
	// Org A's own admin bypass short-circuits BEFORE the workspace lookup
	// in the current design -- this is deliberately checked at the
	// UpdateWorkspace HANDLER level too (org_id = uc.OrgID in every query),
	// so even an org-admin's bypass cannot reach a different org's row.
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: status = %d, want 404 or 403 -- a cross-org workspace_id must never be "+
			"actionable, even by an org-admin whose own bypass skips the membership lookup: %s",
			resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	var name string
	env.pool.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, wsInOrgB).Scan(&name)
	if name != "Org B's Workspace" {
		t.Fatalf("REGRESSION: Org B's workspace was renamed by an Org A caller: now %q", name)
	}

	// Same proof again, this time via requireWorkspaceRole's own
	// non-admin path: a genuine workspace_admin row in Org A for a
	// DIFFERENT workspace must not let its holder reach Org B's workspace
	// ID at all -- the membership lookup itself is joined against
	// w.org_id = the caller's own token org, so no row is ever found.
	wsInOrgA := env.seedWorkspace(t, ctx, orgA, "Org A's Own Workspace")
	userA2 := seedTestUser(t, ctx, env.pool, orgA)
	env.seedMembership(t, ctx, userA2, wsInOrgA, "workspace_admin")
	tokA2 := env.token(t, userA2, orgA, "wsadmin@ws-xorg-a.test", "operator")

	resp2 := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsInOrgB.String(), tokA2, map[string]string{"name": "Hijacked Again"})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("REGRESSION: status = %d, want 403 -- a workspace_admin token scoped to Org A must never "+
			"resolve a workspace_id belonging to Org B: %s", resp2.StatusCode, readWSBody(resp2))
	}
	resp2.Body.Close()
}

// ── Mandatory adversarial test 4 ─────────────────────────────────────────────────
// "Account-wide admin" (B-197 Part A.3): a user holding multiple
// workspace_admin rows across DIFFERENT workspaces just works, with zero
// new code/mechanism -- proven by successfully acting as workspace_admin
// in BOTH, independently, and by /v1/workspaces/mine reporting both real
// rows correctly.
func TestWorkspaceMemberships_RealDB_AccountWideAdminAcrossMultipleWorkspaces_WorksWithNoNewCode(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "ws-multiadmin")
	wsA := env.seedWorkspace(t, ctx, orgID, "Multi A")
	wsB := env.seedWorkspace(t, ctx, orgID, "Multi B")
	wsC := env.seedWorkspace(t, ctx, orgID, "Multi C") // deliberately NOT granted -- negative control

	user := seedTestUser(t, ctx, env.pool, orgID)
	env.seedMembership(t, ctx, user, wsA, "workspace_admin")
	env.seedMembership(t, ctx, user, wsB, "workspace_admin")
	tok := env.token(t, user, orgID, "multiadmin@ws-multiadmin.test", "operator")

	// Acts as workspace_admin in A.
	respA := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsA.String(), tok, map[string]string{"name": "Multi A (updated)"})
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("REGRESSION: workspace_admin of A (one of several memberships) rejected: status=%d: %s", respA.StatusCode, readWSBody(respA))
	}
	respA.Body.Close()

	// Acts as workspace_admin in B too -- the SAME token, same middleware,
	// no special-casing for "which workspace this time."
	respB := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsB.String(), tok, map[string]string{"name": "Multi B (updated)"})
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("REGRESSION: workspace_admin of B (a second, independent membership) rejected: status=%d: %s", respB.StatusCode, readWSBody(respB))
	}
	respB.Body.Close()

	// Negative control: C, where they hold no membership, still correctly rejected.
	respC := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsC.String(), tok, map[string]string{"name": "Should Not Work"})
	if respC.StatusCode != http.StatusForbidden {
		t.Fatalf("negative control failed: workspace C (no membership) status=%d, want 403: %s", respC.StatusCode, readWSBody(respC))
	}
	respC.Body.Close()

	// /v1/workspaces/mine reports exactly the 2 real rows, correctly, not C.
	mineResp := env.do(t, http.MethodGet, "/v1/workspaces/mine", tok, nil)
	if mineResp.StatusCode != http.StatusOK {
		t.Fatalf("mine: status = %d, want 200: %s", mineResp.StatusCode, readWSBody(mineResp))
	}
	var mine struct {
		Data []struct {
			WorkspaceID string `json:"workspace_id"`
			Role        string `json:"role"`
		} `json:"data"`
	}
	json.NewDecoder(mineResp.Body).Decode(&mine)
	mineResp.Body.Close()
	if len(mine.Data) != 2 {
		t.Fatalf("mine: got %d memberships, want exactly 2: %+v", len(mine.Data), mine.Data)
	}
	seen := map[string]string{}
	for _, m := range mine.Data {
		seen[m.WorkspaceID] = m.Role
	}
	if seen[wsA.String()] != "workspace_admin" || seen[wsB.String()] != "workspace_admin" {
		t.Fatalf("mine: unexpected membership contents: %+v", seen)
	}
	if _, ok := seen[wsC.String()]; ok {
		t.Fatalf("REGRESSION: /mine reported a membership in workspace C that was never granted")
	}
}

// ── Mandatory adversarial test 5 (explicit founder addition) ────────────────────
// workspace_memberships has no org_id column of its own (migration
// 000021) -- AddWorkspaceMember's own explicit cross-org check is the
// ONLY thing enforcing that a membership row's user and workspace belong
// to the same org. Proven directly, not assumed correct.
func TestAddWorkspaceMember_RealDB_DifferentOrgUser_Rejected(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()

	orgA := seedTestOrg(t, ctx, env.pool, "ws-addmember-a")
	orgB := seedTestOrg(t, ctx, env.pool, "ws-addmember-b")
	wsInOrgA := env.seedWorkspace(t, ctx, orgA, "Org A Workspace")

	wsAdmin := seedTestUser(t, ctx, env.pool, orgA)
	env.seedMembership(t, ctx, wsAdmin, wsInOrgA, "workspace_admin")
	wsAdminTok := env.token(t, wsAdmin, orgA, "wsadmin@ws-addmember-a.test", "operator")

	// A real user, but seeded in Org B -- not Org A, where the workspace lives.
	userInOrgB := seedTestUser(t, ctx, env.pool, orgB)

	resp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsInOrgA.String()+"/members", wsAdminTok,
		map[string]string{"user_id": userInOrgB.String(), "role": "workspace_member"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("REGRESSION: status = %d, want 400 -- AddWorkspaceMember must reject a user from a "+
			"different org than the workspace; workspace_memberships has no org_id column of its own, "+
			"so this application-layer check is the ONLY thing enforcing that boundary: %s",
			resp.StatusCode, readWSBody(resp))
	}
	resp.Body.Close()

	var count int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM workspace_memberships WHERE workspace_id = $1 AND user_id = $2`,
		wsInOrgA, userInOrgB).Scan(&count)
	if count != 0 {
		t.Fatalf("REGRESSION: a cross-org membership row was actually inserted despite the 400")
	}

	// Positive control: a real, same-org user is accepted normally --
	// proves the check is scoped to the actual org mismatch, not
	// accidentally rejecting every add.
	userInOrgA := seedTestUser(t, ctx, env.pool, orgA)
	okResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsInOrgA.String()+"/members", wsAdminTok,
		map[string]string{"user_id": userInOrgA.String(), "role": "workspace_member"})
	if okResp.StatusCode != http.StatusCreated {
		t.Fatalf("positive control: same-org user rejected, status=%d: %s", okResp.StatusCode, readWSBody(okResp))
	}
	okResp.Body.Close()
}

// ── Membership CRUD + role update/remove ────────────────────────────────────────

func TestWorkspaceMembers_RealDB_AddUpdateRoleRemove(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "ws-membercrud")
	wsID := env.seedWorkspace(t, ctx, orgID, "Member CRUD WS")

	adminID := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, adminID, orgID, "admin@ws-membercrud.test", "admin")
	member := seedTestUser(t, ctx, env.pool, orgID)

	addResp := env.do(t, http.MethodPost, "/v1/workspaces/"+wsID.String()+"/members", adminTok,
		map[string]string{"user_id": member.String(), "role": "workspace_member"})
	if addResp.StatusCode != http.StatusCreated {
		t.Fatalf("add: status = %d, want 201: %s", addResp.StatusCode, readWSBody(addResp))
	}
	addResp.Body.Close()

	listResp := env.do(t, http.MethodGet, "/v1/workspaces/"+wsID.String()+"/members", adminTok, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list members: status = %d: %s", listResp.StatusCode, readWSBody(listResp))
	}
	var list struct {
		Data []struct{ UserID, Role, Email string } `json:"data"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	listResp.Body.Close()
	if len(list.Data) != 1 || list.Data[0].Role != "workspace_member" || list.Data[0].Email == "" {
		t.Fatalf("unexpected member list: %+v", list.Data)
	}

	updResp := env.do(t, http.MethodPatch, "/v1/workspaces/"+wsID.String()+"/members/"+member.String(), adminTok,
		map[string]string{"role": "workspace_admin"})
	if updResp.StatusCode != http.StatusNoContent {
		t.Fatalf("update role: status = %d, want 204: %s", updResp.StatusCode, readWSBody(updResp))
	}
	updResp.Body.Close()
	var role string
	env.pool.QueryRow(ctx, `SELECT role FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2`, member, wsID).Scan(&role)
	if role != "workspace_admin" {
		t.Fatalf("role after update = %q, want workspace_admin", role)
	}

	rmResp := env.do(t, http.MethodDelete, "/v1/workspaces/"+wsID.String()+"/members/"+member.String(), adminTok, nil)
	if rmResp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: status = %d, want 204: %s", rmResp.StatusCode, readWSBody(rmResp))
	}
	rmResp.Body.Close()
	var count int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM workspace_memberships WHERE user_id = $1 AND workspace_id = $2`, member, wsID).Scan(&count)
	if count != 0 {
		t.Fatalf("membership row survived removal")
	}
}
