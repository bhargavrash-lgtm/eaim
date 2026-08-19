// ingest_test.go -- eami-collector/internal/api
// Tests for B-073's core contract: a per-agent credential cannot be used to
// submit a report (or fetch config) claiming to be a DIFFERENT agent. This
// is the actual impersonation fix -- per-agent credentials alone are
// cosmetic without this check, since the report body's agent_id was never
// previously verified against anything.
package api_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eami/collector/internal/api"
)

func newIngestServer(t *testing.T, sqlDB *sql.DB, staticKey string) *httptest.Server {
	t.Helper()
	router := api.Router(sqlDB, staticKey, "", "", testLogger())
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func ingestReport(t *testing.T, srv *httptest.Server, apiKey, agentID, hostname string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"agent_id":     agentID,
		"hostname":     hostname,
		"collected_at": time.Now().UTC().Format(time.RFC3339),
	})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/ingest: %v", err)
	}
	return resp
}

// TestIngest_TwoDistinctAgents_TwoDistinctCredentials proves AC1: two real,
// distinct agents, each with its own credential, both report successfully.
func TestIngest_TwoDistinctAgents_TwoDistinctCredentials(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey agent-a: %v", err)
	}
	if err := api.RegisterKey(sqlDB, "key-agent-b", "agent-b", "Agent B"); err != nil {
		t.Fatalf("RegisterKey agent-b: %v", err)
	}
	srv := newIngestServer(t, sqlDB, "")

	respA := ingestReport(t, srv, "key-agent-a", "agent-a", "host-a")
	defer respA.Body.Close()
	if respA.StatusCode != http.StatusAccepted {
		t.Errorf("agent-a self-reporting: status = %d, want 202", respA.StatusCode)
	}

	respB := ingestReport(t, srv, "key-agent-b", "agent-b", "host-b")
	defer respB.Body.Close()
	if respB.StatusCode != http.StatusAccepted {
		t.Errorf("agent-b self-reporting: status = %d, want 202", respB.StatusCode)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM report_buffer`).Scan(&count); err != nil {
		t.Fatalf("count report_buffer: %v", err)
	}
	if count != 2 {
		t.Errorf("report_buffer rows = %d, want 2", count)
	}
}

// TestIngest_AgentACredential_CannotClaimToBeAgentB proves AC2: the
// central contract of this brief. Agent A's real, valid credential is used
// to submit a report whose agent_id claims to be Agent B -- must be
// rejected, and nothing must land in report_buffer under either identity.
func TestIngest_AgentACredential_CannotClaimToBeAgentB(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey agent-a: %v", err)
	}
	if err := api.RegisterKey(sqlDB, "key-agent-b", "agent-b", "Agent B"); err != nil {
		t.Fatalf("RegisterKey agent-b: %v", err)
	}
	srv := newIngestServer(t, sqlDB, "")

	resp := ingestReport(t, srv, "key-agent-a", "agent-b", "host-b-impersonated")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("agent-a's key claiming to be agent-b: status = %d, want 403", resp.StatusCode)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM report_buffer`).Scan(&count); err != nil {
		t.Fatalf("count report_buffer: %v", err)
	}
	if count != 0 {
		t.Errorf("report_buffer rows after a rejected impersonation attempt = %d, want 0 (no partial/silent write)", count)
	}
}

// TestIngest_RevokedCredential_RejectedOnNextAttempt proves AC5: a
// revoked credential is cleanly rejected on its very next use, not just
// theoretically revocable.
func TestIngest_RevokedCredential_RejectedOnNextAttempt(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}
	srv := newIngestServer(t, sqlDB, "")

	// Confirm it works before revocation.
	respBefore := ingestReport(t, srv, "key-agent-a", "agent-a", "host-a")
	respBefore.Body.Close()
	if respBefore.StatusCode != http.StatusAccepted {
		t.Fatalf("pre-revocation report: status = %d, want 202", respBefore.StatusCode)
	}

	if _, err := api.RevokeKey(sqlDB, "agent-a"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	respAfter := ingestReport(t, srv, "key-agent-a", "agent-a", "host-a")
	defer respAfter.Body.Close()
	if respAfter.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-revocation report: status = %d, want 401", respAfter.StatusCode)
	}
}

// TestIngest_StaticKeyStillAccepted_NoRegressionForUnenrolledFlows proves
// AC4/contract "no regression to existing report ingestion for
// already-enrolled agents": a deployment still using the legacy static key
// (e.g. paste-detection/endpoint-discovery flows that haven't been migrated
// to per-agent keys) is completely unaffected by B-073.
func TestIngest_StaticKeyStillAccepted_NoRegressionForUnenrolledFlows(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	srv := newIngestServer(t, sqlDB, "the-static-fleet-key")

	// Any agent_id is accepted under the static key -- identical to
	// pre-B-073 behavior, since the static key carries no bound identity
	// to check a mismatch against.
	resp := ingestReport(t, srv, "the-static-fleet-key", "whatever-agent-id", "some-host")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("static-key report: status = %d, want 202 (unaffected by B-073)", resp.StatusCode)
	}
}

// TestConfigProxy_AgentACredential_CannotFetchAgentBConfig proves the same
// identity-binding contract applies to GET /v1/agent-config/{agent_id}, not
// just ingest -- an agent could otherwise read another agent's remote
// config by naming it in the URL path.
func TestConfigProxy_AgentACredential_CannotFetchAgentBConfig(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}
	srv := newIngestServer(t, sqlDB, "")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/agent-config/agent-b", nil)
	req.Header.Set("X-API-Key", "key-agent-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/agent-config/agent-b: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("agent-a's key fetching agent-b's config: status = %d, want 403", resp.StatusCode)
	}
}

// TestConfigProxy_OwnAgentID_NotRejectedByIdentityCheck confirms the
// identity check doesn't false-positive-reject an agent fetching its own
// config -- it should proceed past the identity check and hit the
// saas_url-not-configured 503 (this test's server has no saasURL), not a
// 403.
func TestConfigProxy_OwnAgentID_NotRejectedByIdentityCheck(t *testing.T) {
	sqlDB := openTestCollectorDB(t)
	if err := api.RegisterKey(sqlDB, "key-agent-a", "agent-a", "Agent A"); err != nil {
		t.Fatalf("RegisterKey: %v", err)
	}
	srv := newIngestServer(t, sqlDB, "")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/agent-config/agent-a", nil)
	req.Header.Set("X-API-Key", "key-agent-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/agent-config/agent-a: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Error("agent-a fetching its own config: got 403, want past the identity check (503 saas_url not configured)")
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (no saas_url configured in this test server)", resp.StatusCode)
	}
}
