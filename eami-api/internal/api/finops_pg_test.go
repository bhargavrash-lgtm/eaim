// finops_pg_test.go -- eami-api/internal/api
// Integration tests for GET /v1/finops/summary against a real Postgres,
// covering B-097: the "by team" breakdown's GROUP BY clause silently
// grouped by token_usage's own (always-empty) "team" column instead of
// the "team" SELECT alias -- Postgres's documented GROUP BY name
// resolution prefers a matching INPUT column over a same-named OUTPUT
// alias -- leaving ga.owner ungrouped and producing a real 500
// (SQLSTATE 42803) on every call, regardless of org or data. These tests
// exercise the real handler against real seeded token_usage/gateway_agents
// rows and assert on the actual returned numbers (not just "no error"),
// per this brief's own requirement that the fix be verified against real
// data, not just a passing status code.
//
// Same real-Postgres-only convention as paste_events_test.go/
// tools_action_paths_pg_test.go -- no mock/fake store layer exists for
// these queries. Skips cleanly without TEST_DATABASE_URL/POSTGRES_PASSWORD.
//
// Run against the project's docker-compose Postgres:
//   docker compose up -d postgres
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestFinOpsSummary_Real -v

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

func finOpsPgTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run finops integration tests against a real Postgres")
	}
	return fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
}

// finOpsPgTestEnv wires a real *store.Queries against a real Postgres, a
// throwaway org, and a JWT-capable httptest.Server. pool.Close is
// registered via t.Cleanup before any other t.Cleanup that touches the
// database, per this repo's mandatory real-Postgres test lifecycle rule
// (CLAUDE.md) -- never a plain `defer pool.Close()` mixed with t.Cleanup.
type finOpsPgTestEnv struct {
	pool    *pgxpool.Pool
	queries *store.Queries
	srv     *httptest.Server
	authSvc *auth.Service
	orgID   uuid.UUID
}

func newFinOpsPgTestEnv(t *testing.T) *finOpsPgTestEnv {
	t.Helper()
	dsn := finOpsPgTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id, name, slug) VALUES ($1::uuid, $2, $3)`,
		orgID.String(), "finops-test-"+orgID.String()[:8], "finops-test-"+orgID.String(),
	); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	t.Cleanup(func() {
		// Cascades to gateway_agents (org_id FK, ON DELETE CASCADE).
		// token_usage.org_id has no FK (schema.sql), so it's cleaned up
		// explicitly below by whichever test seeded it.
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1::uuid`, orgID.String())
	})

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	cfg := &config.Config{ServiceKey: "test-service-key-finops"}
	s := api.NewServer(q, authSvc, nil, cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &finOpsPgTestEnv{pool: pool, queries: q, srv: ts, authSvc: authSvc, orgID: orgID}
}

func (e *finOpsPgTestEnv) adminToken(t *testing.T) string {
	t.Helper()
	tok, _, err := e.authSvc.IssueAccessToken(uuid.New(), e.orgID, "admin@finops-test.example", "admin")
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}
	return tok
}

func (e *finOpsPgTestEnv) getSummary(t *testing.T, from, to string) (*http.Response, api.TokenSpendSummary) {
	t.Helper()
	path := fmt.Sprintf("/v1/finops/summary?from=%s&to=%s", from, to)
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.adminToken(t))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var out api.TokenSpendSummary
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp, out
}

func (e *finOpsPgTestEnv) seedAgent(t *testing.T, name, owner string) uuid.UUID {
	t.Helper()
	agentID := uuid.New()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO gateway_agents (id, org_id, name, model, owner, scope) VALUES ($1, $2, $3, 'test-model', $4, 'test')`,
		agentID, e.orgID, name, owner,
	); err != nil {
		t.Fatalf("seed gateway_agents row: %v", err)
	}
	return agentID
}

func (e *finOpsPgTestEnv) insertUsage(t *testing.T, agentID uuid.UUID, agentName, model string, tokensIn, tokensOut int32, costUSD float64, recordedAt time.Time) {
	t.Helper()
	e.insertUsageWithTool(t, agentID, agentName, model, "", tokensIn, tokensOut, costUSD, recordedAt)
}

// insertUsageWithTool is insertUsage plus a tool_name (B-108). Kept as a
// separate helper rather than adding a required param to insertUsage so
// every pre-existing call site (none of which care about tool_name) stays
// untouched.
func (e *finOpsPgTestEnv) insertUsageWithTool(t *testing.T, agentID uuid.UUID, agentName, model, toolName string, tokensIn, tokensOut int32, costUSD float64, recordedAt time.Time) {
	t.Helper()
	if err := e.queries.InsertTokenUsage(context.Background(), store.InsertTokenUsageParams{
		OrgID:      e.orgID,
		AgentID:    agentID,
		AgentName:  agentName,
		Model:      model,
		ToolName:   toolName,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostUSD:    costUSD,
		RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("insert token_usage row: %v", err)
	}
}

// ─── B-097 regression + data-correctness ───────────────────────────────────

// TestFinOpsSummary_Real_TeamBreakdown_RegressionAndDataCorrectness is the
// centerpiece test: it reproduces the exact scenario that used to 500
// (a token_usage row joined to a gateway_agents row with a real, non-null
// owner) and asserts the response is not just 200, but numerically correct
// across every breakdown (by-agent, by-team, by-model, and the grand
// totals) -- cross-checked against the real seeded rows, not just "no
// error" (AC2).
func TestFinOpsSummary_Real_TeamBreakdown_RegressionAndDataCorrectness(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	agentA := env.seedAgent(t, "agent-alpha", "team-alpha")
	// agentB deliberately has NO gateway_agents row (agent_id has no FK in
	// token_usage) -- exercises the COALESCE(ga.owner, 'unknown') fallback.
	agentB := uuid.New()

	env.insertUsage(t, agentA, "agent-alpha", "test-model-a", 1000, 200, 1.50, now.Add(-90*time.Minute))
	env.insertUsage(t, agentA, "agent-alpha", "test-model-b", 2000, 300, 2.50, now.Add(-60*time.Minute))
	env.insertUsage(t, agentB, "agent-beta", "test-model-a", 500, 100, 0.75, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)

	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 (this is the exact query that used to 500 with SQLSTATE 42803), got %d", resp.StatusCode)
	}

	// ── Grand totals ────────────────────────────────────────────────────
	const wantTotalCost = 1.50 + 2.50 + 0.75
	const wantTotalIn = 1000 + 2000 + 500
	const wantTotalOut = 200 + 300 + 100
	if summary.TotalCostUSD != wantTotalCost {
		t.Errorf("TotalCostUSD: got %v, want %v", summary.TotalCostUSD, wantTotalCost)
	}
	if summary.TotalTokensIn != wantTotalIn {
		t.Errorf("TotalTokensIn: got %d, want %d", summary.TotalTokensIn, wantTotalIn)
	}
	if summary.TotalTokensOut != wantTotalOut {
		t.Errorf("TotalTokensOut: got %d, want %d", summary.TotalTokensOut, wantTotalOut)
	}

	// ── By team: the exact breakdown that used to 500 ──────────────────
	teamCosts := map[string]float64{}
	for _, ts := range summary.ByTeam {
		teamCosts[ts.Team] = ts.CostUSD
	}
	if got := teamCosts["team-alpha"]; got != 4.00 {
		t.Errorf(`ByTeam["team-alpha"].CostUSD: got %v, want 4.00`, got)
	}
	if got := teamCosts["unknown"]; got != 0.75 {
		t.Errorf(`ByTeam["unknown"].CostUSD: got %v, want 0.75 (COALESCE fallback for the agent with no gateway_agents row)`, got)
	}

	// ── By model ─────────────────────────────────────────────────────────
	modelCosts := map[string]float64{}
	modelTokensIn := map[string]int64{}
	for _, ms := range summary.ByModel {
		modelCosts[ms.Model] = ms.CostUSD
		modelTokensIn[ms.Model] = ms.TokensIn
	}
	if got := modelCosts["test-model-a"]; got != 2.25 {
		t.Errorf(`ByModel["test-model-a"].CostUSD: got %v, want 2.25`, got)
	}
	if got := modelTokensIn["test-model-a"]; got != 1500 {
		t.Errorf(`ByModel["test-model-a"].TokensIn: got %d, want 1500`, got)
	}
	if got := modelCosts["test-model-b"]; got != 2.50 {
		t.Errorf(`ByModel["test-model-b"].CostUSD: got %v, want 2.50`, got)
	}

	// ── By agent ─────────────────────────────────────────────────────────
	agentCosts := map[string]float64{}
	agentRequests := map[string]int64{}
	for _, as := range summary.ByAgent {
		agentCosts[as.AgentName] = as.CostUSD
		agentRequests[as.AgentName] = as.RequestCount
	}
	if got := agentCosts["agent-alpha"]; got != 4.00 {
		t.Errorf(`ByAgent["agent-alpha"].CostUSD: got %v, want 4.00`, got)
	}
	if got := agentRequests["agent-alpha"]; got != 2 {
		t.Errorf(`ByAgent["agent-alpha"].RequestCount: got %d, want 2`, got)
	}
	if got := agentCosts["agent-beta"]; got != 0.75 {
		t.Errorf(`ByAgent["agent-beta"].CostUSD: got %v, want 0.75`, got)
	}
}

// TestFinOpsSummary_Real_OrgIsolation proves a second org's token_usage
// rows never leak into this org's summary -- the WHERE tu.org_id = $1
// scoping isn't touched by this brief's fix, but B-097's regression proves
// the query still compiles/executes correctly, not that org scoping still
// holds; this asserts that explicitly.
func TestFinOpsSummary_Real_OrgIsolation(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	other := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	agentA := env.seedAgent(t, "own-agent", "own-team")
	agentOther := other.seedAgent(t, "other-agent", "other-team")

	env.insertUsage(t, agentA, "own-agent", "test-model", 100, 10, 1.00, now.Add(-30*time.Minute))
	other.insertUsage(t, agentOther, "other-agent", "test-model", 999, 999, 999.00, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)

	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if summary.TotalCostUSD != 1.00 {
		t.Errorf("cross-org leak: TotalCostUSD got %v, want 1.00 (other org's 999.00 row must not be included)", summary.TotalCostUSD)
	}
	if len(summary.ByAgent) != 1 || summary.ByAgent[0].AgentName != "own-agent" {
		t.Errorf("cross-org leak in ByAgent: got %+v", summary.ByAgent)
	}
}

// TestFinOpsSummary_Real_NoDataInRange proves an empty result set (a real,
// valid date range with zero matching rows) still returns a clean 200 with
// zeroed totals and empty breakdown slices, not an error -- the COALESCE(...,
// 0) guards and make([]T, 0) initializers in finops.go depend on this.
func TestFinOpsSummary_Real_NoDataInRange(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	agentA := env.seedAgent(t, "future-agent", "future-team")
	env.insertUsage(t, agentA, "future-agent", "test-model", 100, 10, 1.00, now.Add(-30*time.Minute))

	// Query a range that excludes the seeded row entirely.
	from := now.Add(24 * time.Hour).Format(time.RFC3339)
	to := now.Add(48 * time.Hour).Format(time.RFC3339)

	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 for an empty result set, got %d", resp.StatusCode)
	}
	if summary.TotalCostUSD != 0 || summary.TotalTokensIn != 0 || summary.TotalTokensOut != 0 {
		t.Errorf("want zeroed totals for an empty range, got %+v", summary)
	}
	if len(summary.ByAgent) != 0 || len(summary.ByTeam) != 0 || len(summary.ByModel) != 0 || len(summary.ByTool) != 0 {
		t.Errorf("want empty breakdown slices for an empty range, got by_agent=%d by_team=%d by_model=%d by_tool=%d",
			len(summary.ByAgent), len(summary.ByTeam), len(summary.ByModel), len(summary.ByTool))
	}
	if summary.AvgCostPerOutcome != 0 {
		t.Errorf("want AvgCostPerOutcome=0 for an empty range (no divide-by-zero), got %v", summary.AvgCostPerOutcome)
	}
}

// ─── B-108: by_tool breakdown + avg_cost_per_outcome ───────────────────────

// TestFinOpsSummary_Real_ToolBreakdown_RecordsAndAggregatesByConnector
// proves AC2: a real by_tool breakdown is queryable and numerically correct,
// mirroring by_model's existing test shape exactly. Also proves AC1's
// companion contract at the store level: a token_usage row inserted with a
// real tool_name round-trips through GET /v1/finops/summary correctly.
func TestFinOpsSummary_Real_ToolBreakdown_RecordsAndAggregatesByConnector(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	agentA := env.seedAgent(t, "tool-test-agent-a", "team-a")

	env.insertUsageWithTool(t, agentA, "tool-test-agent-a", "test-model", "claude-connector", 1000, 200, 1.50, now.Add(-90*time.Minute))
	env.insertUsageWithTool(t, agentA, "tool-test-agent-a", "test-model", "claude-connector", 500, 100, 0.50, now.Add(-60*time.Minute))
	env.insertUsageWithTool(t, agentA, "tool-test-agent-a", "test-model", "rest-connector", 300, 50, 0.25, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)

	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	toolCosts := map[string]float64{}
	toolTokensIn := map[string]int64{}
	for _, ts := range summary.ByTool {
		toolCosts[ts.Tool] = ts.CostUSD
		toolTokensIn[ts.Tool] = ts.TokensIn
	}
	if got := toolCosts["claude-connector"]; got != 2.00 {
		t.Errorf(`ByTool["claude-connector"].CostUSD: got %v, want 2.00`, got)
	}
	if got := toolTokensIn["claude-connector"]; got != 1500 {
		t.Errorf(`ByTool["claude-connector"].TokensIn: got %d, want 1500`, got)
	}
	if got := toolCosts["rest-connector"]; got != 0.25 {
		t.Errorf(`ByTool["rest-connector"].CostUSD: got %v, want 0.25`, got)
	}
}

// TestFinOpsSummary_Real_UnresolvedToolMapsToUnknown proves the
// COALESCE(tu.tool_name, 'unknown') fallback: a row with no tool_name (an
// unresolved-tool dispatch -- resolveDynamicTool/resolveAIProviderTool both
// return nil for a name that doesn't resolve) is a real, expected case
// grouped under "unknown", not dropped or errored on, mirroring by_team's
// identical COALESCE(ga.owner, 'unknown') precedent.
func TestFinOpsSummary_Real_UnresolvedToolMapsToUnknown(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	agentA := env.seedAgent(t, "unresolved-tool-agent", "team-a")
	// toolName = "" -- stored as NULL, exactly what an unresolved ac.Tool produces.
	env.insertUsageWithTool(t, agentA, "unresolved-tool-agent", "test-model", "", 400, 80, 0.60, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)

	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if len(summary.ByTool) != 1 {
		t.Fatalf("want exactly 1 by_tool entry, got %d: %+v", len(summary.ByTool), summary.ByTool)
	}
	if summary.ByTool[0].Tool != "unknown" {
		t.Errorf("Tool = %q, want %q", summary.ByTool[0].Tool, "unknown")
	}
	if summary.ByTool[0].CostUSD != 0.60 {
		t.Errorf("CostUSD = %v, want 0.60", summary.ByTool[0].CostUSD)
	}
}

// TestFinOpsSummary_Real_AvgCostPerOutcome proves AC4: avg_cost_per_outcome
// is a real, correctly computed value (total_cost_usd / number of recorded
// token_usage rows in the period), not silently blank/omitted.
func TestFinOpsSummary_Real_AvgCostPerOutcome(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	agentA := env.seedAgent(t, "avg-cost-agent", "team-a")

	// 3 rows, total cost 3.00 -> avg 1.00 exactly.
	env.insertUsage(t, agentA, "avg-cost-agent", "test-model", 100, 10, 1.00, now.Add(-90*time.Minute))
	env.insertUsage(t, agentA, "avg-cost-agent", "test-model", 100, 10, 1.00, now.Add(-60*time.Minute))
	env.insertUsage(t, agentA, "avg-cost-agent", "test-model", 100, 10, 1.00, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)

	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if summary.AvgCostPerOutcome != 1.00 {
		t.Errorf("AvgCostPerOutcome = %v, want 1.00 (3.00 total / 3 rows)", summary.AvgCostPerOutcome)
	}
}

// ─── B-111: caching cost-accounting ─────────────────────────────────────────

// setModelPricing upserts a model_pricing row with all 5 tiers' rates and
// registers cleanup -- model_pricing is a shared, non-org-scoped table, so
// every test that touches it must remove exactly the row it added (never a
// broader DELETE that could affect another concurrently-running test or a
// real seeded model).
func (e *finOpsPgTestEnv) setModelPricing(t *testing.T, model string, costIn, costOut, cacheWrite5m, cacheWrite1h, cacheRead float64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
INSERT INTO model_pricing (model, cost_per_1k_in, cost_per_1k_out, cost_per_1k_cache_write_5m, cost_per_1k_cache_write_1h, cost_per_1k_cache_read)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (model) DO UPDATE SET
  cost_per_1k_in = EXCLUDED.cost_per_1k_in,
  cost_per_1k_out = EXCLUDED.cost_per_1k_out,
  cost_per_1k_cache_write_5m = EXCLUDED.cost_per_1k_cache_write_5m,
  cost_per_1k_cache_write_1h = EXCLUDED.cost_per_1k_cache_write_1h,
  cost_per_1k_cache_read = EXCLUDED.cost_per_1k_cache_read`,
		model, costIn, costOut, cacheWrite5m, cacheWrite1h, cacheRead,
	); err != nil {
		t.Fatalf("upsert model_pricing row for %q: %v", model, err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM model_pricing WHERE model = $1`, model)
	})
}

// updateCacheRates changes only the two cache-write rates on an
// already-seeded model_pricing row -- used by the AC4 rate-change-then-
// requery test, deliberately not going through setModelPricing (which would
// re-register a duplicate, harmless-but-redundant cleanup).
func (e *finOpsPgTestEnv) updateCacheRates(t *testing.T, model string, cacheWrite5m, cacheWrite1h float64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE model_pricing SET cost_per_1k_cache_write_5m = $2, cost_per_1k_cache_write_1h = $3 WHERE model = $1`,
		model, cacheWrite5m, cacheWrite1h,
	); err != nil {
		t.Fatalf("update cache rates for %q: %v", model, err)
	}
}

// insertUsageWithCache is insertUsage plus the 3 raw cache-token counts
// (B-111). costUSD here represents ONLY the base in/out cost, matching what
// the real write-time IngestTokenUsage actually stores (it never computes
// cache cost) -- cache cost is added independently, at query time, by
// finops.go's own SQL, which is exactly the mechanism these tests verify.
func (e *finOpsPgTestEnv) insertUsageWithCache(t *testing.T, agentID uuid.UUID, agentName, model string, tokensIn, tokensOut, cache5m, cache1h, cacheRead int32, costUSD float64, recordedAt time.Time) {
	t.Helper()
	if err := e.queries.InsertTokenUsage(context.Background(), store.InsertTokenUsageParams{
		OrgID:                 e.orgID,
		AgentID:               agentID,
		AgentName:             agentName,
		Model:                 model,
		TokensIn:              tokensIn,
		TokensOut:             tokensOut,
		CostUSD:               costUSD,
		CacheCreation5mTokens: cache5m,
		CacheCreation1hTokens: cache1h,
		CacheReadTokens:       cacheRead,
		RecordedAt:            recordedAt,
	}); err != nil {
		t.Fatalf("insert token_usage row with cache tokens: %v", err)
	}
}

// TestFinOpsSummary_Real_CacheTokens_RecordedRawAndQueriedAtAllFiveTiers is
// the AC1+AC2 centerpiece: a row with non-zero counts across all 5 tiers
// (base in, base out, 5m cache write, 1h cache write, cache read) is
// recorded raw (AC1 -- verified via a direct pool query, standing in for
// the psql check the live-verification step also performs against a real
// dispatch) and priced with each tier's own distinct rate, hand-computed
// (AC2), not the base input rate applied uniformly.
func TestFinOpsSummary_Real_CacheTokens_RecordedRawAndQueriedAtAllFiveTiers(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	model := "b111-tier-test-" + env.orgID.String()[:8]
	// cost_per_1k_in=0.01, out=0.02, cache_write_5m=0.0125 (1.25x in),
	// cache_write_1h=0.02 (2x in), cache_read=0.001 (0.1x in) -- the real
	// Anthropic multipliers, applied to made-up-but-round base rates so
	// the hand-computed expected total is easy to verify independently.
	env.setModelPricing(t, model, 0.01, 0.02, 0.0125, 0.02, 0.001)

	agentA := env.seedAgent(t, "cache-tier-agent", "team-cache")

	// Base cost (as real write-time IngestTokenUsage would compute and
	// store it): 1000/1000*0.01 + 500/1000*0.02 = 0.01 + 0.01 = 0.02.
	const baseCost = 0.02
	env.insertUsageWithCache(t, agentA, "cache-tier-agent", model,
		1000, 500, // tokens_in, tokens_out
		2000, 1000, 3000, // cache_creation_5m, cache_creation_1h, cache_read
		baseCost, now.Add(-30*time.Minute))

	// AC1: raw counts land exactly as inserted, independent of any pricing.
	var gotIn, gotOut, got5m, got1h, gotRead int32
	err := env.pool.QueryRow(context.Background(),
		`SELECT tokens_in, tokens_out, cache_creation_5m_tokens, cache_creation_1h_tokens, cache_read_tokens
		 FROM token_usage WHERE org_id = $1 AND agent_name = 'cache-tier-agent'`,
		env.orgID,
	).Scan(&gotIn, &gotOut, &got5m, &got1h, &gotRead)
	if err != nil {
		t.Fatalf("query raw token_usage row: %v", err)
	}
	if gotIn != 1000 || gotOut != 500 || got5m != 2000 || got1h != 1000 || gotRead != 3000 {
		t.Fatalf("raw counts = in:%d out:%d 5m:%d 1h:%d read:%d, want 1000/500/2000/1000/3000",
			gotIn, gotOut, got5m, got1h, gotRead)
	}

	// AC2: query-time cost = frozen base (0.02) + live cache terms:
	//   5m:   2000/1000 * 0.0125 = 0.025
	//   1h:   1000/1000 * 0.02   = 0.02
	//   read: 3000/1000 * 0.001  = 0.003
	// total cache = 0.048; total row cost = 0.02 + 0.048 = 0.068.
	const wantCost = 0.068

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)
	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	if diff := summary.TotalCostUSD - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("TotalCostUSD = %v, want %v", summary.TotalCostUSD, wantCost)
	}
	// AC5: the same corrected total shows up consistently across every
	// breakdown, not just the grand total.
	if len(summary.ByAgent) != 1 || withinEpsilon(summary.ByAgent[0].CostUSD, wantCost) == false {
		t.Errorf("ByAgent[0].CostUSD = %+v, want %v", summary.ByAgent, wantCost)
	}
	if len(summary.ByModel) != 1 || withinEpsilon(summary.ByModel[0].CostUSD, wantCost) == false {
		t.Errorf("ByModel[0].CostUSD = %+v, want %v", summary.ByModel, wantCost)
	}
	// total_tokens_in/out semantics are unchanged by B-111 -- they still
	// reflect only tokens_in/tokens_out, not cache token counts.
	if summary.TotalTokensIn != 1000 || summary.TotalTokensOut != 500 {
		t.Errorf("TotalTokensIn/Out = %d/%d, want 1000/500 (cache tokens excluded, unchanged semantics)",
			summary.TotalTokensIn, summary.TotalTokensOut)
	}
}

func withinEpsilon(got, want float64) bool {
	diff := got - want
	return diff < 1e-9 && diff > -1e-9
}

// TestFinOpsSummary_Real_NoCaching_PricesExactlyAsBefore is the AC3
// regression guard: a row with zero cache tokens, against a model that DOES
// have cache rates configured, prices identically to pre-B-111 behavior --
// the cache terms contribute exactly $0 when the counts are 0, regardless
// of what the configured rates are.
func TestFinOpsSummary_Real_NoCaching_PricesExactlyAsBefore(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	model := "b111-no-cache-test-" + env.orgID.String()[:8]
	env.setModelPricing(t, model, 0.01, 0.02, 0.0125, 0.02, 0.001)

	agentA := env.seedAgent(t, "no-cache-agent", "team-a")

	const baseCost = 0.02 // 1000/1000*0.01 + 500/1000*0.02
	env.insertUsageWithCache(t, agentA, "no-cache-agent", model,
		1000, 500, 0, 0, 0, // no cache activity at all
		baseCost, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)
	resp, summary := env.getSummary(t, from, to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if summary.TotalCostUSD != baseCost {
		t.Errorf("TotalCostUSD = %v, want %v (cache columns at 0 must not change pre-B-111 pricing)", summary.TotalCostUSD, baseCost)
	}
}

// TestFinOpsSummary_Real_RateChangeThenRequery_ReflectsNewRate is the AC4
// centerpiece: changing model_pricing's cache rate and re-querying the SAME
// already-recorded historical row shows the NEW rate, proving cost is
// genuinely computed at query time from CURRENT rates -- not frozen at
// write time the way base/output cost_usd is.
func TestFinOpsSummary_Real_RateChangeThenRequery_ReflectsNewRate(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	now := time.Now().UTC()

	model := "b111-rate-change-test-" + env.orgID.String()[:8]
	env.setModelPricing(t, model, 0.01, 0.02, 0.0125, 0.02, 0.001)

	agentA := env.seedAgent(t, "rate-change-agent", "team-a")

	const baseCost = 0.0 // isolate the cache term: no in/out tokens at all
	env.insertUsageWithCache(t, agentA, "rate-change-agent", model,
		0, 0, 2000, 0, 0, // 2000 tokens on the 5m cache-write tier only
		baseCost, now.Add(-30*time.Minute))

	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)

	// First query, at the original rate: 2000/1000 * 0.0125 = 0.025.
	resp1, summary1 := env.getSummary(t, from, to)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first query: want 200, got %d", resp1.StatusCode)
	}
	if summary1.TotalCostUSD != 0.025 {
		t.Fatalf("first query TotalCostUSD = %v, want 0.025 (original rate)", summary1.TotalCostUSD)
	}

	// Change ONLY the rate in model_pricing -- the historical token_usage
	// row itself is never touched, per this brief's own "no backfill"
	// contract (mirrors B-078's audit_log snapshot-immutability principle,
	// applied here to raw counts rather than a written cost).
	env.updateCacheRates(t, model, 0.05, 0.02) // 5m rate: 0.0125 -> 0.05

	// Re-query the exact same row: 2000/1000 * 0.05 = 0.10, not 0.025.
	resp2, summary2 := env.getSummary(t, from, to)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second query: want 200, got %d", resp2.StatusCode)
	}
	if summary2.TotalCostUSD != 0.10 {
		t.Errorf("second query TotalCostUSD = %v, want 0.10 (new rate applied to the SAME historical row)", summary2.TotalCostUSD)
	}
	if summary2.TotalCostUSD == summary1.TotalCostUSD {
		t.Error("cost did not change after the rate update -- query-time computation is not actually working")
	}
}
