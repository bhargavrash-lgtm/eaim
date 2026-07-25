// paste_events_test.go -- eami-api/internal/api
// Integration tests for POST /v1/reports/paste-events (paste-detection
// ingestion groundwork). These exercise a real Postgres+TimescaleDB
// instance -- there is no mock/fake layer for *store.Queries in this
// codebase (reports.go's IngestReports has the same property and, as of
// this brief, still has zero test coverage for that reason).
//
// Requires TEST_DATABASE_URL, or POSTGRES_PASSWORD combined with the
// project's docker-compose default layout (eami_app/eami/localhost:5432).
// Skips cleanly if neither is set -- same "not executed without infra"
// convention already used elsewhere in this codebase (see BUILT.md's
// Node/npm-dependent frontend checks).
//
// Run against the project's docker-compose Postgres:
//   docker compose up -d postgres
//   psql <dsn> -f schema/migrations/008_paste_events.sql   (existing DBs only -- fresh DBs get it from schema.sql)
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestPasteEvents -v

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// ─── shared test infra ────────────────────────────────────────────────────────

func pasteEventsTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run paste_events integration tests against a real Postgres")
	}
	return fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
}

// pasteEventsTestEnv wires a real *store.Queries against a real Postgres,
// a throwaway org for isolation, and an httptest.Server exposing the full
// router (so /v1/reports and /v1/reports/paste-events both work, letting
// AC6 prove the two paths coexist without interference). Everything the
// test creates is cleaned up via t.Cleanup.
type pasteEventsTestEnv struct {
	pool       *pgxpool.Pool
	queries    *store.Queries
	srv        *httptest.Server
	orgID      uuid.UUID
	serviceKey string
}

func newPasteEventsTestEnv(t *testing.T) *pasteEventsTestEnv {
	t.Helper()
	dsn := pasteEventsTestDSN(t)
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
		orgID.String(), "paste-events-test-"+orgID.String()[:8], "paste-events-test-"+orgID.String(),
	); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	t.Cleanup(func() {
		// Cascades to endpoints (org_id FK) and paste_events (both org_id
		// and source_endpoint_id FKs are ON DELETE CASCADE).
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1::uuid`, orgID.String())
	})

	const serviceKey = "test-service-key-paste-events"
	cfg := &config.Config{ServiceKey: serviceKey}
	s := api.NewServer(q, nil, nil, cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &pasteEventsTestEnv{pool: pool, queries: q, srv: ts, orgID: orgID, serviceKey: serviceKey}
}

// postRaw posts an arbitrary JSON body (used to test that unrecognised
// extra fields, e.g. a smuggled "content" key, are silently dropped rather
// than stored).
func (e *pasteEventsTestEnv) postRaw(t *testing.T, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/reports/paste-events", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-Key", e.serviceKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func (e *pasteEventsTestEnv) post(t *testing.T, events []api.PasteEventReport) *http.Response {
	t.Helper()
	b, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	return e.postRaw(t, b)
}

func decodeAccepted(t *testing.T, resp *http.Response) (accepted, rejected int) {
	t.Helper()
	defer resp.Body.Close()
	var out struct {
		Accepted int `json:"accepted"`
		Rejected int `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out.Accepted, out.Rejected
}

func ptrInt32(v int32) *int32    { return &v }
func ptrString(v string) *string { return &v }

// ─── AC2: structural no-raw-content guarantee ─────────────────────────────────
// Mirrors B-022's approach: verified via the request type itself, not
// convention. If someone later adds a field capable of carrying pasted
// text, this test fails immediately and names the field.

func TestPasteEventReport_HasNoRawContentField(t *testing.T) {
	typ := reflect.TypeOf(api.PasteEventReport{})
	allowed := map[string]bool{
		"OrgID": true, "AgentID": true, "Hostname": true, "DestinationDomain": true,
		"OccurredAt": true, "ContentLength": true, "ContentHash": true, "OSUsername": true,
	}
	if typ.NumField() != len(allowed) {
		t.Fatalf("PasteEventReport field count changed (want %d, got %d) -- update this test's allowlist deliberately if the new field cannot carry pasted content", len(allowed), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !allowed[name] {
			t.Fatalf("PasteEventReport has an unexpected field %q -- review whether it could carry raw pasted content before allowing it", name)
		}
	}
}

// TestPasteEvents_SmuggledContentFieldIsDropped proves the guarantee holds
// even against a crafted wire payload, not just the typed struct: extra
// JSON keys (e.g. a "content"/"raw_text" field a rogue or buggy client
// might send) are silently ignored by the decoder and never reach any
// column in the row that gets written.
func TestPasteEvents_SmuggledContentFieldIsDropped(t *testing.T) {
	env := newPasteEventsTestEnv(t)
	const sentinel = "THIS-IS-THE-ACTUAL-PASTED-SECRET-TEXT-0xDEADBEEF"

	payload := fmt.Sprintf(`[{
		"org_id": %q,
		"agent_id": "agent-smuggle-test",
		"hostname": "host-smuggle-test",
		"destination_domain": "chat.openai.com",
		"occurred_at": %q,
		"content": %q,
		"raw_text": %q,
		"content_hash": "abc123"
	}]`, env.orgID.String(), time.Now().UTC().Format(time.RFC3339), sentinel, sentinel)

	resp := env.postRaw(t, []byte(payload))
	accepted, rejected := decodeAccepted(t, resp)
	if accepted != 1 || rejected != 0 {
		t.Fatalf("want accepted=1 rejected=0, got accepted=%d rejected=%d", accepted, rejected)
	}

	rows, err := env.pool.Query(context.Background(),
		`SELECT destination_domain, content_hash, os_username, content_length
		 FROM paste_events WHERE org_id = $1::uuid`, env.orgID.String())
	if err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	defer rows.Close()

	found := 0
	for rows.Next() {
		found++
		var domain, hash string
		var osUsername *string
		var length *int32
		if err := rows.Scan(&domain, &hash, &osUsername, &length); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		if strings.Contains(domain, sentinel) || strings.Contains(hash, sentinel) ||
			(osUsername != nil && strings.Contains(*osUsername, sentinel)) {
			t.Fatalf("sentinel pasted-text value leaked into a stored column -- row: domain=%q hash=%q os_username=%v", domain, hash, osUsername)
		}
	}
	if found != 1 {
		t.Fatalf("want exactly 1 row, found %d", found)
	}
}

// ─── AC1 + AC3: batch write correctness, no dedup/collapsing ──────────────────

func TestPasteEvents_BatchPosted_WritesIndividualRows_NotCollapsed(t *testing.T) {
	env := newPasteEventsTestEnv(t)
	agentID := "agent-batch-" + uuid.NewString()[:8]

	const n = 25
	events := make([]api.PasteEventReport, n)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < n; i++ {
		events[i] = api.PasteEventReport{
			OrgID:             env.orgID.String(),
			AgentID:           agentID,
			Hostname:          "host-batch-test",
			DestinationDomain: "chat.openai.com", // same domain, same source, every time
			OccurredAt:        base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			ContentLength:     ptrInt32(int32(50 + i)),
		}
	}

	resp := env.post(t, events)
	accepted, rejected := decodeAccepted(t, resp)
	if accepted != n || rejected != 0 {
		t.Fatalf("want accepted=%d rejected=0, got accepted=%d rejected=%d", n, accepted, rejected)
	}

	var rowCount int
	var distinctTimestamps int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*), COUNT(DISTINCT occurred_at)
		 FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id
		 WHERE pe.org_id = $1::uuid AND ep.agent_id = $2`,
		env.orgID.String(), agentID,
	).Scan(&rowCount, &distinctTimestamps); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}

	// AC1: correct, individual rows -- not collapsed to one row like
	// discovered_endpoints' ON CONFLICT upsert would.
	if rowCount != n {
		t.Fatalf("want %d distinct rows (append-only, no dedup), got %d", n, rowCount)
	}
	// AC3: repeated events to the same domain from the same source produce
	// multiple distinct rows with distinct timestamps.
	if distinctTimestamps != n {
		t.Fatalf("want %d distinct occurred_at values, got %d -- events appear to have collapsed", n, distinctTimestamps)
	}
}

// TestPasteEvents_BatchInsert_IsOneRoundTrip proves the "single efficient
// batch write, not N round trips" requirement quantitatively: attaches a
// pgx.QueryTracer and asserts BatchInsertPasteEvents issues exactly one
// query to Postgres regardless of whether the batch has 1 event or 50.
// This is the gap explicitly flagged against IngestReports' current
// one-Exec-per-event loop -- this test would fail if paste_events regressed
// to that same pattern.
func TestPasteEvents_BatchInsert_IsOneRoundTrip(t *testing.T) {
	dsn := pasteEventsTestDSN(t)
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	tracer := &countingTracer{}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	orgID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1::uuid, $2, $3)`,
		orgID.String(), "paste-events-roundtrip-test", "paste-events-roundtrip-"+orgID.String()); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1::uuid`, orgID.String())

	endpointID, err := q.ResolvePasteSourceEndpoint(ctx, store.ResolvePasteSourceEndpointParams{
		OrgID: orgID, AgentID: "agent-roundtrip", Hostname: "host-roundtrip",
	})
	if err != nil {
		t.Fatalf("resolve endpoint: %v", err)
	}

	for _, batchSize := range []int{1, 50} {
		events := make([]store.PasteEventInput, batchSize)
		for i := range events {
			events[i] = store.PasteEventInput{
				OrgID: orgID, SourceEndpointID: endpointID,
				DestinationDomain: "chat.openai.com",
				OccurredAt:        time.Now().UTC().Add(time.Duration(i) * time.Second),
			}
		}
		before := atomic.LoadInt32(&tracer.n)
		if _, err := q.BatchInsertPasteEvents(ctx, events); err != nil {
			t.Fatalf("BatchInsertPasteEvents(%d events): %v", batchSize, err)
		}
		queriesIssued := atomic.LoadInt32(&tracer.n) - before
		if queriesIssued != 1 {
			t.Fatalf("batch of %d events issued %d queries, want exactly 1 (single multi-row INSERT via unnest)", batchSize, queriesIssued)
		}
	}
}

type countingTracer struct{ n int32 }

func (c *countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	atomic.AddInt32(&c.n, 1)
	return ctx
}
func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestResolvePasteSourceEndpoint_DoesNotClobberScannerMetadata proves the
// reason ResolvePasteSourceEndpoint exists instead of reusing
// UpsertAgentEndpoint: seed an endpoint the way the full eami-agent scan
// pipeline does (ingest.go's processIngestItem -> UpsertAgentEndpoint,
// with real agent_version/os_info), then resolve the same (org_id,
// agent_id) the way a paste event does (only ever a hostname). The
// scanner's richer metadata must survive untouched -- if this test fails,
// paste events are silently degrading data the full scanner already
// collected for this machine.
func TestResolvePasteSourceEndpoint_DoesNotClobberScannerMetadata(t *testing.T) {
	env := newPasteEventsTestEnv(t)
	agentID := "agent-clobber-check-" + uuid.NewString()[:8]

	const wantAgentVersion = "1.2.3-real-scan"
	wantOSInfo := []byte(`{"os":"windows","arch":"amd64","os_version":"11"}`)

	scannerEndpointID, err := env.queries.UpsertAgentEndpoint(context.Background(), store.UpsertAgentEndpointParams{
		OrgID:        env.orgID,
		AgentID:      agentID,
		Hostname:     "host-from-full-scan",
		AgentVersion: wantAgentVersion,
		OSInfo:       wantOSInfo,
	})
	if err != nil {
		t.Fatalf("seed endpoint via UpsertAgentEndpoint (simulating the full scan pipeline): %v", err)
	}

	// A paste event arrives for the same machine, carrying only a hostname
	// -- and, deliberately, a *different* hostname than the scanner
	// reported, to prove hostname isn't blindly overwritten either.
	pasteEndpointID, err := env.queries.ResolvePasteSourceEndpoint(context.Background(), store.ResolvePasteSourceEndpointParams{
		OrgID: env.orgID, AgentID: agentID, Hostname: "host-from-browser-extension",
	})
	if err != nil {
		t.Fatalf("ResolvePasteSourceEndpoint: %v", err)
	}
	if pasteEndpointID != scannerEndpointID {
		t.Fatalf("ResolvePasteSourceEndpoint resolved a different endpoint row (%s) than the scanner created (%s) -- find-or-create by (org_id, agent_id) is broken", pasteEndpointID, scannerEndpointID)
	}

	var gotHostname, gotAgentVersion string
	var gotOSInfo []byte
	if err := env.pool.QueryRow(context.Background(),
		`SELECT hostname, agent_version, os_info FROM endpoints WHERE id = $1::uuid`,
		scannerEndpointID.String(),
	).Scan(&gotHostname, &gotAgentVersion, &gotOSInfo); err != nil {
		t.Fatalf("query endpoints: %v", err)
	}

	if gotAgentVersion != wantAgentVersion {
		t.Fatalf("paste event clobbered agent_version: want %q, got %q", wantAgentVersion, gotAgentVersion)
	}
	// Compare semantically, not byte-for-byte: Postgres's jsonb column
	// re-serializes on read (e.g. adds a space after ":"/","), so an exact
	// string comparison would fail even for identical JSON.
	var wantParsed, gotParsed map[string]any
	if err := json.Unmarshal(wantOSInfo, &wantParsed); err != nil {
		t.Fatalf("unmarshal wantOSInfo: %v", err)
	}
	if err := json.Unmarshal(gotOSInfo, &gotParsed); err != nil {
		t.Fatalf("unmarshal gotOSInfo: %v", err)
	}
	if !reflect.DeepEqual(wantParsed, gotParsed) {
		t.Fatalf("paste event clobbered os_info: want %s, got %s", wantOSInfo, gotOSInfo)
	}
	if gotHostname != "host-from-full-scan" {
		t.Fatalf("paste event overwrote hostname set by the full scan: want %q, got %q", "host-from-full-scan", gotHostname)
	}
}

// ─── AC4: domain allowlist enforced server-side ────────────────────────────────

func TestPasteEvents_UnknownDomain_RejectedServerSide(t *testing.T) {
	env := newPasteEventsTestEnv(t)
	agentID := "agent-allowlist-" + uuid.NewString()[:8]
	now := time.Now().UTC()

	events := []api.PasteEventReport{
		{
			OrgID: env.orgID.String(), AgentID: agentID, Hostname: "host-allowlist-test",
			DestinationDomain: "chat.openai.com", // known
			OccurredAt:        now.Format(time.RFC3339),
		},
		{
			OrgID: env.orgID.String(), AgentID: agentID, Hostname: "host-allowlist-test",
			DestinationDomain: "evil-exfil.example.com", // NOT in KnownPasteDestinations
			OccurredAt:        now.Add(time.Minute).Format(time.RFC3339),
		},
	}

	resp := env.post(t, events)
	accepted, rejected := decodeAccepted(t, resp)
	if accepted != 1 || rejected != 1 {
		t.Fatalf("want accepted=1 rejected=1, got accepted=%d rejected=%d", accepted, rejected)
	}

	var count int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id
		 WHERE pe.org_id = $1::uuid AND ep.agent_id = $2 AND pe.destination_domain = 'evil-exfil.example.com'`,
		env.orgID.String(), agentID,
	).Scan(&count); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("unknown-domain event was written despite server-side allowlist rejection: %d rows", count)
	}
}

func TestMatchesKnownPasteDestination(t *testing.T) {
	cases := []struct {
		domain string
		want   bool
	}{
		{"chat.openai.com", true},
		{"CHAT.OPENAI.COM", true},
		{"my.chat.openai.com", true}, // subdomain
		{"claude.ai", true},
		{"notchat.openai.com.evil.com", false},
		{"evil-exfil.example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := api.MatchesKnownPasteDestination(c.domain); got != c.want {
			t.Errorf("MatchesKnownPasteDestination(%q) = %v, want %v", c.domain, got, c.want)
		}
	}
}

// ─── AC6: discovered_endpoints / reports.go unaffected ─────────────────────────

func TestPasteEvents_DoesNotAffectDiscoveredEndpoints(t *testing.T) {
	env := newPasteEventsTestEnv(t)
	sourceHost := "host-regression-" + uuid.NewString()[:8]

	postReports := func() *http.Response {
		body, _ := json.Marshal([]api.ReportEvent{{
			OrgID:      env.orgID.String(),
			SourceHost: sourceHost,
			Method:     "POST",
			Host:       "api.openai.com",
			Path:       "/v1/chat/completions",
			TLS:        true,
		}})
		req, err := http.NewRequest(http.MethodPost, env.srv.URL+"/v1/reports", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Service-Key", env.serviceKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		return resp
	}

	// Also post a paste event in between, on the same server/DB, to prove
	// the two ingestion paths coexist without interfering with each other.
	postReports().Body.Close()
	env.post(t, []api.PasteEventReport{{
		OrgID: env.orgID.String(), AgentID: "agent-regression", Hostname: "host-regression",
		DestinationDomain: "chat.openai.com", OccurredAt: time.Now().UTC().Format(time.RFC3339),
	}}).Body.Close()
	postReports().Body.Close() // second hit to the same discovered_endpoints tuple

	var hitCount int32
	if err := env.pool.QueryRow(context.Background(),
		`SELECT hit_count FROM discovered_endpoints
		 WHERE org_id = $1::uuid AND source_host = $2 AND method = 'POST' AND host = 'api.openai.com' AND path = '/v1/chat/completions'`,
		env.orgID.String(), sourceHost,
	).Scan(&hitCount); err != nil {
		t.Fatalf("query discovered_endpoints: %v", err)
	}
	// Unchanged upsert-by-tuple behaviour: 2 posts to the identical tuple
	// collapse into 1 row with hit_count=2, exactly as before this brief.
	if hitCount != 2 {
		t.Fatalf("discovered_endpoints upsert behaviour changed: want hit_count=2, got %d", hitCount)
	}
}

// ─── AC5: reporting query performance at a realistic seeded volume ────────────
//
// Seeds two orgs x 200 synthetic endpoints x ~100,000 paste_events each
// (200,000 rows total) spanning a 60-day window across 5 rotating domains,
// then runs the representative CIO-dashboard query -- "paste events by
// domain, last 30 days, for org X" -- through a real EXPLAIN (ANALYZE,
// BUFFERS) and asserts the plan actually uses idx_paste_events_org_domain
// (not a sequential scan). Skipped under -short, since seeding 200k rows
// takes real time.

func TestPasteEvents_ReportingQuery_UsesIndex_AtRealisticVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping seeded performance test in -short mode")
	}
	dsn := pasteEventsTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	defer pool.Close()

	const rowsPerOrg = 100_000
	const endpointsPerOrg = 200

	seedOrg := func(label string) uuid.UUID {
		orgID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO orgs (id, name, slug) VALUES ($1::uuid, $2, $3)`,
			orgID.String(), "paste-events-perf-"+label, "paste-events-perf-"+label+"-"+orgID.String()); err != nil {
			t.Fatalf("seed org %s: %v", label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1::uuid`, orgID.String())
		})

		if _, err := pool.Exec(ctx,
			`INSERT INTO endpoints (org_id, agent_id, hostname)
			 SELECT $1::uuid, 'perf-seed-agent-'||g, 'perf-seed-host-'||g
			 FROM generate_series(1, $2) g`,
			orgID.String(), endpointsPerOrg,
		); err != nil {
			t.Fatalf("seed endpoints for org %s: %v", label, err)
		}

		if _, err := pool.Exec(ctx, `
			WITH eps AS (
				SELECT array_agg(id ORDER BY agent_id) AS ids
				FROM endpoints WHERE org_id = $1::uuid AND agent_id LIKE 'perf-seed-agent-%'
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
	_ = seedOrg("b") // second org -- makes the org filter actually selective, not the whole table

	const reportingQuery = `
		EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
		SELECT destination_domain, date_trunc('day', occurred_at) AS day, COUNT(*)
		FROM paste_events
		WHERE org_id = $1::uuid
		  AND destination_domain = 'chat.openai.com'
		  AND occurred_at >= NOW() - INTERVAL '30 days'
		GROUP BY destination_domain, day
		ORDER BY day`

	rows, err := pool.Query(ctx, reportingQuery, orgA.String())
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE query: %v", err)
	}
	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatalf("scan plan line: %v", err)
		}
		planLines = append(planLines, line)
	}
	rows.Close()
	plan := strings.Join(planLines, "\n")
	t.Logf("EXPLAIN ANALYZE output for org+domain+30day reporting query (200,000 seeded rows):\n%s", plan)

	if strings.Contains(plan, "Seq Scan") {
		t.Fatalf("reporting query used a sequential scan at 200,000-row volume -- expected idx_paste_events_org_domain to be used. Full plan:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_paste_events_org_domain") {
		t.Fatalf("reporting query plan does not mention idx_paste_events_org_domain -- expected it to be the driving index. Full plan:\n%s", plan)
	}
}
