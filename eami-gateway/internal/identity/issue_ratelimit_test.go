// issue_ratelimit_test.go -- eami-gateway/internal/identity
//
// Pure unit tests for the rateLimiter type added by B-118 (issue_http.go),
// needing no Postgres -- unlike issue_http_b116_b118_pg_test.go's
// integration tests, these run in any CI environment and specifically cover
// the limiter's own algorithm in isolation: exactly what
// workflow/ratelimit_test.go (B-070, the pattern this duplicates) does for
// its own copy. Added after this brief's own mandatory code-review pass
// flagged that the hardcoded 20-request/60s constants meant every existing
// test needed either 20 real requests or a 60s sleep to observe a window
// actually expiring -- these tests construct a *rateLimiter directly with
// short, test-only limit/window values instead.
package identity

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestRateLimiter_AllowsExactlyLimit_ThenDenies(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow("k"); !ok {
			t.Fatalf("attempt %d: want allowed (within limit)", i+1)
		}
	}
	if ok, retryAfter := rl.Allow("k"); ok {
		t.Fatal("4th attempt: want denied (over limit)")
	} else if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want a positive duration", retryAfter)
	}
}

func TestRateLimiter_DifferentKeys_IndependentBuckets(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	if ok, _ := rl.Allow("a"); !ok {
		t.Fatal("key a, 1st attempt: want allowed")
	}
	if ok, _ := rl.Allow("a"); ok {
		t.Fatal("key a, 2nd attempt: want denied (over its own limit)")
	}
	if ok, _ := rl.Allow("b"); !ok {
		t.Fatal("key b, 1st attempt: want allowed -- independent bucket from key a")
	}
}

// TestRateLimiter_WindowExpires_AllowsAgain proves the limiter actually
// recovers once the window elapses -- the one property none of the
// integration tests in issue_http_b116_b118_pg_test.go can cheaply prove,
// since those use the real 60s production window.
func TestRateLimiter_WindowExpires_AllowsAgain(t *testing.T) {
	rl := newRateLimiter(1, 30*time.Millisecond)
	if ok, _ := rl.Allow("k"); !ok {
		t.Fatal("1st attempt: want allowed")
	}
	if ok, _ := rl.Allow("k"); ok {
		t.Fatal("2nd attempt, still inside window: want denied")
	}
	time.Sleep(40 * time.Millisecond)
	if ok, _ := rl.Allow("k"); !ok {
		t.Fatal("3rd attempt, after window elapsed: want allowed again")
	}
}

// TestRateLimiter_DeniedAttempt_DoesNotExtendWindow proves a denied request
// doesn't itself get recorded as a new attempt -- otherwise a client that
// keeps hammering past its limit would keep pushing its own reset time
// forward and never recover.
func TestRateLimiter_DeniedAttempt_DoesNotExtendWindow(t *testing.T) {
	rl := newRateLimiter(1, 40*time.Millisecond)
	if ok, _ := rl.Allow("k"); !ok {
		t.Fatal("1st attempt: want allowed")
	}
	// Hammer well past the limit, inside the window.
	for i := 0; i < 5; i++ {
		if ok, _ := rl.Allow("k"); ok {
			t.Fatalf("hammering attempt %d: want denied", i+1)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if ok, _ := rl.Allow("k"); !ok {
		t.Fatal("after the ORIGINAL window elapsed: want allowed -- denied attempts must not have pushed the window forward")
	}
}

// TestRateLimiter_NonPositiveLimit_FailsClosed: newRateLimiter is only ever
// constructed with the positive tokenIssueRateLimit constant in production
// code (unlike workflow.newRateLimiter, whose config-driven limit can in
// principle be misconfigured to zero/negative at startup), but Allow() stays
// defensive regardless, matching workflow/ratelimit.go's identical
// precedent and its stated reasoning (a caller could still construct one
// directly, as this test does).
func TestRateLimiter_NonPositiveLimit_FailsClosed(t *testing.T) {
	rl := newRateLimiter(0, time.Minute)
	if ok, _ := rl.Allow("k"); ok {
		t.Fatal("zero limit: want denied (fail closed), not a panic or a false allow")
	}
	rl = newRateLimiter(-1, time.Minute)
	if ok, _ := rl.Allow("k"); ok {
		t.Fatal("negative limit: want denied (fail closed), not a panic or a false allow")
	}
}

func TestSetRetryAfter_RoundsUpToWholeSeconds(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "1"}, // rounds up, and the 1s floor both agree here
		{1500 * time.Millisecond, "2"},
		{2 * time.Second, "2"},
		{0, "1"}, // floor applies
	}
	for _, tc := range tests {
		rec := httptest.NewRecorder()
		setRetryAfter(rec, tc.d)
		got := rec.Header().Get("Retry-After")
		if got != tc.want {
			t.Errorf("setRetryAfter(%v): Retry-After = %q, want %q", tc.d, got, tc.want)
		}
		if _, err := strconv.Atoi(got); err != nil {
			t.Errorf("setRetryAfter(%v): Retry-After = %q is not a valid integer", tc.d, got)
		}
	}
}
