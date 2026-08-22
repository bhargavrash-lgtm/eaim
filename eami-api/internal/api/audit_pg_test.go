// audit_pg_test.go -- eami-api/internal/api
// Real-Postgres integration tests for B-094 (Audit entry detail view):
// (1) data_handling_designation (B-078 column) is now actually selected and
//     returned by GET /v1/audit -- previously written by eami-gateway's
//     writer but never read back anywhere (found by the B-084 investigation).
// (2) GET /v1/audit/verify, called the way the new detail panel calls it
//     (bounded by ?to=<one entry's timestamp>, no ?from), correctly reports
//     both an intact and a tampered real chain. VerifyAuditChain's own
//     verification logic is untouched by this brief -- these tests exercise
//     it, they don't change it.
//
// Rows are inserted directly via SQL (bypassing eami-gateway's audit writer
// entirely, which lives in a different Go module) with hand-computed
// hash-chain values following the exact same formula as
// eami-gateway/internal/audit/writer.go and eami-api/internal/store/verify.go:
//
//	SHA-256(prevHash || id || orgID || agentName || toolName || action || decision || timestamp.UTC().RFC3339)
//
// seeded with genesisHash = SHA-256("eami-genesis-2026") for each test org's
// first row. Because VerifyAuditChain (called with no ?from) always seeds
// the walk at genesisHash regardless of what any other org's rows in the
// shared table look like, and its SELECT is org_id-scoped, a fresh test org
// with its own from-genesis chain verifies correctly in isolation --
// confirmed directly, not assumed, before relying on it here.
//
// Follows workflows_test.go's t.Cleanup-only pool-lifecycle convention
// (CLAUDE.md's mandatory pattern).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestAudit_RealDB -v
package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func genesisHashTest() string {
	h := sha256.Sum256([]byte("eami-genesis-2026"))
	return hex.EncodeToString(h[:])
}

// computeAuditHashTest mirrors eami-gateway/internal/audit/writer.go's and
// eami-api/internal/store/verify.go's formula exactly.
func computeAuditHashTest(prevHash string, id, orgID uuid.UUID, agentName, toolName, action, decision string, ts time.Time) string {
	content := prevHash + id.String() + orgID.String() + agentName + toolName + action + decision + ts.UTC().Format(time.RFC3339)
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

type seededAuditRow struct {
	id        uuid.UUID
	timestamp time.Time
	hash      string
}

// seedAuditRow inserts one real, correctly hash-chained audit_log row.
// dataHandling may be "" to leave the column NULL.
func seedAuditRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, prevHash, agentName, toolName, action, decision, dataHandling string, ts time.Time) seededAuditRow {
	t.Helper()
	id := uuid.New()
	hash := computeAuditHashTest(prevHash, id, orgID, agentName, toolName, action, decision, ts)
	var dh *string
	if dataHandling != "" {
		dh = &dataHandling
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO audit_log (id, org_id, agent_name, tool_name, action, decision, timestamp, prev_hash, hash, data_handling_designation)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, orgID, agentName, toolName, action, decision, ts, prevHash, hash, dh,
	)
	if err != nil {
		t.Fatalf("seed audit_log row: %v", err)
	}
	return seededAuditRow{id: id, timestamp: ts, hash: hash}
}

func auditTestServer(t *testing.T, q *store.Queries) (*httptest.Server, *auth.Service) {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	return httptest.NewServer(srv.Handler()), authSvc
}

// TestAudit_RealDB_DataHandlingDesignation_RoundTrips proves AC5: a
// data_handling_designation written directly to audit_log (as
// eami-gateway's writer would, per B-078) is now actually returned by
// GET /v1/audit, and a row with none set omits the field rather than
// fabricating a value.
func TestAudit_RealDB_DataHandlingDesignation_RoundTrips(t *testing.T) {
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
	orgID := seedTestOrg(t, ctx, pool, "audit-dh")
	userID := seedTestUser(t, ctx, pool, orgID)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1`, orgID) })

	ts, authSvc := auditTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@audit-dh.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	now := time.Now().UTC()
	withDH := seedAuditRow(t, ctx, pool, orgID, genesisHashTest(), "claude-1", "openai-connector", "chat.completions", "allowed", "zero_retention", now)
	withoutDH := seedAuditRow(t, ctx, pool, orgID, withDH.hash, "claude-1", "internal-tool", "query", "allowed", "", now.Add(time.Second))

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/audit", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/audit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Data []api.AuditEntryResp `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("got %d entries, want 2", len(body.Data))
	}

	var gotWith, gotWithout *api.AuditEntryResp
	for i := range body.Data {
		switch body.Data[i].ID {
		case withDH.id.String():
			gotWith = &body.Data[i]
		case withoutDH.id.String():
			gotWithout = &body.Data[i]
		}
	}
	if gotWith == nil || gotWithout == nil {
		t.Fatalf("did not find both seeded rows in response")
	}
	if gotWith.DataHandling == nil || *gotWith.DataHandling != "zero_retention" {
		t.Errorf("DataHandling for withDH row = %v, want \"zero_retention\"", gotWith.DataHandling)
	}
	if gotWithout.DataHandling != nil {
		t.Errorf("DataHandling for withoutDH row = %v, want nil (never fabricate a designation nobody set)", *gotWithout.DataHandling)
	}
}

// TestAudit_RealDB_VerifyChainToEntry_IntactChain_ReturnsValid proves the
// backend half of AC3: calling GET /v1/audit/verify?to=<entry timestamp>
// the way the new "Verify chain to this entry" button will, against a real,
// genuinely intact chain, returns valid:true. Does not modify
// VerifyAuditChain -- exercises it as shipped.
func TestAudit_RealDB_VerifyChainToEntry_IntactChain_ReturnsValid(t *testing.T) {
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
	orgID := seedTestOrg(t, ctx, pool, "audit-verify-ok")
	userID := seedTestUser(t, ctx, pool, orgID)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1`, orgID) })

	ts, authSvc := auditTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@audit-verify-ok.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	now := time.Now().UTC()
	row1 := seedAuditRow(t, ctx, pool, orgID, genesisHashTest(), "claude-1", "tool-a", "read", "allowed", "", now)
	row2 := seedAuditRow(t, ctx, pool, orgID, row1.hash, "claude-1", "tool-b", "write", "allowed", "", now.Add(time.Second))
	_ = seedAuditRow(t, ctx, pool, orgID, row2.hash, "claude-1", "tool-c", "delete", "denied", "", now.Add(2*time.Second))

	// Bound the verify to row2 specifically -- mirrors the panel calling
	// "Verify chain to this entry" for the second of three rows, not the
	// whole log.
	url := ts.URL + "/v1/audit/verify?to=" + row2.timestamp.Format(time.RFC3339Nano)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/audit/verify: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result store.AuditVerifyResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.Valid {
		t.Errorf("Valid = false, want true for a genuinely intact chain (message: %s)", result.Message)
	}
	if result.TotalRows != 2 {
		t.Errorf("TotalRows = %d, want 2 (row1+row2 only, row3 is after the ?to bound)", result.TotalRows)
	}
	if result.FirstBrokenAt != nil {
		t.Errorf("FirstBrokenAt = %v, want nil", *result.FirstBrokenAt)
	}
}

// TestAudit_RealDB_VerifyChainToEntry_TamperedRow_ReturnsInvalid proves the
// negative case: a real tampered row (hash overwritten after insert, exactly
// what an attacker editing the row would produce) is genuinely detected as
// broken, not silently reported valid.
func TestAudit_RealDB_VerifyChainToEntry_TamperedRow_ReturnsInvalid(t *testing.T) {
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
	orgID := seedTestOrg(t, ctx, pool, "audit-verify-bad")
	userID := seedTestUser(t, ctx, pool, orgID)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1`, orgID) })

	ts, authSvc := auditTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@audit-verify-bad.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	now := time.Now().UTC()
	row1 := seedAuditRow(t, ctx, pool, orgID, genesisHashTest(), "claude-1", "tool-a", "read", "allowed", "", now)
	row2 := seedAuditRow(t, ctx, pool, orgID, row1.hash, "claude-1", "tool-b", "write", "allowed", "", now.Add(time.Second))

	// Simulate tampering: someone changed row2's recorded action after the
	// fact (directly via SQL -- audit_log's RLS UPDATE-deny policy is a DB
	// user-level control, irrelevant to what this test needs to prove about
	// the verify logic itself, which is what a superuser bypassing RLS, or a
	// restore-from-backup, could actually produce).
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET action = 'delete' WHERE id = $1`, row2.id); err != nil {
		t.Fatalf("tamper row: %v", err)
	}

	url := ts.URL + "/v1/audit/verify?to=" + row2.timestamp.Format(time.RFC3339Nano)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/audit/verify: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result store.AuditVerifyResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Valid {
		t.Fatalf("Valid = true, want false for a genuinely tampered row")
	}
	if result.FirstBrokenAt == nil || *result.FirstBrokenAt != row2.id.String() {
		t.Errorf("FirstBrokenAt = %v, want %s", result.FirstBrokenAt, row2.id.String())
	}
}
