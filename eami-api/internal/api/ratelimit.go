package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// ── Rate limiting (B-070) ───────────────────────────────────────────────────
//
// rateLimiter is a small in-memory, per-key fixed-window limiter --
// deliberately hand-rolled rather than a new dependency: ADR-020 Model A
// (the only deployment model that exists today) is always a single process,
// so an in-memory limiter is the whole story, not a partial one. Originally
// introduced by B-055 as setupRateLimiter (bootstrap-only); generalized here
// for B-070 to also back login's per-IP/per-account limiting. Behavior is
// unchanged from B-055 for existing callers -- only the name and the Allow
// signature (now reporting a Retry-After duration) changed.
type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{attempts: make(map[string][]time.Time), limit: limit, window: window}
}

// Allow records an attempt for key and reports whether it's within the
// window's limit. When it is not, retryAfter is the duration until the
// window's oldest surviving attempt ages out, suitable for a Retry-After
// response header -- rounded up to the nearest second so callers never send
// "Retry-After: 0" while still technically over the limit.
//
// Not cryptographically precise (a caller could spoof source IPs on some
// networks) -- it exists to blunt casual/automated guessing, not as a sole
// defense. For bootstrap specifically, the setup token's own 256-bit entropy
// is the primary defense; for login, this is genuinely the primary
// brute-force defense (see the standing feedback memory on rate-limit
// threshold reasoning if one exists).
// A non-positive limit fails closed (every request denied) rather than
// panicking on the kept[0] index below -- config.go's validate() already
// rejects a non-positive configured limit at startup, but Allow() stays
// defensive independent of that, since a caller could still construct one
// directly.
func (rl *rateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if rl.limit <= 0 {
		return false, rl.window
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)
	var kept []time.Time
	for _, t := range rl.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.limit {
		rl.attempts[key] = kept
		oldest := kept[0]
		retryAfter = oldest.Add(rl.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}
	rl.attempts[key] = append(kept, now)
	return true, 0
}

// Known, accepted limitation: attempts never removes a key once created,
// even after every timestamp under it has expired -- a distinct source IP
// or account key that hits a limited route once leaves a small permanent
// map entry for the rest of the process's lifetime. Not fixed here: these
// are a single appliance's routes with no realistic reason to see sustained
// traffic from many thousands of distinct keys (this isn't a multi-tenant
// SaaS endpoint), and a correct fix needs a background sweep goroutine --
// real added complexity for a bound this narrow-purpose process is very
// unlikely to ever hit in practice. Same accepted tradeoff B-055 made for
// setupRateLimiter, now shared by every caller of this type.

// clientKey returns the caller's real TCP source IP -- see Handler()'s
// middleware.ClientIPFromRemoteAddr comment for why caller-supplied
// X-Forwarded-For/X-Real-IP headers are never trusted here.
func clientKey(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// setRetryAfter sets the Retry-After header from a rate-limit duration,
// rounding up to a whole number of seconds per RFC 9110 §10.2.3 (an integer
// seconds value, not a fractional one).
func setRetryAfter(w http.ResponseWriter, d time.Duration) {
	secs := int(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}
