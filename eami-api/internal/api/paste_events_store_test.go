// paste_events_store_test.go -- eami-api/internal/api
// Store/query-layer tests for paste_events that are shared across BOTH
// ingestion paths (the real one, ingest.go's processPasteEventRelayItem,
// and the historical POST /v1/reports/paste-events removed 2026-09-19 --
// see BACKLOG.md's B-19x closing-IngestPasteEvents entry). These tests
// exercise store.Queries directly, never through IngestPasteEvents' own
// HTTP handler, so none of them depended on that endpoint and none needed
// to change when it was removed -- moved out of the old
// paste_events_test.go (deleted whole) into this dedicated file rather
// than deleted along with it.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/eami/api/internal/store"
)

func pasteEventsTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run paste_events store-layer tests against a real Postgres")
	}
	return fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
}

// newPasteEventsStorePool connects a real *pgxpool.Pool + store.Queries
// and seeds a throwaway org, with no HTTP server/router involved -- these
// tests only ever call store.Queries methods directly.
func newPasteEventsStorePool(t *testing.T) (*pgxpool.Pool, *store.Queries, uuid.UUID) {
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
		orgID.String(), "paste-events-store-test-"+orgID.String()[:8], "paste-events-store-test-"+orgID.String(),
	); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1::uuid`, orgID.String())
	})

	return pool, q, orgID
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

// TestResolvePasteSourceEndpoint_DoesNotClobberScannerMetadata proves the
// reason ResolvePasteSourceEndpoint exists instead of reusing
// UpsertAgentEndpoint: seed an endpoint the way the full eami-agent scan
// pipeline does (ingest.go's processIngestItem -> UpsertAgentEndpoint,
// with real agent_version/os_info), then resolve the same (org_id,
// agent_id) the way a paste event does (only ever a hostname). The
// scanner's richer metadata must survive untouched -- if this test fails,
// paste events are silently degrading data the full scanner already
// collected for this machine. Used by the real ingestion path
// (processPasteEventRelayItem), not tied to any HTTP endpoint.
func TestResolvePasteSourceEndpoint_DoesNotClobberScannerMetadata(t *testing.T) {
	pool, q, orgID := newPasteEventsStorePool(t)
	agentID := "agent-clobber-check-" + uuid.NewString()[:8]

	const wantAgentVersion = "1.2.3-real-scan"
	wantOSInfo := []byte(`{"os":"windows","arch":"amd64","os_version":"11"}`)

	scannerEndpointID, err := q.UpsertAgentEndpoint(context.Background(), store.UpsertAgentEndpointParams{
		OrgID:        orgID,
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
	pasteEndpointID, err := q.ResolvePasteSourceEndpoint(context.Background(), store.ResolvePasteSourceEndpointParams{
		OrgID: orgID, AgentID: agentID, Hostname: "host-from-browser-extension",
	})
	if err != nil {
		t.Fatalf("ResolvePasteSourceEndpoint: %v", err)
	}
	if pasteEndpointID != scannerEndpointID {
		t.Fatalf("ResolvePasteSourceEndpoint resolved a different endpoint row (%s) than the scanner created (%s) -- find-or-create by (org_id, agent_id) is broken", pasteEndpointID, scannerEndpointID)
	}

	var gotHostname, gotAgentVersion string
	var gotOSInfo []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT hostname, agent_version, os_info FROM endpoints WHERE id = $1::uuid`,
		scannerEndpointID.String(),
	).Scan(&gotHostname, &gotAgentVersion, &gotOSInfo); err != nil {
		t.Fatalf("query endpoints: %v", err)
	}

	if gotAgentVersion != wantAgentVersion {
		t.Fatalf("paste event clobbered agent_version: want %q, got %q", wantAgentVersion, gotAgentVersion)
	}
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

// TestPasteEvents_BatchInsert_IsOneRoundTrip proves the "single efficient
// batch write, not N round trips" requirement quantitatively: attaches a
// pgx.QueryTracer and asserts BatchInsertPasteEvents issues exactly one
// query to Postgres regardless of whether the batch has 1 event or 50.
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

// TestPasteEvents_ReportingQuery_UsesIndex_AtRealisticVolume seeds its own
// throwaway database (mirroring bootstrap_test.go's newThrowawayDB/
// applyMigrations pattern), seeding paste_events directly via SQL --
// never through any HTTP endpoint -- then runs the representative
// CIO-dashboard query ("paste events by domain, last 30 days, for org X")
// through a real EXPLAIN (ANALYZE, BUFFERS) and asserts the plan uses
// idx_paste_events_org_domain, not a sequential scan. Skipped under
// -short, since seeding 200k rows takes real time.
func TestPasteEvents_ReportingQuery_UsesIndex_AtRealisticVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping seeded performance test in -short mode")
	}
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
			orgID.String(), "paste-events-perf-"+label, "paste-events-perf-"+label+"-"+orgID.String()); err != nil {
			t.Fatalf("seed org %s: %v", label, err)
		}
		// No per-org cleanup needed here -- newThrowawayDB's own t.Cleanup
		// drops the entire throwaway database, taking every row seeded
		// below with it.

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
