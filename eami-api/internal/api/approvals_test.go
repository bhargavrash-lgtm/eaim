// approvals_test.go — eami-api/internal/api
// QA-EAMI — handler-level unit tests for /v1/approvals.
//
// Tests: create, get, list (filter + cross-org), decide (approve/deny/double/invalid).
// Note: these tests will 500 until TASK-059 lands (approvals.go dual-path via storeIface).
//
// Run: go test -count=1 ./internal/api/... -run TestApproval

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/eami/api/internal/api"
	"github.com/google/uuid"
)

// ─── shared fixtures ─────────────────────────────────────────────────────────

var (
	approvalOrgID   = uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	approvalOtherOrg = uuid.MustParse("dddddddd-0000-0000-0000-000000000001")

	approvalAdminID    = uuid.MustParse("cccccccc-0000-0000-0000-000000000010")
	approvalOperatorID = uuid.MustParse("cccccccc-0000-0000-0000-000000000011")
	approvalViewerID   = uuid.MustParse("cccccccc-0000-0000-0000-000000000012")
	approvalAgentID    = uuid.MustParse("cccccccc-0000-0000-0000-000000000020")
)

func validApprovalPayload() map[string]interface{} {
	return map[string]interface{}{
		"agent_id":            approvalAgentID.String(),
		"agent_name":          "test-agent",
		"tool_name":           "database",
		"action":              "delete_records",
		"justification":       "Cleaning up test data from the staging environment",
		"risk_level":          "high",
		"expires_in_seconds":  300,
		"gateway_session_id":  "sess-001",
		"gateway_node_address": "10.0.0.1:8080",
	}
}

func newApprovalTestServer(t *testing.T) (*testServer, *api.MockStore) {
	t.Helper()
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	return ts, ms
}

// seedPendingApproval adds a pending approval owned by approvalOrgID.
func seedPendingApproval(ms *api.MockStore) api.StoreApproval {
	a := api.StoreApproval{
		ID:            uuid.New(),
		OrgID:         approvalOrgID,
		AgentID:       approvalAgentID,
		AgentName:     "test-agent",
		ToolName:      "database",
		Action:        "delete_records",
		Justification: "Test justification",
		RiskLevel:     "high",
		Status:        "pending",
		ExpiresAt:     time.Now().UTC().Add(5 * time.Minute),
		CreatedAt:     time.Now().UTC(),
	}
	ms.SeedApproval(a)
	return a
}

// ─── POST /v1/approvals ───────────────────────────────────────────────────────

func TestCreateApproval_Success(t *testing.T) {
	ts, ms := newApprovalTestServer(t)

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	resp := ts.do(t, http.MethodPost, "/v1/approvals", token, validApprovalPayload())
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200/201, got %d — body: %v", resp.StatusCode, body)
	}
	if id, _ := body["id"].(string); id == "" {
		t.Error("created approval must have an id in the response")
	}
	if ms.CreateApprovalCalls != 1 {
		t.Errorf("CreateApproval should be called once, called %d times", ms.CreateApprovalCalls)
	}
}

func TestCreateApproval_ViewerForbidden(t *testing.T) {
	ts, ms := newApprovalTestServer(t)

	token := ts.bearerToken(t, approvalViewerID, approvalOrgID, "viewer")
	resp := ts.do(t, http.MethodPost, "/v1/approvals", token, validApprovalPayload())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer must get 403 on create, got %d", resp.StatusCode)
	}
	if ms.CreateApprovalCalls != 0 {
		t.Error("CreateApproval must not be called when viewer is rejected")
	}
}

// ─── GET /v1/approvals/{approvalId} ─────────────────────────────────────────

func TestGetApproval_Success(t *testing.T) {
	ts, ms := newApprovalTestServer(t)
	a := seedPendingApproval(ms)

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	resp := ts.do(t, http.MethodGet,
		fmt.Sprintf("/v1/approvals/%s", a.ID),
		token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %v", resp.StatusCode, body)
	}
	if status, _ := body["status"].(string); status != "pending" {
		t.Errorf("want status=pending, got %q", status)
	}
}

// ─── GET /v1/approvals ────────────────────────────────────────────────────────

func TestListApprovals_RequiresAuth(t *testing.T) {
	ts, _ := newApprovalTestServer(t)

	resp := ts.do(t, http.MethodGet, "/v1/approvals", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 with no token, got %d", resp.StatusCode)
	}
}

func TestListApprovals_FilterByStatus(t *testing.T) {
	ts, ms := newApprovalTestServer(t)

	// Seed: 2 pending for our org, 1 approved for our org, 1 pending for other org.
	seedPendingApproval(ms)
	seedPendingApproval(ms)
	ms.SeedApproval(api.StoreApproval{
		ID: uuid.New(), OrgID: approvalOrgID, AgentID: approvalAgentID,
		AgentName: "test-agent", ToolName: "db", Action: "read",
		Justification: "j", RiskLevel: "low",
		Status: "approved", ExpiresAt: time.Now().Add(time.Minute), CreatedAt: time.Now(),
	})
	ms.SeedApproval(api.StoreApproval{
		ID: uuid.New(), OrgID: approvalOtherOrg, AgentID: approvalAgentID,
		AgentName: "other-agent", ToolName: "db", Action: "read",
		Justification: "j", RiskLevel: "low",
		Status: "pending", ExpiresAt: time.Now().Add(time.Minute), CreatedAt: time.Now(),
	})

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	resp := ts.do(t, http.MethodGet, "/v1/approvals?status=pending", token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %v", resp.StatusCode, body)
	}
	data, _ := body["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("want 2 pending approvals for own org, got %d", len(data))
	}
}

// ─── POST /v1/approvals/{approvalId}/decide ──────────────────────────────────

func TestDecideApproval_Approve(t *testing.T) {
	ts, ms := newApprovalTestServer(t)
	a := seedPendingApproval(ms)

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	payload := map[string]interface{}{
		"decision":   "approved",
		"decided_by": "admin@example.com",
	}
	resp := ts.do(t, http.MethodPost,
		fmt.Sprintf("/v1/approvals/%s/decide", a.ID),
		token, payload)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %v", resp.StatusCode, body)
	}
	if status, _ := body["status"].(string); status != "approved" {
		t.Errorf("want status=approved, got %q", status)
	}
	if ms.DecideApprovalCalls != 1 {
		t.Errorf("DecideApproval should be called once, called %d times", ms.DecideApprovalCalls)
	}
}

func TestDecideApproval_Deny(t *testing.T) {
	ts, ms := newApprovalTestServer(t)
	a := seedPendingApproval(ms)

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	payload := map[string]interface{}{
		"decision":   "denied",
		"decided_by": "admin@example.com",
		"reason":     "Not authorized for production data",
	}
	resp := ts.do(t, http.MethodPost,
		fmt.Sprintf("/v1/approvals/%s/decide", a.ID),
		token, payload)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %v", resp.StatusCode, body)
	}
	if status, _ := body["status"].(string); status != "denied" {
		t.Errorf("want status=denied, got %q", status)
	}
}

func TestDecideApproval_DoubleDecide(t *testing.T) {
	ts, ms := newApprovalTestServer(t)
	a := seedPendingApproval(ms)

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	payload := map[string]interface{}{
		"decision":   "approved",
		"decided_by": "admin@example.com",
	}
	path := fmt.Sprintf("/v1/approvals/%s/decide", a.ID)

	// First decision — should succeed.
	resp1 := ts.do(t, http.MethodPost, path, token, payload)
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first decide: want 200, got %d", resp1.StatusCode)
	}

	// Second decision on same approval — must 409.
	resp2 := ts.do(t, http.MethodPost, path, token, payload)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("double decide: want 409, got %d", resp2.StatusCode)
	}
}

func TestDecideApproval_InvalidDecision(t *testing.T) {
	ts, ms := newApprovalTestServer(t)
	a := seedPendingApproval(ms)

	token := ts.bearerToken(t, approvalAdminID, approvalOrgID, "admin")
	payload := map[string]interface{}{
		"decision":   "maybe",
		"decided_by": "admin@example.com",
	}
	resp := ts.do(t, http.MethodPost,
		fmt.Sprintf("/v1/approvals/%s/decide", a.ID),
		token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid decision: want 400, got %d", resp.StatusCode)
	}
	if ms.DecideApprovalCalls != 0 {
		t.Error("DecideApproval must not be called when decision is invalid")
	}
}
