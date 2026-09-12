// alerts_pg_test.go -- eami-api/internal/api
// Integration tests for the alert-rule creation/update contract fixed under
// B-184. Two real, independent bugs were confirmed by direct reproduction
// against current code (not the original B-182 diagnosis, which was based on
// a hand-crafted request body rather than a captured real-UI submission):
//
//   1. api/openapi.yaml's AlertRuleCreate/AlertRuleUpdate write schemas sent
//      "window" while eami-api's Go request structs expect JSON key
//      "window_minutes" -- every real submission zero-valued WindowMinutes,
//      which then failed validateAlertRuleReq's window-enum check. This
//      blocked ALL rule creation, not the missing-"condition" cause B-182
//      originally logged (the frontend's toApiCreate() already hardcoded
//      condition: "gt" -- that part never actually reproduced through the
//      real UI).
//   2. The same write schemas declared severity: [low, medium, high,
//      critical], but validateAlertRuleReq (and every other severity surface
//      in the app) uses info/warning/high/critical -- "info"/"warning" rules
//      always 400'd too, independently of bug 1.
//
// Fixed by correcting the openapi.yaml write schemas to match the backend's
// actual field name/enum (the spec was the outlier, not the Go handler or
// the rest of the frontend's own domain types), regenerating schema.ts, and
// removing the now-unneeded METRIC_TO_API/SEVERITY_TO_API translation layers
// in eami-ui/src/hooks/useAlerts.ts.
//
// Same real-Postgres-only convention as finops_pg_test.go -- skips cleanly
// without TEST_DATABASE_URL/POSTGRES_PASSWORD.
//
// Run against the project's docker-compose Postgres:
//   docker compose up -d postgres
//   POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestAlertRule -v
package api_test

import (
	"bytes"
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

	"github.com/eami/api/internal/alerting"
	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
)

func alertsPgTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	pw := os.Getenv("POSTGRES_PASSWORD")
	if pw == "" {
		t.Skip("skipping: set TEST_DATABASE_URL (or POSTGRES_PASSWORD, using the docker-compose eami_app/eami/localhost:5432 layout) to run alerts integration tests against a real Postgres")
	}
	return fmt.Sprintf("postgresql://eami_app:%s@localhost:5432/eami", pw)
}

// alertsPgTestEnv wires a real *store.Queries against a real Postgres, a
// throwaway org, and a JWT-capable httptest.Server. pool.Close is registered
// via t.Cleanup before any other t.Cleanup that touches the database, per
// this repo's mandatory real-Postgres test lifecycle rule (CLAUDE.md) --
// never a plain `defer pool.Close()` mixed with t.Cleanup.
type alertsPgTestEnv struct {
	pool    *pgxpool.Pool
	queries *store.Queries
	srv     *httptest.Server
	authSvc *auth.Service
	orgID   uuid.UUID
	userID  uuid.UUID
}

func newAlertsPgTestEnv(t *testing.T) *alertsPgTestEnv {
	t.Helper()
	dsn := alertsPgTestDSN(t)
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
		orgID.String(), "alerts-test-"+orgID.String()[:8], "alerts-test-"+orgID.String(),
	); err != nil {
		t.Fatalf("seed test org: %v", err)
	}
	t.Cleanup(func() {
		// alert_rules.org_id / alerts.org_id both FK to orgs with ON DELETE
		// CASCADE (schema.sql) -- deleting the org cleans up every rule/alert
		// this test created without a separate explicit DELETE.
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1::uuid`, orgID.String())
	})

	// alert_rules.created_by is a real FK to users(id) (schema.sql) -- unlike
	// finops_pg_test.go's env, these tests must seed a real user row or every
	// CreateAlertRule call 500s on the FK violation.
	userID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'admin')`,
		userID, orgID, "admin-"+userID.String()[:8]+"@alerts-test.example",
	); err != nil {
		t.Fatalf("seed test user: %v", err)
	}

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	cfg := &config.Config{ServiceKey: "test-service-key-alerts"}
	// Empty collector URL/key: queryMetricWithCollector logs a warning and
	// returns 0 for failed_delivery_count rather than erroring, which is
	// exactly the behavior these tests want -- they exercise validation and
	// the create/update/test/delete contract, not the collector integration.
	engine := alerting.NewEngine(q, "", "")
	s := api.NewServer(q, authSvc, engine, cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &alertsPgTestEnv{pool: pool, queries: q, srv: ts, authSvc: authSvc, orgID: orgID, userID: userID}
}

func (e *alertsPgTestEnv) adminToken(t *testing.T) string {
	t.Helper()
	tok, _, err := e.authSvc.IssueAccessToken(e.userID, e.orgID, "admin@alerts-test.example", "admin")
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}
	return tok
}

// rawRequest sends an arbitrary raw JSON body (a map, not a typed struct) so
// tests can reproduce exactly the wire shape a real client would send --
// including the OLD, buggy "window" key, which a typed Go struct using the
// current field name could never accidentally produce.
func (e *alertsPgTestEnv) rawRequest(t *testing.T, method, path string, body map[string]any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.adminToken(t))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeAlertRule(t *testing.T, resp *http.Response) api.AlertRuleResp {
	t.Helper()
	defer resp.Body.Close()
	var out api.AlertRuleResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode AlertRuleResp: %v", err)
	}
	return out
}

func validRuleBody(name, metric, severity string, windowMinutes int) map[string]any {
	return map[string]any{
		"name":           name,
		"metric":         metric,
		"condition":      "gt",
		"threshold":      5,
		"window_minutes": windowMinutes,
		"severity":       severity,
	}
}

// ─── B-184: window field-name regression ────────────────────────────────────

// TestCreateAlertRule_Real_WindowMinutesFieldName_CorrectShapeSucceeds proves
// the fixed contract: a request body using "window_minutes" (matching the
// corrected openapi.yaml/schema.ts/toApiCreate, and always matching the Go
// handler) creates successfully and round-trips the exact window back out.
func TestCreateAlertRule_Real_WindowMinutesFieldName_CorrectShapeSucceeds(t *testing.T) {
	env := newAlertsPgTestEnv(t)

	resp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules",
		validRuleBody("b184-window-fieldname-test", "token_spend_usd", "high", 15))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	rule := decodeAlertRule(t, resp)
	if rule.WindowMinutes != 15 {
		t.Errorf("WindowMinutes = %d, want 15", rule.WindowMinutes)
	}
}

// TestCreateAlertRule_Real_OldWindowKey_StillRejected is the actual B-184
// regression guard: the OLD, buggy wire shape (a bare "window" key, matching
// what the pre-fix openapi.yaml/frontend used to send) must still fail
// validation rather than silently succeed with a wrong/zeroed window -- if
// this ever starts passing again, the field-name bug has resurfaced.
func TestCreateAlertRule_Real_OldWindowKey_StillRejected(t *testing.T) {
	env := newAlertsPgTestEnv(t)

	body := map[string]any{
		"name":      "b184-old-window-key-test",
		"metric":    "token_spend_usd",
		"condition": "gt",
		"threshold": 5,
		"window":    15, // old, wrong key -- WindowMinutes must decode as 0
		"severity":  "high",
	}
	resp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for the old \"window\" key (WindowMinutes decodes to 0), got %d", resp.StatusCode)
	}
}

// ─── B-184: severity enum regression ────────────────────────────────────────

// TestCreateAlertRule_Real_AllFourSeverities_Succeed proves the second,
// independent B-184 bug is fixed: the OLD write-schema enum (low/medium/high/
// critical) would have 400'd "info" and "warning" outright since the backend
// validator never accepted those spellings -- now all four of the backend's
// actual severities succeed.
func TestCreateAlertRule_Real_AllFourSeverities_Succeed(t *testing.T) {
	env := newAlertsPgTestEnv(t)
	for _, sev := range []string{"info", "warning", "high", "critical"} {
		t.Run(sev, func(t *testing.T) {
			resp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules",
				validRuleBody("b184-severity-"+sev, "token_spend_usd", sev, 60))
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("severity %q: want 201, got %d", sev, resp.StatusCode)
			}
			rule := decodeAlertRule(t, resp)
			if rule.Severity != sev {
				t.Errorf("severity %q: got %q back", sev, rule.Severity)
			}
		})
	}
}

// TestCreateAlertRule_Real_OldWriteSchemaSeverities_StillRejected guards
// against the old low/medium spellings ever being silently accepted -- they
// were never valid backend severities and must keep 400ing.
func TestCreateAlertRule_Real_OldWriteSchemaSeverities_StillRejected(t *testing.T) {
	env := newAlertsPgTestEnv(t)
	for _, sev := range []string{"low", "medium"} {
		t.Run(sev, func(t *testing.T) {
			resp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules",
				validRuleBody("b184-old-severity-"+sev, "token_spend_usd", sev, 60))
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("severity %q: want 400, got %d", sev, resp.StatusCode)
			}
		})
	}
}

// ─── B-184: metric-key regression, all 6 metrics ────────────────────────────

// TestCreateAlertRule_Real_AllSixMetrics_Succeed proves every metric the
// frontend's canonical MetricKey type now offers creates successfully --
// the pre-fix frontend only ever sent the correct string for token_spend_usd
// by coincidence (no suffix on either side); the other 5 used the OLD
// unsuffixed keys, which validateAlertRuleReq has never accepted.
func TestCreateAlertRule_Real_AllSixMetrics_Succeed(t *testing.T) {
	env := newAlertsPgTestEnv(t)
	metrics := []string{
		"denied_actions_count", "escalated_actions_count", "scope_drift_count",
		"new_endpoints_count", "token_spend_usd", "failed_delivery_count",
	}
	for _, m := range metrics {
		t.Run(m, func(t *testing.T) {
			resp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules",
				validRuleBody("b184-metric-"+m, m, "high", 60))
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("metric %q: want 201, got %d", m, resp.StatusCode)
			}
			rule := decodeAlertRule(t, resp)
			if rule.Metric != m {
				t.Errorf("metric %q: got %q back", m, rule.Metric)
			}
		})
	}
}

// ─── B-184: TestAlertRule response field-name regression ───────────────────

// TestTestAlertRule_Real_ResponseFieldsMatchFrontendType proves the third
// B-184 finding: eami-api's TestAlertRuleResp already returns {metric,
// metric_value, ...} (not the frontend's old {metric_key, current_value}
// expectations) -- confirmed here at the wire level so a future rename on
// either side is caught by this test, not discovered as an "undefined" toast
// message in the live UI.
func TestTestAlertRule_Real_ResponseFieldsMatchFrontendType(t *testing.T) {
	env := newAlertsPgTestEnv(t)

	createResp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules",
		validRuleBody("b184-test-endpoint-fields", "token_spend_usd", "high", 60))
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", createResp.StatusCode)
	}
	rule := decodeAlertRule(t, createResp)

	testResp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules/"+rule.ID+"/test", nil)
	defer testResp.Body.Close()
	if testResp.StatusCode != http.StatusOK {
		t.Fatalf("test: want 200, got %d", testResp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(testResp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	for _, field := range []string{"metric", "metric_value", "threshold", "would_fire"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("response missing expected field %q (raw: %+v)", field, raw)
		}
	}
	for _, oldField := range []string{"metric_key", "current_value"} {
		if _, ok := raw[oldField]; ok {
			t.Errorf("response unexpectedly has OLD field %q -- frontend/backend field names have drifted back apart", oldField)
		}
	}
}

// ─── Full lifecycle, real data ───────────────────────────────────────────────

// TestAlertRuleLifecycle_Real_CreateUpdateTestDelete exercises the complete
// create -> list -> update -> test -> delete flow through the real HTTP
// handlers and real Postgres, matching what the live UI click-through
// verifies -- and specifically confirms updating unrelated fields (name)
// leaves metric/severity/window untouched, the invariant the frontend's
// Edit-mode relies on now that MetricKey matches the backend exactly.
func TestAlertRuleLifecycle_Real_CreateUpdateTestDelete(t *testing.T) {
	env := newAlertsPgTestEnv(t)

	createResp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules",
		validRuleBody("b184-lifecycle", "denied_actions_count", "warning", 5))
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", createResp.StatusCode)
	}
	created := decodeAlertRule(t, createResp)
	if created.Metric != "denied_actions_count" || created.Severity != "warning" || created.WindowMinutes != 5 {
		t.Fatalf("created rule fields wrong: %+v", created)
	}

	// List: the new rule appears.
	listResp := env.rawRequest(t, http.MethodGet, "/v1/alerts/rules", nil)
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", listResp.StatusCode)
	}
	var listOut api.AlertRuleListResp
	if err := json.NewDecoder(listResp.Body).Decode(&listOut); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, r := range listOut.Data {
		if r.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("newly created rule not present in list response")
	}

	// Update: change only the name -- metric/severity/window must survive
	// unchanged (this is the invariant the frontend's Edit form depends on).
	updateResp := env.rawRequest(t, http.MethodPut, "/v1/alerts/rules/"+created.ID,
		map[string]any{"name": "b184-lifecycle-renamed"})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update: want 200, got %d", updateResp.StatusCode)
	}
	updated := decodeAlertRule(t, updateResp)
	if updated.Name != "b184-lifecycle-renamed" {
		t.Errorf("Name = %q, want renamed", updated.Name)
	}
	if updated.Metric != "denied_actions_count" {
		t.Errorf("Metric changed after a name-only update: got %q, want denied_actions_count unchanged", updated.Metric)
	}
	if updated.Severity != "warning" {
		t.Errorf("Severity changed after a name-only update: got %q, want warning unchanged", updated.Severity)
	}
	if updated.WindowMinutes != 5 {
		t.Errorf("WindowMinutes changed after a name-only update: got %d, want 5 unchanged", updated.WindowMinutes)
	}

	// Test: dry-run evaluation succeeds against real (empty) audit_log data.
	testResp := env.rawRequest(t, http.MethodPost, "/v1/alerts/rules/"+created.ID+"/test", nil)
	defer testResp.Body.Close()
	if testResp.StatusCode != http.StatusOK {
		t.Fatalf("test: want 200, got %d", testResp.StatusCode)
	}

	// Delete: rule is gone from a subsequent list.
	delResp := env.rawRequest(t, http.MethodDelete, "/v1/alerts/rules/"+created.ID, nil)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", delResp.StatusCode)
	}
	listResp2 := env.rawRequest(t, http.MethodGet, "/v1/alerts/rules", nil)
	defer listResp2.Body.Close()
	var listOut2 api.AlertRuleListResp
	if err := json.NewDecoder(listResp2.Body).Decode(&listOut2); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	for _, r := range listOut2.Data {
		if r.ID == created.ID {
			t.Error("deleted rule still present in list response")
		}
	}
}
