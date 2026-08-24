// revoke_http_pg_test.go -- eami-gateway/internal/identity
//
// Integration tests for RevokeHandler (B-042) against a REAL Postgres and a
// real internal/registry.Registry, following the same pattern established
// by tokens_pg_test.go (B-041) and internal/approval/router_pg_test.go
// (B-039): TEST_DATABASE_URL / POSTGRES_PASSWORD fallback, throwaway
// orgs/gateway_agents fixtures cleaned up via t.Cleanup.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/identity/... -run TestRevokeHandler -v
package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/registry"
)

func newRevokeTestMux(h *RevokeHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateway/tokens/{jti}/revoke", h.HandleRevoke)
	return mux
}

func revokeRequestJSON(t *testing.T, jti, serviceKey, agentName, orgID string) *http.Request {
	t.Helper()
	body, err := json.Marshal(revokeRequest{AgentName: agentName, OrgID: orgID})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/tokens/"+jti+"/revoke", strings.NewReader(string(body)))
	if serviceKey != "" {
		req.Header.Set("X-Service-Key", serviceKey)
	}
	return req
}

// TestRevokeHandler_HappyPath_RevokesRealToken proves the full real path:
// a real token issued via Manager.Issue, revoked via the real HTTP route
// (real service key, real (agent_name, org_id) resolved via a real
// registry.Registry against a real gateway_agents row), and rejected by a
// subsequent Validate.
func TestRevokeHandler_HappyPath_RevokesRealToken(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}

	resp, err := m.Issue(IssueRequest{AgentID: "agent:identity-token-test-agent", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revoke Validate: %v", err)
	}

	reg := registry.New(env.pool)
	handler := NewRevokeHandler(m, reg, "the-real-service-key", NewPostgresTokenEventStore(env.pool))
	mux := newRevokeTestMux(handler)

	req := revokeRequestJSON(t, claims.ID, "the-real-service-key", "identity-token-test-agent", env.orgID.String())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("HandleRevoke: status = %d, body = %q, want 204", w.Code, w.Body.String())
	}

	if _, err := m.Validate(resp.Token); err == nil {
		t.Fatal("expected error validating a token revoked via the real HTTP route, got nil")
	}
}

// TestRevokeHandler_WrongServiceKey_Returns401 proves an unauthorized
// caller (AC2) is rejected before any revocation happens.
func TestRevokeHandler_WrongServiceKey_Returns401(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	resp, err := m.Issue(IssueRequest{AgentID: "agent:identity-token-test-agent", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revoke Validate: %v", err)
	}

	reg := registry.New(env.pool)
	handler := NewRevokeHandler(m, reg, "the-real-service-key", NewPostgresTokenEventStore(env.pool))
	mux := newRevokeTestMux(handler)

	for _, tc := range []struct {
		name string
		key  string
	}{
		{"wrong key", "not-the-real-key"},
		{"missing key", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := revokeRequestJSON(t, claims.ID, tc.key, "identity-token-test-agent", env.orgID.String())
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}

	// The token must still be valid -- neither attempt should have revoked it.
	if _, err := m.Validate(resp.Token); err != nil {
		t.Errorf("token was revoked despite an unauthorized request: %v", err)
	}
}

// TestRevokeHandler_UnknownAgent_Returns403 proves a request naming an
// agent that doesn't exist in gateway_agents is rejected -- closing B-042's
// gap 2 (unvalidated agent reference) for this endpoint.
func TestRevokeHandler_UnknownAgent_Returns403(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	resp, err := m.Issue(IssueRequest{AgentID: "agent:identity-token-test-agent", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revoke Validate: %v", err)
	}

	reg := registry.New(env.pool)
	handler := NewRevokeHandler(m, reg, "the-real-service-key", NewPostgresTokenEventStore(env.pool))
	mux := newRevokeTestMux(handler)

	req := revokeRequestJSON(t, claims.ID, "the-real-service-key", "an-agent-name-that-does-not-exist", env.orgID.String())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q, want 403", w.Code, w.Body.String())
	}

	if _, err := m.Validate(resp.Token); err != nil {
		t.Errorf("token was revoked despite an unresolvable agent_name: %v", err)
	}
}

// TestRevokeHandler_CrossOrgAgentName_Returns403 proves the org-scoping fix:
// a real agent named "identity-token-test-agent" exists (in env's org), but
// a request claiming a DIFFERENT, unrelated org_id must not resolve it --
// otherwise a caller from one org could revoke another org's
// identically-named agent's token. This is the exact gap found during this
// task's own review and fixed via registry.LookupByNameAndOrg, not part of
// the originally stated scope -- see BACKLOG.md B-042.
func TestRevokeHandler_CrossOrgAgentName_Returns403(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	resp, err := m.Issue(IssueRequest{AgentID: "agent:identity-token-test-agent", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revoke Validate: %v", err)
	}

	// A second, unrelated org -- the real agent above does NOT belong to it.
	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(),
		`INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "identity-token-test-other-org-"+otherOrgID.String()[:8], "identity-token-test-other-org-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other-org fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)
	})

	reg := registry.New(env.pool)
	handler := NewRevokeHandler(m, reg, "the-real-service-key", NewPostgresTokenEventStore(env.pool))
	mux := newRevokeTestMux(handler)

	// Same real agent_name, but claiming the wrong org.
	req := revokeRequestJSON(t, claims.ID, "the-real-service-key", "identity-token-test-agent", otherOrgID.String())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q, want 403 (cross-org agent_name must not resolve)", w.Code, w.Body.String())
	}

	if _, err := m.Validate(resp.Token); err != nil {
		t.Errorf("token was revoked despite a cross-org agent_name/org_id mismatch: %v", err)
	}
}

// fakeResolver returns a fixed, fabricated AgentRecord for every lookup --
// used only to inject an agentID that resolves successfully at the handler
// level but does not correspond to any real gateway_agents row, so the
// downstream Postgres INSERT genuinely violates the NOT NULL FK. This is
// exactly the fault injection AgentResolver's interface exists to enable
// (see revoke_http.go's doc comment) -- the real registry.Registry can
// never itself return a nonexistent agent, since it only returns rows it
// found.
type fakeResolver struct {
	rec *registry.AgentRecord
}

func (f *fakeResolver) LookupByNameAndOrg(_ context.Context, _, _ string) (*registry.AgentRecord, error) {
	return f.rec, nil
}

// TestRevokeHandler_PersistenceFailure_Returns500NotSilent204 proves
// B-042's gap 2 fix: a genuine DB persistence failure (here, a real FK
// violation from a fabricated, nonexistent agent_id) surfaces as a real
// error response, not a silent 204 the caller would wrongly read as
// "revoked."
func TestRevokeHandler_PersistenceFailure_Returns500NotSilent204(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	resp, err := m.Issue(IssueRequest{AgentID: "agent:identity-token-test-agent", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("pre-revoke Validate: %v", err)
	}

	fake := &fakeResolver{rec: &registry.AgentRecord{
		ID:     uuid.New().String(), // does not exist in gateway_agents
		OrgID:  env.orgID.String(),
		Name:   "ghost-agent",
		Status: "active",
	}}
	handler := NewRevokeHandler(m, fake, "the-real-service-key", NewPostgresTokenEventStore(env.pool))
	mux := newRevokeTestMux(handler)

	req := revokeRequestJSON(t, claims.ID, "the-real-service-key", "ghost-agent", env.orgID.String())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %q, want 500", w.Code, w.Body.String())
	}

	// The in-memory revocation still took effect (Revoke() sets it before
	// attempting to persist) -- this process itself still rejects the
	// token immediately, even though the 500 correctly told the caller
	// persistence failed and it may not survive a restart.
	if _, err := m.Validate(resp.Token); err == nil {
		t.Error("expected the token to still be rejected in-memory despite the persistence failure")
	}
}
