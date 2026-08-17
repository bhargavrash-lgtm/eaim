package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
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

// clientKey returns the caller's real source IP for rate-limiting purposes.
//
// B-071 changed this from B-070's original behavior: Handler()'s own
// middleware.ClientIPFromRemoteAddr (used for every OTHER purpose in this
// package) still deliberately ignores X-Forwarded-For/X-Real-IP, per
// B-047's original reasoning -- "eami-api is reachable both via eami-ui's
// nginx proxy ... and directly on its published port -- there is no single
// trusted ingress." B-071's own docker-compose changes are what invalidate
// that premise specifically for rate limiting: eami-api no longer
// publishes a port at all, so eami-proxy (Caddy, the new TLS-terminating
// edge) is the only path a request from OUTSIDE the docker network can
// take to reach this process.
//
// That is not, by itself, enough to trust X-Forwarded-For unconditionally
// -- a security-review finding caught this: docker-compose.prod.yml has no
// network segmentation, so any OTHER container on the same compose network
// (e.g. eami-collector, which has its own real external-facing attack
// surface via endpoint-agent report ingestion) can still reach
// eami-api:8081 directly by service name and send an arbitrary
// X-Forwarded-For value, defeating login/setup-wizard rate limiting via a
// completely unrelated compromise. trustedProxyPeer (below) closes this:
// X-Forwarded-For is trusted ONLY when the request's actual TCP peer
// resolves to eami-proxy's own address, verified per-request via DNS --
// any other caller (including one on the same internal network) falls
// back to the strict RemoteAddr-based behavior below, unable to spoof
// anything.
//
// Takes the LAST comma-separated entry in X-Forwarded-For, not the first:
// Caddy is the outermost hop (nothing sits in front of it), so it always
// appends the real, directly-observed TCP peer's address as its own entry
// -- correct whether Caddy replaces or appends to an inbound header, and
// safe against a malicious client prepending a fake leading entry, since
// only the trailing entry is ever Caddy's own. Verified empirically during
// B-071's own live verification, not just asserted -- see BUILT.md.
//
// Falls back to middleware.GetClientIP/RemoteAddr when the header is
// absent, or the peer isn't eami-proxy (e.g. a request reaching eami-api
// directly over the internal docker network with no proxy in front at
// all, such as manual `docker compose exec` debugging, or the
// same-network-container scenario above) -- this fallback path behaves
// exactly as it did before B-071.
func clientKey(r *http.Request) string {
	// Header presence checked BEFORE trustedProxyPeer, not after -- the
	// latter does a real DNS lookup, and there is nothing to validate
	// trust for when the header is absent anyway. Every request without
	// X-Forwarded-For (the common case for any direct-to-eami-api caller,
	// and every existing test that doesn't specifically exercise this
	// path) skips the lookup entirely.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" && trustedProxyPeer(r.RemoteAddr) {
		parts := strings.Split(xff, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if last != "" {
			return last
		}
	}
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// trustedProxyPeer is a package var (not a plain function) so tests can
// substitute it -- "eami-proxy" only resolves inside the real
// docker-compose network, never in a Go test sandbox, matching this
// codebase's established test-seam convention (e.g. eami-gateway's
// tokenUsageWriteFunc).
var trustedProxyPeer = defaultTrustedProxyPeer

// defaultTrustedProxyPeer reports whether remoteAddr (an r.RemoteAddr
// value, "ip:port") is eami-proxy's own container. Resolved fresh via DNS
// on every call rather than cached: this only guards the low-frequency
// login/setup-wizard routes (not a hot path), so the extra lookup's cost
// is negligible, and a fresh lookup can never go stale if eami-proxy is
// ever recreated with a new address. A resolution failure fails closed
// (returns false, same as "not eami-proxy") -- if eami-proxy's own name
// can't even be resolved, there is nothing to trust.
func defaultTrustedProxyPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ips, err := net.LookupIP("eami-proxy")
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.String() == host {
			return true
		}
	}
	return false
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
