// memory_test.go -- eami-api/internal/api
// Tests for the /v1/memory/episodes* routes after B-002 Brief 3's cutover:
// these are the actual frontend-facing routes (MemoryPage.tsx), now served
// by the same org-isolated gateway proxy handlers Brief 2 built (memory.go
// itself, and its old direct/unprotected episodes-table query, are gone).
//
// The centerpiece here is TestMemoryEpisodes_List_MismatchedOrgIDSupplied_
// Returns403_GatewayNeverCalled -- it re-proves Brief 2's org-isolation
// guarantee at the actual route the browser calls, not just at Brief 2's
// own /v1/gateway/episodes route in isolation. That's the whole point of
// this brief: one path to full episode content, and it's the protected one.
//
// Run: go test -count=1 ./internal/api/... -run TestMemoryEpisodes
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/eami/api/internal/api"
	"github.com/google/uuid"
)

func TestMemoryEpisodes_List_ValidRequest_ReturnsData(t *testing.T) {
	gw := &fakeGatewayClient{listResult: api.GatewayListResult{
		Episodes: []api.GatewayEpisode{{ID: uuid.New(), OrgID: agentTestOrgID, Steps: json.RawMessage(`[{"tool_name":"salesforce"}]`)}},
		Total:    1,
	}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Errorf("got %d episodes in response, want 1", len(data))
	}
	if gw.listCalls != 1 {
		t.Errorf("gateway ListEpisodes called %d times, want 1", gw.listCalls)
	}
	if gw.gotOrgID != agentTestOrgID {
		t.Errorf("gateway called with org_id = %s, want session org %s", gw.gotOrgID, agentTestOrgID)
	}
}

// TestMemoryEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled
// is the AC #3 test: proves the org-isolation guarantee holds at the real
// frontend-facing route (/v1/memory/episodes), not just at Brief 2's own
// /v1/gateway/episodes route.
func TestMemoryEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled(t *testing.T) {
	gw := &fakeGatewayClient{listResult: api.GatewayListResult{
		Episodes: []api.GatewayEpisode{{ID: uuid.New(), OrgID: otherOrgID}},
		Total:    1,
	}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes?org_id="+otherOrgID.String(), tok, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if gw.listCalls != 0 {
		t.Errorf("gateway ListEpisodes was called %d time(s) -- must be 0, the request should be rejected before ever reaching the gateway", gw.listCalls)
	}
	body := mustDecode(t, resp)
	if data, ok := body["data"]; ok {
		t.Errorf("403 response unexpectedly contains a data field: %v -- must not leak any episode content", data)
	}
}

func TestMemoryEpisodes_Search_ValidRequest_ReturnsData(t *testing.T) {
	gw := &fakeGatewayClient{listResult: api.GatewayListResult{
		Episodes: []api.GatewayEpisode{{ID: uuid.New(), OrgID: agentTestOrgID}},
	}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes/search?q=salesforce", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gw.gotOrgID != agentTestOrgID {
		t.Errorf("gateway called with org_id = %s, want session org %s", gw.gotOrgID, agentTestOrgID)
	}
}

func TestMemoryEpisodes_Search_MismatchedOrgIDSupplied_Returns403(t *testing.T) {
	gw := &fakeGatewayClient{}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes/search?q=x&org_id="+otherOrgID.String(), tok, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestMemoryEpisodesDetail_ValidRequest_ReturnsFullSteps exercises the new
// /v1/memory/episodes/{episodeId} route -- documented in api/openapi.yaml
// but never implemented until this brief. Proves full step content
// (tool calls, args, results) round-trips through the org-isolated path --
// the actual point of the whole B-002 effort.
func TestMemoryEpisodesDetail_ValidRequest_ReturnsFullSteps(t *testing.T) {
	steps := json.RawMessage(`[{"tool_name":"salesforce","action":"delete_records","params":{"id":"001"},"decision":"allowed"}]`)
	gw := &fakeGatewayClient{getResult: &api.GatewayEpisode{ID: uuid.New(), OrgID: agentTestOrgID, Steps: steps, Outcome: "success"}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes/"+uuid.New().String(), tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got api.GatewayEpisode
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Steps) == 0 {
		t.Fatal("response Steps is empty -- the regression check for the whole B-002 effort failed")
	}
	if gw.gotOrgID != agentTestOrgID {
		t.Errorf("gateway called with org_id = %s, want session org %s", gw.gotOrgID, agentTestOrgID)
	}
}

// TestMemoryEpisodesDetail_CrossOrgEpisodeID_ReturnsNotFound mirrors Brief
// 2's cross-org GetEpisode test, now at the real frontend-facing route.
func TestMemoryEpisodesDetail_CrossOrgEpisodeID_ReturnsNotFound(t *testing.T) {
	gw := &fakeGatewayClient{getErr: &api.GatewayError{StatusCode: http.StatusNotFound}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes/"+uuid.New().String(), tok, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestMemoryEpisodes_NoAuth_Returns401 confirms the route group's existing
// jwtMiddleware still gates these routes exactly as it did before the
// cutover -- no accidental auth weakening from the handler swap.
func TestMemoryEpisodes_NoAuth_Returns401(t *testing.T) {
	gw := &fakeGatewayClient{}
	ts := newGatewayTestServer(t, gw)

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if gw.listCalls != 0 {
		t.Errorf("gateway was called %d time(s) on an unauthenticated request -- must be 0", gw.listCalls)
	}
}
