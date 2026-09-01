// ratelimit_test.go — eami-gateway/internal/workflow
// B-070 — unit tests for RateLimitRunMiddleware, the per-agent-identity
// rate limiter wrapping POST /v1/gateway/workflows/{workflowId}/run. Uses a
// real *identity.Manager (ephemeral file-backed keypair, mirrors
// internal/identity's own test convention), a fake AgentResolver (avoids a
// real registry/DB -- these tests are about the rate-limit decision itself,
// not agent resolution), and a fake "next" handler that always returns 200.
// The real Executor/HandleRun is deliberately not involved here (see
// http_test.go / executor_test.go for that).
//
// Run: go test ./internal/workflow/... -run TestRateLimitRunMiddleware -v
package workflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/eami/gateway/internal/identity"
	"github.com/eami/gateway/internal/registry"
)

func newTestIdentityManager(t *testing.T) *identity.Manager {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "gateway.key")
	m, err := identity.NewManager(keyPath, 300, "eami-gateway")
	if err != nil {
		t.Fatalf("identity.NewManager: %v", err)
	}
	return m
}

// issueTestToken issues a token carrying a real org_id claim (B-141) --
// every real token minted after the cutover has one, and every consumer
// of it (including RateLimitRunMiddleware) requires it. See
// issueTestTokenNoOrg for the specific pre-cutover case.
func issueTestToken(t *testing.T, idm *identity.Manager, agentSubject, orgID string) string {
	t.Helper()
	resp, err := idm.Issue(identity.IssueRequest{AgentID: agentSubject, OrgID: orgID, TTLSeconds: 300})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return resp.Token
}

// issueTestTokenNoOrg issues a pre-cutover-shaped token (no org_id claim)
// -- the exact shape every real token minted before B-141 shipped has.
func issueTestTokenNoOrg(t *testing.T, idm *identity.Manager, agentSubject string) string {
	t.Helper()
	resp, err := idm.Issue(identity.IssueRequest{AgentID: agentSubject, TTLSeconds: 300})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return resp.Token
}

func alwaysOKHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func doRun(t *testing.T, h http.HandlerFunc, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/gateway/workflows/wf-1/run", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// fakeResolver is a minimal AgentResolver backed by an in-memory map keyed
// on agent NAME (not ID) -- mirrors the real registry's own by-name lookup
// shape, letting tests construct the exact "two orgs, same agent name"
// collision scenario the rate-limit key fix (keying on agentRec.ID, not
// claims.Subject) exists to close. Keyed on (name, orgID) -- not name
// alone -- mirroring the real registry.LookupByNameAndOrg contract
// (B-141), so this fake can represent two different orgs' rows sharing
// an identical agent name using two real tokens with distinct org_id
// claims, rather than a context-smuggling test-only device.
type fakeResolver struct {
	byNameOrg map[[2]string]*registry.AgentRecord
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{byNameOrg: make(map[[2]string]*registry.AgentRecord)}
}

func (f *fakeResolver) add(name, orgID string, rec *registry.AgentRecord) *fakeResolver {
	f.byNameOrg[[2]string{name, orgID}] = rec
	return f
}

func (f *fakeResolver) LookupByNameAndOrg(_ context.Context, name, orgID string) (*registry.AgentRecord, error) {
	if rec, ok := f.byNameOrg[[2]string{name, orgID}]; ok {
		return rec, nil
	}
	return nil, errors.New("fakeResolver: agent not found")
}

// TestRateLimitRunMiddleware_TripsAfterThreshold_ForSameAgent (AC4):
// repeated rapid workflow-run calls from the SAME agent are rate-limited
// once the configured per-agent threshold is crossed.
func TestRateLimitRunMiddleware_TripsAfterThreshold_ForSameAgent(t *testing.T) {
	idm := newTestIdentityManager(t)
	token := issueTestToken(t, idm, "agent:limiter-test-1", "org-a")
	resolver := newFakeResolver().add("limiter-test-1", "org-a", &registry.AgentRecord{ID: "agent-uuid-1", OrgID: "org-a", Status: "active"})
	h := RateLimitRunMiddleware(idm, resolver, 5, time.Minute, alwaysOKHandler)

	for i := 0; i < 5; i++ {
		rec := doRun(t, h, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: want 200 (still under threshold), got %d", i+1, rec.Code)
		}
	}

	rec := doRun(t, h, token)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th rapid attempt: want 429, got %d", rec.Code)
	}
	ra := rec.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("429 response missing Retry-After header")
	}
	if secs, err := strconv.Atoi(ra); err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer", ra)
	}
}

// TestRateLimitRunMiddleware_DifferentAgents_IndependentLimits: one agent
// hitting its own limit must not affect a different agent's own quota --
// per-agent-identity, not global.
func TestRateLimitRunMiddleware_DifferentAgents_IndependentLimits(t *testing.T) {
	idm := newTestIdentityManager(t)
	tokenA := issueTestToken(t, idm, "agent:limiter-test-a", "org-a")
	tokenB := issueTestToken(t, idm, "agent:limiter-test-b", "org-a")
	resolver := newFakeResolver().
		add("limiter-test-a", "org-a", &registry.AgentRecord{ID: "agent-uuid-a", OrgID: "org-a", Status: "active"}).
		add("limiter-test-b", "org-a", &registry.AgentRecord{ID: "agent-uuid-b", OrgID: "org-a", Status: "active"})
	h := RateLimitRunMiddleware(idm, resolver, 3, time.Minute, alwaysOKHandler)

	for i := 0; i < 3; i++ {
		if rec := doRun(t, h, tokenA); rec.Code != http.StatusOK {
			t.Fatalf("agent A attempt %d: want 200, got %d", i+1, rec.Code)
		}
	}
	if rec := doRun(t, h, tokenA); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("agent A over its own limit: want 429, got %d", rec.Code)
	}

	// Agent B has made zero requests -- must be completely unaffected.
	if rec := doRun(t, h, tokenB); rec.Code != http.StatusOK {
		t.Fatalf("agent B (independent quota): want 200, got %d", rec.Code)
	}
}

// TestRateLimitRunMiddleware_SameName_DifferentOrgs_IndependentLimits is the
// regression test for the cross-org collision this brief's own security
// review found: agent names are unique only per-org (schema.sql: UNIQUE
// (org_id, name)), not globally, so two different orgs can each register
// an agent named "researcher". Before B-141, keying the limiter on the raw
// JWT claims.Subject (just "agent:researcher" for both) would let one
// org's agent exhaust the other's quota -- and even after the earlier
// agentRec.ID-keying fix, resolving that record via the unscoped
// LookupByName could itself return the WRONG org's "researcher" row (the
// actual B-141 bug, one layer below this middleware's own fix). This test
// now uses two real tokens, each carrying its own real org_id claim
// (B-141), resolved through a (name, org)-keyed fakeResolver that mirrors
// the real registry.LookupByNameAndOrg contract -- no test-only
// context-smuggling device needed anymore, since production code now has
// a real mechanism (Claims.OrgID) to distinguish the two callers, which
// is exactly what this test now exercises directly.
func TestRateLimitRunMiddleware_SameName_DifferentOrgs_IndependentLimits(t *testing.T) {
	idm := newTestIdentityManager(t)
	tokenOrgA := issueTestToken(t, idm, "agent:researcher", "org-a")
	tokenOrgB := issueTestToken(t, idm, "agent:researcher", "org-b")

	resolver := newFakeResolver().
		add("researcher", "org-a", &registry.AgentRecord{ID: "agent-uuid-org-a", OrgID: "org-a", Name: "researcher", Status: "active"}).
		add("researcher", "org-b", &registry.AgentRecord{ID: "agent-uuid-org-b", OrgID: "org-b", Name: "researcher", Status: "active"})
	h := RateLimitRunMiddleware(idm, resolver, 3, time.Minute, alwaysOKHandler)

	for i := 0; i < 3; i++ {
		if rec := doRun(t, h, tokenOrgA); rec.Code != http.StatusOK {
			t.Fatalf("org A's researcher, attempt %d: want 200, got %d", i+1, rec.Code)
		}
	}
	if rec := doRun(t, h, tokenOrgA); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("org A's researcher over its own limit: want 429, got %d", rec.Code)
	}

	// Org B's identically-NAMED agent has made zero requests -- must be
	// completely unaffected by org A's exhausted quota.
	if rec := doRun(t, h, tokenOrgB); rec.Code != http.StatusOK {
		t.Fatalf("org B's identically-named researcher (independent quota): want 200, got %d -- cross-org rate-limit collision", rec.Code)
	}
}

// TestRateLimitRunMiddleware_SingleRun_NotBlocked: a single, genuine
// workflow-run call is never falsely rate-limited.
func TestRateLimitRunMiddleware_SingleRun_NotBlocked(t *testing.T) {
	idm := newTestIdentityManager(t)
	token := issueTestToken(t, idm, "agent:limiter-test-single", "org-a")
	resolver := newFakeResolver().add("limiter-test-single", "org-a", &registry.AgentRecord{ID: "agent-uuid-single", OrgID: "org-a", Status: "active"})
	h := RateLimitRunMiddleware(idm, resolver, 5, time.Minute, alwaysOKHandler)

	rec := doRun(t, h, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("single run: want 200, got %d", rec.Code)
	}
}

// TestRateLimitRunMiddleware_InvalidAuth_PassesThroughUnmodified: missing or
// invalid bearer auth is NOT intercepted by the limiter -- it reaches next()
// unchanged so the real handler produces its own 401, per this middleware's
// documented design (avoids two different auth-error code paths).
func TestRateLimitRunMiddleware_InvalidAuth_PassesThroughUnmodified(t *testing.T) {
	idm := newTestIdentityManager(t)
	resolver := newFakeResolver()

	authRejecting := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
	h := RateLimitRunMiddleware(idm, resolver, 1, time.Minute, authRejecting)

	// No Authorization header at all.
	if rec := doRun(t, h, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: want pass-through to next()'s 401, got %d", rec.Code)
	}
	// Garbage bearer token.
	if rec := doRun(t, h, "not-a-real-jwt"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token: want pass-through to next()'s 401, got %d", rec.Code)
	}
}

// TestRateLimitRunMiddleware_UnresolvableAgent_PassesThroughUnmodified: a
// validly-signed token whose agent name doesn't resolve (e.g. deleted after
// the token was issued) is NOT intercepted by the limiter -- it reaches
// next() unchanged so HandleRun produces its own real 403, matching the
// invalid-auth pass-through design above.
func TestRateLimitRunMiddleware_UnresolvableAgent_PassesThroughUnmodified(t *testing.T) {
	idm := newTestIdentityManager(t)
	token := issueTestToken(t, idm, "agent:ghost-agent", "org-a")
	resolver := newFakeResolver() // deliberately empty -- "ghost-agent" resolves to nothing

	forbidding := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	h := RateLimitRunMiddleware(idm, resolver, 1, time.Minute, forbidding)

	if rec := doRun(t, h, token); rec.Code != http.StatusForbidden {
		t.Fatalf("unresolvable agent: want pass-through to next()'s 403, got %d", rec.Code)
	}
}

// TestRateLimitRunMiddleware_PreCutoverToken_PassesThroughUnmodified (B-141):
// a validly-signed token with no org_id claim (the pre-cutover shape) is
// NOT intercepted by the limiter -- it reaches next() unchanged, same
// best-effort pass-through as an unresolvable agent above. This middleware
// is not the security boundary (HandleRun's own LookupByNameAndOrg call is
// the real, authoritative rejection of a pre-cutover token) -- confirmed
// as an acceptable, pre-existing design property, not a new gap.
func TestRateLimitRunMiddleware_PreCutoverToken_PassesThroughUnmodified(t *testing.T) {
	idm := newTestIdentityManager(t)
	token := issueTestTokenNoOrg(t, idm, "agent:no-org-agent")
	resolver := newFakeResolver().add("no-org-agent", "org-a", &registry.AgentRecord{ID: "agent-uuid-no-org", OrgID: "org-a", Status: "active"})

	forbidding := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	h := RateLimitRunMiddleware(idm, resolver, 1, time.Minute, forbidding)

	if rec := doRun(t, h, token); rec.Code != http.StatusForbidden {
		t.Fatalf("pre-cutover token: want pass-through to next()'s 403, got %d", rec.Code)
	}
}
