package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eami/api/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

func TestAuditExportCSV_NeutralizesSpreadsheetFormulas(t *testing.T) {
	csv, err := auditExportCSV([]store.AuditExportRow{{
		ID:        uuid.New(),
		AgentName: "=SUM(1,1)",
		ToolName:  "+formula-tool",
		Action:    "@formula-action",
		Decision:  "allowed",
		LatencyMS: pgtype.Int4{Int32: 12, Valid: true},
		Timestamp: time.Date(2026, 9, 25, 4, 0, 0, 123, time.UTC),
		Hash:      "abc123",
	}})
	if err != nil {
		t.Fatalf("auditExportCSV: %v", err)
	}
	for _, want := range []string{"'=SUM(1,1)", "'+formula-tool", "'@formula-action"} {
		if !strings.Contains(string(csv), want) {
			t.Errorf("CSV does not neutralize %q: %q", want, csv)
		}
	}
}

func TestAuditExport_RateLimitsPerOrganization(t *testing.T) {
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	s := NewServer(store.New(nil), authSvc, nil, nil)
	s.auditExportLimiter = newRateLimiter(1, time.Minute)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	orgID := uuid.New()
	token, _, err := authSvc.IssueAccessToken(uuid.New(), orgID, "viewer@audit-export.test", "viewer")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	for attempt, wantStatus := range []int{http.StatusInternalServerError, http.StatusTooManyRequests} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/audit/export", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
		resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Errorf("attempt %d status = %d, want %d", attempt+1, resp.StatusCode, wantStatus)
		}
		if wantStatus == http.StatusTooManyRequests && resp.Header.Get("Retry-After") == "" {
			t.Error("rate-limited export is missing Retry-After")
		}
	}
}

func TestAuditExport_InvalidFiltersDoNotConsumeExportQuota(t *testing.T) {
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	s := NewServer(nil, authSvc, nil, nil)
	s.auditExportLimiter = newRateLimiter(4, time.Minute)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	orgID := uuid.New()
	token, _, err := authSvc.IssueAccessToken(uuid.New(), orgID, "viewer@audit-export.test", "viewer")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/audit/export?from=not-a-date", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("invalid export request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid export status = %d, want 400", resp.StatusCode)
	}
	for i := 0; i < 4; i++ {
		if ok, _ := s.auditExportLimiter.Allow(orgID.String()); !ok {
			t.Fatalf("invalid filter consumed export quota at slot %d", i+1)
		}
	}
}

func TestAuditExport_OnlyOneActiveExportPerOrganization(t *testing.T) {
	s := NewServer(nil, nil, nil, nil)
	const orgID = "one-org"
	if !s.beginAuditExport(orgID) {
		t.Fatal("first export must acquire the organization slot")
	}
	if s.beginAuditExport(orgID) {
		t.Fatal("second concurrent export must not acquire the organization slot")
	}
	s.endAuditExport(orgID)
	if !s.beginAuditExport(orgID) {
		t.Fatal("export slot must be released after completion")
	}
	s.endAuditExport(orgID)
}

func TestWriteAuditExportQueryError_DeadlineIsActionable(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAuditExportQueryError(rec, context.DeadlineExceeded)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "narrow the filters") {
		t.Errorf("deadline error is not actionable: %s", rec.Body.String())
	}
}

func TestAuditExportCSV_RejectsOversizeResponse(t *testing.T) {
	tooLarge := strings.Repeat("a", auditExportMaxBytes)
	_, err := auditExportCSV([]store.AuditExportRow{{
		ID:        uuid.New(),
		AgentName: tooLarge,
		Decision:  "allowed",
		Timestamp: time.Now().UTC(),
	}})
	if !errors.Is(err, errAuditExportTooLarge) {
		t.Fatalf("auditExportCSV error = %v, want errAuditExportTooLarge", err)
	}
}
