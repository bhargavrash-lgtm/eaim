// issue_http_pg_test.go -- eami-gateway/internal/identity
//
// Integration tests for IssueHandler (B-098) against a REAL Postgres and a
// real internal/registry.Registry, proving POST /v1/gateway/tokens is
// actually gated by a real, scoped api_keys row -- following the same
// pattern established by revoke_http_pg_test.go (B-042).
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/identity/... -run TestIssueHandler -v
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/registry"
)

func newIssueTestMux(h *IssueHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/gateway/tokens", h.HandleIssue)
	return mux
}

type apiKeyOpts struct {
	revoked   bool
	expiresAt *time.Time
}

// issueTestAPIKeyReal inserts a real api_keys row directly (no eami-api
// dependency available from this module -- separate Go module/workspace
// entry, see BACKLOG.md B-098) and returns the raw key string a caller
// would present via X-API-Key. agentID may be uuid.Nil for an
// org-scoped-only (not agent-scoped) key.
func issueTestAPIKeyReal(t *testing.T, env *identityTestEnv, orgID, agentID uuid.UUID, mutate ...func(*apiKeyOpts)) string {
	t.Helper()
	opts := apiKeyOpts{}
	for _, m := range mutate {
		m(&opts)
	}

	raw := "eami_k_test_" + uuid.New().String()
	sum := sha256.Sum256([]byte(raw))
	hash := fmt.Sprintf("%x", sum)
	prefix := raw[:12]

	var agentArg any
	if agentID != uuid.Nil {
		agentArg = agentID
	}
	var expiresArg any
	if opts.expiresAt != nil {
		expiresArg = *opts.expiresAt
	}

	keyID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO api_keys (id, org_id, name, key_hash, prefix, agent_id, expires_at, revoked)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, keyID, orgID, "issue-http-test-key-"+keyID.String()[:8], hash, prefix, agentArg, expiresArg, opts.revoked); err != nil {
		t.Fatalf("insert test api_key: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM api_keys WHERE id = $1`, keyID)
	})
	return raw
}

func withRevoked() func(*apiKeyOpts) {
	return func(o *apiKeyOpts) { o.revoked = true }
}

func withExpiresAt(t time.Time) func(*apiKeyOpts) {
	return func(o *apiKeyOpts) { o.expiresAt = &t }
}

func issueRequestJSON(t *testing.T, apiKey, agentID string) *http.Request {
	t.Helper()
	body, err := json.Marshal(IssueRequest{AgentID: agentID, Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/tokens", strings.NewReader(string(body)))
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	return req
}

func newIssueHandlerForTest(t *testing.T, env *identityTestEnv) (*IssueHandler, *Manager) {
	t.Helper()
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	reg := registry.New(env.pool)
	h := NewIssueHandler(m, reg, NewPostgresAPIKeyValidator(env.pool), NewPostgresTokenEventStore(env.pool))
	return h, m
}

// ─── AC1: no API key, no token ─────────────────────────────────────────────

func TestIssueHandler_MissingAPIKey_Returns401_NoTokenIssued(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	req := issueRequestJSON(t, "", "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %q, want 401", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil && resp.Token != "" {
		t.Fatal("a token was issued despite a missing API key")
	}
}

func TestIssueHandler_UnknownAPIKey_Returns401(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	req := issueRequestJSON(t, "eami_k_this_key_was_never_created", "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %q, want 401", w.Code, w.Body.String())
	}
}

// ─── AC2: real key scoped to a DIFFERENT agent is rejected ────────────────

func TestIssueHandler_KeyScopedToDifferentAgent_Returns403(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	// A second, real agent in the SAME org as env.agentID.
	otherAgentID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, otherAgentID, env.orgID, "issue-http-test-other-agent", "claude-sonnet-5", "test@example.com", "read:test"); err != nil {
		t.Fatalf("insert second test agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, otherAgentID)
	})

	// Key is bound to otherAgentID, but the request asks for env.agentID's name.
	apiKey := issueTestAPIKeyReal(t, env, env.orgID, otherAgentID)

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q, want 403 (cross-agent scoping must be rejected)", w.Code, w.Body.String())
	}
}

// ─── AC3: real, correctly-scoped, unrevoked key succeeds ──────────────────

func TestIssueHandler_CorrectlyScopedKey_IssuesRealToken(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, m := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q, want 200", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("expected a real token, got empty")
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("issued token failed to validate: %v", err)
	}
	if claims.Subject != "agent:identity-token-test-agent" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "agent:identity-token-test-agent")
	}
}

// ─── AC4: a revoked key is rejected on its next use ────────────────────────

func TestIssueHandler_RevokedAPIKey_Returns401(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID, withRevoked())

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %q, want 401 (revoked key)", w.Code, w.Body.String())
	}
}

// ─── AC5: issuance is recorded in ai_token_events, live ───────────────────

func TestIssueHandler_SuccessfulIssue_RecordsAITokenEvent(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q, want 200", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.JTI == "" {
		t.Fatal("expected a non-empty JTI in the response")
	}

	var eventType, agentName string
	var gotAgentID uuid.UUID
	err := env.pool.QueryRow(context.Background(),
		`SELECT event_type, agent_id, agent_name FROM ai_token_events WHERE jti = $1`, resp.JTI).
		Scan(&eventType, &gotAgentID, &agentName)
	if err != nil {
		t.Fatalf("expected an ai_token_events row for jti %q, got error: %v", resp.JTI, err)
	}
	if eventType != "issued" {
		t.Errorf("event_type = %q, want %q", eventType, "issued")
	}
	if gotAgentID != env.agentID {
		t.Errorf("agent_id = %v, want %v", gotAgentID, env.agentID)
	}
	if agentName != "identity-token-test-agent" {
		t.Errorf("agent_name = %q, want %q", agentName, "identity-token-test-agent")
	}
}

// ─── AC6: expires_at is settable and enforced ──────────────────────────────

func TestIssueHandler_ExpiredAPIKey_Returns401(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID, withExpiresAt(time.Now().Add(-time.Hour)))

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %q, want 401 (expired key)", w.Code, w.Body.String())
	}
}

func TestIssueHandler_NotYetExpiredAPIKey_Succeeds(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID, withExpiresAt(time.Now().Add(time.Hour)))

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q, want 200 (not yet expired)", w.Code, w.Body.String())
	}
}

// ─── Additional coverage: an org-scoped-only key (agent_id NULL) ──────────

func TestIssueHandler_OrgScopedOnlyKey_Returns403(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, uuid.Nil)

	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q, want 403 (key not scoped to any agent)", w.Code, w.Body.String())
	}
}
