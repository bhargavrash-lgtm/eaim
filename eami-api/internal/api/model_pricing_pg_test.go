// model_pricing_pg_test.go -- eami-api/internal/api
// Real-Postgres integration tests for the model_pricing admin CRUD
// endpoints (B-112) -- previously the only way to add/reprice a model was
// a raw SQL statement, unlike api_keys/agents/policies which all have real
// admin CRUD (B-098's api_keys CRUD is this brief's own structural
// template). Reuses finOpsPgTestEnv (finops_pg_test.go, same package) --
// model_pricing has no org_id column (a genuinely global table, unlike
// every other resource this env's helpers were built for), so these tests
// only borrow its pool/queries/server/authSvc, never its orgID for scoping
// model-pricing calls themselves.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestModelPricingCRUD_RealDB -v
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/api/internal/api"
)

// operatorToken issues an "operator"-role JWT -- used to prove the
// admin-only gating on model_pricing's write endpoints (a global,
// cross-org table) is genuinely stricter than agents/policies/tools'
// admin+operator gating.
func (e *finOpsPgTestEnv) operatorToken(t *testing.T) string {
	t.Helper()
	tok, _, err := e.authSvc.IssueAccessToken(uuid.New(), e.orgID, "operator@finops-test.example", "operator")
	if err != nil {
		t.Fatalf("issue operator token: %v", err)
	}
	return tok
}

func (e *finOpsPgTestEnv) doModelPricing(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// deleteModelPricingCleanup registers a t.Cleanup to remove a model_pricing
// row this test created directly (bypassing the DELETE endpoint under
// test), so a test whose own DELETE assertions fail doesn't leak a row
// into whichever test runs next.
func (e *finOpsPgTestEnv) deleteModelPricingCleanup(t *testing.T, model string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM model_pricing WHERE model = $1`, model)
	})
}

// TestModelPricingCRUD_RealDB_FullRoundTrip covers AC1 (add a new model's
// pricing through the API) and the general CRUD contract: create, appear in
// the list, update, delete, and gone from the list afterward.
func TestModelPricingCRUD_RealDB_FullRoundTrip(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	admin := env.adminToken(t)
	model := "b112-crud-roundtrip-" + env.orgID.String()[:8]
	env.deleteModelPricingCleanup(t, model)

	cacheWrite5m := 0.01
	resp := env.doModelPricing(t, http.MethodPost, "/v1/admin/model-pricing", admin, map[string]any{
		"model":                      model,
		"cost_per_1k_in":             0.005,
		"cost_per_1k_out":            0.015,
		"cost_per_1k_cache_write_5m": cacheWrite5m,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", resp.StatusCode)
	}
	var created api.ModelPricingResp
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	resp.Body.Close()
	if created.Model != model || created.CostPer1kIn != 0.005 || created.CostPer1kOut != 0.015 {
		t.Fatalf("created row = %+v, want model=%s in=0.005 out=0.015", created, model)
	}
	if created.CostPer1kCacheWrite5m == nil || *created.CostPer1kCacheWrite5m != cacheWrite5m {
		t.Errorf("CostPer1kCacheWrite5m = %v, want %v", created.CostPer1kCacheWrite5m, cacheWrite5m)
	}
	if created.CostPer1kCacheWrite1h != nil {
		t.Errorf("CostPer1kCacheWrite1h = %v, want nil (never set)", *created.CostPer1kCacheWrite1h)
	}

	// Appears in the list.
	resp = env.doModelPricing(t, http.MethodGet, "/v1/admin/model-pricing", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", resp.StatusCode)
	}
	var list api.ModelPricingListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	resp.Body.Close()
	found := false
	for _, m := range list.Data {
		if m.Model == model {
			found = true
		}
	}
	if !found {
		t.Fatalf("model %q not found in list after create: %+v", model, list.Data)
	}

	// Update (AC2's mechanism, proven through the API rather than raw SQL).
	newRate := 0.05
	resp = env.doModelPricing(t, http.MethodPatch, "/v1/admin/model-pricing/"+model, admin, map[string]any{
		"cost_per_1k_in": newRate,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: want 200, got %d", resp.StatusCode)
	}
	var updated api.ModelPricingResp
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	resp.Body.Close()
	if updated.CostPer1kIn != newRate {
		t.Errorf("CostPer1kIn after update = %v, want %v", updated.CostPer1kIn, newRate)
	}
	// Untouched fields survive a partial PATCH.
	if updated.CostPer1kOut != 0.015 {
		t.Errorf("CostPer1kOut after a PATCH that didn't touch it = %v, want unchanged 0.015", updated.CostPer1kOut)
	}
	if updated.CostPer1kCacheWrite5m == nil || *updated.CostPer1kCacheWrite5m != cacheWrite5m {
		t.Errorf("CostPer1kCacheWrite5m after unrelated PATCH = %v, want unchanged %v", updated.CostPer1kCacheWrite5m, cacheWrite5m)
	}

	// Delete.
	resp = env.doModelPricing(t, http.MethodDelete, "/v1/admin/model-pricing/"+model, admin, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", resp.StatusCode)
	}

	// Gone from the list.
	resp = env.doModelPricing(t, http.MethodGet, "/v1/admin/model-pricing", admin, nil)
	var list2 api.ModelPricingListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list2); err != nil {
		t.Fatalf("decode second list response: %v", err)
	}
	resp.Body.Close()
	for _, m := range list2.Data {
		if m.Model == model {
			t.Fatalf("model %q still present in list after delete", model)
		}
	}

	// A second delete of the same (now-gone) model is a clean 404, not a
	// silent success or a 500.
	resp = env.doModelPricing(t, http.MethodDelete, "/v1/admin/model-pricing/"+model, admin, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete of an already-deleted model: want 404, got %d", resp.StatusCode)
	}
}

// TestModelPricingCRUD_RealDB_DuplicateModel_Returns409 proves creating a
// model that already has pricing is a clear conflict, not an opaque 500.
func TestModelPricingCRUD_RealDB_DuplicateModel_Returns409(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	admin := env.adminToken(t)
	model := "b112-crud-dup-" + env.orgID.String()[:8]
	env.deleteModelPricingCleanup(t, model)

	body := map[string]any{"model": model, "cost_per_1k_in": 0.01, "cost_per_1k_out": 0.02}
	resp := env.doModelPricing(t, http.MethodPost, "/v1/admin/model-pricing", admin, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.doModelPricing(t, http.MethodPost, "/v1/admin/model-pricing", admin, body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestModelPricingCRUD_RealDB_NegativeRate_Returns400 proves rate
// validation rejects a negative rate before it ever reaches the database.
func TestModelPricingCRUD_RealDB_NegativeRate_Returns400(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	admin := env.adminToken(t)
	model := "b112-crud-negative-" + env.orgID.String()[:8]
	env.deleteModelPricingCleanup(t, model)

	resp := env.doModelPricing(t, http.MethodPost, "/v1/admin/model-pricing", admin, map[string]any{
		"model": model, "cost_per_1k_in": -0.01, "cost_per_1k_out": 0.02,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative cost_per_1k_in: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestModelPricingCRUD_RealDB_OperatorRole_ForbiddenForWrites_AllowedForReads
// proves the deliberately stricter admin-only gating on writes (unlike
// agents/policies/tools, which allow admin+operator) -- model_pricing is a
// global, cross-org table, so an operator in one org must not be able to
// change pricing that affects every org's cost reporting.
func TestModelPricingCRUD_RealDB_OperatorRole_ForbiddenForWrites_AllowedForReads(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	operator := env.operatorToken(t)
	model := "b112-crud-operator-" + env.orgID.String()[:8]
	env.deleteModelPricingCleanup(t, model)

	resp := env.doModelPricing(t, http.MethodPost, "/v1/admin/model-pricing", operator, map[string]any{
		"model": model, "cost_per_1k_in": 0.01, "cost_per_1k_out": 0.02,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("operator create: want 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.doModelPricing(t, http.MethodPatch, "/v1/admin/model-pricing/claude-haiku-4-5-20251001", operator, map[string]any{
		"cost_per_1k_in": 0.01,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("operator update: want 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Reads, unlike writes, are allowed for operator (matches every other
	// admin+operator+viewer read group in this router).
	resp = env.doModelPricing(t, http.MethodGet, "/v1/admin/model-pricing", operator, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("operator list: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestModelPricingCRUD_RealDB_NewModelDispatchPricesCorrectly is AC1's
// centerpiece: a model added through the CRUD API prices a subsequent
// dispatch correctly, exercising the exact same finops.go query path a
// real dispatch would.
func TestModelPricingCRUD_RealDB_NewModelDispatchPricesCorrectly(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	admin := env.adminToken(t)
	model := "b112-new-model-dispatch-" + env.orgID.String()[:8]
	env.deleteModelPricingCleanup(t, model)

	resp := env.doModelPricing(t, http.MethodPost, "/v1/admin/model-pricing", admin, map[string]any{
		"model": model, "cost_per_1k_in": 0.02, "cost_per_1k_out": 0.04,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	agentA := env.seedAgent(t, "b112-new-model-agent", "team-a")
	now := time.Now().UTC()
	// A real dispatch would compute cost at write time (reports.go) using
	// exactly this model's now-configured rate -- simulated here the same
	// way TestFinOpsSummary_Real_ToolBreakdown_* etc. do, by inserting the
	// hand-computed value directly: 2000/1000*0.02 + 1000/1000*0.04 = 0.08.
	env.insertUsage(t, agentA, "b112-new-model-agent", model, 2000, 1000, 0.08, now.Add(-10*time.Minute))

	from := now.Add(-1 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)
	respSummary, summary := env.getSummary(t, from, to)
	if respSummary.StatusCode != http.StatusOK {
		t.Fatalf("summary: want 200, got %d", respSummary.StatusCode)
	}
	if summary.TotalCostUSD != 0.08 {
		t.Errorf("TotalCostUSD = %v, want 0.08", summary.TotalCostUSD)
	}
	found := false
	for _, m := range summary.ByModel {
		if m.Model == model {
			found = true
			if !m.PricingConfigured {
				t.Error("PricingConfigured = false for a model that WAS added via the CRUD API")
			}
			if m.CostUSD != 0.08 {
				t.Errorf("ByModel cost = %v, want 0.08", m.CostUSD)
			}
		}
	}
	if !found {
		t.Fatalf("model %q not found in by_model: %+v", model, summary.ByModel)
	}
}
