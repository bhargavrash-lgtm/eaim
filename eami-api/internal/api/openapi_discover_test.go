// openapi_discover_test.go -- eami-api/internal/api
// Handler-level tests for B-075's POST /v1/gateway/openapi/discover:
// role gating, request validation, the real request/response wire shape,
// and the HTTP-layer body-size bound (added by this brief's own mandatory
// security review -- see openapi_discover.go's DiscoverOpenAPI doc comment).
package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/openapidiscovery"
)

const tinyValidSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/x": {
      "get": {
        "operationId": "getX",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func TestDiscoverOpenAPI_AdminRole_ParsesRealSpec(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", token,
		map[string]string{"spec_content": tinyValidSpec})
	body := mustDecode(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 -- body: %v", resp.StatusCode, body)
	}
	actions, _ := body["actions"].(map[string]interface{})
	if _, ok := actions["getX"]; !ok {
		t.Errorf("expected action \"getX\" in response, got %v", body)
	}
}

func TestDiscoverOpenAPI_ViewerRole_Forbidden(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	token := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", token,
		map[string]string{"spec_content": tinyValidSpec})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer role: status = %d, want 403", resp.StatusCode)
	}
}

func TestDiscoverOpenAPI_NoAuth_Unauthorized(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)

	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", "",
		map[string]string{"spec_content": tinyValidSpec})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no auth: status = %d, want 401", resp.StatusCode)
	}
}

func TestDiscoverOpenAPI_NeitherURLNorContent_Rejected(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", token, map[string]string{})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("neither field set: status = %d, want 400", resp.StatusCode)
	}
}

func TestDiscoverOpenAPI_BothURLAndContent_Rejected(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", token,
		map[string]string{"spec_url": "https://example.com/spec.json", "spec_content": tinyValidSpec})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("both fields set: status = %d, want 400", resp.StatusCode)
	}
}

// TestDiscoverOpenAPI_OversizedSpecContent_RejectedAtHTTPLayer proves the
// security-review fix: a spec_content body larger than the bound is
// rejected by http.MaxBytesReader before openapidiscovery.Parse's own
// (later, in-memory) size check would even run -- closing the gap where
// an arbitrarily large body was fully read into memory before any bound
// was checked.
func TestDiscoverOpenAPI_OversizedSpecContent_RejectedAtHTTPLayer(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")

	huge := strings.Repeat("a", openapidiscovery.MaxSpecBytes*2+1024)
	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", token,
		map[string]string{"spec_content": huge})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized spec_content: status = %d, want 400", resp.StatusCode)
	}
}

func TestDiscoverOpenAPI_MalformedSpec_Returns400NotDuplicate(t *testing.T) {
	ms := api.NewMockStore()
	ts := newTestServer(t, ms)
	token := ts.bearerToken(t, adminUserID, agentTestOrgID, "admin")

	resp := ts.do(t, http.MethodPost, "/v1/gateway/openapi/discover", token,
		map[string]string{"spec_content": "not a valid spec { [ garbage"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed spec: status = %d, want 400", resp.StatusCode)
	}
}
