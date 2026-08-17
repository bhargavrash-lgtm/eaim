// ratelimit_clientkey_test.go — eami-api/internal/api
// B-071 — white-box tests for clientKey()'s new X-Forwarded-For handling.
// package api (not api_test) because clientKey/trustedProxyPeer are
// unexported.
//
// Does NOT touch or duplicate router_realip_test.go's coverage: that file
// tests the GLOBAL middleware.ClientIPFromRemoteAddr (router.go), which
// B-071 left completely unmodified and which still ignores
// X-Forwarded-For for every other consumer in this package. clientKey()
// is a separate, narrowly-scoped function used only by the rate limiters.
//
// Every test that wants X-Forwarded-For actually trusted overrides the
// trustedProxyPeer package var and restores it via t.Cleanup -- "eami-proxy"
// never resolves in a Go test sandbox, so the real defaultTrustedProxyPeer
// would fail closed (return false) for all of these otherwise.
//
// Run: go test ./internal/api/... -run TestClientKey -v
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

// trustAllPeers overrides trustedProxyPeer for the duration of the calling
// test, restoring the real function on cleanup -- t.Cleanup, not a plain
// defer, per this package's own test-hygiene convention elsewhere.
func trustAllPeers(t *testing.T) {
	t.Helper()
	orig := trustedProxyPeer
	trustedProxyPeer = func(string) bool { return true }
	t.Cleanup(func() { trustedProxyPeer = orig })
}

func TestClientKey_TrustsXForwardedFor_SingleEntry(t *testing.T) {
	trustAllPeers(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "172.18.0.5:44444" // eami-proxy's own docker-network address
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	if got := clientKey(req); got != "203.0.113.7" {
		t.Fatalf("clientKey() = %q, want %q (the single X-Forwarded-For entry)", got, "203.0.113.7")
	}
}

// TestClientKey_MultipleEntries_TrustsOnlyLastEntry is the regression test
// for the exact spoofing scenario this design must resist: a malicious
// client prepends a fake leading entry, but only the LAST entry -- the one
// Caddy itself appends as the outermost hop -- is ever trusted.
func TestClientKey_MultipleEntries_TrustsOnlyLastEntry(t *testing.T) {
	trustAllPeers(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "172.18.0.5:44444"
	// "1.2.3.4" is attacker-supplied (sent directly to Caddy); "203.0.113.9"
	// is what Caddy itself appended after observing the real TCP peer.
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")

	if got := clientKey(req); got != "203.0.113.9" {
		t.Fatalf("clientKey() = %q, want %q (only the trailing, Caddy-appended entry) -- a spoofed leading entry must not be trusted", got, "203.0.113.9")
	}
}

func TestClientKey_TrimsWhitespaceAroundEntries(t *testing.T) {
	trustAllPeers(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "172.18.0.5:44444"
	req.Header.Set("X-Forwarded-For", "1.2.3.4,  203.0.113.9  ")

	if got := clientKey(req); got != "203.0.113.9" {
		t.Fatalf("clientKey() = %q, want %q (trimmed)", got, "203.0.113.9")
	}
}

// TestClientKey_FallsBackToRemoteAddr_WhenHeaderAbsent covers a request
// reaching eami-api with no proxy in front at all (e.g. internal docker-
// network debugging via `docker compose exec`) -- behaves exactly as it
// did before B-071. Routed through middleware.ClientIPFromRemoteAddr
// first, matching Handler()'s real middleware chain (GetClientIP reads a
// context value only that middleware populates -- without it, clientKey's
// final fallback would return the raw "ip:port" RemoteAddr string, a
// test-setup gap rather than anything B-071 changed).
func TestClientKey_FallsBackToRemoteAddr_WhenHeaderAbsent(t *testing.T) {
	trustAllPeers(t)
	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = clientKey(r)
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.ClientIPFromRemoteAddr(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "198.51.100.9:9999"
	// No X-Forwarded-For set at all.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got != "198.51.100.9" {
		t.Fatalf("clientKey() = %q, want %q (RemoteAddr fallback)", got, "198.51.100.9")
	}
}

// TestClientKey_DifferentForwardedForValues_ProduceDifferentKeys proves
// the actual functional payoff of this fix: two different real clients
// (as Caddy would report them) are now distinguishable again for rate
// limiting, rather than collapsing into one shared bucket keyed on Caddy's
// own internal container IP.
func TestClientKey_DifferentForwardedForValues_ProduceDifferentKeys(t *testing.T) {
	trustAllPeers(t)
	reqA := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	reqA.RemoteAddr = "172.18.0.5:1"
	reqA.Header.Set("X-Forwarded-For", "203.0.113.7")

	reqB := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	reqB.RemoteAddr = "172.18.0.5:2" // same Caddy container, different real client
	reqB.Header.Set("X-Forwarded-For", "203.0.113.8")

	keyA, keyB := clientKey(reqA), clientKey(reqB)
	if keyA == keyB {
		t.Fatalf("clientKey() produced the same key (%q) for two different X-Forwarded-For values -- per-IP rate limiting would collapse into one shared bucket", keyA)
	}
}

// TestClientKey_UntrustedPeer_IgnoresXForwardedFor is the regression test
// for a real security-review finding: closing eami-api's published port
// only blocks access from OUTSIDE the docker network -- any other
// container on the same compose network (e.g. eami-collector, which has
// its own real external-facing attack surface) can still reach eami-api
// directly by service name and send an arbitrary X-Forwarded-For value.
// trustedProxyPeer returning false (simulating "this isn't really
// eami-proxy") must mean the header is completely ignored, not just
// deprioritized -- falling back to the strict RemoteAddr-based behavior,
// unspoofable by the caller.
func TestClientKey_UntrustedPeer_IgnoresXForwardedFor(t *testing.T) {
	orig := trustedProxyPeer
	trustedProxyPeer = func(string) bool { return false }
	t.Cleanup(func() { trustedProxyPeer = orig })

	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = clientKey(r)
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.ClientIPFromRemoteAddr(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "172.19.0.9:5555" // some OTHER container, not eami-proxy
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got != "172.19.0.9" {
		t.Fatalf("clientKey() = %q, want %q (RemoteAddr, ignoring the spoofed X-Forwarded-For) -- an untrusted peer must not be able to set its own rate-limit key", got, "172.19.0.9")
	}
}

// TestDefaultTrustedProxyPeer_UnresolvableName_FailsClosed exercises the
// real defaultTrustedProxyPeer (not an override) -- "eami-proxy" never
// resolves in this test sandbox, so this proves the fail-closed behavior
// documented on that function is real, not just asserted.
func TestDefaultTrustedProxyPeer_UnresolvableName_FailsClosed(t *testing.T) {
	if defaultTrustedProxyPeer("172.18.0.5:44444") {
		t.Fatal(`defaultTrustedProxyPeer(...) = true, want false -- "eami-proxy" cannot resolve in a test sandbox, so nothing should be trusted`)
	}
}
