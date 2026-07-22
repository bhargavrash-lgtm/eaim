// gateway_episodes_test.go -- eami-api/internal/api
// Tests for the gateway episode proxy (B-002 Brief 2). The centerpiece is
// TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled:
// eami-gateway's service-key auth path (Brief 1) enforces zero org
// authorization on its own -- this proxy is the only place that ever will,
// and this test is the proof.
//
// Run: go test -count=1 ./internal/api/... -run TestGatewayEpisodes
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/google/uuid"
)

// fakeGatewayClient is a hand-rolled api.GatewayEpisodeClient double.
// Records what it was called with (the assertion surface for every
// org-isolation test) and how many times.
type fakeGatewayClient struct {
	listCalls int
	gotOrgID  uuid.UUID
	gotLimit  int
	gotOffset int

	listResult api.GatewayListResult
	listErr    error
	getResult  *api.GatewayEpisode
	getErr     error
}

func (f *fakeGatewayClient) ListEpisodes(_ context.Context, orgID uuid.UUID, _ string, limit, offset int) (api.GatewayListResult, error) {
	f.listCalls++
	f.gotOrgID, f.gotLimit, f.gotOffset = orgID, limit, offset
	return f.listResult, f.listErr
}

func (f *fakeGatewayClient) SearchEpisodes(_ context.Context, orgID uuid.UUID, _ string) ([]api.GatewayEpisode, error) {
	f.gotOrgID = orgID
	return f.listResult.Episodes, f.listErr
}

func (f *fakeGatewayClient) GetEpisode(_ context.Context, orgID, _ uuid.UUID) (*api.GatewayEpisode, error) {
	f.gotOrgID = orgID
	return f.getResult, f.getErr
}

// newGatewayTestServer builds a testServer (agents_test.go's helper type,
// same package) backed by a *fakeGatewayClient instead of a real HTTP call.
// newTestServer itself doesn't expose the underlying *api.Server needed for
// WithGatewayClient, so this constructs one directly -- everything else
// (testServer, bearerToken, do, mustDecode, org/user fixtures) comes
// straight from agents_test.go, unmodified.
func newGatewayTestServer(t *testing.T, gw *fakeGatewayClient) *testServer {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	h := api.NewHandler(api.NewMockStore(), authSvc).WithGatewayClient(gw)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return &testServer{srv: srv, authSvc: authSvc}
}

func TestGatewayEpisodes_List_ValidRequest_ReturnsData(t *testing.T) {
	gw := &fakeGatewayClient{listResult: api.GatewayListResult{
		Episodes: []api.GatewayEpisode{{ID: uuid.New(), OrgID: agentTestOrgID, Steps: json.RawMessage(`[{"tool_name":"salesforce"}]`)}},
		Total:    1,
	}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes", tok, nil)
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

func TestGatewayEpisodes_List_SameOrgIDSupplied_Succeeds(t *testing.T) {
	gw := &fakeGatewayClient{}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes?org_id="+agentTestOrgID.String(), tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gw.listCalls != 1 {
		t.Errorf("gateway called %d times, want 1", gw.listCalls)
	}
}

// TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled
// is the centerpiece test of this brief: a user authenticated for
// agentTestOrgID cannot retrieve otherOrgID's episode content through this
// proxy, even by supplying otherOrgID's org_id directly in the request.
func TestGatewayEpisodes_List_MismatchedOrgIDSupplied_Returns403_GatewayNeverCalled(t *testing.T) {
	gw := &fakeGatewayClient{listResult: api.GatewayListResult{
		Episodes: []api.GatewayEpisode{{ID: uuid.New(), OrgID: otherOrgID}},
		Total:    1,
	}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes?org_id="+otherOrgID.String(), tok, nil)
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

func TestGatewayEpisodes_Get_CrossOrgEpisodeID_ReturnsGatewayNotFoundAs404(t *testing.T) {
	// Simulates real Brief 1 behavior: an episode ID belonging to a different
	// org than the one eami-api sent (which is always the session's own org)
	// comes back from the gateway as "not found" -- indistinguishable from a
	// genuinely missing id, avoiding an existence oracle.
	gw := &fakeGatewayClient{getErr: &api.GatewayError{StatusCode: http.StatusNotFound}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes/"+uuid.New().String(), tok, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["code"] != "not_found" {
		t.Errorf("error code = %v, want %q", body["code"], "not_found")
	}
}

func TestGatewayEpisodes_List_PaginationTranslatesToLimitOffset(t *testing.T) {
	gw := &fakeGatewayClient{}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes?page=3&per_page=10", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gw.gotLimit != 10 {
		t.Errorf("gotLimit = %d, want 10 (per_page)", gw.gotLimit)
	}
	if gw.gotOffset != 20 {
		t.Errorf("gotOffset = %d, want 20 ((page-1)*per_page = (3-1)*10)", gw.gotOffset)
	}
}

// TestGatewayEpisodes_List_GatewayReturns401_SurfacesAs502JSON proves
// gateway's raw error status/body never reaches the caller verbatim -- a
// 401 from the gateway (eami-api's own credential problem, not the end
// user's) becomes a generic JSON 502, not a misleading "unauthorized".
func TestGatewayEpisodes_List_GatewayReturns401_SurfacesAs502JSON(t *testing.T) {
	gw := &fakeGatewayClient{listErr: &api.GatewayError{StatusCode: http.StatusUnauthorized}}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes", tok, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (must not leak gateway's plain-text body)", ct)
	}
	body := mustDecode(t, resp)
	if body["code"] != "upstream_error" {
		t.Errorf("error code = %v, want %q", body["code"], "upstream_error")
	}
}

// TestGatewayEpisodes_List_GatewayUnreachable_Returns502_NoFallback covers a
// network-level failure (not an HTTP error response at all). The handler
// has no code path back to s.queries/s.storeIface -- it only ever calls
// s.gatewayClient -- so there is structurally no direct-DB fallback to
// regress; this asserts the caller-facing contract (clean 502, no partial
// data) holds for that failure mode too.
func TestGatewayEpisodes_List_GatewayUnreachable_Returns502_NoFallback(t *testing.T) {
	gw := &fakeGatewayClient{listErr: context.DeadlineExceeded}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes", tok, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if _, ok := body["data"]; ok {
		t.Error("502 response unexpectedly contains a data field -- must not return partial data")
	}
}

func TestGatewayEpisodes_Search_EmptyQuery_Returns400(t *testing.T) {
	gw := &fakeGatewayClient{}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes/search", tok, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	// Asserting the error code, not just the status: GetGatewayEpisode's
	// UUID-parse failure also returns 400, so a status-only check would
	// still pass if /search were ever accidentally shadowed by
	// /{episodeId} (e.g. episodeId="search") -- this distinguishes correct
	// routing from that failure mode.
	body := mustDecode(t, resp)
	if body["code"] != "bad_request" {
		t.Errorf("error code = %v, want %q", body["code"], "bad_request")
	}
	if body["message"] != "q is required" {
		t.Errorf("error message = %v, want %q", body["message"], "q is required")
	}
}

// TestGatewayEpisodes_List_GatewayNotConfigured_Returns502 covers
// gatewayNotConfigured directly. newGatewayTestServer's WithGatewayClient
// always backfills placeholder Gateway.URL/EpisodeReadServiceKey values
// specifically so the fake client is reachable in every other test -- this
// test deliberately builds a server WITHOUT that call, so cfg.Gateway stays
// empty and the request must fail closed (502) before ever touching
// s.gatewayClient (which is nil on a NewHandler-only server, and would
// panic if reached -- reaching 502 instead is itself proof the guard runs
// first).
func TestGatewayEpisodes_List_GatewayNotConfigured_Returns502(t *testing.T) {
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	h := api.NewHandler(api.NewMockStore(), authSvc) // no WithGatewayClient
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, authSvc: authSvc}
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/gateway/episodes", tok, nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body := mustDecode(t, resp)
	if body["code"] != "upstream_error" {
		t.Errorf("error code = %v, want %q", body["code"], "upstream_error")
	}
}

// TestMemoryEpisodes_RouteStillRegistered_NotShadowedByNewRoutes guards
// against the realistic risk this brief introduces: the new
// /v1/gateway/episodes* routes were added right next to the existing
// /v1/memory/episodes* ones in router.go, so a copy-paste mistake could
// have overwritten or shadowed the old registration. memory.go itself is
// untouched (zero lines changed -- verifiable via diff), and no test
// exercises its handlers today (they call the real s.queries, nil in
// NewHandler-built test servers, a pre-existing gap outside this brief's
// scope) -- so this only proves the route still exists and dispatches to a
// real handler (any status but 404), not that the handler's own behavior
// is unchanged (nothing could prove that without a real DB, before or
// after this brief).
func TestMemoryEpisodes_RouteStillRegistered_NotShadowedByNewRoutes(t *testing.T) {
	gw := &fakeGatewayClient{}
	ts := newGatewayTestServer(t, gw)
	tok := ts.bearerToken(t, viewerUserID, agentTestOrgID, "viewer")

	resp := ts.do(t, http.MethodGet, "/v1/memory/episodes", tok, nil)
	if resp.StatusCode == http.StatusNotFound {
		t.Error("status = 404 -- /v1/memory/episodes route registration appears to have been removed or shadowed")
	}
}
