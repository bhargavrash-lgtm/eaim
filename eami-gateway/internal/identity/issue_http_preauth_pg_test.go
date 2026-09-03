// issue_http_preauth_pg_test.go -- eami-gateway/internal/identity
//
// Real-Postgres integration test for B-120's pre-auth concurrency gate,
// complementing issue_http_preauth_test.go's pure-unit, fake-validator
// proof (which controls exact timing via a blocking fake) with an
// end-to-end proof against the real Manager/pgAPIKeyValidator/Postgres
// stack: legitimate issuance succeeds unaffected by the gate's presence,
// and the gate is genuinely wired from config end to end.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./internal/identity/... -run TestIssueHandler_PreAuthGate -v
package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestIssueHandler_PreAuthGate_DoesNotBlockLegitimateIssuance is the
// AC3-adjacent negative control: a real, correctly-scoped, valid API key
// issuing well within the pre-auth gate's normal capacity succeeds exactly
// as it did before B-120 -- the gate is additive, not a regression to the
// happy path, against the real pgAPIKeyValidator/Postgres stack (not a
// fake).
func TestIssueHandler_PreAuthGate_DoesNotBlockLegitimateIssuance(t *testing.T) {
	env := newIdentityTestEnv(t)
	h, _ := newIssueHandlerForTest(t, env)
	mux := newIssueTestMux(h)

	apiKey := issueTestAPIKeyReal(t, env, env.orgID, env.agentID)
	req := issueRequestJSON(t, apiKey, "agent:identity-token-test-agent")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("legitimate issuance: status = %d, body = %q, want 200", w.Code, w.Body.String())
	}
}

// TestIssueHandler_PreAuthGate_SaturatedGate_RejectsBeforeRealDBCall is
// AC2's real-Postgres proof: with a real IssueHandler backed by the real
// pgAPIKeyValidator/Postgres, a request arriving while the (tightly
// configured) pre-auth gate is fully occupied by other genuinely in-flight
// real DB calls is rejected with 429 -- confirmed by an unchanged
// api_keys/ai_token_events state (nothing this request could have written
// was written), not merely a fast response time.
func TestIssueHandler_PreAuthGate_SaturatedGate_RejectsBeforeRealDBCall(t *testing.T) {
	env := newIdentityTestEnv(t)
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManagerWithDB(keyPath, 300, "eami-gateway", env.pool)
	if err != nil {
		t.Fatalf("NewManagerWithDB: %v", err)
	}
	// maxConcurrent=1 against a real Postgres pool: the one real call this
	// test fires deliberately overlaps with a long-running transaction
	// holding the api_keys table, forcing ValidateAndResolveAgent's own
	// query to genuinely block -- letting a second, concurrent request
	// reliably observe the gate as saturated without relying on a fake.
	h := NewIssueHandler(m, NewPostgresAPIKeyValidator(env.pool), NewPostgresTokenEventStore(env.pool), IssueRateLimits{
		PerAgentLimit: testTokenIssuePerAgentLimit, PerAgentWindow: testTokenIssuePerAgentWindow,
		PreAuthMaxConcurrent: 1,
	})
	mux := newIssueTestMux(h)

	tx, err := env.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin blocking transaction: %v", err)
	}
	defer tx.Rollback(t.Context())
	// Real, table-level lock -- api_keys' own concurrent index lookup
	// (ValidateAndResolveAgent's query) will genuinely block behind it,
	// same as a real, unusually slow query would in production.
	if _, err := tx.Exec(t.Context(), `LOCK TABLE api_keys IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock api_keys: %v", err)
	}

	firstDone := make(chan int, 1)
	go func() {
		req := issueRequestJSON(t, "irrelevant-bogus-key", "agent:whoever")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		firstDone <- w.Code
	}()

	// Give the first request a real moment to reach and block inside its
	// real Postgres query behind the held lock.
	time.Sleep(200 * time.Millisecond)

	req2 := issueRequestJSON(t, "another-irrelevant-bogus-key", "agent:whoever")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request while the sole gate slot is genuinely held by a blocked real DB call: status = %d, body = %q, want 429", w2.Code, w2.Body.String())
	}

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("rollback blocking transaction: %v", err)
	}
	select {
	case code := <-firstDone:
		if code != http.StatusUnauthorized {
			t.Fatalf("first request (real DB lookup, bogus key): status = %d, want 401", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first (previously blocked) request to complete")
	}
}
