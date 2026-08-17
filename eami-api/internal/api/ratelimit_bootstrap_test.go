// ratelimit_bootstrap_test.go — eami-api/internal/api
// B-070 — real-Postgres integration test proving the first-boot setup
// wizard's pre-existing per-IP rate limiting (B-055) now also returns a
// real Retry-After header on its 429s, and that normal (below-threshold)
// setup usage is unaffected. Reuses bootstrap_test.go's exact
// env/helpers -- no new plumbing.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestBootstrapRateLimit -v
package api_test

import (
	"net/http"
	"testing"
	"time"
)

// TestBootstrapRateLimit_TokenValidate_TripsAfterThreshold (AC3): repeated
// rapid token-validation attempts against the setup wizard are rate-limited
// (default 10/15min -- config.DefaultRateLimitConfig(), the same values
// B-055 originally hardcoded), with a real Retry-After header on the 429.
func TestBootstrapRateLimit_TokenValidate_TripsAfterThreshold(t *testing.T) {
	env := newBootstrapTestEnv(t)

	// 10 wrong-guess attempts: all must be plain 401s, still under threshold.
	for i := 0; i < 10; i++ {
		resp := env.postValidate(t, "guess-that-is-not-the-real-token")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401 (still under threshold), got %d: %s", i+1, resp.StatusCode, readBody(resp))
		}
	}

	// 11th attempt in the same window: rate-limited, not another 401.
	resp := env.postValidate(t, "guess-that-is-not-the-real-token")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("11th rapid attempt: want 429, got %d: %s", resp.StatusCode, readBody(resp))
	}
	assertRetryAfterHeader(t, resp)

	// The limiter is shared per-IP across ValidateSetupToken AND Bootstrap
	// (same s.setupLimiter, same design as B-055 shipped) -- confirm a
	// genuinely valid Bootstrap call is ALSO blocked while rate-limited,
	// not just repeats of the same validate call.
	raw := "genuinely-valid-token-blocked-by-limiter"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)
	bresp := env.postBootstrap(t, raw, "Should Not Bootstrap", "blocked@example.com", "supersecret123")
	if bresp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("bootstrap while rate-limited: want 429, got %d: %s", bresp.StatusCode, readBody(bresp))
	}
	if n := scalarInt(t, env.pool, `SELECT count(*) FROM orgs`); n != 0 {
		t.Fatalf("orgs count = %d, want 0 -- a rate-limited request must create nothing", n)
	}
}

// TestBootstrapRateLimit_NormalUsage_NotBlocked (AC6): a single legitimate
// setup flow -- one status check, one token validate, one real bootstrap --
// well under the threshold, is completely unaffected by B-070's changes.
func TestBootstrapRateLimit_NormalUsage_NotBlocked(t *testing.T) {
	env := newBootstrapTestEnv(t)
	raw := "normal-usage-token"
	insertSetupToken(t, env.pool, raw, time.Now().Add(30*time.Minute), false)

	if resp := env.get(t, "/v1/setup/status"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", resp.StatusCode, readBody(resp))
	}
	if resp := env.postValidate(t, raw); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("validate: want 204, got %d: %s", resp.StatusCode, readBody(resp))
	}
	resp := env.postBootstrap(t, raw, "Normal Org", "normal-admin@example.com", "supersecret123")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: want 201, got %d: %s", resp.StatusCode, readBody(resp))
	}
}
