// agent_lineage_pg_test.go -- eami-api/internal/api
// Real-Postgres tests for Agent Lineage: GET /v1/gateway/agents/{id}/lineage.
// Every displayed aggregate is checked twice: against hard-coded values
// derived from the seed below, and against an independent direct SQL query
// over the same rows. Includes the cross-org leak test (foreign-org rows
// carrying the SAME agent_id must never count, and a foreign-org caller
// gets 404).
//
//	POSTGRES_PASSWORD=<...> go test ./internal/api/ -run TestAgentLineage -v
package api_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

type lineageEnv struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	ts      *httptest.Server
	authSvc *auth.Service
	prev    string // audit hash-chain tail for the next seeded row
}

func newLineageEnv(t *testing.T) *lineageEnv {
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
	// Registered FIRST so it runs LAST, after every per-org DELETE below.
	t.Cleanup(func() { pool.Close() })
	q := store.New(pool)
	ts, authSvc := agentConnectionsTestServer(t, q)
	t.Cleanup(ts.Close)
	return &lineageEnv{pool: pool, q: q, ts: ts, authSvc: authSvc, prev: realCurrentTailHash(t, ctx, pool)}
}

// org seeds an org plus a user, and registers deletion of the org's
// audit_log/token_usage rows (neither table has an org FK to cascade).
func (e *lineageEnv) org(t *testing.T, label string) (orgID, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	orgID = seedTestOrg(t, ctx, e.pool, label)
	userID = seedTestUser(t, ctx, e.pool, orgID)
	t.Cleanup(func() {
		e.pool.Exec(context.Background(), `DELETE FROM token_usage WHERE org_id = $1`, orgID)
		e.pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1`, orgID)
	})
	return orgID, userID
}

func (e *lineageEnv) agent(t *testing.T, orgID uuid.UUID, name, risk, owner string) store.GatewayAgent {
	t.Helper()
	a, err := e.q.CreateAgent(context.Background(), store.CreateAgentParams{
		OrgID: orgID, Name: name, Model: "claude-sonnet-5", Owner: owner, Scope: "lineage test", RiskTier: risk, TokenTTLSeconds: 900,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return *a
}

func (e *lineageEnv) audit(t *testing.T, orgID, agentID uuid.UUID, policyID *uuid.UUID, tool, decision string, ts time.Time) {
	t.Helper()
	r := seedAuditRowFull(t, context.Background(), e.pool, orgID, agentID, policyID, e.prev, "lineage-agent", tool, "call", decision, ts)
	e.prev = r.hash
}

// resolution seeds the gateway's escalation-resolution row: a clone of an
// escalated call with approval_id set and the final decision
// (dispatcher.go "holdOutcome.Resolved"). Must never count as a new call.
func (e *lineageEnv) resolution(t *testing.T, orgID, agentID uuid.UUID, policyID *uuid.UUID, tool, decision string, ts time.Time) {
	t.Helper()
	id := uuid.New()
	hash := computeAuditHashTest(e.prev, id, orgID, "lineage-agent", tool, "call", decision, ts)
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO audit_log (id, org_id, agent_id, agent_name, tool_name, action, decision, policy_id, approval_id, timestamp, prev_hash, hash)
		VALUES ($1,$2,$3,'lineage-agent',$4,'call',$5,$6,$7,$8,$9,$10)`,
		id, orgID, agentID, tool, decision, policyID, uuid.New(), ts, e.prev, hash); err != nil {
		t.Fatalf("seed resolution row: %v", err)
	}
	e.prev = hash
}

// usage seeds a token_usage row; tool "" is stored as NULL, the way the
// real writer stores an unresolved tool (store/token_usage.sql.go, B-108).
func (e *lineageEnv) usage(t *testing.T, orgID, agentID uuid.UUID, tool, model string, in, out int, cost *float64, ts time.Time) {
	t.Helper()
	e.usageCache(t, orgID, agentID, tool, model, in, out, 0, 0, 0, cost, ts)
}

func (e *lineageEnv) usageCache(t *testing.T, orgID, agentID uuid.UUID, tool, model string, in, out, c5m, c1h, cread int, cost *float64, ts time.Time) {
	t.Helper()
	var toolName *string
	if tool != "" {
		toolName = &tool
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO token_usage (org_id, agent_id, agent_name, model, tool_name, tokens_in, tokens_out, cache_creation_5m_tokens, cache_creation_1h_tokens, cache_read_tokens, cost_usd, recorded_at)
		VALUES ($1,$2,'lineage-agent',$3,$4,$5,$6,$7,$8,$9,$10,$11)`, orgID, agentID, model, toolName, in, out, c5m, c1h, cread, cost, ts); err != nil {
		t.Fatalf("seed token_usage: %v", err)
	}
}

func (e *lineageEnv) run(t *testing.T, orgID, workflowID, agentID uuid.UUID, started time.Time) {
	t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO workflow_runs (id, org_id, workflow_id, agent_id, status, started_at) VALUES ($1,$2,$3,$4,'completed',$5)`,
		id, orgID, workflowID, agentID, started); err != nil {
		t.Fatalf("seed workflow_run: %v", err)
	}
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM workflow_runs WHERE id = $1`, id) })
}

func (e *lineageEnv) aiTool(t *testing.T, orgID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO gateway_tools (org_id, name, type, auth_type, provider) VALUES ($1,$2,'ai_provider','api_key','claude') RETURNING id`,
		orgID, name).Scan(&id); err != nil {
		t.Fatalf("seed ai tool: %v", err)
	}
	return id
}

func (e *lineageEnv) get(t *testing.T, userID, orgID uuid.UUID, role, agentID, query string) (int, []byte) {
	t.Helper()
	tok, _, err := e.authSvc.IssueAccessToken(userID, orgID, role+"@lineage.test", role)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/v1/gateway/agents/"+agentID+"/lineage"+query, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET lineage: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func decodeLineage(t *testing.T, b []byte) api.AgentLineageResp {
	t.Helper()
	var r api.AgentLineageResp
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("decode: %v (%s)", err, b)
	}
	return r
}

func f64(v float64) *float64 { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// seedLineageScenario builds agent A1 in org A with history spanning every
// window edge, plus noise that must NOT count: another agent in the same
// org, and rows in org B that carry A1's own agent_id.
type lineageScenario struct {
	orgA, userA, orgB, userB uuid.UUID
	a1, a2                   store.GatewayAgent
	aiToolID, restToolID     uuid.UUID
	policyID, workflowID     uuid.UUID
}

func seedLineageScenario(t *testing.T, e *lineageEnv) lineageScenario {
	t.Helper()
	ctx := context.Background()
	var s lineageScenario
	s.orgA, s.userA = e.org(t, "lineage-a")
	s.orgB, s.userB = e.org(t, "lineage-b")
	s.a1 = e.agent(t, s.orgA, "lineage-a1", "high", "payments-team")
	s.a2 = e.agent(t, s.orgA, "lineage-a2", "low", "other-team")
	s.aiToolID = e.aiTool(t, s.orgA, "lin-claude")
	e.aiTool(t, s.orgA, "lin-claude-2") // an AI connector with calls but no usage ever: cost "—"
	e.aiTool(t, s.orgB, "lin-gone")     // same name as A's deleted connector, in org B: must not resolve for A
	// A fixture model with every rate set, so the cache-tier cost terms are
	// exercised without depending on the shared, editable model_pricing rows.
	// Per-run name: a plain INSERT (no ON CONFLICT) that this run owns, so
	// cleanup can never delete a real row or another concurrent run's row.
	testModel := "lineage-test-model-" + uuid.NewString()[:8]
	if _, err := e.pool.Exec(ctx, `INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out, cost_per_1k_cache_write_5m, cost_per_1k_cache_write_1h, cost_per_1k_cache_read)
		VALUES ($1, 1, 2, 3, 4, 5)`, testModel); err != nil {
		t.Fatalf("seed model_pricing: %v", err)
	}
	t.Cleanup(func() {
		e.pool.Exec(context.Background(), `DELETE FROM model_pricing WHERE model = $1`, testModel)
	})
	s.restToolID = seedTestTool(t, ctx, e.q, s.orgA, "lin-rest")
	pol, err := e.q.CreatePolicy(ctx, store.CreatePolicyParams{OrgID: s.orgA, Name: "lin-escalate", Priority: 1, Action: "escalate", Status: "active"})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	s.policyID = pol.ID
	wf, err := e.q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: s.orgA, Name: "lin-workflow", Status: "active"})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	s.workflowID = wf.ID
	polB, err := e.q.CreatePolicy(ctx, store.CreatePolicyParams{OrgID: s.orgB, Name: "lin-foreign-policy", Priority: 1, Action: "deny", Status: "active"})
	if err != nil {
		t.Fatalf("policy B: %v", err)
	}

	now := time.Now().UTC()
	h := func(n float64) time.Time { return now.Add(-time.Duration(n * float64(time.Hour))) }
	pid := s.policyID
	// A1 audit history (oldest first so the hash chain is ordered).
	e.audit(t, s.orgA, s.a1.ID, nil, "lin-claude", "denied", h(40*24))      // outside 30d
	e.audit(t, s.orgA, s.a1.ID, nil, "lin-gone", "escalated", h(20*24))     // deleted connector, in 30d
	e.resolution(t, s.orgA, s.a1.ID, nil, "lin-gone", "denied", h(19*24))   // resolves the 20d escalation: not a denial
	e.audit(t, s.orgA, s.a1.ID, nil, "lin-rest", "allowed", h(10*24))       // in 30d only
	e.audit(t, s.orgA, s.a1.ID, &pid, "lin-rest", "denied", h(3*24))        // in 7d
	e.audit(t, s.orgA, s.a1.ID, &pid, "lin-claude", "escalated", h(2))      // in 24h
	e.resolution(t, s.orgA, s.a1.ID, &pid, "lin-claude", "allowed", h(1.5)) // resolves the 2h escalation: not a new call
	e.audit(t, s.orgA, s.a1.ID, nil, "lin-claude", "allowed", h(1))         // in 24h
	fpid := polB.ID
	e.audit(t, s.orgA, s.a1.ID, &fpid, "lin-claude-2", "allowed", h(0.5)) // carries ORG B's policy id: policy must not appear
	// Noise: another agent in org A, and org B rows carrying A1's id.
	e.audit(t, s.orgA, s.a2.ID, &pid, "lin-claude", "denied", h(1))
	e.audit(t, s.orgB, s.a1.ID, nil, "lin-claude", "escalated", h(1))
	e.audit(t, s.orgB, s.a1.ID, nil, "lin-foreign", "denied", h(1))

	// Token usage for A1: 0.50 stored (24h); the fixture model at 1000 tokens
	// in every tier = 1+2+3+4+5 = 15.00 (2d); unpriced model (3h); REST tool
	// 1.00 (1h, agent total only); 2.00 at 10d (30d only); a NULL tool_name
	// 0.25 (5h, agent total only); a future-dated 50.00 (never counted).
	e.usage(t, s.orgA, s.a1.ID, "lin-claude", "claude-sonnet-5", 10, 10, f64(0.50), h(1))
	e.usageCache(t, s.orgA, s.a1.ID, "lin-claude", testModel, 1000, 1000, 1000, 1000, 1000, nil, h(48)) // 1+2+3+4+5 = 15.00
	e.usage(t, s.orgA, s.a1.ID, "lin-claude", "claude-sonnet-5", 1, 1, f64(50), now.Add(2*time.Hour))   // future-dated: excluded
	e.usage(t, s.orgA, s.a1.ID, "lin-claude", "lineage-unpriced-model", 500, 500, nil, h(3))
	e.usage(t, s.orgA, s.a1.ID, "lin-rest", "claude-sonnet-5", 1, 1, f64(1.00), h(1))
	e.usage(t, s.orgA, s.a1.ID, "lin-claude", "claude-sonnet-5", 1, 1, f64(2.00), h(10*24))
	e.usage(t, s.orgA, s.a1.ID, "", "claude-sonnet-5", 1, 1, f64(0.25), h(5))
	// Noise.
	e.usage(t, s.orgA, s.a2.ID, "lin-claude", "claude-sonnet-5", 1, 1, f64(100), h(1))
	e.usage(t, s.orgB, s.a1.ID, "lin-claude", "claude-sonnet-5", 1, 1, f64(1000), h(1))

	// Workflow runs: A1 at 1h and 12d; A2 at 1h (noise); org B run naming A1 (noise).
	e.run(t, s.orgA, s.workflowID, s.a1.ID, h(1))
	e.run(t, s.orgA, s.workflowID, s.a1.ID, h(12*24))
	e.run(t, s.orgA, s.workflowID, s.a2.ID, h(1))
	wfB0, err := e.q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: s.orgB, Name: "lin-foreign-wf-0", Status: "active"})
	if err != nil {
		t.Fatalf("workflow B0: %v", err)
	}
	e.run(t, s.orgA, wfB0.ID, s.a1.ID, h(1)) // org A run pointing at ORG B's workflow: must not appear
	wfB, err := e.q.CreateWorkflow(ctx, store.CreateWorkflowParams{OrgID: s.orgB, Name: "lin-foreign-wf", Status: "active"})
	if err != nil {
		t.Fatalf("workflow B: %v", err)
	}
	e.run(t, s.orgB, wfB.ID, s.a1.ID, h(1))
	return s
}

// TestAgentLineage_RealDB_AggregatesMatchSeedAndDirectQueries: every number
// for every window equals both the hand-derived expectation and a direct,
// independently written SQL query.
func TestAgentLineage_RealDB_AggregatesMatchSeedAndDirectQueries(t *testing.T) {
	e := newLineageEnv(t)
	s := seedLineageScenario(t, e)
	ctx := context.Background()

	type toolWant struct {
		calls, allowed, escalated, denied, total int64
		cost                                     *float64 // nil = "—"
		unpriced                                 int64
	}
	cases := []struct {
		window                      string
		hours                       float64
		calls                       int64
		agentCost                   float64
		agentUnpriced               int64
		claude, claude2, rest, gone toolWant
		policyHits, workflowRuns    int64
	}{
		// agent cost = lin-claude usage + lin-rest 1.00 + NULL-tool 0.25; never the future-dated 50.
		{"24h", 24, 3, 0.50 + 1.00 + 0.25, 1,
			toolWant{2, 1, 1, 0, 3, f64(0.50), 1}, toolWant{1, 1, 0, 0, 1, nil, 0}, toolWant{0, 0, 0, 0, 2, nil, 0}, toolWant{0, 0, 0, 0, 1, nil, 0}, 1, 1},
		{"7d", 7 * 24, 4, 0.50 + 15.00 + 1.00 + 0.25, 1,
			toolWant{2, 1, 1, 0, 3, f64(15.50), 1}, toolWant{1, 1, 0, 0, 1, nil, 0}, toolWant{1, 0, 0, 1, 2, nil, 0}, toolWant{0, 0, 0, 0, 1, nil, 0}, 2, 1},
		{"30d", 30 * 24, 6, 0.50 + 15.00 + 1.00 + 0.25 + 2.00, 1,
			toolWant{2, 1, 1, 0, 3, f64(17.50), 1}, toolWant{1, 1, 0, 0, 1, nil, 0}, toolWant{2, 1, 0, 1, 2, nil, 0}, toolWant{1, 0, 1, 0, 1, nil, 0}, 2, 2},
	}
	for _, c := range cases {
		code, body := e.get(t, s.userA, s.orgA, "viewer", s.a1.ID.String(), "?window="+c.window)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", c.window, code, body)
		}
		r := decodeLineage(t, body)
		if r.Window != c.window || r.AgentID != s.a1.ID.String() {
			t.Fatalf("%s: window/agent = %s/%s", c.window, r.Window, r.AgentID)
		}
		sum := r.Summary
		// Hand-derived expectations.
		if sum.RiskTier != "high" || sum.Owner != "payments-team" {
			t.Errorf("%s: risk/owner = %s/%s", c.window, sum.RiskTier, sum.Owner)
		}
		if sum.ToolsEverTouched != 4 || sum.CallsInWindow != c.calls || sum.Escalations30d != 2 || sum.Denials30d != 1 {
			t.Errorf("%s: summary = tools %d calls %d esc30 %d den30 %d; want 4/%d/2/1", c.window, sum.ToolsEverTouched, sum.CallsInWindow, sum.Escalations30d, sum.Denials30d, c.calls)
		}
		if sum.CostUSDWindow == nil || !near(*sum.CostUSDWindow, c.agentCost) || sum.UnpricedCallsWindow != c.agentUnpriced {
			t.Errorf("%s: agent cost = %v unpriced %d; want %v/%d", c.window, sum.CostUSDWindow, sum.UnpricedCallsWindow, c.agentCost, c.agentUnpriced)
		}
		byName := map[string]api.AgentLineageToolResp{}
		for _, tr := range r.Tools {
			byName[tr.ToolName] = tr
		}
		if len(r.Tools) != 4 {
			t.Fatalf("%s: tools = %d (%v), want 4 (no foreign lin-foreign)", c.window, len(r.Tools), byName)
		}
		for name, w := range map[string]toolWant{"lin-claude": c.claude, "lin-claude-2": c.claude2, "lin-rest": c.rest, "lin-gone": c.gone} {
			g := byName[name]
			if g.Calls != w.calls || g.Allowed != w.allowed || g.Escalated != w.escalated || g.Denied != w.denied || g.CallsTotal != w.total || g.UnpricedCalls != w.unpriced {
				t.Errorf("%s %s: got %+v want %+v", c.window, name, g, w)
			}
			if (w.cost == nil) != (g.CostUSD == nil) || (w.cost != nil && !near(*w.cost, *g.CostUSD)) {
				t.Errorf("%s %s: cost = %v, want %v", c.window, name, g.CostUSD, w.cost)
			}
		}
		if g := byName["lin-claude"]; g.ToolID == nil || *g.ToolID != s.aiToolID.String() || g.ToolType == nil || *g.ToolType != "ai_provider" {
			t.Errorf("%s: lin-claude id/type = %v/%v", c.window, g.ToolID, g.ToolType)
		}
		if g := byName["lin-gone"]; g.ToolID != nil || g.ToolType != nil {
			t.Errorf("%s: deleted connector must have nil id/type, got %v/%v", c.window, g.ToolID, g.ToolType)
		}
		if len(r.Policies) != 1 || r.Policies[0].PolicyID != s.policyID.String() || r.Policies[0].Hits != c.policyHits {
			t.Errorf("%s: policies = %+v, want 1 with %d hits", c.window, r.Policies, c.policyHits)
		}
		if len(r.Workflows) != 1 || r.Workflows[0].WorkflowID != s.workflowID.String() || r.Workflows[0].Runs != c.workflowRuns {
			t.Errorf("%s: workflows = %+v, want 1 with %d runs", c.window, r.Workflows, c.workflowRuns)
		}

		// Independent direct queries over the same rows. "approval_id IS NULL"
		// stands in for production's isCall: they agree because the gateway
		// sets approval_id only on resolution rows (dispatcher.go, the
		// holdOutcome.Resolved clone), never on an escalated row.
		since := time.Now().UTC().Add(-time.Duration(c.hours * float64(time.Hour)))
		d30 := time.Now().UTC().Add(-30 * 24 * time.Hour)
		var dTools, dCalls, dEsc, dDen int64
		var dFirst, dLast time.Time
		if err := e.pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM (SELECT DISTINCT tool_name FROM audit_log WHERE org_id=$1 AND agent_id=$2) x),
			(SELECT count(*) FROM audit_log WHERE org_id=$1 AND agent_id=$2 AND timestamp >= $3 AND approval_id IS NULL),
			(SELECT count(*) FROM audit_log WHERE org_id=$1 AND agent_id=$2 AND decision='escalated' AND timestamp >= $4),
			(SELECT count(*) FROM audit_log WHERE org_id=$1 AND agent_id=$2 AND decision='denied' AND timestamp >= $4 AND approval_id IS NULL),
			(SELECT min(timestamp) FROM audit_log WHERE org_id=$1 AND agent_id=$2),
			(SELECT max(timestamp) FROM audit_log WHERE org_id=$1 AND agent_id=$2)`,
			s.orgA, s.a1.ID, since, d30).Scan(&dTools, &dCalls, &dEsc, &dDen, &dFirst, &dLast); err != nil {
			t.Fatal(err)
		}
		if sum.ToolsEverTouched != dTools || sum.CallsInWindow != dCalls || sum.Escalations30d != dEsc || sum.Denials30d != dDen ||
			*sum.FirstSeen != dFirst.UTC().Format(time.RFC3339) || *sum.LastSeen != dLast.UTC().Format(time.RFC3339) {
			t.Errorf("%s: summary != direct query (%d %d %d %d %s %s)", c.window, dTools, dCalls, dEsc, dDen, dFirst, dLast)
		}
		var dCost float64
		if err := e.pool.QueryRow(ctx, `SELECT COALESCE(SUM(COALESCE(tu.cost_usd, tu.tokens_in*mp.cost_per_1k_in/1000.0 + tu.tokens_out*mp.cost_per_1k_out/1000.0)
				+ tu.cache_creation_5m_tokens*COALESCE(mp.cost_per_1k_cache_write_5m,0)/1000.0 + tu.cache_creation_1h_tokens*COALESCE(mp.cost_per_1k_cache_write_1h,0)/1000.0
				+ tu.cache_read_tokens*COALESCE(mp.cost_per_1k_cache_read,0)/1000.0),0)::float8
			FROM token_usage tu LEFT JOIN model_pricing mp ON mp.model=tu.model WHERE tu.org_id=$1 AND tu.agent_id=$2 AND tu.recorded_at >= $3 AND tu.recorded_at < now()`,
			s.orgA, s.a1.ID, since).Scan(&dCost); err != nil {
			t.Fatal(err)
		}
		if !near(*sum.CostUSDWindow, dCost) {
			t.Errorf("%s: agent cost %v != direct %v", c.window, *sum.CostUSDWindow, dCost)
		}
		for name, g := range byName {
			var dc, da, de, dd int64
			if err := e.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE decision='allowed'), count(*) FILTER (WHERE decision='escalated'), count(*) FILTER (WHERE decision='denied')
				FROM audit_log WHERE org_id=$1 AND agent_id=$2 AND tool_name=$3 AND timestamp >= $4 AND approval_id IS NULL`, s.orgA, s.a1.ID, name, since).Scan(&dc, &da, &de, &dd); err != nil {
				t.Fatal(err)
			}
			if g.Calls != dc || g.Allowed != da || g.Escalated != de || g.Denied != dd {
				t.Errorf("%s %s: %d/%d/%d/%d != direct %d/%d/%d/%d", c.window, name, g.Calls, g.Allowed, g.Escalated, g.Denied, dc, da, de, dd)
			}
		}
	}

	// Window validation and default.
	if code, _ := e.get(t, s.userA, s.orgA, "admin", s.a1.ID.String(), "?window=1y"); code != http.StatusBadRequest {
		t.Errorf("window=1y: %d, want 400", code)
	}
	if code, body := e.get(t, s.userA, s.orgA, "admin", s.a1.ID.String(), ""); code != http.StatusOK || decodeLineage(t, body).Window != "7d" {
		t.Errorf("default window: %d %s", code, body)
	}
}

// TestAgentLineage_RealDB_CrossOrg: a caller in org B asking for org A's
// agent gets a 404 carrying none of A's data, even though org B holds rows
// with that same agent_id; and org B's rows never appear in A's response
// (asserted value-by-value in the aggregate test; asserted by absence here).
func TestAgentLineage_RealDB_CrossOrg(t *testing.T) {
	e := newLineageEnv(t)
	s := seedLineageScenario(t, e)
	for _, role := range []string{"admin", "operator", "viewer"} {
		code, body := e.get(t, s.userB, s.orgB, role, s.a1.ID.String(), "?window=30d")
		if code != http.StatusNotFound {
			t.Fatalf("org B %s: status %d, want 404: %s", role, code, body)
		}
		for _, leak := range []string{"lin-claude", "lin-foreign", "payments-team", "lin-escalate", "lin-workflow"} {
			if strings.Contains(string(body), leak) {
				t.Fatalf("org B %s 404 body leaks %q: %s", role, leak, body)
			}
		}
	}
	code, body := e.get(t, s.userA, s.orgA, "admin", s.a1.ID.String(), "?window=30d")
	if code != http.StatusOK {
		t.Fatalf("org A: %d", code)
	}
	for _, leak := range []string{"lin-foreign", "lin-foreign-wf"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("org A response contains org B's %q: %s", leak, body)
		}
	}
	if r := decodeLineage(t, body); !near(*r.Summary.CostUSDWindow, 18.75) { // exact: org B ($1000) or A2 ($100) usage would show
		t.Fatalf("org A 30d cost %v, want exactly 18.75 (no org B or A2 usage)", *r.Summary.CostUSDWindow)
	}
	if code, _ := e.get(t, s.userA, s.orgA, "admin", uuid.New().String(), ""); code != http.StatusNotFound {
		t.Errorf("unknown agent: %d, want 404", code)
	}
}

// TestAgentLineage_RealDB_EmptyAndRoles: an agent with no history gets
// honest nulls and empty lists (never fabricated zeros for cost or dates);
// approvers are refused (audit reads exclude them).
func TestAgentLineage_RealDB_EmptyAndRoles(t *testing.T) {
	e := newLineageEnv(t)
	orgID, userID := e.org(t, "lineage-empty")
	a := e.agent(t, orgID, "lineage-quiet", "medium", "nobody")
	code, body := e.get(t, userID, orgID, "operator", a.ID.String(), "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	r := decodeLineage(t, body)
	sum := r.Summary
	if sum.FirstSeen != nil || sum.LastSeen != nil || sum.CostUSDWindow != nil || sum.ToolsEverTouched != 0 || sum.CallsInWindow != 0 {
		t.Errorf("empty agent summary = %+v", sum)
	}
	if r.Tools == nil || r.Policies == nil || r.Workflows == nil || len(r.Tools)+len(r.Policies)+len(r.Workflows) != 0 {
		t.Errorf("empty agent lists must be [] not null: %s", body)
	}
	if sum.RiskTier != "medium" || sum.Owner != "nobody" {
		t.Errorf("risk/owner = %s/%s", sum.RiskTier, sum.Owner)
	}
	if code, _ := e.get(t, userID, orgID, "approver", a.ID.String(), ""); code != http.StatusForbidden {
		t.Errorf("approver: %d, want 403", code)
	}
}
