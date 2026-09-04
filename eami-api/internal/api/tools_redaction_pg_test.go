// tools_redaction_pg_test.go -- eami-api/internal/api
// Real-Postgres integration tests for B-156/B-167: per-connector pattern-
// based redaction config for ai_provider connectors (AC4). Proves create/
// edit/type-guard/invalid-pattern-rejection at the real HTTP + real
// Postgres level, not just against fakeToolStore. Mirrors
// tools_data_handling_pg_test.go's own structure exactly (same t.Cleanup-
// only pool-lifecycle convention, CLAUDE.md's mandatory pattern).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestRedactionRules_RealDB -v
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
)

func newRedactionTestServer(t *testing.T, orgSlug string) (*httptest.Server, *pgxpool.Pool, string) {
	t.Helper()
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
	orgID := seedTestOrg(t, ctx, pool, orgSlug)
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@"+orgSlug+".test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return ts, pool, token
}

func doJSONRedaction(t *testing.T, ts *httptest.Server, token, method, path string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// TestRedactionRules_RealDB_CreateAndUpdate_PersistsCorrectly proves AC4's
// own prerequisite end to end: an admin can set redaction_rules on create
// and later change it via PATCH, both landing in the real gateway_tools
// row eami-gateway's Router.Dispatch reads at real dispatch time.
func TestRedactionRules_RealDB_CreateAndUpdate_PersistsCorrectly(t *testing.T) {
	ts, pool, token := newRedactionTestServer(t, "redact-crud")
	ctx := context.Background()

	createResp := doJSONRedaction(t, ts, token, http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "redact-crud-connector", "type": "ai_provider", "auth_type": "api_key",
		"provider":        "claude",
		"redaction_rules": map[string]any{"enabled": false},
	})
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(createResp.Body)
		t.Fatalf("create: status = %d, want 201: %s", createResp.StatusCode, errBody.String())
	}
	var created api.ToolResp
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(created.RedactionRules) == 0 {
		t.Fatal("created RedactionRules is empty, want the submitted {\"enabled\": false}")
	}

	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT redaction_rules FROM gateway_tools WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read back gateway_tools row: %v", err)
	}
	var storedParsed map[string]any
	if err := json.Unmarshal(stored, &storedParsed); err != nil {
		t.Fatalf("stored redaction_rules is not valid JSON: %v (%s)", err, stored)
	}
	if enabled, _ := storedParsed["enabled"].(bool); enabled {
		t.Errorf("stored redaction_rules[enabled] = true, want false")
	}

	// Edit: switch to a custom pattern.
	patchResp := doJSONRedaction(t, ts, token, http.MethodPatch, "/v1/gateway/tools/"+created.ID.String(), map[string]any{
		"redaction_rules": map[string]any{
			"custom_patterns": []map[string]string{{"name": "employee_id", "pattern": `EMP-\d{6}`}},
		},
	})
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(patchResp.Body)
		t.Fatalf("patch: status = %d, want 200: %s", patchResp.StatusCode, errBody.String())
	}

	if err := pool.QueryRow(ctx, `SELECT redaction_rules FROM gateway_tools WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("re-read gateway_tools row after patch: %v", err)
	}
	if err := json.Unmarshal(stored, &storedParsed); err != nil {
		t.Fatalf("stored redaction_rules after patch is not valid JSON: %v (%s)", err, stored)
	}
	patterns, _ := storedParsed["custom_patterns"].([]any)
	if len(patterns) != 1 {
		t.Fatalf("post-patch custom_patterns = %v, want exactly 1 entry", storedParsed["custom_patterns"])
	}
}

// TestRedactionRules_RealDB_NewConnector_DefaultsToNull proves the
// fail-safe-on default holds via the real column's NULL default (no
// explicit config submitted at all) -- eami-gateway's redaction.ParseConfig
// treats this identically to an explicit {"enabled": true}.
func TestRedactionRules_RealDB_NewConnector_DefaultsToNull(t *testing.T) {
	ts, pool, token := newRedactionTestServer(t, "redact-default")
	ctx := context.Background()

	resp := doJSONRedaction(t, ts, token, http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "redact-default-connector", "type": "ai_provider", "auth_type": "api_key", "provider": "claude",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created api.ToolResp
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.RedactionRules) != 0 {
		t.Errorf("RedactionRules = %s, want empty/absent (no override submitted)", created.RedactionRules)
	}

	var stored *string
	if err := pool.QueryRow(ctx, `SELECT redaction_rules::text FROM gateway_tools WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored != nil {
		t.Errorf("stored redaction_rules = %v, want a real SQL NULL", *stored)
	}
}

// TestRedactionRules_RealDB_NonAIProviderType_Rejected proves the same
// type-guard AuditMode/DataHandling already have (B-047/B-078) extends
// correctly to redaction_rules: a rest_api connector cannot carry a
// redaction config that no dispatch path will ever read.
func TestRedactionRules_RealDB_NonAIProviderType_Rejected(t *testing.T) {
	ts, _, token := newRedactionTestServer(t, "redact-typeguard")

	resp := doJSONRedaction(t, ts, token, http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "redact-typeguard-connector", "type": "rest_api", "auth_type": "api_key",
		"base_url":        "https://example.com",
		"redaction_rules": map[string]any{"enabled": false},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, errBody.String())
	}
}

// TestRedactionRules_RealDB_InvalidCustomPattern_Rejected proves an
// uncompilable admin-submitted regex is caught at write time (a clear
// 400), not discovered only when a real dispatch later fails inside
// eami-gateway's own redaction.ParseConfig.
func TestRedactionRules_RealDB_InvalidCustomPattern_Rejected(t *testing.T) {
	ts, _, token := newRedactionTestServer(t, "redact-badpattern")

	resp := doJSONRedaction(t, ts, token, http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "redact-badpattern-connector", "type": "ai_provider", "auth_type": "api_key",
		"provider": "claude",
		"redaction_rules": map[string]any{
			"custom_patterns": []map[string]string{{"name": "bad", "pattern": "("}},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, errBody.String())
	}
}

// TestRedactionRules_RealDB_TooManyCustomPatterns_Rejected proves the
// security-review finding's fix: an unbounded custom_patterns list is a
// per-dispatch CPU amplification lever (each pattern is recompiled and
// evaluated against every string in every real dispatch through this
// connector) -- rejected at write time, not just at dispatch time.
func TestRedactionRules_RealDB_TooManyCustomPatterns_Rejected(t *testing.T) {
	ts, _, token := newRedactionTestServer(t, "redact-toomany")

	patterns := make([]map[string]string, 51)
	for i := range patterns {
		patterns[i] = map[string]string{"name": "p", "pattern": "x"}
	}
	resp := doJSONRedaction(t, ts, token, http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "redact-toomany-connector", "type": "ai_provider", "auth_type": "api_key",
		"provider":        "claude",
		"redaction_rules": map[string]any{"custom_patterns": patterns},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, errBody.String())
	}
}

// TestRedactionRules_RealDB_ZeroWidthCustomPattern_Rejected proves the
// other security-review fix: a pattern matching the literal empty string
// fires at every rune boundary in real content -- rejected at write time.
func TestRedactionRules_RealDB_ZeroWidthCustomPattern_Rejected(t *testing.T) {
	ts, _, token := newRedactionTestServer(t, "redact-zerowidth")

	resp := doJSONRedaction(t, ts, token, http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "redact-zerowidth-connector", "type": "ai_provider", "auth_type": "api_key",
		"provider": "claude",
		"redaction_rules": map[string]any{
			"custom_patterns": []map[string]string{{"name": "zw", "pattern": "a*"}},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, errBody.String())
	}
}
