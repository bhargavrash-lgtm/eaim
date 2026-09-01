// http_org_scoping_test.go -- eami-gateway/internal/workflow
//
// B-141's centerpiece proof, workflow-run leg: two real orgs, each with an
// agent sharing the IDENTICAL name, two real minted JWTs (each carrying
// its own real org_id claim). HandleRun must resolve each token to ITS
// OWN org's agent -- never the other's -- confirmed by querying the real
// workflow_runs row HandleRun produces, not merely a status code. This is
// HandleRun's first direct test coverage; previously only exercised
// indirectly via ratelimit_test.go's RateLimitRunMiddleware wrapping a
// stub "next" handler.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/workflow/... -run TestHandleRun_OrgScoping -v
package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
)

func TestHandleRun_OrgScoping_TwoOrgsSameAgentName_ResolvesCorrectAgentEachTime(t *testing.T) {
	env := newWorkflowTestEnv(t) // org A
	ctx := context.Background()

	// Org B, real, inline -- same pattern as cmd/gateway's own
	// dispatcher_org_scoping_test.go (B-128).
	orgB := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgB, "b141-hrun-org-b-"+orgB.String()[:8], "b141-hrun-org-b-"+orgB.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}
	t.Cleanup(func() { env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgB) })

	const sharedName = "shared-run-agent"
	agentA := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test scope')`,
		agentA, env.orgID, sharedName); err != nil {
		t.Fatalf("insert org A agent: %v", err)
	}
	agentB := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, 'test-model', 'test-owner', 'test scope')`,
		agentB, orgB, sharedName); err != nil {
		t.Fatalf("insert org B agent: %v", err)
	}

	// One ai_provider tool + one-step workflow per org (seedWorkflow always
	// inserts under env.orgID, so org B's workflow is a direct SQL insert
	// mirroring the identical shape).
	toolA := env.insertAIProviderTool(t, "b141-hrun-tool-a", "provider-a")
	wfA := seedWorkflow(t, env, "b141-hrun-wf-a", []struct {
		toolID uuid.UUID
		action string
		params map[string]any
	}{{toolA, "query", nil}})

	toolB := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4)`,
		toolB, orgB, "b141-hrun-tool-b", "provider-a"); err != nil {
		t.Fatalf("insert org B tool: %v", err)
	}
	wfB := uuid.New()
	if _, err := env.pool.Exec(ctx, `INSERT INTO workflows (id, org_id, name, status) VALUES ($1, $2, $3, 'active')`,
		wfB, orgB, "b141-hrun-wf-b"); err != nil {
		t.Fatalf("insert org B workflow: %v", err)
	}
	stepB := uuid.New()
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO workflow_steps (id, workflow_id, step_order, gateway_tool_id, action)
		VALUES ($1, $2, 0, $3, 'query')`, stepB, wfB, toolB); err != nil {
		t.Fatalf("insert org B workflow_step: %v", err)
	}

	// One real dispatch/executor pair, org-parameterized at call time (the
	// underlying toolrouter/aiProviderRouter resolve by whatever OrgID is
	// on the ActionContext they're handed, not a value baked in at
	// construction) -- exactly how one real production gateway process
	// serves every org.
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{"provider-a": &fakeAdapter{name: "provider-a"}})

	idm := newTestIdentityManager(t)
	reg := registry.New(env.pool)
	h := NewHTTPHandler(idm, reg, de.exec)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateway/workflows/{workflowId}/run", h.HandleRun)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	tokenA, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + sharedName, OrgID: env.orgID.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue org A token: %v", err)
	}
	tokenB, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + sharedName, OrgID: orgB.String(), TTLSeconds: 300})
	if err != nil {
		t.Fatalf("issue org B token: %v", err)
	}

	doRun := func(bearer string, workflowID uuid.UUID) uuid.UUID {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, server.URL+"/v1/gateway/workflows/"+workflowID.String()+"/run", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.SetPathValue("workflowId", workflowID.String())
		rec := httptest.NewRecorder()
		h.HandleRun(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleRun: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var result struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("unmarshal RunResult: %v", err)
		}
		runID, err := uuid.Parse(result.RunID)
		if err != nil {
			t.Fatalf("parse run_id %q: %v", result.RunID, err)
		}
		return runID
	}

	runIDA := doRun(tokenA.Token, wfA)
	var gotOrgA, gotAgentA uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT org_id, agent_id FROM workflow_runs WHERE id = $1`, runIDA).Scan(&gotOrgA, &gotAgentA); err != nil {
		t.Fatalf("query org A's workflow_runs row: %v", err)
	}
	if gotOrgA != env.orgID || gotAgentA != agentA {
		t.Errorf("org A's run: got org_id=%s agent_id=%s, want org_id=%s agent_id=%s (org A's own agent, "+
			"never org B's identically-named one)", gotOrgA, gotAgentA, env.orgID, agentA)
	}

	runIDB := doRun(tokenB.Token, wfB)
	var gotOrgB, gotAgentB uuid.UUID
	if err := env.pool.QueryRow(ctx, `SELECT org_id, agent_id FROM workflow_runs WHERE id = $1`, runIDB).Scan(&gotOrgB, &gotAgentB); err != nil {
		t.Fatalf("query org B's workflow_runs row: %v", err)
	}
	if gotOrgB != orgB || gotAgentB != agentB {
		t.Errorf("org B's run: got org_id=%s agent_id=%s, want org_id=%s agent_id=%s (org B's own agent, "+
			"never org A's identically-named one)", gotOrgB, gotAgentB, orgB, agentB)
	}
}

// TestHandleRun_PreCutoverToken_ReturnsUnauthorized (B-141): a token
// minted before Claims gained OrgID is rejected outright, before the
// resolver is ever consulted.
func TestHandleRun_PreCutoverToken_ReturnsUnauthorized(t *testing.T) {
	env := newWorkflowTestEnv(t)
	de := newDispatchEnv(t, env, map[string]aiprovider.Adapter{})
	idm := newTestIdentityManager(t)
	reg := registry.New(env.pool)
	h := NewHTTPHandler(idm, reg, de.exec)

	resp, err := idm.Issue(identity.IssueRequest{AgentID: "agent:" + env.agentName, TTLSeconds: 300}) // no OrgID
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/workflows/"+uuid.New().String()+"/run", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	req.SetPathValue("workflowId", uuid.New().String())
	rec := httptest.NewRecorder()
	h.HandleRun(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 -- a pre-cutover token (no org_id claim) must be rejected outright", rec.Code)
	}
}

