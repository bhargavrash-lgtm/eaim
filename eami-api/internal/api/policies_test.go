// policies_test.go — eami-api/internal/api
// QA-EAMI — handler-level unit tests for /v1/gateway/policies.
//
// Tests: empty list, validation (priority, action), cross-org blocking, reorder.
//
// Run: go test -count=1 ./internal/api/... -run TestPolicy

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/eami/api/internal/api"
	"github.com/google/uuid"
)

// ─── fixtures ────────────────────────────────────────────────────────────────

var (
	policyTestOrgID  = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	policyOtherOrgID = uuid.MustParse("dddddddd-0000-0000-0000-000000000001")
	policyAdminID    = uuid.MustParse("cccccccc-0000-0000-0000-000000000010")
	policyViewerID   = uuid.MustParse("cccccccc-0000-0000-0000-000000000011")
)

func validPolicyPayload() map[string]interface{} {
	return map[string]interface{}{
		"name":     "deny-delete-production",
		"priority": 1,
		"conditions": map[string]interface{}{
			"action_types": []string{"delete"},
			"environments": []string{"production"},
		},
		"action": "deny",
		"status": "active",
	}
}

// seedPolicy adds a policy to the mock store with the given org and returns its ID.
func seedPolicy(ms *api.MockStore, orgID uuid.UUID, name string, priority int32) uuid.UUID {
	id := uuid.New()
	ms.SeedPolicy(api.StorePolicy{
		ID:       id,
		OrgID:    orgID,
		Name:     name,
		Priority: priority,
		Action:   "deny",
		Status:   "active",
	})
	return id
}

// ─── GET /v1/gateway/policies ────────────────────────────────────────────────

func TestListPolicies_RequiresAuth(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	resp := ts.do(t, http.MethodGet, "/v1/gateway/policies", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 with no token, got %d", resp.StatusCode)
	}
}

func TestListPolicies_Empty(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet, "/v1/gateway/policies", token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %v", resp.StatusCode, body)
	}

	data, ok := body["data"].([]interface{})
	if !ok {
		t.Fatalf("response must contain 'data' array, got: %v", body)
	}
	if len(data) != 0 {
		t.Errorf("want empty data array, got %d elements", len(data))
	}
}

func TestListPolicies_ReturnsOnlyOwnOrg(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	seedPolicy(ms, policyTestOrgID, "mine", 1)
	seedPolicy(ms, policyOtherOrgID, "theirs", 1)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet, "/v1/gateway/policies", token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	data := body["data"].([]interface{})
	if len(data) != 1 {
		t.Errorf("want 1 policy (own org only), got %d", len(data))
	}
}

func TestListPolicies_OrderedByPriority(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	// Seed policies out of order.
	seedPolicy(ms, policyTestOrgID, "third", 3)
	seedPolicy(ms, policyTestOrgID, "first", 1)
	seedPolicy(ms, policyTestOrgID, "second", 2)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet, "/v1/gateway/policies", token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	data := body["data"].([]interface{})
	if len(data) != 3 {
		t.Fatalf("want 3 policies, got %d", len(data))
	}

	// Check ascending priority order.
	for i, item := range data {
		p := item.(map[string]interface{})
		gotPriority := int(p["priority"].(float64))
		wantPriority := i + 1
		if gotPriority != wantPriority {
			t.Errorf("policies[%d]: want priority %d, got %d", i, wantPriority, gotPriority)
		}
	}
}

// ─── POST /v1/gateway/policies ───────────────────────────────────────────────

func TestCreatePolicy_Success(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, validPolicyPayload())
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200/201, got %d — body: %v", resp.StatusCode, body)
	}
	if id, _ := body["id"].(string); id == "" {
		t.Error("created policy must have an id")
	}
	if ms.CreatePolicyCalls != 1 {
		t.Errorf("CreatePolicy should have been called once, called %d times", ms.CreatePolicyCalls)
	}
}

func TestCreatePolicy_PriorityZero_Invalid(t *testing.T) {
	// Priority minimum is 1 per OpenAPI spec.
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := validPolicyPayload()
	payload["priority"] = 0

	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("priority=0 must yield 400, got %d", resp.StatusCode)
	}
	if ms.CreatePolicyCalls != 0 {
		t.Error("CreatePolicy must not be called when validation fails")
	}
}

func TestCreatePolicy_NegativePriority_Invalid(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := validPolicyPayload()
	payload["priority"] = -5

	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative priority must yield 400, got %d", resp.StatusCode)
	}
}

func TestCreatePolicy_InvalidAction(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := validPolicyPayload()
	payload["action"] = "block" // not a valid enum: must be allow|deny|escalate

	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid action must yield 400, got %d", resp.StatusCode)
	}
}

func TestCreatePolicy_MissingName(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := validPolicyPayload()
	delete(payload, "name")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing name must yield 400, got %d", resp.StatusCode)
	}
}

func TestCreatePolicy_MissingConditions(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := validPolicyPayload()
	delete(payload, "conditions")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing conditions must yield 400, got %d", resp.StatusCode)
	}
}

func TestCreatePolicy_ViewerRole_Forbidden(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyViewerID, policyTestOrgID, "viewer")
	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, validPolicyPayload())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer must get 403 on policy create, got %d", resp.StatusCode)
	}
}

// ─── GET /v1/gateway/policies/{id} ───────────────────────────────────────────

func TestGetPolicy_NotFound(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet,
		fmt.Sprintf("/v1/gateway/policies/%s", uuid.New()),
		token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for non-existent policy, got %d", resp.StatusCode)
	}
}

func TestGetPolicy_CrossOrg_NotFound(t *testing.T) {
	// Policy belongs to policyOtherOrgID — should appear as 404 to policyTestOrgID user.
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	id := seedPolicy(ms, policyOtherOrgID, "other-org-policy", 1)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet,
		fmt.Sprintf("/v1/gateway/policies/%s", id),
		token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-org policy get must be 404 or 403, got %d", resp.StatusCode)
	}
}

// ─── DELETE /v1/gateway/policies/{id} ────────────────────────────────────────

func TestDeletePolicy_NotFound(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodDelete,
		fmt.Sprintf("/v1/gateway/policies/%s", uuid.New()),
		token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for non-existent policy, got %d", resp.StatusCode)
	}
}

// ─── PUT /v1/gateway/policies/reorder ────────────────────────────────────────

func TestReorderPolicies_Success(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	id1 := seedPolicy(ms, policyTestOrgID, "policy-a", 1)
	id2 := seedPolicy(ms, policyTestOrgID, "policy-b", 2)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := map[string]interface{}{
		"policy_ids": []string{id2.String(), id1.String()}, // swap order
	}
	resp := ts.do(t, http.MethodPut, "/v1/gateway/policies/reorder", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 200/204 on reorder, got %d", resp.StatusCode)
	}
	if ms.ReorderCalls != 1 {
		t.Errorf("ReorderPolicies should be called once, called %d times", ms.ReorderCalls)
	}
}

func TestReorderPolicies_CrossOrgPolicy_Rejected(t *testing.T) {
	// Trying to reorder a policy from a different org should fail.
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	myID := seedPolicy(ms, policyTestOrgID, "mine", 1)
	otherID := seedPolicy(ms, policyOtherOrgID, "theirs", 1)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := map[string]interface{}{
		"policy_ids": []string{myID.String(), otherID.String()},
	}
	resp := ts.do(t, http.MethodPut, "/v1/gateway/policies/reorder", token, payload)
	defer resp.Body.Close()

	// Should be 404 or 403 — not 200.
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		t.Fatalf("reorder including cross-org policy ID must not succeed, got %d", resp.StatusCode)
	}
}

func TestReorderPolicies_EmptyList_BadRequest(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	payload := map[string]interface{}{
		"policy_ids": []string{},
	}
	resp := ts.do(t, http.MethodPut, "/v1/gateway/policies/reorder", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty policy_ids must yield 400, got %d", resp.StatusCode)
	}
}

// ─── Response shape invariants ───────────────────────────────────────────────

func TestCreatePolicy_ResponseShape(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")
	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, validPolicyPayload())
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200/201, got %d", resp.StatusCode)
	}

	// Verify required fields per OpenAPI Policy schema.
	requiredFields := []string{"id", "name", "priority", "conditions", "action", "status", "created_at"}
	for _, f := range requiredFields {
		if _, ok := body[f]; !ok {
			t.Errorf("response missing required field %q", f)
		}
	}

	// Priority must be ≥ 1.
	if priority, ok := body["priority"].(float64); !ok || priority < 1 {
		t.Errorf("response priority must be ≥ 1, got %v", body["priority"])
	}

	// Action must be one of allow|deny|escalate.
	validActions := map[string]bool{"allow": true, "deny": true, "escalate": true}
	if action, _ := body["action"].(string); !validActions[action] {
		t.Errorf("response action must be allow|deny|escalate, got %q", action)
	}

	// id must be a valid UUID string.
	if idStr, _ := body["id"].(string); idStr == "" {
		t.Error("id must be non-empty")
	} else if _, err := uuid.Parse(idStr); err != nil {
		t.Errorf("id must be a valid UUID, got %q: %v", idStr, err)
	}

	// Verify the policy was actually persisted by listing.
	listResp := ts.do(t, http.MethodGet, "/v1/gateway/policies", token, nil)
	listBody := mustDecode(t, listResp)
	data := listBody["data"].([]interface{})
	if len(data) != 1 {
		t.Errorf("after create, list should return 1 policy, got %d", len(data))
	}
}

// ─── Error response shape ────────────────────────────────────────────────────

func TestAPIErrors_HaveCodeAndMessage(t *testing.T) {
	// All 4xx errors must return JSON with "code" and "message" fields.
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, policyAdminID, policyTestOrgID, "admin")

	// Trigger a 400.
	payload := validPolicyPayload()
	payload["priority"] = 0
	resp := ts.do(t, http.MethodPost, "/v1/gateway/policies", token, payload)

	var errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatalf("400 response must be valid JSON: %v", err)
	}
	if errBody.Code == "" {
		t.Error("error response must have a non-empty 'code' field")
	}
	if errBody.Message == "" {
		t.Error("error response must have a non-empty 'message' field")
	}
}
