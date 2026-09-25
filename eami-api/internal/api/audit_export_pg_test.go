package api_test

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/store"
)

func TestAuditExport_RealDB_RespectsFiltersAndOrgIsolation(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
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
	orgID := seedTestOrg(t, ctx, pool, "audit-export-own")
	otherOrgID := seedTestOrg(t, ctx, pool, "audit-export-other")
	userID := seedTestUser(t, ctx, pool, orgID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_log WHERE org_id = $1 OR org_id = $2`, orgID, otherOrgID)
	})

	ts, authSvc := auditTestServer(t, q)
	t.Cleanup(ts.Close)
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@audit-export.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	ownRow := seedAuditRow(t, ctx, pool, orgID, genesisHashTest(), "=matching-agent", "matching-tool", "safe.action", "allowed", "", now)
	seedAuditRow(t, ctx, pool, orgID, genesisHashTest(), "other-agent", "matching-tool", "safe.action", "allowed", "", now.Add(time.Second))
	foreignRow := seedAuditRow(t, ctx, pool, otherOrgID, genesisHashTest(), "=matching-agent", "matching-tool", "safe.action", "allowed", "", now)

	query := url.Values{
		"agent_name": {"=matching-agent"},
		"tool_name":  {"matching-tool"},
		"decision":   {"allowed"},
		"from":       {now.Add(-time.Second).Format(time.RFC3339)},
		"to":         {now.Add(time.Second).Format(time.RFC3339)},
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/audit/export?"+query.Encode(), nil)
	if err != nil {
		t.Fatalf("new export request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET audit export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET audit export status = %d, want 200", resp.StatusCode)
	}
	rows, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV rows = %d, want header plus one matching own-org row: %#v", len(rows), rows)
	}
	if got, want := strings.Join(rows[0], ","), "timestamp,agent,tool,action,decision,latency_ms,tokens_in,tokens_out,hash"; got != want {
		t.Errorf("CSV header = %q, want %q", got, want)
	}
	if got := rows[1][1]; got != "'=matching-agent" {
		t.Errorf("CSV agent = %q, want formula-safe matching own-org agent", got)
	}
	if got := rows[1][8]; got != ownRow.hash {
		t.Errorf("CSV hash = %q, want own-org row hash %q", got, ownRow.hash)
	}
	if got := strings.Join(rows[1], ","); strings.Contains(got, foreignRow.hash) {
		t.Errorf("cross-org audit row leaked into export: %q", got)
	}
}

func TestAuditExport_RealDB_RejectsMalformedDate(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
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
	orgID := seedTestOrg(t, ctx, pool, "audit-export-dates")
	userID := seedTestUser(t, ctx, pool, orgID)
	ts, authSvc := auditTestServer(t, q)
	t.Cleanup(ts.Close)
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@audit-export-dates.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	for _, path := range []string{"/v1/audit?from=not-a-date", "/v1/audit/export?to=not-a-date"} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", path, resp.StatusCode)
		}
	}
}
