// tools_data_handling_pg_test.go -- eami-api/internal/api
// Real-Postgres integration tests for B-078: data-handling visibility for
// ai_provider connectors. Proves AC1 (create/edit persisted correctly) at
// the real HTTP + real Postgres level, not just against fakeToolStore.
//
// Follows workflows_test.go's t.Cleanup-only pool-lifecycle convention
// (CLAUDE.md's mandatory pattern) -- NOT tools_update_pg_test.go's older
// plain-defer-pair style, which CLAUDE.md explicitly says is no longer the
// template to extend.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestDataHandling_RealDB -v
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

// TestDataHandling_RealDB_CreateAndUpdate_PersistsCorrectly proves AC1
// end to end against a real Postgres: create an ai_provider connector
// with an explicit designation+note, confirm it round-trips via GET, then
// PATCH the designation and confirm the update lands too.
func TestDataHandling_RealDB_CreateAndUpdate_PersistsCorrectly(t *testing.T) {
	dsn := toolsUpdateTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach test database: %v", err)
	}
	// t.Cleanup, not a plain defer -- see CLAUDE.md's mandatory
	// pool-lifecycle rule / BACKLOG.md B-056: t.Cleanup runs LIFO,
	// strictly after this function's own defers, so a plain defer
	// pool.Close() here would close the pool before the org-delete
	// t.Cleanup registered below ever got a chance to run.
	t.Cleanup(func() { pool.Close() })

	q := store.New(pool)
	orgID := seedTestOrg(t, ctx, pool, "dh-crud")
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@dh-crud.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	doJSON := func(method, path string, body map[string]any) *http.Response {
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

	// Create with an explicit designation + note.
	createResp := doJSON(http.MethodPost, "/v1/gateway/tools", map[string]any{
		"name": "dh-crud-connector", "type": "ai_provider", "auth_type": "api_key",
		"provider":                  "claude",
		"data_handling_designation": "zero_retention",
		"data_handling_note":        "Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum",
	})
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", createResp.StatusCode)
	}
	var created api.ToolResp
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.DataHandlingDesignation != "zero_retention" {
		t.Errorf("created DataHandlingDesignation = %q, want zero_retention", created.DataHandlingDesignation)
	}
	if created.DataHandlingNote == nil || *created.DataHandlingNote != "Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum" {
		t.Errorf("created DataHandlingNote = %v, want the submitted citation", created.DataHandlingNote)
	}

	// Confirm it's real, persisted state -- read back directly via SQL,
	// not just trusting the create response's own RETURNING clause.
	var storedDesignation string
	var storedNote *string
	if err := pool.QueryRow(ctx,
		`SELECT data_handling_designation, data_handling_note FROM gateway_tools WHERE id = $1`, created.ID,
	).Scan(&storedDesignation, &storedNote); err != nil {
		t.Fatalf("read back gateway_tools row: %v", err)
	}
	if storedDesignation != "zero_retention" {
		t.Errorf("stored data_handling_designation = %q, want zero_retention", storedDesignation)
	}
	if storedNote == nil || *storedNote != "Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum" {
		t.Errorf("stored data_handling_note = %v, want the submitted citation", storedNote)
	}

	// Edit: flip the designation and clear the note.
	patchResp := doJSON(http.MethodPatch, "/v1/gateway/tools/"+created.ID.String(), map[string]any{
		"data_handling_designation": "standard_retention",
		"data_handling_note":        "",
	})
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(patchResp.Body)
		t.Fatalf("patch: status = %d, want 200: %s", patchResp.StatusCode, errBody.String())
	}
	var updated api.ToolResp
	if err := json.NewDecoder(patchResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.DataHandlingDesignation != "standard_retention" {
		t.Errorf("updated DataHandlingDesignation = %q, want standard_retention", updated.DataHandlingDesignation)
	}
	// A non-nil pointer to "" -- json's omitempty on *string only omits a
	// nil pointer, not an empty-string value behind a real pointer, so
	// this correctly round-trips "the note was explicitly cleared" rather
	// than looking identical to "no note was ever set" (nil).
	if updated.DataHandlingNote == nil || *updated.DataHandlingNote != "" {
		t.Errorf("updated DataHandlingNote = %v, want a non-nil pointer to \"\"", updated.DataHandlingNote)
	}

	if err := pool.QueryRow(ctx,
		`SELECT data_handling_designation, data_handling_note FROM gateway_tools WHERE id = $1`, created.ID,
	).Scan(&storedDesignation, &storedNote); err != nil {
		t.Fatalf("re-read gateway_tools row after patch: %v", err)
	}
	if storedDesignation != "standard_retention" {
		t.Errorf("post-patch stored data_handling_designation = %q, want standard_retention", storedDesignation)
	}
	if storedNote == nil || *storedNote != "" {
		t.Errorf("post-patch stored data_handling_note = %v, want a real empty string (cleared, not NULL/unchanged)", storedNote)
	}
}

// TestDataHandling_RealDB_NewConnector_DefaultsToUnknown proves the
// fail-safe default holds through the real DB DEFAULT, not just Go-side
// logic -- creating a connector with no designation submitted at all
// still returns "unknown" from the real column default.
func TestDataHandling_RealDB_NewConnector_DefaultsToUnknown(t *testing.T) {
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
	orgID := seedTestOrg(t, ctx, pool, "dh-default")
	userID := seedTestUser(t, ctx, pool, orgID)

	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := api.NewServer(q, authSvc, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@dh-default.test", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name": "dh-default-connector", "type": "ai_provider", "auth_type": "api_key", "provider": "claude",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/gateway/tools", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/gateway/tools: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created api.ToolResp
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.DataHandlingDesignation != "unknown" {
		t.Errorf("DataHandlingDesignation = %q, want unknown (real DB DEFAULT, not just Go-side)", created.DataHandlingDesignation)
	}

	var storedDesignation string
	if err := pool.QueryRow(ctx, `SELECT data_handling_designation FROM gateway_tools WHERE id = $1`, created.ID).Scan(&storedDesignation); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if storedDesignation != "unknown" {
		t.Errorf("stored data_handling_designation = %q, want unknown", storedDesignation)
	}
}
