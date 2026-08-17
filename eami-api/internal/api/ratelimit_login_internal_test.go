// ratelimit_login_internal_test.go — eami-api/internal/api
// B-070 — white-box regression test for a real fail-open bug found by this
// brief's own mandatory security review: io.ReadAll can return a non-nil
// error alongside a body it had ALREADY fully read (e.g. a malformed
// chunked-transfer trailer arriving after every real body byte). The
// original rateLimitLogin treated "read errored" as "skip rate limiting
// entirely and forward the request" -- letting a syntactically valid,
// fully-readable credential-guess bypass both limiters. Fixed by ignoring
// the read error and always counting whatever bytes were captured against
// the IP limiter. package api (white-box) because rateLimitLogin is
// unexported and this test calls it directly, bypassing the router/Login
// chain entirely -- it exercises only the middleware's own decision logic.
//
// Run: go test ./internal/api/... -run TestRateLimitLogin_BodyReadError -v
package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// flakyBody mimics a request body that delivers its full, valid payload on
// the first Read call, then returns a non-nil, non-EOF error on the next --
// exactly the shape io.ReadAll sees from a chunked body whose trailer
// section fails to parse after all real data has already arrived.
type flakyBody struct {
	data []byte
	read bool
}

func (f *flakyBody) Read(p []byte) (int, error) {
	if !f.read {
		f.read = true
		n := copy(p, f.data)
		return n, nil
	}
	return 0, errors.New("simulated malformed chunked trailer")
}

func (f *flakyBody) Close() error { return nil }

func TestRateLimitLogin_BodyReadError_StillCountsAgainstIPLimiter(t *testing.T) {
	s := NewHandler(nil, nil)
	// Tight limit so the bug (or its absence) is observable in 2 requests.
	s.loginIPLimiter = newRateLimiter(1, time.Minute)

	nextCalls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusOK)
	})
	h := s.rateLimitLogin(next)

	validBody := []byte(`{"email":"attacker@example.com","password":"guess-1"}`)

	// First request: body read errors AFTER full data was captured. Must
	// still count against the IP limiter and still reach next() (the
	// captured bytes are valid, so Login() should get its normal chance).
	req1 := httptest.NewRequest(http.MethodPost, "/v1/auth/login", &flakyBody{data: validBody})
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if nextCalls != 1 {
		t.Fatalf("request 1 (flaky body, still fully readable): want next() called once, got %d calls", nextCalls)
	}

	// Second request, same source IP, identical flaky-body shape: the IP
	// limiter (limit=1) must now be tripped. Before the fix, the read
	// error path skipped Allow() entirely, so this would incorrectly reach
	// next() a second time too, proving the bypass.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/auth/login", &flakyBody{data: validBody})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("request 2 (same IP, over limit): want 429, got %d -- rate limiting was bypassed via the body-read-error path", rec2.Code)
	}
	if nextCalls != 1 {
		t.Fatalf("request 2 must NOT reach next(): want 1 total call to next(), got %d -- rate limiting was bypassed", nextCalls)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

// TestRateLimiter_Allow_NonPositiveLimit_FailsClosedNotPanic is the
// regression test for the second security-review finding: rl.Allow indexed
// kept[0] once len(kept) >= rl.limit, which is immediately true (0 >= 0)
// for a non-positive limit on the very first call, panicking rather than
// returning an error. config.go's validate() now rejects a non-positive
// configured value at startup, but Allow() itself must also stay
// defensive -- this test constructs limiters directly, bypassing config
// entirely, to prove the guard lives in Allow(), not just in config
// validation.
func TestRateLimiter_Allow_NonPositiveLimit_FailsClosedNotPanic(t *testing.T) {
	for _, limit := range []int{0, -1, -100} {
		rl := newRateLimiter(limit, time.Minute)
		ok, retryAfter := rl.Allow("any-key")
		if ok {
			t.Errorf("limit=%d: Allow() returned ok=true, want fail-closed (false)", limit)
		}
		if retryAfter <= 0 {
			t.Errorf("limit=%d: Allow() returned retryAfter=%v, want a positive duration", limit, retryAfter)
		}
	}
}

// TestRateLimitLogin_AccountKey_SurvivesTrailingGarbage is the regression
// test for the third security-review finding: the middleware originally
// parsed the peeked body with json.Unmarshal (errors on trailing data)
// while Login() parses the SAME restored bytes with
// json.NewDecoder(...).Decode (silently ignores trailing data after the
// first JSON value). A body with one extra byte appended would pass
// Login()'s parser but fail the middleware's, skipping the per-account
// limiter entirely while still reaching a normal login attempt --
// effectively downgrading account-level brute-force protection to the
// much looser per-IP limit just by appending garbage. Both parsers must
// now agree.
func TestRateLimitLogin_AccountKey_SurvivesTrailingGarbage(t *testing.T) {
	s := NewHandler(nil, nil)
	s.loginAccountLimiter = newRateLimiter(1, time.Minute)
	// Deliberately loose so only the account limiter's behavior is under
	// test here.
	s.loginIPLimiter = newRateLimiter(1000, time.Minute)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.rateLimitLogin(next)

	// Valid JSON body with one trailing garbage byte -- json.Unmarshal
	// errors on this; json.NewDecoder(...).Decode (what Login() actually
	// uses) does not.
	bodyWithTrailingGarbage := `{"email":"victim@example.com","password":"guess"}X`

	req1 := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(bodyWithTrailingGarbage))
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("request 1 (trailing garbage, still under limit): want 200 passthrough, got %d", rec1.Code)
	}

	// Second request, same account, identical trailing-garbage shape: the
	// account limiter (limit=1) must now be tripped. Before the fix, the
	// account key was never extracted for either request (Unmarshal always
	// errored), so this would incorrectly reach next() again too.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(bodyWithTrailingGarbage))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("request 2 (same account, trailing garbage, over limit): want 429, got %d -- account limiter was bypassed via trailing-data parser mismatch", rec2.Code)
	}
}
