// cmdb_workspace_merge_pg_test.go -- eami-api/internal/api
//
// Real-Postgres regression tests for B-196 increment 1: GET /v1/gateway/
// agents, GET /v1/gateway/agents/{agentId}, and GET /v1/endpoints must
// surface a real workspace assignment (workspace_id/workspace_name) when
// one exists, and omit both fields (nil) when it doesn't -- the same
// merge-not-extend pattern B-214 established for GET /v1/gateway/policies,
// applied here to the two other real CI-like tables that already carry a
// workspace_id column (gateway_agents, endpoints). Reuses workspaceTestEnv
// (workspaces_pg_test.go) exactly, same as B-210's other test files.
//
// gateway_tools has no workspace_id column at all (confirmed live before
// this increment) -- deliberately NOT tested here, since there's nothing
// to merge; ToolResp simply never carries the field, which is the
// CMDB frontend's own signal to render "Not workspace-scoped" instead of
// a workspace badge.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestCMDBWorkspaceMerge -v
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// seedDiscoveryLicense grants the "discovery" module to orgID -- GET
// /v1/endpoints is gated by requireModuleLicensed("discovery") (router.go),
// which a plain seedTestOrg has no entitlement for by default. Reuses
// license_pg_test.go's own genuineTestVendorKey/signGenuineTestLicense
// helpers (same file/package, already proven to produce a validly-signed
// test license) rather than re-deriving the signing setup here.
func seedDiscoveryLicense(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID) {
	t.Helper()
	raw := signGenuineTestLicense(t, orgID.String(), []string{"discovery"})
	if _, err := env.pool.Exec(ctx,
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		orgID, raw, []string{"discovery"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("seed discovery license: %v", err)
	}
}

// seedCMDBEndpoint inserts a real endpoints row directly, mirroring
// endpoint_agent_link_test.go's insertEndpoint helper (agent_version left
// as '' to match UpsertAgentEndpoint's real never-NULL convention).
func seedCMDBEndpoint(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID, hostname string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO endpoints (id, org_id, agent_id, hostname, agent_version)
		VALUES ($1, $2, $3, $4, '')
	`, id, orgID, "cmdb-test-"+id.String(), hostname); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM endpoints WHERE id = $1`, id) })
	return id
}

func TestCMDBWorkspaceMerge_ListAgents_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-agents-merge")
	wsID := env.seedWorkspace(t, ctx, orgID, "CMDB Agents Workspace")
	admin := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, admin, orgID, "admin@cmdb-agents-merge.test", "admin")

	q := store.New(env.pool)
	assigned, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "cmdb-assigned-agent", Model: "claude-sonnet-5", Owner: "qa", Scope: "test", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create assigned agent: %v", err)
	}
	unassigned, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "cmdb-unassigned-agent", Model: "claude-sonnet-5", Owner: "qa", Scope: "test", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create unassigned agent: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE gateway_agents SET workspace_id = $1 WHERE id = $2`, wsID, assigned.ID); err != nil {
		t.Fatalf("assign workspace: %v", err)
	}

	resp := env.do(t, http.MethodGet, "/v1/gateway/agents", adminTok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListAgents: expected 200, got %d: %s", resp.StatusCode, readWSBody(resp))
	}
	var got struct {
		Data []struct {
			ID            string  `json:"id"`
			WorkspaceID   *string `json:"workspace_id"`
			WorkspaceName *string `json:"workspace_name"`
		} `json:"data"`
	}
	body := readWSBody(resp)
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var sawAssigned, sawUnassigned bool
	for _, a := range got.Data {
		switch a.ID {
		case assigned.ID.String():
			sawAssigned = true
			if a.WorkspaceID == nil || *a.WorkspaceID != wsID.String() {
				t.Errorf("assigned agent: workspace_id = %v, want %q", a.WorkspaceID, wsID.String())
			}
			if a.WorkspaceName == nil || *a.WorkspaceName != "CMDB Agents Workspace" {
				t.Errorf("assigned agent: workspace_name = %v, want %q", a.WorkspaceName, "CMDB Agents Workspace")
			}
		case unassigned.ID.String():
			sawUnassigned = true
			if a.WorkspaceID != nil || a.WorkspaceName != nil {
				t.Errorf("unassigned agent: expected nil workspace fields, got id=%v name=%v", a.WorkspaceID, a.WorkspaceName)
			}
		}
	}
	if !sawAssigned || !sawUnassigned {
		t.Fatalf("expected both seeded agents in response, sawAssigned=%v sawUnassigned=%v, body=%s", sawAssigned, sawUnassigned, body)
	}
}

func TestCMDBWorkspaceMerge_GetAgent_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-getagent-merge")
	wsID := env.seedWorkspace(t, ctx, orgID, "CMDB GetAgent Workspace")
	admin := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, admin, orgID, "admin@cmdb-getagent-merge.test", "admin")

	q := store.New(env.pool)
	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "cmdb-getagent-test", Model: "claude-sonnet-5", Owner: "qa", Scope: "test", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE gateway_agents SET workspace_id = $1 WHERE id = $2`, wsID, agent.ID); err != nil {
		t.Fatalf("assign workspace: %v", err)
	}

	resp := env.do(t, http.MethodGet, "/v1/gateway/agents/"+agent.ID.String(), adminTok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetAgent: expected 200, got %d: %s", resp.StatusCode, readWSBody(resp))
	}
	var got struct {
		WorkspaceID   *string `json:"workspace_id"`
		WorkspaceName *string `json:"workspace_name"`
	}
	if err := json.Unmarshal([]byte(readWSBody(resp)), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.WorkspaceID == nil || *got.WorkspaceID != wsID.String() {
		t.Errorf("workspace_id = %v, want %q", got.WorkspaceID, wsID.String())
	}
	if got.WorkspaceName == nil || *got.WorkspaceName != "CMDB GetAgent Workspace" {
		t.Errorf("workspace_name = %v, want %q", got.WorkspaceName, "CMDB GetAgent Workspace")
	}
}

func TestCMDBWorkspaceMerge_ListEndpoints_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "cmdb-endpoints-merge")
	seedDiscoveryLicense(t, env, ctx, orgID)
	wsID := env.seedWorkspace(t, ctx, orgID, "CMDB Endpoints Workspace")
	admin := seedTestUser(t, ctx, env.pool, orgID)
	adminTok := env.token(t, admin, orgID, "admin@cmdb-endpoints-merge.test", "admin")

	assigned := seedCMDBEndpoint(t, env, ctx, orgID, "cmdb-assigned-host")
	unassigned := seedCMDBEndpoint(t, env, ctx, orgID, "cmdb-unassigned-host")
	if _, err := env.pool.Exec(ctx, `UPDATE endpoints SET workspace_id = $1 WHERE id = $2`, wsID, assigned); err != nil {
		t.Fatalf("assign workspace: %v", err)
	}

	resp := env.do(t, http.MethodGet, "/v1/endpoints", adminTok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListAgentEndpoints: expected 200, got %d: %s", resp.StatusCode, readWSBody(resp))
	}
	var got struct {
		Data []struct {
			ID            string  `json:"id"`
			WorkspaceID   *string `json:"workspace_id"`
			WorkspaceName *string `json:"workspace_name"`
		} `json:"data"`
	}
	body := readWSBody(resp)
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var sawAssigned, sawUnassigned bool
	for _, e := range got.Data {
		switch e.ID {
		case assigned.String():
			sawAssigned = true
			if e.WorkspaceID == nil || *e.WorkspaceID != wsID.String() {
				t.Errorf("assigned endpoint: workspace_id = %v, want %q", e.WorkspaceID, wsID.String())
			}
			if e.WorkspaceName == nil || *e.WorkspaceName != "CMDB Endpoints Workspace" {
				t.Errorf("assigned endpoint: workspace_name = %v, want %q", e.WorkspaceName, "CMDB Endpoints Workspace")
			}
		case unassigned.String():
			sawUnassigned = true
			if e.WorkspaceID != nil || e.WorkspaceName != nil {
				t.Errorf("unassigned endpoint: expected nil workspace fields, got id=%v name=%v", e.WorkspaceID, e.WorkspaceName)
			}
		}
	}
	if !sawAssigned || !sawUnassigned {
		t.Fatalf("expected both seeded endpoints in response, sawAssigned=%v sawUnassigned=%v, body=%s", sawAssigned, sawUnassigned, body)
	}
}
