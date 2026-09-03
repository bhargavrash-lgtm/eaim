// issue_http_pg_test.go -- eami-gateway/internal/identity
//
// Integration tests for IssueHandler (B-098) against a REAL Postgres,
// proving POST /v1/gateway/tokens is actually gated by a real, scoped
// api_keys row -- following the same pattern established by
// revoke_http_pg_test.go (B-042). Since B-107, IssueHandler resolves the
// target agent via APIKeyValidator.ValidateAndResolveAgent's own combined
// query rather than a separate internal/registry.Registry lookup -- these
// tests exercise the real pgAPIKeyValidator directly, no registry import
// needed here anymore (registry.Registry itself is untouched, still used
// unmodified by revoke_http_pg_test.go).
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
	"github.com/jackc/pgx/v5/pgxpool"
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

// testTokenIssuePerAgentLimit/testTokenIssuePerAgentWindow (B-119) are the
// values newIssueHandlerForTest constructs its per-agent limiter with --
// byte-for-byte B-118's original hardcoded tokenIssueRateLimit/
// tokenIssueRateLimitWindow constants, so every existing B-098/116/118 test
// built on this helper keeps observing exactly the same behavior (AC3: zero
// regression), now arriving via explicit constructor args instead of a
// package-level constant.
const (
	testTokenIssuePerAgentLimit  = 20
	testTokenIssuePerAgentWindow = 60 * time.Second
	// testTokenIssuePreAuthMaxConcurrent is deliberately generous -- these
	// tests exercise B-098/116/118's authenticated per-agent behavior, not
	// B-120's pre-auth concurrency gate, and every request in a given test
	// runs sequentially anyway (net/http/httptest's ServeHTTP is called
	// synchronously here), so even a tight gate would never actually
	// engage -- generous purely for clarity of intent.
	// issue_http_preauth_pg_test.go/issue_http_preauth_test.go exercise the
	// pre-auth gate directly, with their own tightly-configured handlers.
	testTokenIssuePreAuthMaxConcurrent = 1000
)

func newIssueHandlerForTest(t *testing.T, env *identityTestEnv) (*IssueHandler, *Manager) {
	t.Helper()
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	h := NewIssueHandler(m, NewPostgresAPIKeyValidator(env.pool), NewPostgresTokenEventStore(env.pool), IssueRateLimits{
		PerAgentLimit:        testTokenIssuePerAgentLimit,
		PerAgentWindow:       testTokenIssuePerAgentWindow,
		PreAuthMaxConcurrent: testTokenIssuePreAuthMaxConcurrent,
	})
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

// TestIssueHandler_OversizedBody_Returns400Bounded proves the MaxBytesReader
// added during B-090/107 security review: an unauthenticated caller (no
// X-API-Key at all, so this can't be mistaken for an authenticated attacker)
// sending a body past the 8KiB cap gets a bounded 400, not an unbounded
// buffer allocation followed by whatever json.Decode does with it.
func TestIssueHandler_OversizedBody_Returns400Bounded(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	oversized := strings.Repeat("a", 9<<10) // 9KiB > the handler's 8KiB cap
	body := `{"agent_id":"agent:identity-token-test-agent","scope":"` + oversized + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/tokens", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %q, want 400 (oversized body must be rejected before any DB work)", w.Code, w.Body.String())
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

// TestIssueHandler_KeyScopedToSuspendedAgent_Returns403 (B-107 regression
// guard): before this brief, "is the resolved agent suspended/revoked"
// was AgentResolver.LookupByNameAndOrg's own checkStatus check, reused
// automatically. ValidateAndResolveAgent's combined query doesn't call
// checkStatus at all -- HandleIssue now replicates that check itself
// (rec.Status == "suspended" || rec.Status == "revoked") -- this proves
// that hand-replication is actually correct, not just assumed equivalent.
func TestIssueHandler_KeyScopedToSuspendedAgent_Returns403(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	suspendedAgentID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'suspended')
	`, suspendedAgentID, env.orgID, "issue-http-test-suspended-agent", "claude-sonnet-5", "test@example.com", "read:test"); err != nil {
		t.Fatalf("insert suspended test agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, suspendedAgentID)
	})

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, suspendedAgentID)

	req := issueRequestJSON(t, apiKey, "agent:issue-http-test-suspended-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %q, want 403 (issuance to a suspended agent must be rejected)", w.Code, w.Body.String())
	}
}

// TestIssueHandler_SameAgentNameDifferentOrgs_ResolvesOwnOrgOnly covers a
// property the B-090/107 code review flagged as real but untested:
// ValidateAndResolveAgent's combined query joins gateway_agents on
// `ga.org_id = ak.org_id AND ga.name = $2`, so two different orgs each
// having an agent with the identical name is an explicitly expected state
// (gateway_agents.name is unique only per-org) that must not let a key from
// one org resolve or issue a token bound to the other org's same-named
// agent.
func TestIssueHandler_SameAgentNameDifferentOrgs_ResolvesOwnOrgOnly(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, m := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	const sharedName = "issue-http-test-shared-name-agent"

	// env.orgID already has an agent named "identity-token-test-agent";
	// give it a SECOND agent using the shared name so both orgs have a
	// same-named row.
	orgAAgentID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, orgAAgentID, env.orgID, sharedName, "claude-sonnet-5", "test@example.com", "read:test"); err != nil {
		t.Fatalf("insert org A shared-name agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, orgAAgentID)
	})

	orgBID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		orgBID, "issue-http-test-org-b-"+orgBID.String()[:8], "issue-http-test-org-b-"+orgBID.String()); err != nil {
		t.Fatalf("insert org B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, orgBID)
	})
	orgBAgentID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_agents (id, org_id, name, model, owner, scope)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, orgBAgentID, orgBID, sharedName, "claude-sonnet-5", "test@example.com", "read:test"); err != nil {
		t.Fatalf("insert org B shared-name agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM gateway_agents WHERE id = $1`, orgBAgentID)
	})

	// A key bound to org A's shared-name agent must resolve org A's row and
	// succeed, never org B's same-named row.
	apiKey := issueTestAPIKeyReal(t, env, env.orgID, orgAAgentID)
	req := issueRequestJSON(t, apiKey, "agent:"+sharedName)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("org A key for shared-name agent: status = %d, body = %q, want 200", w.Code, w.Body.String())
	}
	var resp IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	claims, err := m.Validate(resp.Token)
	if err != nil {
		t.Fatalf("issued token failed to validate: %v", err)
	}
	if claims.Subject != "agent:"+sharedName {
		t.Errorf("Subject = %q, want %q", claims.Subject, "agent:"+sharedName)
	}

	// A key bound to org A's OTHER agent (not the shared-name one) must not
	// be able to claim org B's shared-name agent even though the name
	// matches -- the join is scoped by org, and the caller's key is bound to
	// a specific agent ID, not merely an org.
	crossKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)
	crossReq := issueRequestJSON(t, crossKey, "agent:"+sharedName)
	crossW := httptest.NewRecorder()
	mux.ServeHTTP(crossW, crossReq)
	if crossW.Code != http.StatusForbidden {
		t.Fatalf("org A key not scoped to the shared-name agent: status = %d, body = %q, want 403", crossW.Code, crossW.Body.String())
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

// waitForTokenEvent polls ai_token_events for jti's row -- B-107 made
// RecordIssued fire-and-forget (mirroring main.go's established
// safeWriteTokenUsage/B-099 pattern), so the row is no longer guaranteed to
// exist the instant HandleIssue's HTTP response returns. A short poll,
// not an immediate single query, is the correct way to observe an
// intentionally-async write -- same reasoning as testenv_test.go's
// waitForPendingApproval in the workflow package.
func waitForTokenEvent(t *testing.T, pool *pgxpool.Pool, jti string, timeout time.Duration) (eventType, agentName string, agentID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = pool.QueryRow(context.Background(),
			`SELECT event_type, agent_id, agent_name FROM ai_token_events WHERE jti = $1`, jti).
			Scan(&eventType, &agentID, &agentName)
		if lastErr == nil {
			return eventType, agentName, agentID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected an ai_token_events row for jti %q within %s, last error: %v", jti, timeout, lastErr)
	return "", "", uuid.Nil
}

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

	eventType, agentName, gotAgentID := waitForTokenEvent(t, env.pool, resp.JTI, 2*time.Second)
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
