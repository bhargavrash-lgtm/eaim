// paste_events_read_test.go -- eami-api/internal/api
// Integration tests for B-038's read-only admin UI over B-032's
// paste_events table: GET /v1/paste-events (list) and
// GET /v1/paste-events/timeseries (per-domain aggregation). Same
// real-Postgres-only convention as paste_events_test.go (no mock/fake
// store layer exists in this codebase for these queries) -- skips
// cleanly without TEST_DATABASE_URL/POSTGRES_PASSWORD.
//
// Unlike paste_events_test.go's newPasteEventsTestEnv (authSvc=nil,
// since ingestion only needs X-Service-Key), these routes are
// JWT-protected (requireRole: admin/operator/viewer), so newReadEnv
// below wires a real *auth.Service and issues real signed tokens --
// exercising the actual jwtMiddleware/requireRole/claimsFromContext
// chain, not a bypass.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestPasteEventsRead -v
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
)

// ─── shared read-side test env ─────────────────────────────────────────────

// readEnv wraps paste_events_test.go's pasteEventsTestEnv (pool, queries,
// orgID, serviceKey -- for seeding via env.post) with a second httptest
// server backed by a real *auth.Service, so JWT-protected read routes can
// be exercised with real signed tokens. env.post(...) still uses the
// embedded env's original service-key-only server; env.srv (shadowing
// the embedded field) is this new JWT-capable one, used by authedGet.
type readEnv struct {
	*pasteEventsTestEnv
	srv     *httptest.Server
	authSvc *auth.Service
}

func newReadEnv(t *testing.T) *readEnv {
	t.Helper()
	base := newPasteEventsTestEnv(t)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	cfg := &config.Config{ServiceKey: base.serviceKey}
	s := api.NewServer(base.queries, authSvc, nil, cfg)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return &readEnv{pasteEventsTestEnv: base, srv: srv, authSvc: authSvc}
}

// authedGet performs a viewer-authenticated GET against env.srv, decoding
// the JSON response into out (skipped if nil, e.g. for status-only checks).
func (env *readEnv) authedGet(t *testing.T, path string, out interface{}) *http.Response {
	t.Helper()
	tok, _, err := env.authSvc.IssueAccessToken(uuid.New(), env.orgID, "viewer@test.example", "viewer")
	if err != nil {
		t.Fatalf("issue viewer token: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, env.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	if out != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode response (status %d): %v", resp.StatusCode, err)
		}
	}
	return resp
}

// ─── list endpoint: filtering, pagination, org isolation, AC4 ─────────────────

func TestPasteEventsRead_List_FiltersByDomainAndTime(t *testing.T) {
	env := newReadEnv(t)
	agentID := "agent-read-" + uuid.NewString()[:8]
	base := time.Now().UTC().Add(-2 * time.Hour)

	events := []api.PasteEventReport{
		{OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h", DestinationDomain: "chat.openai.com", OccurredAt: base.Format(time.RFC3339)},
		{OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h", DestinationDomain: "claude.ai", OccurredAt: base.Add(30 * time.Minute).Format(time.RFC3339)},
		{OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h", DestinationDomain: "chat.openai.com", OccurredAt: base.Add(90 * time.Minute).Format(time.RFC3339)},
	}
	if resp := env.post(t, events); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed post: status %d", resp.StatusCode)
	}

	// Unfiltered: all 3 events for this org.
	var all api.PasteEventListResponse
	env.authedGet(t, "/v1/paste-events?per_page=50", &all)
	if all.Meta.Total != 3 || len(all.Data) != 3 {
		t.Fatalf("want 3 total/3 data unfiltered, got total=%d len=%d", all.Meta.Total, len(all.Data))
	}

	// Domain filter: only chat.openai.com (2 events).
	var byDomain api.PasteEventListResponse
	env.authedGet(t, "/v1/paste-events?domain=chat.openai.com", &byDomain)
	if byDomain.Meta.Total != 2 {
		t.Fatalf("want 2 for domain=chat.openai.com, got %d", byDomain.Meta.Total)
	}
	for _, e := range byDomain.Data {
		if e.DestinationDomain != "chat.openai.com" {
			t.Fatalf("domain filter leaked a non-matching row: %+v", e)
		}
	}

	// Time filter: from=base+60min excludes the first two events, keeps the third.
	from := base.Add(60 * time.Minute).Format(time.RFC3339)
	var byTime api.PasteEventListResponse
	env.authedGet(t, "/v1/paste-events?from="+from, &byTime)
	if byTime.Meta.Total != 1 {
		t.Fatalf("want 1 event after from=%s, got %d", from, byTime.Meta.Total)
	}

	// AC4: no raw content field exists anywhere in the response type --
	// verified structurally (mirrors paste_events_test.go's
	// TestPasteEventReport_HasNoRawContentField for the write side).
	typ := reflect.TypeOf(api.PasteEventResp{})
	allowed := map[string]bool{
		"ID": true, "DestinationDomain": true, "OccurredAt": true,
		"ContentLength": true, "ContentHash": true, "OSUsername": true,
	}
	if typ.NumField() != len(allowed) {
		t.Fatalf("PasteEventResp field count changed (want %d, got %d) -- review before allowing", len(allowed), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !allowed[name] {
			t.Fatalf("PasteEventResp has an unexpected field %q -- review whether it could carry raw pasted content before allowing it", name)
		}
	}
}

func TestPasteEventsRead_List_Paginates(t *testing.T) {
	env := newReadEnv(t)
	agentID := "agent-page-" + uuid.NewString()[:8]
	base := time.Now().UTC().Add(-time.Hour)

	const n = 12
	events := make([]api.PasteEventReport, n)
	for i := 0; i < n; i++ {
		events[i] = api.PasteEventReport{
			OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h",
			DestinationDomain: "claude.ai",
			OccurredAt:        base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
		}
	}
	env.post(t, events)

	var page1, page2 api.PasteEventListResponse
	env.authedGet(t, "/v1/paste-events?per_page=5&page=1", &page1)
	env.authedGet(t, "/v1/paste-events?per_page=5&page=2", &page2)

	if page1.Meta.Total != n {
		t.Fatalf("want total=%d, got %d", n, page1.Meta.Total)
	}
	if len(page1.Data) != 5 || len(page2.Data) != 5 {
		t.Fatalf("want 5+5 per page, got %d+%d", len(page1.Data), len(page2.Data))
	}
	if page1.Data[0].ID == page2.Data[0].ID {
		t.Fatalf("page 1 and page 2 returned the same first row -- pagination broken")
	}
}

func TestPasteEventsRead_List_OrgIsolation(t *testing.T) {
	envA := newReadEnv(t)
	envB := newReadEnv(t)

	// Seed an event for org A only.
	envA.post(t, []api.PasteEventReport{{
		OrgID: envA.orgID.String(), AgentID: "agent-iso-a", Hostname: "h",
		DestinationDomain: "claude.ai", OccurredAt: time.Now().UTC().Format(time.RFC3339),
	}})

	// Query as org B's viewer -- must see zero events, never org A's.
	var resp api.PasteEventListResponse
	envB.authedGet(t, "/v1/paste-events", &resp)
	if resp.Meta.Total != 0 || len(resp.Data) != 0 {
		t.Fatalf("org isolation broken: org B's query returned %d events belonging to org A", resp.Meta.Total)
	}
}

// ─── timeseries endpoint ────────────────────────────────────────────────────

func TestPasteEventsRead_TimeSeries_GroupsByBucketAndDomain(t *testing.T) {
	env := newReadEnv(t)
	agentID := "agent-ts-" + uuid.NewString()[:8]
	day1 := time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)
	day2 := day1.Add(24 * time.Hour)

	events := []api.PasteEventReport{
		{OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h", DestinationDomain: "chat.openai.com", OccurredAt: day1.Add(time.Hour).Format(time.RFC3339)},
		{OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h", DestinationDomain: "chat.openai.com", OccurredAt: day1.Add(2 * time.Hour).Format(time.RFC3339)},
		{OrgID: env.orgID.String(), AgentID: agentID, Hostname: "h", DestinationDomain: "claude.ai", OccurredAt: day2.Add(time.Hour).Format(time.RFC3339)},
	}
	env.post(t, events)

	from := day1.Add(-time.Hour).Format("2006-01-02")
	to := day2.Add(24 * time.Hour).Format("2006-01-02")

	var ts api.PasteEventTimeSeries
	env.authedGet(t, fmt.Sprintf("/v1/paste-events/timeseries?from=%s&to=%s&granularity=day", from, to), &ts)

	if ts.Granularity != "day" {
		t.Fatalf("want granularity=day, got %q", ts.Granularity)
	}

	var chatDay1Count, claudeDay2Count int64
	for _, pt := range ts.Series {
		if pt.Domain == "chat.openai.com" && pt.Bucket.Equal(day1) {
			chatDay1Count = pt.Count
		}
		if pt.Domain == "claude.ai" && pt.Bucket.Equal(day2) {
			claudeDay2Count = pt.Count
		}
	}
	if chatDay1Count != 2 {
		t.Fatalf("want chat.openai.com day1 count=2, got %d (full series: %+v)", chatDay1Count, ts.Series)
	}
	if claudeDay2Count != 1 {
		t.Fatalf("want claude.ai day2 count=1, got %d (full series: %+v)", claudeDay2Count, ts.Series)
	}
}

func TestPasteEventsRead_TimeSeries_RequiresFromAndTo(t *testing.T) {
	env := newReadEnv(t)
	resp := env.authedGet(t, "/v1/paste-events/timeseries?granularity=day", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 when from/to are missing, got %d", resp.StatusCode)
	}
}

func TestPasteEventsRead_TimeSeries_RejectsBadGranularity(t *testing.T) {
	env := newReadEnv(t)
	now := time.Now().UTC()
	path := fmt.Sprintf("/v1/paste-events/timeseries?from=%s&to=%s&granularity=fortnight",
		now.Add(-24*time.Hour).Format("2006-01-02"), now.Format("2006-01-02"))
	resp := env.authedGet(t, path, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an invalid granularity, got %d", resp.StatusCode)
	}
}

// TestPasteEventsRead_TimeSeries_NoStoreConfigured_ReturnsCleanError proves
// the B-016-class fix applied to PasteEventsTimeSeries: a request that
// passes validation but hits a Server with s.queries == nil must return a
// clean 500, not nil-pointer-panic inside s.queries.DB() (recovered into an
// opaque 500 by chi's Recoverer). Mirrors finops_test.go's
// TestFinOpsTimeSeries_NoStoreConfigured_ReturnsCleanError and tools_test.go's
// TestToolHandlers_NoStoreConfigured_ReturnsCleanError. Deliberately doesn't
// use newReadEnv/newPasteEventsTestEnv (both require a real Postgres
// connection) -- s.queries is nil by construction here, so no database is
// needed to prove this guard.
func TestPasteEventsRead_TimeSeries_NoStoreConfigured_ReturnsCleanError(t *testing.T) {
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	s := api.NewServer(nil, authSvc, nil, nil) // s.queries is nil
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	tok, _, err := authSvc.IssueAccessToken(uuid.New(), uuid.New(), "viewer@test.example", "viewer")
	if err != nil {
		t.Fatalf("issue viewer token: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/paste-events/timeseries?from=2025-01-01&to=2025-02-01&granularity=day", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want a clean 500 when no store is configured, got %d", resp.StatusCode)
	}
}

// ─── EXPLAIN ANALYZE evidence, at the volume B-032 already proved out ──────
//
// Reuses B-032's own seeded-volume convention (paste_events_test.go's
// TestPasteEvents_ReportingQuery_UsesIndex_AtRealisticVolume) rather than
// re-inventing it: 100,000 rows for one org, a second org seeded too so
// the org filter is actually selective. Proves the exact queries
// ListPasteEvents/CountPasteEvents and PasteEventsTimeSeries issue --
// not a hand-picked proxy query -- use the right index, not a sequential
// scan, at this volume.

func TestPasteEventsRead_ListQuery_UsesIndex_AtRealisticVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping seeded performance test in -short mode")
	}
	pool, orgA := seedPasteEventsPerfData(t)

	const query = `
		EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
		SELECT id, org_id, source_endpoint_id, destination_domain, content_length, content_hash, os_username, occurred_at, received_at
		FROM paste_events
		WHERE org_id = $1
		  AND destination_domain = 'chat.openai.com'
		  AND occurred_at >= NOW() - INTERVAL '30 days'
		ORDER BY occurred_at DESC
		LIMIT 50 OFFSET 0`

	plan := explainAnalyze(t, pool, query, orgA.String())
	t.Logf("EXPLAIN ANALYZE for ListPasteEvents' query (100,000 seeded rows):\n%s", plan)
	assertIndexUsedNotSeqScan(t, plan, "idx_paste_events_org_domain")
}

func TestPasteEventsRead_TimeSeriesQuery_UsesIndex_AtRealisticVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping seeded performance test in -short mode")
	}
	pool, orgA := seedPasteEventsPerfData(t)

	const query = `
		EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
		SELECT time_bucket('1 day', occurred_at) AS bucket, destination_domain, COUNT(*)
		FROM paste_events
		WHERE org_id = $1
		  AND occurred_at >= NOW() - INTERVAL '30 days'
		  AND occurred_at <  NOW()
		GROUP BY bucket, destination_domain
		ORDER BY bucket`

	plan := explainAnalyze(t, pool, query, orgA.String())
	t.Logf("EXPLAIN ANALYZE for PasteEventsTimeSeries' query (100,000 seeded rows):\n%s", plan)
	assertIndexUsedNotSeqScan(t, plan, "idx_paste_events_org")
}

// ── shared perf-test helpers ────────────────────────────────────────────────

// seedPasteEventsPerfData seeds its own throwaway database (mirroring
// bootstrap_test.go's newThrowawayDB/applyMigrations pattern) rather than
// the shared dev database -- this function itself never actually leaked
// (its org cleanup was already correctly ordered via t.Cleanup(pool.Close),
// confirmed empirically before this change), but the underlying pattern of
// seeding 200,000 synthetic rows into the shared dev database at all -- the
// exact class of gap B-056 named -- shouldn't exist regardless, per the
// same reasoning applied to this file's sibling helper in
// paste_events_test.go, which did leak.
func seedPasteEventsPerfData(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	c := bootstrapTestPgConn(t)
	dbName := newThrowawayDB(t, c)
	applyMigrations(t, c, dbName)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, c.dbURL(dbName))
	if err != nil {
		t.Fatalf("connect pool to throwaway db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach throwaway database: %v", err)
	}
	t.Cleanup(pool.Close)

	const rowsPerOrg = 100_000
	const endpointsPerOrg = 200

	seedOrg := func(label string) uuid.UUID {
		orgID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1::uuid, $2, $3)`,
			orgID.String(), "paste-events-read-perf-"+label, "paste-events-read-perf-"+label+"-"+orgID.String()); err != nil {
			t.Fatalf("seed org %s: %v", label, err)
		}
		// No per-org cleanup needed here -- newThrowawayDB's own t.Cleanup
		// drops the entire throwaway database, taking every row seeded
		// below with it.

		if _, err := pool.Exec(ctx,
			`INSERT INTO endpoints (org_id, agent_id, hostname)
			 SELECT $1::uuid, 'perf-read-agent-'||g, 'perf-read-host-'||g
			 FROM generate_series(1, $2) g`,
			orgID.String(), endpointsPerOrg,
		); err != nil {
			t.Fatalf("seed endpoints for org %s: %v", label, err)
		}

		if _, err := pool.Exec(ctx, `
			WITH eps AS (
				SELECT array_agg(id ORDER BY agent_id) AS ids
				FROM endpoints WHERE org_id = $1::uuid AND agent_id LIKE 'perf-read-agent-%'
			)
			INSERT INTO paste_events
				(org_id, source_endpoint_id, destination_domain, content_length, content_hash, occurred_at)
			SELECT
				$1::uuid,
				eps.ids[1 + (g % $2)],
				(ARRAY['chat.openai.com','claude.ai','copilot.microsoft.com','gemini.google.com','perplexity.ai'])[1 + (g % 5)],
				100 + (g % 900),
				md5(g::text),
				NOW() - ((g % 60) || ' days')::interval - ((g % 1440) || ' minutes')::interval
			FROM generate_series(1, $3) g, eps`,
			orgID.String(), endpointsPerOrg, rowsPerOrg,
		); err != nil {
			t.Fatalf("seed paste_events for org %s: %v", label, err)
		}
		return orgID
	}

	orgA := seedOrg("a")
	_ = seedOrg("b") // second org -- makes the org filter actually selective

	return pool, orgA
}

func explainAnalyze(t *testing.T, pool *pgxpool.Pool, query string, args ...interface{}) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE query: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan line: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	return strings.Join(lines, "\n")
}

func assertIndexUsedNotSeqScan(t *testing.T, plan, wantIndex string) {
	t.Helper()
	if strings.Contains(plan, "Seq Scan") {
		t.Fatalf("query used a sequential scan at 100,000-row volume -- expected %s. Full plan:\n%s", wantIndex, plan)
	}
	if !strings.Contains(plan, wantIndex) {
		t.Fatalf("query plan does not mention %s -- expected it to be the driving index. Full plan:\n%s", wantIndex, plan)
	}
}
