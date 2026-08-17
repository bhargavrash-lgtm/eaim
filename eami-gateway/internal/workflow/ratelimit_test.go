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

func issueTestToken(t *testing.T, idm *identity.Manager, agentSubject string) string {
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
// claims.Subject) exists to close.
type fakeResolver struct {
	byName map[string]*registry.AgentRecord
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{byName: make(map[string]*registry.AgentRecord)}
}

func (f *fakeResolver) add(name string, rec *registry.AgentRecord) *fakeResolver {
	f.byName[name] = rec
	return f
}

func (f *fakeResolver) LookupByName(_ context.Context, name string) (*registry.AgentRecord, error) {
	if rec, ok := f.byName[name]; ok {
		return rec, nil
	}
	return nil, errors.New("fakeResolver: agent not found")
}

// TestRateLimitRunMiddleware_TripsAfterThreshold_ForSameAgent (AC4):
// repeated rapid workflow-run calls from the SAME agent are rate-limited
// once the configured per-agent threshold is crossed.
func TestRateLimitRunMiddleware_TripsAfterThreshold_ForSameAgent(t *testing.T) {
	idm := newTestIdentityManager(t)
	token := issueTestToken(t, idm, "agent:limiter-test-1")
	resolver := newFakeResolver().add("limiter-test-1", &registry.AgentRecord{ID: "agent-uuid-1", OrgID: "org-a", Status: "active"})
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
	tokenA := issueTestToken(t, idm, "agent:limiter-test-a")
	tokenB := issueTestToken(t, idm, "agent:limiter-test-b")
	resolver := newFakeResolver().
		add("limiter-test-a", &registry.AgentRecord{ID: "agent-uuid-a", OrgID: "org-a", Status: "active"}).
		add("limiter-test-b", &registry.AgentRecord{ID: "agent-uuid-b", OrgID: "org-a", Status: "active"})
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
// an agent named "researcher". Before the fix, keying the limiter on the
// raw JWT claims.Subject (just "agent:researcher" for both) would let one
// org's agent exhaust the other's quota. The fix resolves each token to its
// own registry record and keys on agentRec.ID, a real globally-unique
// value -- this test proves that by giving both fake records the identical
// Name but distinct ID/OrgID, and confirming their quotas stay independent
// exactly like TestRateLimitRunMiddleware_DifferentAgents_IndependentLimits
// above.
func TestRateLimitRunMiddleware_SameName_DifferentOrgs_IndependentLimits(t *testing.T) {
	idm := newTestIdentityManager(t)
	// Both tokens carry the identical claims.Subject shape a naive
	// name-only key would collide on: LookupByName's real signature is
	// (ctx, name), so it never sees which org a caller belongs to, only
	// the bare name -- exactly the ambiguity that makes the collision
	// possible in production. To let this ONE test still construct two
	// distinct resolutions for the identical name, the org is threaded
	// through r.Context() instead (a test-only device -- production code
	// has no such mechanism, which is precisely the bug being regression
	// tested: nothing besides the resolved record's own ID distinguishes
	// the two callers).
	tokenOrgA := issueTestToken(t, idm, "agent:researcher")
	tokenOrgB := issueTestToken(t, idm, "agent:researcher")

	recordByOrg := map[string]*registry.AgentRecord{
		"org-a": {ID: "agent-uuid-org-a", OrgID: "org-a", Name: "researcher", Status: "active"},
		"org-b": {ID: "agent-uuid-org-b", OrgID: "org-b", Name: "researcher", Status: "active"},
	}
	resolver := &ctxOrgResolver{byOrg: recordByOrg}
	h := RateLimitRunMiddleware(idm, resolver, 3, time.Minute, alwaysOKHandler)

	doRunAsOrg := func(bearer, org string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/gateway/workflows/wf-1/run", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req = req.WithContext(context.WithValue(req.Context(), ctxOrgKey{}, org))
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}

	for i := 0; i < 3; i++ {
		if rec := doRunAsOrg(tokenOrgA, "org-a"); rec.Code != http.StatusOK {
			t.Fatalf("org A's researcher, attempt %d: want 200, got %d", i+1, rec.Code)
		}
	}
	if rec := doRunAsOrg(tokenOrgA, "org-a"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("org A's researcher over its own limit: want 429, got %d", rec.Code)
	}

	// Org B's identically-NAMED agent has made zero requests -- must be
	// completely unaffected by org A's exhausted quota.
	if rec := doRunAsOrg(tokenOrgB, "org-b"); rec.Code != http.StatusOK {
		t.Fatalf("org B's identically-named researcher (independent quota): want 200, got %d -- cross-org rate-limit collision", rec.Code)
	}
}

type ctxOrgKey struct{}

// ctxOrgResolver resolves using an org value smuggled through the request
// context (test-only -- see the comment above) instead of the name
// argument, so this one test can represent two distinct orgs' rows sharing
// an identical agent name. Real production code never does this;
// registry.Registry resolves within one org's actual DB rows.
type ctxOrgResolver struct {
	byOrg map[string]*registry.AgentRecord
}

func (c *ctxOrgResolver) LookupByName(ctx context.Context, _ string) (*registry.AgentRecord, error) {
	org, _ := ctx.Value(ctxOrgKey{}).(string)
	if rec, ok := c.byOrg[org]; ok {
		return rec, nil
	}
	return nil, errors.New("ctxOrgResolver: unknown org")
}

// TestRateLimitRunMiddleware_SingleRun_NotBlocked: a single, genuine
// workflow-run call is never falsely rate-limited.
func TestRateLimitRunMiddleware_SingleRun_NotBlocked(t *testing.T) {
	idm := newTestIdentityManager(t)
	token := issueTestToken(t, idm, "agent:limiter-test-single")
	resolver := newFakeResolver().add("limiter-test-single", &registry.AgentRecord{ID: "agent-uuid-single", OrgID: "org-a", Status: "active"})
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
	token := issueTestToken(t, idm, "agent:ghost-agent")
	resolver := newFakeResolver() // deliberately empty -- "ghost-agent" resolves to nothing

	forbidding := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	h := RateLimitRunMiddleware(idm, resolver, 1, time.Minute, forbidding)

	if rec := doRun(t, h, token); rec.Code != http.StatusForbidden {
		t.Fatalf("unresolvable agent: want pass-through to next()'s 403, got %d", rec.Code)
	}
}
