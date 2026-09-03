// issue_http_b116_b118_pg_test.go -- eami-gateway/internal/identity
//
// Integration tests for B-116 (JWT claims bound to DB values, not trusted
// from client input) and B-118 (per-agent rate limiting on
// POST /v1/gateway/tokens), against a REAL Postgres. Reuses
// issue_http_pg_test.go's fixtures (identityTestEnv, issueTestAPIKeyReal,
// newIssueHandlerForTest, newIssueTestMux) rather than duplicating them.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/identity/... -run 'TestIssueHandler_(ForgedClaims|RateLimit)' -v
package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─── B-116: signed claims reflect the DB record, not client input ─────────

// TestIssueHandler_ForgedClaims_OverwrittenFromDB proves the fix for
// BACKLOG.md's B-116: a request that supplies Scope/Model/Owner/RiskTier/
// TTLSeconds values different from the resolved agent's real gateway_agents
// row gets back a token whose claims (and expiry) carry the DB's values,
// not the ones it sent. newIdentityTestEnv's fixture agent is seeded with
// model="claude-sonnet-5", owner="test@example.com", scope="read:test",
// risk_tier defaulting to "low", and token_ttl_seconds defaulting to 900
// (schema.sql's column defaults) -- all deliberately different from what
// this test's request body claims. TTLSeconds coverage was added after this
// brief's own mandatory review passes independently found it was the one
// claim still client-controlled that Manager.Validate actually enforces
// (via exp) -- see issue_http.go's B-116 comment for the full reasoning.
func TestIssueHandler_ForgedClaims_OverwrittenFromDB(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, m := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)

	body, err := json.Marshal(IssueRequest{
		AgentID:    "agent:identity-token-test-agent",
		Scope:      "forged:admin-scope",
		Task:       "legitimate per-request task string",
		Model:      "forged-model-claim",
		Owner:      "attacker@evil.example",
		RiskTier:   "high",
		TTLSeconds: 14400, // max allowed by Manager.Issue's own clamp -- must NOT win over the DB's 900s default
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/tokens", strings.NewReader(string(body)))
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q, want 200", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("issued token failed to validate: %v", err)
	}

	// The identity-bearing fields must reflect the DB record, not the
	// forged request body.
	if claims.Scope != "read:test" {
		t.Errorf("Scope = %q, want %q (the DB value, not the forged %q)", claims.Scope, "read:test", "forged:admin-scope")
	}
	if claims.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want %q (the DB value, not the forged %q)", claims.Model, "claude-sonnet-5", "forged-model-claim")
	}
	// exp - iat must equal the DB's token_ttl_seconds default (900s), not
	// the forged 14400s the request asked for -- this is the claim
	// Manager.Validate actually enforces, unlike Scope/Model/Owner/RiskTier.
	gotTTL := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if wantTTL := 900 * time.Second; gotTTL != wantTTL {
		t.Errorf("token TTL = %v, want %v (the DB's token_ttl_seconds default, not the forged 14400s)", gotTTL, wantTTL)
	}
	if claims.Owner != "test@example.com" {
		t.Errorf("Owner = %q, want %q (the DB value, not the forged %q)", claims.Owner, "test@example.com", "attacker@evil.example")
	}
	if claims.RiskTier != "low" {
		t.Errorf("RiskTier = %q, want %q (the DB default, not the forged %q)", claims.RiskTier, "low", "high")
	}
	// Task has no gateway_agents column to rebuild from -- deliberately
	// left client-supplied (see issue_http.go's B-116 comment).
	if claims.Task != "legitimate per-request task string" {
		t.Errorf("Task = %q, want the client-supplied value preserved unchanged", claims.Task)
	}
}

// ─── B-118: per-agent rate limiting ────────────────────────────────────────

// TestIssueHandler_RateLimit_TripsAfterLimit_PerAgent proves
// testTokenIssuePerAgentLimit (20 requests / testTokenIssuePerAgentWindow) is
// enforced: the (limit+1)th request from the same agent within the window is rejected
// with 429 and a real Retry-After header, while the first `limit` requests
// all succeed.
func TestIssueHandler_RateLimit_TripsAfterLimit_PerAgent(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)

	for i := 0; i < testTokenIssuePerAgentLimit; i++ {
		req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, body = %q, want 200 (within limit)", i+1, w.Code, w.Body.String())
		}
	}

	// The (limit+1)th request in the same window must be rejected.
	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d: status = %d, body = %q, want 429 (over limit)", testTokenIssuePerAgentLimit+1, w.Code, w.Body.String())
	}
	retryAfter := w.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected a Retry-After header on the 429 response")
	}
	if secs, err := strconv.Atoi(retryAfter); err != nil || secs < 1 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", retryAfter)
	}
}

// TestIssueHandler_RateLimit_BucketIsolatedPerAgent proves the limiter keys
// on the resolved agent's registry UUID, not something global to the
// handler or the API key: a second, different agent (bound to its own key)
// is unaffected by the first agent having exhausted its own bucket.
func TestIssueHandler_RateLimit_BucketIsolatedPerAgent(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)
	for i := 0; i < testTokenIssuePerAgentLimit; i++ {
		req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("agent 1, request %d: status = %d, want 200", i+1, w.Code)
		}
	}
	// Confirm agent 1 is now actually over its own limit.
	exhaustedReq := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	exhaustedW := httptest.NewRecorder()
	mux.ServeHTTP(exhaustedW, exhaustedReq)
	if exhaustedW.Code != http.StatusTooManyRequests {
		t.Fatalf("agent 1 should be over its limit: status = %d, want 429", exhaustedW.Code)
	}

	otherAgentID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, otherAgentID, env.orgID, "issue-http-ratelimit-test-other-agent", "claude-sonnet-5", "test@example.com", "read:test"); err != nil {
		t.Fatalf("insert second test agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, otherAgentID)
	})
	otherKey := issueTestAPIKeyReal(t, env, env.orgID, otherAgentID)

	otherReq := issueRequestJSON(t, otherKey, "agent:issue-http-ratelimit-test-other-agent")
	otherW := httptest.NewRecorder()
	mux.ServeHTTP(otherW, otherReq)
	if otherW.Code != http.StatusOK {
		t.Fatalf("a different agent must have its own rate-limit bucket: status = %d, body = %q, want 200", otherW.Code, otherW.Body.String())
	}
}
