// config_proxy_upstream_test.go -- eami-collector/internal/api
//
// B-165: TestConfigProxy_AgentACredential_CannotFetchAgentBConfig and
// TestConfigProxy_OwnAgentID_NotRejectedByIdentityCheck (ingest_test.go)
// are the only two existing tests for GET /v1/agent-config/{agent_id} --
// both build their test server with NO saasURL configured, so both stop
// at ConfigProxyHandler's own "saas_url not configured" 503 branch and
// never once exercise the actual proxied HTTP call this handler exists to
// make. That gap is exactly why the real proxy target
// (saasURL+"/v1/agents/"+agentID+"/config") being a URL that had never
// existed anywhere in eami-api's router went uncaught since this code was
// written (see BACKLOG.md's B-165).
//
// These tests close that gap: a real httptest.Server stands in for
// eami-api, and assertions run against what it actually received (method,
// path, X-Service-Key header) -- not just what the collector claims to
// have sent -- proving the full proxy round trip, not merely the
// pre-proxy identity check the existing two tests already covered.
package api_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eami/collector/internal/api"
)

func newIngestServerWithUpstream(t *testing.T, sqlDB *sql.DB, staticKey, saasURL, serviceKey string) *httptest.Server {
	t.Helper()
	router := api.Router(sqlDB, staticKey, saasURL, serviceKey, testLogger())
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// TestConfigProxy_RealUpstreamCall_PathAndHeaderCorrect proves the actual
// proxied request -- not just the collector's own pre-proxy identity
// check -- reaches a real HTTP server at the exact path/method eami-agent's
// FetchConfig and eami-api's real router both expect
// (GET /v1/agents/{agent_id}/config), carrying the configured
// X-Service-Key. This is the literal request eami-api's router had no
// route for until B-165; this test proves the collector's OWN half of
// that contract was always correct.
func TestConfigProxy_RealUpstreamCall_PathAndHeaderCorrect(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}

	var gotPath, gotMethod, gotServiceKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotServiceKey = r.Header.Get("X-Service-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"scan_interval_seconds":120,"model_scan_paths":["/custom"],"max_report_size_bytes":1048576,"enabled_scanners":["models"]}`))
	}))
	t.Cleanup(upstream.Close)

	srv := newIngestServerWithUpstream(t, sqlDB, "", upstream.URL, "the-real-service-key")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/agent-config/agent-a", nil)
	req.Header.Set("X-API-Key", "key-agent-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/agent-config/agent-a: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (proxied straight through from the real upstream)", resp.StatusCode)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("upstream received method %q, want GET", gotMethod)
	}
	if gotPath != "/v1/agents/agent-a/config" {
		t.Errorf("upstream received path %q, want /v1/agents/agent-a/config -- the exact route B-165 had to create in eami-api", gotPath)
	}
	if gotServiceKey != "the-real-service-key" {
		t.Errorf("upstream received X-Service-Key = %q, want the configured service key", gotServiceKey)
	}
}

// TestConfigProxy_RealUpstream404_PassedThroughVerbatim proves the "not
// registered yet" contract survives a genuine round trip: when the real
// upstream returns 404 (an unlinked/unregistered agent, per B-165's own
// AgentRemoteConfig), the collector passes that 404 straight through
// rather than masking it as a 200 or a 500.
func TestConfigProxy_RealUpstream404_PassedThroughVerbatim(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(upstream.Close)

	srv := newIngestServerWithUpstream(t, sqlDB, "", upstream.URL, "the-real-service-key")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/agent-config/agent-a", nil)
	req.Header.Set("X-API-Key", "key-agent-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/agent-config/agent-a: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (passed through from the real upstream)", resp.StatusCode)
	}
}
