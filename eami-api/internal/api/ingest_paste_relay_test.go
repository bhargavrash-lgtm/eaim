// ingest_paste_relay_test.go -- eami-api/internal/api
// Integration tests for B-035: paste events relayed by eami-agent's
// native-messaging host (B0), through eami-collector's existing
// buffer-and-forward pipeline unchanged, arriving at POST /v1/ingest/batch
// shaped exactly like nativemsg.outboundReport
// ({agent_id, hostname, collected_at, paste_events}) -- no scan data at all.
//
// GetDefaultOrgID ("oldest org") means these tests cannot use a disposable
// per-test org the way paste_events_test.go's ingestion tests do -- the
// server always resolves org_id to whatever org already exists and is
// oldest in this database, which in a real dev environment is very likely
// a real, non-disposable org. Every test below therefore: (1) asks the
// server what its own default org actually is, via the exact same
// GetDefaultOrgID call IngestBatch itself uses, rather than assuming or
// seeding one; (2) uses a unique agent_id per test so cleanup can delete
// exactly (and only) what it created, via ON DELETE CASCADE from the
// endpoints row, never touching any other data in that shared org.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestIngestBatch_PasteEventRelay -v
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
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

// ─── shared test env ────────────────────────────────────────────────────────

type ingestRelayEnv struct {
	pool       *pgxpool.Pool
	queries    *store.Queries
	srv        *httptest.Server
	orgID      uuid.UUID // the server's actual GetDefaultOrgID result, asked for, not assumed
	serviceKey string
}

func newIngestRelayEnv(t *testing.T) *ingestRelayEnv {
	t.Helper()
	dsn := pasteEventsTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	t.Cleanup(pool.Close)

	q := store.New(pool)

	orgID, err := q.GetDefaultOrgID(ctx)
	if err != nil {
		t.Skipf("skipping: no default org exists yet (run reseed.sql): %v", err)
	}

	const serviceKey = "test-service-key-ingest-relay"
	cfg := &config.Config{ServiceKey: serviceKey}
	s := api.NewServer(q, nil, nil, cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &ingestRelayEnv{pool: pool, queries: q, srv: ts, orgID: orgID, serviceKey: serviceKey}
}

func (e *ingestRelayEnv) postBatch(t *testing.T, reports []map[string]interface{}) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"reports": reports})
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/ingest/batch", bytes.NewReader(body))
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

// cleanupAgent deletes exactly (and only) the endpoints row for agentID
// (and everything ON DELETE CASCADE from it -- endpoint_reports,
// paste_events) -- never touches the shared default org or any other data.
func (e *ingestRelayEnv) cleanupAgent(t *testing.T, agentID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM endpoints WHERE agent_id = $1`, agentID)
	})
}

func decodeIngestAccepted(t *testing.T, resp *http.Response) int {
	t.Helper()
	defer resp.Body.Close()
	var out struct {
		Accepted int `json:"accepted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response (status %d): %v", resp.StatusCode, err)
	}
	return out.Accepted
}

// relayItem builds one batchIngestItem whose report is shaped exactly like
// nativemsg.outboundReport -- what eami-collector actually forwards
// unchanged from a real B0 relay.
func relayItem(agentID, hostname string, pasteEvents []map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":          uuid.NewString(),
		"agent_id":    agentID,
		"hostname":    hostname,
		"received_at": time.Now().UTC().Format(time.RFC3339),
		"report": map[string]interface{}{
			"agent_id":     agentID,
			"hostname":     hostname,
			"collected_at": time.Now().UTC().Format(time.RFC3339),
			"paste_events": pasteEvents,
		},
	}
}

// ─── AC1 + AC4: relay lands in paste_events, not endpoint_reports ──────────

func TestIngestBatch_PasteEventRelay_WritesToPasteEvents_NotEndpointReports(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b-035-relay-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	occurredAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	item := relayItem(agentID, "relay-host", []map[string]interface{}{
		{"destination_domain": "claude.ai", "occurred_at": occurredAt, "content_length": 42, "content_hash": "abc123"},
	})

	resp := env.postBatch(t, []map[string]interface{}{item})
	if accepted := decodeIngestAccepted(t, resp); accepted != 1 {
		t.Fatalf("want accepted=1, got %d (status %d)", accepted, resp.StatusCode)
	}

	var pasteCount int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id
		 WHERE ep.agent_id = $1 AND pe.org_id = $2::uuid AND pe.destination_domain = 'claude.ai'`,
		agentID, env.orgID.String(),
	).Scan(&pasteCount); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	if pasteCount != 1 {
		t.Fatalf("want 1 paste_events row under the resolved default org, got %d", pasteCount)
	}

	// AC4: the interim blob path is retired -- no endpoint_reports row for
	// this relay item at all, not just "doesn't extract from it".
	var reportCount int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM endpoint_reports er JOIN endpoints ep ON ep.id = er.endpoint_id
		 WHERE ep.agent_id = $1`, agentID,
	).Scan(&reportCount); err != nil {
		t.Fatalf("query endpoint_reports: %v", err)
	}
	if reportCount != 0 {
		t.Fatalf("want 0 endpoint_reports rows for a paste-event-only relay item, got %d -- AC4 violated", reportCount)
	}
}

// ─── AC2: org resolution cannot be spoofed by the endpoint machine ─────────

func TestIngestBatch_PasteEventRelay_OrgIDCannotBeSpoofed(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b-035-spoof-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	fakeOrgID := uuid.New() // an org that does not exist at all
	occurredAt := time.Now().UTC().Format(time.RFC3339)

	// Smuggle a fake org_id into the inner report -- agentReport has no
	// OrgID field to unmarshal it into, but prove that empirically: even
	// if it somehow got through, the row must land under the real
	// resolved default org, never the smuggled value.
	item := relayItem(agentID, "spoof-host", []map[string]interface{}{
		{"destination_domain": "claude.ai", "occurred_at": occurredAt},
	})
	item["report"].(map[string]interface{})["org_id"] = fakeOrgID.String()

	resp := env.postBatch(t, []map[string]interface{}{item})
	if accepted := decodeIngestAccepted(t, resp); accepted != 1 {
		t.Fatalf("want accepted=1, got %d (status %d)", accepted, resp.StatusCode)
	}

	var gotOrgID string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT pe.org_id::text FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id
		 WHERE ep.agent_id = $1`, agentID,
	).Scan(&gotOrgID); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	if gotOrgID == fakeOrgID.String() {
		t.Fatalf("org resolution was spoofed by a client-supplied org_id -- got %s, want the server's real default org %s", gotOrgID, env.orgID)
	}
	if gotOrgID != env.orgID.String() {
		t.Fatalf("want the resolved default org %s, got %s", env.orgID, gotOrgID)
	}

	// Also confirm no org was created for the fake ID -- the smuggled
	// value had zero effect anywhere, not just on this one row.
	var fakeOrgExists bool
	if err := env.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM orgs WHERE id = $1::uuid)`, fakeOrgID.String(),
	).Scan(&fakeOrgExists); err != nil {
		t.Fatalf("query orgs: %v", err)
	}
	if fakeOrgExists {
		t.Fatalf("smuggled org_id %s unexpectedly created a real org row", fakeOrgID)
	}
}

// ─── domain allowlist still enforced server-side on the relay path ────────

func TestIngestBatch_PasteEventRelay_UnknownDomainRejected(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b-035-baddomain-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	item := relayItem(agentID, "baddomain-host", []map[string]interface{}{
		{"destination_domain": "evil-exfil.example.com", "occurred_at": time.Now().UTC().Format(time.RFC3339)},
	})

	resp := env.postBatch(t, []map[string]interface{}{item})
	if accepted := decodeIngestAccepted(t, resp); accepted != 0 {
		t.Fatalf("want accepted=0 for an unrecognized domain, got %d", accepted)
	}

	var count int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id
		 WHERE ep.agent_id = $1`, agentID,
	).Scan(&count); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("unknown-domain relay event was written despite server-side allowlist rejection: %d rows", count)
	}
}

// ─── AC5: a real full-scan report is completely unaffected ────────────────

func TestIngestBatch_NormalScanReport_StillWritesEndpointReports_Unaffected(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b-035-normalscan-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	item := map[string]interface{}{
		"id":          uuid.NewString(),
		"agent_id":    agentID,
		"hostname":    "normal-scan-host",
		"received_at": time.Now().UTC().Format(time.RFC3339),
		"report": map[string]interface{}{
			"agent_id":      agentID,
			"hostname":      "normal-scan-host",
			"collected_at":  time.Now().UTC().Format(time.RFC3339),
			"agent_version": "9.9.9-test",
			"platform":      map[string]interface{}{"os": "windows", "arch": "amd64", "os_version": "11"},
			"ai_apps":       []map[string]interface{}{{"name": "TestApp", "version": "1.0", "source": "test"}},
		},
	}

	resp := env.postBatch(t, []map[string]interface{}{item})
	if accepted := decodeIngestAccepted(t, resp); accepted != 1 {
		t.Fatalf("want accepted=1, got %d (status %d)", accepted, resp.StatusCode)
	}

	var reportCount, aiAppCount int
	var agentVersion string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM endpoint_reports er JOIN endpoints ep ON ep.id = er.endpoint_id WHERE ep.agent_id = $1`,
		agentID,
	).Scan(&reportCount); err != nil {
		t.Fatalf("query endpoint_reports: %v", err)
	}
	if reportCount != 1 {
		t.Fatalf("want 1 endpoint_reports row for a real scan report (B-035 must not affect this path), got %d", reportCount)
	}
	if err := env.pool.QueryRow(context.Background(),
		`SELECT agent_version FROM endpoints WHERE agent_id = $1`, agentID,
	).Scan(&agentVersion); err != nil {
		t.Fatalf("query endpoints: %v", err)
	}
	if agentVersion != "9.9.9-test" {
		t.Fatalf("want agent_version=9.9.9-test written normally, got %q", agentVersion)
	}
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM endpoint_ai_apps eaa JOIN endpoints ep ON ep.id = eaa.endpoint_id WHERE ep.agent_id = $1`,
		agentID,
	).Scan(&aiAppCount); err != nil {
		t.Fatalf("query endpoint_ai_apps: %v", err)
	}
	if aiAppCount != 1 {
		t.Fatalf("want 1 normalised ai_app row, got %d", aiAppCount)
	}

	var pasteCount int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id WHERE ep.agent_id = $1`,
		agentID,
	).Scan(&pasteCount); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	if pasteCount != 0 {
		t.Fatalf("a normal scan report with no paste_events must never produce a paste_events row, got %d", pasteCount)
	}
}

// ─── The crux of this design: relaying paste events must never clobber a
// real endpoint's scanner-collected metadata ─────────────────────────────

func TestIngestBatch_PasteEventRelay_DoesNotClobberScannerMetadata(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b-035-clobbercheck-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	// 1. A real full eami-agent scan reports rich metadata for this machine.
	scanItem := map[string]interface{}{
		"id":          uuid.NewString(),
		"agent_id":    agentID,
		"hostname":    "host-from-full-scan",
		"received_at": time.Now().UTC().Format(time.RFC3339),
		"report": map[string]interface{}{
			"agent_id":      agentID,
			"hostname":      "host-from-full-scan",
			"collected_at":  time.Now().UTC().Format(time.RFC3339),
			"agent_version": "1.2.3-real-scan",
			"platform":      map[string]interface{}{"os": "windows", "arch": "amd64", "os_version": "11"},
		},
	}
	if resp := env.postBatch(t, []map[string]interface{}{scanItem}); decodeIngestAccepted(t, resp) != 1 {
		t.Fatalf("seed scan report: not accepted")
	}

	// 2. A paste event is relayed for the SAME agent_id shortly after,
	// carrying only a hostname (per nativemsg.outboundReport's real
	// shape) -- and, deliberately, a different hostname, to prove it
	// isn't blindly overwritten either.
	relayEvItem := relayItem(agentID, "host-from-browser-extension", []map[string]interface{}{
		{"destination_domain": "claude.ai", "occurred_at": time.Now().UTC().Format(time.RFC3339)},
	})
	if resp := env.postBatch(t, []map[string]interface{}{relayEvItem}); decodeIngestAccepted(t, resp) != 1 {
		t.Fatalf("relay paste event: not accepted")
	}

	// 3. The endpoint's real scanner-collected metadata must survive
	// untouched -- this is the entire reason processPasteEventRelayItem
	// uses ResolvePasteSourceEndpoint instead of UpsertAgentEndpoint.
	var gotHostname, gotAgentVersion string
	var gotOSInfo []byte
	if err := env.pool.QueryRow(context.Background(),
		`SELECT hostname, agent_version, os_info FROM endpoints WHERE agent_id = $1`, agentID,
	).Scan(&gotHostname, &gotAgentVersion, &gotOSInfo); err != nil {
		t.Fatalf("query endpoints: %v", err)
	}
	if gotAgentVersion != "1.2.3-real-scan" {
		t.Fatalf("paste event relay clobbered agent_version: want %q, got %q", "1.2.3-real-scan", gotAgentVersion)
	}
	var osInfo map[string]any
	if err := json.Unmarshal(gotOSInfo, &osInfo); err != nil {
		t.Fatalf("unmarshal os_info: %v", err)
	}
	if osInfo["os"] != "windows" {
		t.Fatalf("paste event relay clobbered os_info: got %s", gotOSInfo)
	}
	if gotHostname != "host-from-full-scan" {
		t.Fatalf("paste event relay overwrote hostname set by the full scan: want %q, got %q", "host-from-full-scan", gotHostname)
	}

	// And the paste event itself still landed correctly, resolved to the
	// SAME endpoint the scan created (proving find, not re-create).
	var pasteCount int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM paste_events pe JOIN endpoints ep ON ep.id = pe.source_endpoint_id
		 WHERE ep.agent_id = $1 AND pe.destination_domain = 'claude.ai'`, agentID,
	).Scan(&pasteCount); err != nil {
		t.Fatalf("query paste_events: %v", err)
	}
	if pasteCount != 1 {
		t.Fatalf("want 1 paste_events row resolved to the scan-created endpoint, got %d", pasteCount)
	}
}

// TestIngestBatch_EmptyPasteEventsArray_StillDoesNotClobber proves the
// exact case a review pass flagged: encoding/json sets a slice field to a
// non-nil, zero-length slice when the JSON key is present as `[]`, distinct
// from nil when the key is absent entirely. processIngestItem must route
// on that presence (rep.PasteEvents != nil), not on len() > 0 -- otherwise
// an empty-but-present `"paste_events": []` would fall through to the
// normal scan-report path and clobber real metadata with this item's blank
// agent_version/os_info, exactly like TestIngestBatch_PasteEventRelay_
// DoesNotClobberScannerMetadata proves for a non-empty array.
func TestIngestBatch_EmptyPasteEventsArray_StillDoesNotClobber(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b-035-emptyarray-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	scanItem := map[string]interface{}{
		"id":          uuid.NewString(),
		"agent_id":    agentID,
		"hostname":    "host-from-full-scan",
		"received_at": time.Now().UTC().Format(time.RFC3339),
		"report": map[string]interface{}{
			"agent_id":      agentID,
			"hostname":      "host-from-full-scan",
			"collected_at":  time.Now().UTC().Format(time.RFC3339),
			"agent_version": "1.2.3-real-scan",
			"platform":      map[string]interface{}{"os": "windows", "arch": "amd64", "os_version": "11"},
		},
	}
	if resp := env.postBatch(t, []map[string]interface{}{scanItem}); decodeIngestAccepted(t, resp) != 1 {
		t.Fatalf("seed scan report: not accepted")
	}

	// A relay item whose paste_events key is present but empty (e.g. a
	// hypothetical future no-op flush) -- carries a different hostname,
	// same as the non-empty-array test, to prove it isn't overwritten.
	emptyRelayItem := relayItem(agentID, "host-from-empty-flush", []map[string]interface{}{})

	resp := env.postBatch(t, []map[string]interface{}{emptyRelayItem})
	// Zero valid events means nothing to accept -- this item is correctly
	// not counted as accepted, but critically must not have fallen through
	// to the clobbering path either.
	if accepted := decodeIngestAccepted(t, resp); accepted != 0 {
		t.Fatalf("want accepted=0 for an empty paste_events array, got %d", accepted)
	}

	var gotAgentVersion, gotHostname string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT hostname, agent_version FROM endpoints WHERE agent_id = $1`, agentID,
	).Scan(&gotHostname, &gotAgentVersion); err != nil {
		t.Fatalf("query endpoints: %v", err)
	}
	if gotAgentVersion != "1.2.3-real-scan" {
		t.Fatalf("empty paste_events array clobbered agent_version: want %q, got %q", "1.2.3-real-scan", gotAgentVersion)
	}
	if gotHostname != "host-from-full-scan" {
		t.Fatalf("empty paste_events array overwrote hostname set by the full scan: want %q, got %q", "host-from-full-scan", gotHostname)
	}
}
