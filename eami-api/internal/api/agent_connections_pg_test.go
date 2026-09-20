// agent_connections_pg_test.go -- eami-api/internal/api
// Real-Postgres integration tests for B-200: GET /v1/gateway/agents/{id}/
// connections, the backend for the new Agent Detail relationship graph
// (DESIGN_SYSTEM.md §7.1). Follows workflows_test.go's/agents_pg_test.go's
// seedTestOrg/seedTestUser/api.NewServer HTTP-level convention exactly, and
// audit_pg_test.go's computeAuditHashTest/genesisHashTest for real,
// correctly hash-chained audit_log rows (both already package-level in
// api_test, reused here unmodified).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestAgentConnections -v
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

// seedAuditRowFull inserts a real, correctly hash-chained audit_log row
// carrying agent_id/policy_id -- the two columns seedAuditRow (audit_pg_
// test.go) deliberately omits, since this test needs both live.
func seedAuditRowFull(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, agentID uuid.UUID, policyID *uuid.UUID, prevHash, agentName, toolName, action, decision string, ts time.Time) seededAuditRow {
	t.Helper()
	id := uuid.New()
	hash := computeAuditHashTest(prevHash, id, orgID, agentName, toolName, action, decision, ts)
	_, err := pool.Exec(ctx, `
		INSERT INTO audit_log (id, org_id, agent_id, agent_name, tool_name, action, decision, policy_id, timestamp, prev_hash, hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, orgID, agentID, agentName, toolName, action, decision, policyID, ts, prevHash, hash,
	)
	if err != nil {
		t.Fatalf("seed audit_log row: %v", err)
	}
	return seededAuditRow{id: id, timestamp: ts, hash: hash}
}

// seedWorkflowRun inserts a real workflow_runs row directly -- no existing
// store helper for this table (only workflows.go's CreateWorkflow exists),
// mirroring endpoint_agent_link_test.go's raw-INSERT-with-t.Cleanup style.
func seedWorkflowRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, workflowID, agentID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO workflow_runs (id, org_id, workflow_id, agent_id, status)
		VALUES ($1, $2, $3, $4, 'completed')`,
		id, orgID, workflowID, agentID,
	); err != nil {
		t.Fatalf("seed workflow_run: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM workflow_runs WHERE id = $1`, id) })
	return id
}

// seedEndpointLinkedToAgent inserts a real endpoints row already linked to
// gatewayAgentID via B-164's gateway_agent_id FK.
func seedEndpointLinkedToAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, gatewayAgentID uuid.UUID, hostname string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO endpoints (id, org_id, agent_id, hostname, agent_version, gateway_agent_id)
		VALUES ($1, $2, $3, $4, '', $5)`,
		id, orgID, "discovery-"+id.String()[:8], hostname, gatewayAgentID,
	); err != nil {
		t.Fatalf("seed linked endpoint: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM endpoints WHERE id = $1`, id) })
	return id
}

func agentConnectionsTestServer(t *testing.T, q *store.Queries) (*httptest.Server, *auth.Service) {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	return httptest.NewServer(srv.Handler()), authSvc
}

func getAgentConnections(t *testing.T, ts *httptest.Server, token, agentID string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/gateway/agents/"+agentID+"/connections", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET .../connections: %v", err)
	}
	return resp
}

// TestAgentConnections_RealDB_AllFourTypes_RealDataReturned proves AC1: a
// real agent with real, non-trivial history across every relationship type
// gets back real data for all four -- not placeholder content -- including
// the is_active flag landing on the genuinely higher-24h-volume tool.
func TestAgentConnections_RealDB_AllFourTypes_RealDataReturned(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "agentconn-full")
	userID := seedTestUser(t, ctx, pool, orgID)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1`, orgID) })

	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "b200-full-agent", Model: "claude-sonnet-5", Owner: "qa",
		Scope: "real relationship graph test", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Tool: real gateway_tools row + real audit_log dispatch history through
	// it, at two different volumes so is_active is a genuine, checkable
	// signal, not a coincidence of insertion order.
	activeToolID := seedTestTool(t, ctx, q, orgID, "claude-connector")
	quietToolID := seedTestTool(t, ctx, q, orgID, "slack-connector")

	// Policy: a real policy that will actually be referenced by policy_id.
	policy, err := q.CreatePolicy(ctx, store.CreatePolicyParams{
		OrgID: orgID, Name: "b200-escalate-policy", Priority: 1, Action: "escalate", Status: "active",
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	// Workflow + a real run naming this agent.
	wf, err := q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: orgID, Name: "b200-incident-workflow", Status: "active"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	seedWorkflowRun(t, ctx, pool, orgID, wf.ID, agent.ID)

	// Endpoint linked via B-164.
	seedEndpointLinkedToAgent(t, ctx, pool, orgID, agent.ID, "b200-laptop")

	// Real, hash-chained audit_log history: 3 calls through the active
	// tool (all within the last 24h, one carrying the real policy_id), 1
	// older call through the quiet tool (outside the 24h window).
	prev := realCurrentTailHash(t, ctx, pool)
	now := time.Now().UTC()
	pid := policy.ID
	r1 := seedAuditRowFull(t, ctx, pool, orgID, agent.ID, &pid, prev, agent.Name, "claude-connector", "messages", "allowed", now.Add(-2*time.Hour))
	r2 := seedAuditRowFull(t, ctx, pool, orgID, agent.ID, nil, r1.hash, agent.Name, "claude-connector", "messages", "allowed", now.Add(-1*time.Hour))
	r3 := seedAuditRowFull(t, ctx, pool, orgID, agent.ID, &pid, r2.hash, agent.Name, "claude-connector", "messages", "escalated", now.Add(-30*time.Minute))
	_ = seedAuditRowFull(t, ctx, pool, orgID, agent.ID, nil, r3.hash, agent.Name, "slack-connector", "post_message", "allowed", now.Add(-48*time.Hour))

	ts, authSvc := agentConnectionsTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@agentconn-full.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	resp := getAgentConnections(t, ts, token, agent.ID.String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got api.AgentConnectionsResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Tools: 2 distinct tools, active tool correctly resolved to its real
	// tool_id, quiet tool correctly NOT marked active (its only call is
	// outside the 24h window).
	if len(got.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(got.Tools))
	}
	var activeSeen, quietSeen bool
	for _, tc := range got.Tools {
		switch tc.ToolName {
		case "claude-connector":
			activeSeen = true
			if tc.ToolID == nil || *tc.ToolID != activeToolID.String() {
				t.Errorf("claude-connector tool_id = %v, want %s", tc.ToolID, activeToolID)
			}
			if !tc.IsActive {
				t.Error("claude-connector (3 calls, all <24h) should be is_active, was false")
			}
			if tc.CallCount24h != 3 || tc.CallCountTotal != 3 {
				t.Errorf("claude-connector counts = 24h:%d total:%d, want 3/3", tc.CallCount24h, tc.CallCountTotal)
			}
		case "slack-connector":
			quietSeen = true
			if tc.ToolID == nil || *tc.ToolID != quietToolID.String() {
				t.Errorf("slack-connector tool_id = %v, want %s", tc.ToolID, quietToolID)
			}
			if tc.IsActive {
				t.Error("slack-connector (1 call, >24h old) should NOT be is_active, was true")
			}
			if tc.CallCount24h != 0 || tc.CallCountTotal != 1 {
				t.Errorf("slack-connector counts = 24h:%d total:%d, want 0/1", tc.CallCount24h, tc.CallCountTotal)
			}
		}
	}
	if !activeSeen || !quietSeen {
		t.Fatalf("expected both claude-connector and slack-connector in response, got %+v", got.Tools)
	}

	// Policy: exactly the one real policy that actually applied (via 2 of
	// the 4 seeded rows), not a fabricated "assigned to this agent" list.
	if len(got.Policies) != 1 {
		t.Fatalf("policies = %d, want 1 (only policies that actually matched a real decision)", len(got.Policies))
	}
	if got.Policies[0].PolicyID != policy.ID.String() || got.Policies[0].Name != "b200-escalate-policy" || got.Policies[0].Action != "escalate" {
		t.Errorf("policy connection = %+v, want id=%s name=b200-escalate-policy action=escalate", got.Policies[0], policy.ID)
	}

	// Workflow: the one real run.
	if len(got.Workflows) != 1 {
		t.Fatalf("workflows = %d, want 1", len(got.Workflows))
	}
	if got.Workflows[0].WorkflowID != wf.ID.String() || got.Workflows[0].Name != "b200-incident-workflow" {
		t.Errorf("workflow connection = %+v, want id=%s name=b200-incident-workflow", got.Workflows[0], wf.ID)
	}

	// Endpoint: the one real linked endpoint.
	if got.Endpoint == nil {
		t.Fatal("endpoint = nil, want the real linked endpoint")
	}
	if got.Endpoint.Hostname != "b200-laptop" {
		t.Errorf("endpoint hostname = %q, want %q", got.Endpoint.Hostname, "b200-laptop")
	}
}

// TestAgentConnections_RealDB_ZeroConnections_ReturnsEmptyNotError proves a
// real agent with genuinely no history yet gets a clean 200 with empty
// arrays and a null endpoint -- an honest state, not an error, and not
// indistinguishable from a request that silently failed.
func TestAgentConnections_RealDB_ZeroConnections_ReturnsEmptyNotError(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "agentconn-empty")
	userID := seedTestUser(t, ctx, pool, orgID)

	agent, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgID, Name: "b200-empty-agent", Model: "claude-sonnet-5", Owner: "qa",
		Scope: "zero-connections case", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	ts, authSvc := agentConnectionsTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@agentconn-empty.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	resp := getAgentConnections(t, ts, token, agent.ID.String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (zero connections is a real, valid state, not an error)", resp.StatusCode)
	}
	var got api.AgentConnectionsResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Tools) != 0 || len(got.Policies) != 0 || len(got.Workflows) != 0 || got.Endpoint != nil {
		t.Errorf("expected all-empty response for a brand-new agent, got %+v", got)
	}
}

// TestAgentConnections_RealDB_AgentNotFound_Returns404 proves an unknown
// agent ID is a clean 404, not a 200 with empty arrays -- callers must be
// able to distinguish "this agent has no connections" from "this agent
// doesn't exist."
func TestAgentConnections_RealDB_AgentNotFound_Returns404(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "agentconn-404")
	userID := seedTestUser(t, ctx, pool, orgID)

	ts, authSvc := agentConnectionsTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@agentconn-404.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	resp := getAgentConnections(t, ts, token, uuid.New().String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a nonexistent agent", resp.StatusCode)
	}
}

// TestAgentConnections_RealDB_OrgIsolation_NeverLeaksOtherOrgsData is the
// mandatory case for this brief's first new org-scoped query surface.
// Proves two things, not one: (1) org A's own connections response never
// contains org B's rows even when both orgs use the IDENTICAL tool name
// and policy name (so a bug that dropped the org_id filter from the JOIN,
// but kept it on the outer WHERE, would still silently leak); (2) org A's
// user token requesting org B's real agent ID by UUID gets a clean 404,
// never org B's real data and never a 500.
func TestAgentConnections_RealDB_OrgIsolation_NeverLeaksOtherOrgsData(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgA := seedTestOrg(t, ctx, pool, "agentconn-isoA")
	orgB := seedTestOrg(t, ctx, pool, "agentconn-isoB")
	userA := seedTestUser(t, ctx, pool, orgA)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1 OR org_id = $2`, orgA, orgB) })

	agentA, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgA, Name: "iso-agent-a", Model: "claude-sonnet-5", Owner: "qa", Scope: "org isolation test", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}
	agentB, err := q.CreateAgent(ctx, store.CreateAgentParams{
		OrgID: orgB, Name: "iso-agent-b", Model: "claude-sonnet-5", Owner: "qa", Scope: "org isolation test", RiskTier: "low", TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create agent B: %v", err)
	}

	// Deliberately identical names in both orgs -- gateway_tools/policies
	// are only unique per (org_id, name), so this is a real, legal, live
	// scenario, not a contrived edge case.
	seedTestTool(t, ctx, q, orgA, "shared-connector-name")
	seedTestTool(t, ctx, q, orgB, "shared-connector-name")
	policyA, err := q.CreatePolicy(ctx, store.CreatePolicyParams{OrgID: orgA, Name: "shared-policy-name", Priority: 1, Action: "allow", Status: "active"})
	if err != nil {
		t.Fatalf("create policy A: %v", err)
	}
	policyB, err := q.CreatePolicy(ctx, store.CreatePolicyParams{OrgID: orgB, Name: "shared-policy-name", Priority: 1, Action: "deny", Status: "active"})
	if err != nil {
		t.Fatalf("create policy B: %v", err)
	}
	wfA, err := q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: orgA, Name: "shared-workflow-name", Status: "active"})
	if err != nil {
		t.Fatalf("create workflow A: %v", err)
	}
	wfB, err := q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: orgB, Name: "shared-workflow-name", Status: "active"})
	if err != nil {
		t.Fatalf("create workflow B: %v", err)
	}
	seedWorkflowRun(t, ctx, pool, orgA, wfA.ID, agentA.ID)
	seedWorkflowRun(t, ctx, pool, orgB, wfB.ID, agentB.ID)
	seedEndpointLinkedToAgent(t, ctx, pool, orgA, agentA.ID, "org-a-laptop")
	seedEndpointLinkedToAgent(t, ctx, pool, orgB, agentB.ID, "org-b-laptop")

	prevA := realCurrentTailHash(t, ctx, pool)
	pidA := policyA.ID
	rA := seedAuditRowFull(t, ctx, pool, orgA, agentA.ID, &pidA, prevA, agentA.Name, "shared-connector-name", "messages", "allowed", time.Now().UTC())
	pidB := policyB.ID
	seedAuditRowFull(t, ctx, pool, orgB, agentB.ID, &pidB, rA.hash, agentB.Name, "shared-connector-name", "messages", "denied", time.Now().UTC())

	ts, authSvc := agentConnectionsTestServer(t, q)
	defer ts.Close()
	tokenA, _, err := authSvc.IssueAccessToken(userA, orgA, "admin@agentconn-isoA.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken A: %v", err)
	}

	// (1) Org A's own agent: must see ONLY org A's rows for the shared names.
	resp := getAgentConnections(t, ts, tokenA, agentA.ID.String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("org A status = %d, want 200", resp.StatusCode)
	}
	var gotA api.AgentConnectionsResp
	if err := json.NewDecoder(resp.Body).Decode(&gotA); err != nil {
		t.Fatalf("decode org A: %v", err)
	}
	if len(gotA.Policies) != 1 || gotA.Policies[0].PolicyID != policyA.ID.String() || gotA.Policies[0].Action != "allow" {
		t.Fatalf("org A policies = %+v, want exactly org A's own allow policy (%s) -- never org B's deny policy", gotA.Policies, policyA.ID)
	}
	if len(gotA.Workflows) != 1 || gotA.Workflows[0].WorkflowID != wfA.ID.String() {
		t.Fatalf("org A workflows = %+v, want exactly org A's own workflow (%s)", gotA.Workflows, wfA.ID)
	}
	if gotA.Endpoint == nil || gotA.Endpoint.Hostname != "org-a-laptop" {
		t.Fatalf("org A endpoint = %+v, want org-a-laptop, never org B's endpoint", gotA.Endpoint)
	}
	if len(gotA.Tools) != 1 || gotA.Tools[0].CallCountTotal != 1 {
		t.Fatalf("org A tools = %+v, want exactly 1 call (org A's own row only, not org A's + org B's combined)", gotA.Tools)
	}

	// (2) Org A's token requesting org B's real agent ID directly: clean
	// 404, never org B's real data, never a 500.
	crossOrgResp := getAgentConnections(t, ts, tokenA, agentB.ID.String())
	defer crossOrgResp.Body.Close()
	if crossOrgResp.StatusCode != http.StatusNotFound {
		t.Errorf("org A token requesting org B's agent: status = %d, want 404 (must never see org B's data)", crossOrgResp.StatusCode)
	}
}
