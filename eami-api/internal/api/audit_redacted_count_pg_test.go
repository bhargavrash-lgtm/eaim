// audit_redacted_count_pg_test.go -- eami-api/internal/api
// Real-Postgres integration test for B-156/B-167's AC3: a real audit_log
// entry records how many items were redacted, never the redacted content
// itself -- and a row with no redaction applied (or written before this
// column existed) omits the field rather than fabricating a 0.
//
// Mirrors audit_pg_test.go's TestAudit_RealDB_DataHandlingDesignation_
// RoundTrips exactly (same seedAuditRow-style direct-SQL insert, same
// t.Cleanup-only pool lifecycle).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestAudit_RealDB_RedactedCount -v
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/store"
)

func TestAudit_RealDB_RedactedCount_RoundTrips(t *testing.T) {
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
	orgID := seedTestOrg(t, ctx, pool, "audit-redact")
	userID := seedTestUser(t, ctx, pool, orgID)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1`, orgID) })

	ts, authSvc := auditTestServer(t, q)
	defer ts.Close()
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@audit-redact.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	now := time.Now().UTC()
	withRedaction := seedAuditRow(t, ctx, pool, orgID, genesisHashTest(), "claude-1", "claude-connector", "messages", "allowed", "", now)
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET redacted_count = 3 WHERE id = $1`, withRedaction.id); err != nil {
		t.Fatalf("set redacted_count: %v", err)
	}
	// A real dispatch where redaction ran and found nothing -- 0 is a
	// real, meaningful, DIFFERENT value from "not applicable" (the next
	// row below), not the same as omitting the column.
	ranFoundNothing := seedAuditRow(t, ctx, pool, orgID, withRedaction.hash, "claude-1", "claude-connector", "messages", "allowed", "", now.Add(time.Second))
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET redacted_count = 0 WHERE id = $1`, ranFoundNothing.id); err != nil {
		t.Fatalf("set redacted_count: %v", err)
	}
	notApplicable := seedAuditRow(t, ctx, pool, orgID, ranFoundNothing.hash, "claude-1", "internal-tool", "query", "allowed", "", now.Add(2*time.Second))

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

	var gotRedacted, gotZero, gotNA *api.AuditEntryResp
	for i := range body.Data {
		switch body.Data[i].ID {
		case withRedaction.id.String():
			gotRedacted = &body.Data[i]
		case ranFoundNothing.id.String():
			gotZero = &body.Data[i]
		case notApplicable.id.String():
			gotNA = &body.Data[i]
		}
	}
	if gotRedacted == nil || gotZero == nil || gotNA == nil {
		t.Fatalf("did not find all 3 seeded rows in response")
	}
	if gotRedacted.RedactedCount == nil || *gotRedacted.RedactedCount != 3 {
		t.Errorf("RedactedCount for withRedaction row = %v, want 3", gotRedacted.RedactedCount)
	}
	if gotZero.RedactedCount == nil || *gotZero.RedactedCount != 0 {
		t.Errorf("RedactedCount for ranFoundNothing row = %v, want a real 0, not nil", gotZero.RedactedCount)
	}
	if gotNA.RedactedCount != nil {
		t.Errorf("RedactedCount for notApplicable row = %v, want nil (never fabricate a count nobody recorded)", *gotNA.RedactedCount)
	}

	// Never the actual redacted values -- this brief's own contract.
	// audit_log.parameters for these rows was never set to contain any
	// sensitive content in the first place (seedAuditRow doesn't set
	// parameters), so this also confirms the response carries only the
	// count field, nothing resembling redacted content alongside it.
	if gotRedacted.Parameters != nil {
		t.Errorf("Parameters unexpectedly present: %v", gotRedacted.Parameters)
	}
}
