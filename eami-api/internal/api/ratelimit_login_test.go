// ratelimit_login_test.go — eami-api/internal/api
// B-070 — handler-level tests for POST /v1/auth/login's rate limiting.
// Reuses auth_test.go's exact fixtures/helpers (newAuthTestServer,
// seedValidUser, post, decodeBody) -- no database, no network. Each test
// calls newAuthTestServer independently so every test gets fresh limiters
// (api.NewHandler builds new ones per call), never sharing state across
// tests.
//
// Run: go test -count=1 ./internal/api/... -run TestLoginRateLimit -v
package api_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/eami/api/internal/api"
)

// TestLoginRateLimit_PerAccount_TripsAfterThreshold (AC1): repeated rapid
// login attempts against the SAME account, even with the wrong password
// each time, get rate-limited after the default per-account threshold
// (5/5min -- api.NewHandler wires config.DefaultRateLimitConfig()).
func TestLoginRateLimit_PerAccount_TripsAfterThreshold(t *testing.T) {
	ms := api.NewMockStore()
	seedValidUser(t, ms)
	srv := newAuthTestServer(t, ms)

	var last *http.Response
	for i := 0; i < 5; i++ {
		last = post(t, srv, "/v1/auth/login", map[string]string{
			"email":    validEmail,
			"password": "wrong-password",
		})
		if last.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401 (still under threshold), got %d", i+1, last.StatusCode)
		}
	}

	// 6th attempt within the window must be rate-limited, not another 401.
	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": "wrong-password",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th rapid attempt: want 429, got %d", resp.StatusCode)
	}
	assertRetryAfterHeader(t, resp)

	// Even the CORRECT password must be rejected while rate-limited -- the
	// limiter gates before Login() ever checks credentials.
	resp2 := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": validPassword,
	})
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("attempt with correct password while rate-limited: want 429, got %d", resp2.StatusCode)
	}
}

// TestLoginRateLimit_PerIP_TripsAcrossDifferentAccounts (AC2): repeated
// rapid login attempts from the same client (same IP, since httptest's
// client always connects from one loopback source) against MANY DIFFERENT
// accounts are also rate-limited, once the per-IP threshold (20/5min) is
// crossed -- proving per-IP limiting doesn't just piggyback on the
// per-account limiter.
func TestLoginRateLimit_PerIP_TripsAcrossDifferentAccounts(t *testing.T) {
	ms := api.NewMockStore()
	srv := newAuthTestServer(t, ms)
	// Deliberately no seeded user -- every distinct email 404s via
	// "invalid email or password" (401), never trips the (per-account)
	// limiter meaningfully since each email is used exactly once.

	for i := 0; i < 20; i++ {
		resp := post(t, srv, "/v1/auth/login", map[string]string{
			"email":    "nobody" + strconv.Itoa(i) + "@example.com",
			"password": "anything",
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d (distinct account): want 401 (still under per-IP threshold), got %d", i+1, resp.StatusCode)
		}
	}

	// 21st attempt, yet another distinct account, same source IP: per-IP
	// limiter must trip even though no single account has been retried.
	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    "nobody-final@example.com",
		"password": "anything",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st distinct-account attempt from same IP: want 429, got %d", resp.StatusCode)
	}
	assertRetryAfterHeader(t, resp)
}

// TestLoginRateLimit_GenuineRetries_NotBlocked (AC5): a real user mistyping
// their password once or twice, well under either threshold, is never
// falsely rate-limited -- the second attempt (with the correct password)
// must succeed normally.
func TestLoginRateLimit_GenuineRetries_NotBlocked(t *testing.T) {
	ms := api.NewMockStore()
	seedValidUser(t, ms)
	srv := newAuthTestServer(t, ms)

	// Attempt 1: typo'd password.
	resp1 := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": "wrong-once",
	})
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("attempt 1 (typo): want 401, got %d", resp1.StatusCode)
	}

	// Attempt 2: another typo.
	resp2 := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": "wrong-twice",
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("attempt 2 (typo): want 401, got %d", resp2.StatusCode)
	}

	// Attempt 3: correct password -- must succeed, not be rate-limited.
	resp3 := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    validEmail,
		"password": validPassword,
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("attempt 3 (correct, after 2 genuine typos): want 200, got %d", resp3.StatusCode)
	}
	body := decodeBody(t, resp3)
	if tok, _ := body["access_token"].(string); tok == "" {
		t.Fatalf("expected a real access_token after a legitimate retry succeeded, got: %v", body)
	}
}

// TestLoginRateLimit_AccountKey_CaseInsensitive: "Alice@Example.com" and
// "alice@example.com" must share one rate-limit bucket (normalizeLoginEmail),
// otherwise case variation would let an attacker double their effective
// per-account quota.
func TestLoginRateLimit_AccountKey_CaseInsensitive(t *testing.T) {
	ms := api.NewMockStore()
	seedValidUser(t, ms)
	srv := newAuthTestServer(t, ms)

	emails := []string{
		"alice@example.com", "Alice@Example.com", "ALICE@EXAMPLE.COM",
		"AlIcE@exAmPle.cOm", "alice@example.com",
	}
	for i, e := range emails {
		resp := post(t, srv, "/v1/auth/login", map[string]string{
			"email":    e,
			"password": "wrong-password",
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d (email=%q): want 401 (still under threshold), got %d", i+1, e, resp.StatusCode)
		}
	}

	// 6th attempt, yet another case variant of the same account: must trip.
	resp := post(t, srv, "/v1/auth/login", map[string]string{
		"email":    "aLICE@example.COM",
		"password": "wrong-password",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th case-variant attempt: want 429 (case must not evade the limiter), got %d", resp.StatusCode)
	}
}

func assertRetryAfterHeader(t *testing.T, resp *http.Response) {
	t.Helper()
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		t.Fatal("429 response missing Retry-After header")
	}
	secs, err := strconv.Atoi(ra)
	if err != nil {
		t.Fatalf("Retry-After header %q is not an integer: %v", ra, err)
	}
	if secs < 1 {
		t.Fatalf("Retry-After header must be >= 1 second, got %d", secs)
	}
}
