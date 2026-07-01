// agents_test.go — eami-api/internal/api
// QA-EAMI — handler-level unit tests for /v1/gateway/agents.
//
// Tests: auth requirement, role-based access, validation, 404 paths.
//
// Run: go test -count=1 ./internal/api/... -run TestAgent

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/google/uuid"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// testServer holds the httptest.Server and auth service, letting tests issue
// JWTs for specific users/roles without a real database.
type testServer struct {
	srv     *httptest.Server
	authSvc *auth.Service
}

// newTestServer creates a full test server with a mock store.
func newTestServer(t *testing.T, ms *api.MockStore) *testServer {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	h := api.NewHandler(ms, authSvc)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return &testServer{srv: srv, authSvc: authSvc}
}

// bearerToken issues a signed JWT for a user with the given role, org, and ID.
func (ts *testServer) bearerToken(t *testing.T, userID uuid.UUID, orgID uuid.UUID, role string) string {
	t.Helper()
	tok, _, err := ts.authSvc.IssueAccessToken(userID, orgID, "test@example.com", role)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// do sends an authenticated HTTP request and returns the response.
func (ts *testServer) do(t *testing.T, method, path, token string, body interface{}) *http.Response {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, ts.srv.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(b) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// mustDecode reads and decodes a JSON body.
func mustDecode(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		// Some responses (204, empty) may have no body — return nil.
		return nil
	}
	return m
}

// ─── shared fixtures ─────────────────────────────────────────────────────────

var (
	agentTestOrgID = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	otherOrgID     = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")

	adminUserID    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000010")
	operatorUserID = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000011")
	viewerUserID   = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000012")
)

// validAgentPayload is a fully populated AgentCreate request.
func validAgentPayload() map[string]interface{} {
	return map[string]interface{}{
		"name":              "claude-test-01",
		"model":             "claude-sonnet-4-6",
		"owner":             "qa-team",
		"scope":             "Run read-only queries against the CRM for ticket triage",
		"risk_tier":         "low",
		"token_ttl_seconds": 900,
	}
}

// ─── GET /v1/gateway/agents ───────────────────────────────────────────────────

func TestListAgents_RequiresAuth(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	// No Authorization header.
	resp := ts.do(t, http.MethodGet, "/v1/gateway/agents", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 when no token provided, got %d", resp.StatusCode)
	}
}

func TestListAgents_InvalidToken(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	resp := ts.do(t, http.MethodGet, "/v1/gateway/agents", "not.a.valid.jwt", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 for invalid JWT, got %d", resp.StatusCode)
	}
}

func TestListAgents_ValidToken_EmptyList(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet, "/v1/gateway/agents", token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %v", resp.StatusCode, body)
	}

	data, ok := body["data"].([]interface{})
	if !ok {
		t.Fatalf("response must have a 'data' array, got: %v", body)
	}
	if len(data) != 0 {
		t.Errorf("want empty data array, got %d elements", len(data))
	}
}

func TestListAgents_ReturnsOnlyOwnOrg(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	// Seed one agent for agentTestOrgID and one for otherOrgID.
	ms.SeedAgent(api.StoreAgent{
		ID: uuid.New(), OrgID: agentTestOrgID,
		Name: "mine", Model: "claude-sonnet-4-6",
		Owner: "qa", Scope: "s", RiskTier: "low", Status: "active",
	})
	ms.SeedAgent(api.StoreAgent{
		ID: uuid.New(), OrgID: otherOrgID,
		Name: "theirs", Model: "gpt-4o",
		Owner: "other", Scope: "s", RiskTier: "low", Status: "active",
	})

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	resp := ts.do(t, http.MethodGet, "/v1/gateway/agents", token, nil)
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	data := body["data"].([]interface{})
	if len(data) != 1 {
		t.Errorf("want exactly 1 agent (own org), got %d", len(data))
	}
}

// ─── POST /v1/gateway/agents ─────────────────────────────────────────────────

func TestCreateAgent_AdminRole_Succeeds(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", token, validAgentPayload())
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200/201, got %d — body: %v", resp.StatusCode, body)
	}
	if ms.CreateAgentCalls != 1 {
		t.Errorf("CreateAgent should have been called exactly once, called %d times", ms.CreateAgentCalls)
	}
	if id, _ := body["id"].(string); id == "" {
		t.Error("created agent must have an id in the response")
	}
}

func TestCreateAgent_OperatorRole_Succeeds(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, operatorUserID, agentTestOrgID, "operator")
	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", token, validAgentPayload())
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("operator role must be allowed to create agents, got 403")
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200/201 for operator, got %d", resp.StatusCode)
	}
}

func TestCreateAgent_ViewerRole_Forbidden(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")
	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", token, validAgentPayload())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer role must get 403 on create, got %d", resp.StatusCode)
	}
	if ms.CreateAgentCalls != 0 {
		t.Error("CreateAgent must not be called when viewer is rejected")
	}
}

func TestCreateAgent_MissingName(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	payload := validAgentPayload()
	delete(payload, "name")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing name must yield 400, got %d", resp.StatusCode)
	}
	if ms.CreateAgentCalls != 0 {
		t.Error("CreateAgent must not be called when validation fails")
	}
}

func TestCreateAgent_MissingModel(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	payload := validAgentPayload()
	delete(payload, "model")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing model must yield 400, got %d", resp.StatusCode)
	}
}

func TestCreateAgent_InvalidRiskTier(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	payload := validAgentPayload()
	payload["risk_tier"] = "extreme" // not a valid enum value

	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", token, payload)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid risk_tier must yield 400, got %d", resp.StatusCode)
	}
}

func TestCreateAgent_NoAuth(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	resp := ts.do(t, http.MethodPost, "/v1/gateway/agents", "", validAgentPayload())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 with no token, got %d", resp.StatusCode)
	}
}

// ─── DELETE /v1/gateway/agents/{id} ──────────────────────────────────────────

func TestDeleteAgent_NotFound(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	nonExistentID := uuid.New()
	resp := ts.do(t, http.MethodDelete,
		fmt.Sprintf("/v1/gateway/agents/%s", nonExistentID),
		token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for non-existent agent, got %d", resp.StatusCode)
	}
}

func TestDeleteAgent_Success(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	// Seed an agent that belongs to the authenticated org.
	agentID := uuid.New()
	ms.SeedAgent(api.StoreAgent{
		ID:    agentID,
		OrgID: agentTestOrgID,
		Name:  "to-delete",
		Model: "claude-sonnet-4-6",
		Owner: "qa", Scope: "s", RiskTier: "low", Status: "active",
	})

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	resp := ts.do(t, http.MethodDelete,
		fmt.Sprintf("/v1/gateway/agents/%s", agentID),
		token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200/204 on delete, got %d", resp.StatusCode)
	}
	if ms.DeleteAgentCalls != 1 {
		t.Errorf("DeleteAgent should have been called once, called %d times", ms.DeleteAgentCalls)
	}
}

func TestDeleteAgent_CrossOrgBlocked(t *testing.T) {
	// An agent owned by otherOrgID cannot be deleted by a user from agentTestOrgID.
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	agentID := uuid.New()
	ms.SeedAgent(api.StoreAgent{
		ID:    agentID,
		OrgID: otherOrgID, // different org
		Name:  "other-org-agent",
		Model: "gpt-4o",
		Owner: "other", Scope: "s", RiskTier: "low", Status: "active",
	})

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	resp := ts.do(t, http.MethodDelete,
		fmt.Sprintf("/v1/gateway/agents/%s", agentID),
		token, nil)
	defer resp.Body.Close()

	// Handler should return 404 (not 403) to avoid leaking resource existence.
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-org delete must yield 404 or 403, got %d", resp.StatusCode)
	}
}

func TestDeleteAgent_InvalidUUID(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")
	resp := ts.do(t, http.MethodDelete, "/v1/gateway/agents/not-a-uuid", token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("invalid UUID in path must yield 400 or 404, got %d", resp.StatusCode)
	}
}

func TestDeleteAgent_ViewerRole_Forbidden(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	agentID := uuid.New()
	ms.SeedAgent(api.StoreAgent{
		ID:    agentID,
		OrgID: agentTestOrgID,
		Name:  "target",
		Model: "claude-sonnet-4-6",
		Owner: "qa", Scope: "s", RiskTier: "low", Status: "active",
	})

	token := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")
	resp := ts.do(t, http.MethodDelete,
		fmt.Sprintf("/v1/gateway/agents/%s", agentID),
		token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer DELETE agent: got %d, want 403 Forbidden", resp.StatusCode)
	}
}
