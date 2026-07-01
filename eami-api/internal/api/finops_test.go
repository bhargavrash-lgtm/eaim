// finops_test.go — eami-api/internal/api
// QA-EAMI — validation-path unit tests for FinOps handlers.
//
// Package api (white-box) so parseDateParam (unexported helper) can be called
// directly. MockStore and handler infrastructure are in the same package.
//
// IMPORTANT — DB path:
//   Both FinOpsSummary and FinOpsTimeSeries call s.queries.DB() to run raw SQL.
//   None of the tests below reach that call — all requests fail validation first.
//   s.queries is nil in this test server; that is intentional and safe.
//
// Run:
//   go test -count=1 -run TestParseDateParam ./internal/api/...
//   go test -count=1 -run TestFinOpsSummary  ./internal/api/...
//   go test -count=1 -run TestFinOpsTimeSeries ./internal/api/...

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/eami/api/internal/auth"
	"github.com/google/uuid"
)

// ─── parseDateParam unit tests ────────────────────────────────────────────────
// parseDateParam reads r.URL.Query().Get(key), so we build a minimal request
// with the value set as a query parameter named "v".

func makeDateRequest(value string) *http.Request {
	u := &url.URL{Path: "/", RawQuery: url.Values{"v": {value}}.Encode()}
	r := httptest.NewRequest(http.MethodGet, u.String(), nil)
	return r
}

func TestParseDateParam_RFC3339(t *testing.T) {
	input := "2025-01-01T00:00:00Z"
	got, err := parseDateParam(makeDateRequest(input), "v")
	if err != nil {
		t.Fatalf("unexpected error for RFC3339 input %q: %v", input, err)
	}
	want := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("RFC3339 parse: got %v, want %v", got, want)
	}
}

func TestParseDateParam_DateOnly(t *testing.T) {
	input := "2025-01-01"
	got, err := parseDateParam(makeDateRequest(input), "v")
	if err != nil {
		t.Fatalf("unexpected error for date-only input %q: %v", input, err)
	}
	if got.Year() != 2025 || got.Month() != 1 || got.Day() != 1 {
		t.Errorf("date-only parse: got %v, want 2025-01-01", got)
	}
}

func TestParseDateParam_Missing(t *testing.T) {
	// key "v" absent — empty string → error
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := parseDateParam(r, "v")
	if err == nil {
		t.Error("missing param must return an error")
	}
}

func TestParseDateParam_Invalid(t *testing.T) {
	_, err := parseDateParam(makeDateRequest("notadate"), "v")
	if err == nil {
		t.Error(`"notadate" must return an error`)
	}
}

func TestParseDateParam_Variants(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"RFC3339 with offset", "2025-06-01T12:30:00+05:30", false},
		{"date only midyear", "2025-07-15", false},
		{"partial date", "2025-07", true},
		{"unix timestamp", "1735689600", true},
		{"slash separated", "2025/01/01", true},
		{"whitespace", "  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDateParam(makeDateRequest(tt.input), "v")
			if tt.wantErr && err == nil {
				t.Errorf("input %q: want error, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("input %q: want nil error, got %v", tt.input, err)
			}
		})
	}
}

// ─── Test server helper ───────────────────────────────────────────────────────

type finOpsTestEnv struct {
	srv     *httptest.Server
	authSvc *auth.Service
}

func newFinOpsTestEnv(t *testing.T) *finOpsTestEnv {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	// queries is nil — safe because all test requests fail validation before
	// reaching s.queries.DB().
	srv := NewServer(nil, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &finOpsTestEnv{srv: ts, authSvc: authSvc}
}

func (fe *finOpsTestEnv) adminToken(t *testing.T) string {
	t.Helper()
	userID := uuid.MustParse("ffffffff-0000-0000-0000-000000000001")
	orgID := uuid.MustParse("ffffffff-0000-0000-0000-000000000002")
	tok, _, err := fe.authSvc.IssueAccessToken(userID, orgID, "admin@example.com", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

func (fe *finOpsTestEnv) get(t *testing.T, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fe.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := fe.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// ─── FinOpsSummary validation tests ──────────────────────────────────────────

func TestFinOpsSummary_RequiresAuth(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	resp := fe.get(t, "/v1/finops/summary?from=2025-01-01&to=2025-02-01", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

func TestFinOpsSummary_MissingFrom(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/summary?to=2025-02-01", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing 'from' must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsSummary_MissingTo(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/summary?from=2025-01-01", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing 'to' must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsSummary_ToBeforeFrom(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/summary?from=2025-12-01&to=2025-01-01", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("to before from must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsSummary_EqualFromTo(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/summary?from=2025-06-01&to=2025-06-01", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("equal from/to must yield 400 (to must be after from), got %d", resp.StatusCode)
	}
}

func TestFinOpsSummary_InvalidFrom(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/summary?from=notadate&to=2025-02-01", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid 'from' must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsSummary_InvalidTo(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/summary?from=2025-01-01&to=notadate", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid 'to' must yield 400, got %d", resp.StatusCode)
	}
}

// ─── FinOpsTimeSeries validation tests ───────────────────────────────────────

func TestFinOpsTimeSeries_RequiresAuth(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	resp := fe.get(t, "/v1/finops/timeseries?from=2025-01-01&to=2025-02-01&granularity=day", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

func TestFinOpsTimeSeries_MissingFrom(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/timeseries?to=2025-02-01&granularity=day", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing 'from' must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsTimeSeries_MissingTo(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/timeseries?from=2025-01-01&granularity=day", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing 'to' must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsTimeSeries_ToBeforeFrom(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/timeseries?from=2025-12-01&to=2025-01-01&granularity=day", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("to before from must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsTimeSeries_InvalidGranularity(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/timeseries?from=2025-01-01&to=2025-02-01&granularity=month", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid granularity must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsTimeSeries_InvalidAgentID(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/timeseries?from=2025-01-01&to=2025-02-01&granularity=day&agent_id=notauuid", tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid agent_id UUID must yield 400, got %d", resp.StatusCode)
	}
}

func TestFinOpsTimeSeries_ValidGranularities(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	for _, g := range []string{"hour", "day", "week"} {
		t.Run(g, func(t *testing.T) {
			path := fmt.Sprintf("/v1/finops/timeseries?from=2025-01-01&to=2025-02-01&granularity=%s", g)
			resp := fe.get(t, path, tok)
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusBadRequest {
				t.Errorf("valid granularity %q must not yield 400", g)
			}
			if resp.StatusCode == http.StatusUnauthorized {
				t.Errorf("valid token unexpectedly rejected for granularity %q", g)
			}
		})
	}
}

func TestFinOpsTimeSeries_ValidAgentID_PassesValidation(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	agentID := uuid.New().String()
	path := fmt.Sprintf(
		"/v1/finops/timeseries?from=2025-01-01&to=2025-02-01&granularity=day&agent_id=%s",
		agentID,
	)
	resp := fe.get(t, path, tok)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		t.Errorf("valid UUID agent_id %q must not yield 400", agentID)
	}
}

func TestFinOpsTimeSeries_MissingGranularity_UsesDefault(t *testing.T) {
	fe := newFinOpsTestEnv(t)
	tok := fe.adminToken(t)
	resp := fe.get(t, "/v1/finops/timeseries?from=2025-01-01&to=2025-02-01", tok)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		t.Error("missing granularity should default to day and not yield 400")
	}
}
