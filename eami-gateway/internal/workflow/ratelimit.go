package workflow

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eami/gateway/internal/identity"
)

// rateLimiter is a small in-memory, per-key fixed-window limiter guarding
// POST /v1/gateway/workflows/{workflowId}/run (B-070). Deliberately
// duplicated from eami-api's identical internal/api.rateLimiter rather than
// shared: eami-gateway and eami-api are separate Go modules (confirmed
// during B-044's own routing work -- neither can import the other's
// internal packages regardless of export status), and this repo's own
// established precedent for this exact situation (B-025, B-044) is a small
// deliberate duplicate rather than new shared infrastructure. ADR-020 Model
// A (the only deployment model that exists today) is always a single
// process, so in-memory state is correct here for the same reason it is in
// eami-api.
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
// response header.
//
// A non-positive limit fails closed (every request denied) rather than
// panicking on the kept[0] index below -- config.go's validate() already
// rejects a non-positive configured limit at startup, but Allow() stays
// defensive independent of that, since a caller could still construct one
// directly (as this package's own tests do).
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

// setRetryAfter sets the Retry-After header from a rate-limit duration,
// rounding up to a whole number of seconds per RFC 9110 §10.2.3.
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

// RateLimitRunMiddleware wraps an HandleRun-style http.HandlerFunc with
// per-agent rate limiting (B-070). It duplicates two things HandleRun
// already does -- validating the bearer JWT via identity.Manager.Validate,
// and resolving the agent name to its registry record via resolver -- both
// stateless, side-effect-free reads, purely to learn the calling agent's
// real identity before HandleRun runs. HandleRun itself is never modified:
//   - missing/invalid auth, or an agent name that doesn't resolve, is NOT
//     rejected here; it passes through unchanged so HandleRun produces its
//     own real 401/403 with its own error text, avoiding two slightly
//     different auth-error code paths.
//   - only a request whose token validates, whose agent resolves, AND
//     whose agent is over its limit is intercepted, with a 429 +
//     Retry-After.
//
// The rate-limit key is the resolved agent's registry UUID (agentRec.ID),
// NOT the raw JWT claims.Subject (an agent name). Agent names are unique
// only per-org (schema.sql: UNIQUE (org_id, name)), not globally -- keying
// on the bare name would let two different orgs' identically-named agents
// (e.g. both called "researcher") share one rate-limit bucket, so one
// org's agent could exhaust the other's quota. This is the same class of
// cross-org collision B-042's registry.LookupByNameAndOrg was introduced
// to close for the token-revoke path; the fix here is analogous but
// simpler, since a resolved *registry.AgentRecord already carries a real
// globally-unique primary key.
func RateLimitRunMiddleware(idm *identity.Manager, resolver AgentResolver, limit int, window time.Duration, next http.HandlerFunc) http.HandlerFunc {
	limiter := newRateLimiter(limit, window)
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			if claims, err := idm.Validate(token); err == nil {
				agentName := strings.TrimPrefix(claims.Subject, "agent:")
				if agentRec, err := resolver.LookupByName(r.Context(), agentName); err == nil {
					if ok, retryAfter := limiter.Allow(agentRec.ID); !ok {
						setRetryAfter(w, retryAfter)
						http.Error(w, "too many workflow-run requests for this agent -- try again later", http.StatusTooManyRequests)
						return
					}
				}
			}
		}
		next(w, r)
	}
}
