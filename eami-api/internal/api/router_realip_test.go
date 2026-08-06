// router_realip_test.go — eami-api/internal/api
// QA-EAMI — proves the middleware.RealIP -> ClientIPFromRemoteAddr swap
// (router.go) resolves the client IP from the TCP connection only, and
// does not trust a caller-supplied X-Forwarded-For/X-Real-IP header. This
// is the exact spoofing class the deprecated RealIP middleware was
// vulnerable to.
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestClientIPFromRemoteAddr_IgnoresSpoofedForwardedForHeader(t *testing.T) {
	var gotIP string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIP = middleware.GetClientIP(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.ClientIPFromRemoteAddr(next)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Real-IP", "10.0.0.2")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotIP != "203.0.113.7" {
		t.Fatalf("GetClientIP = %q, want %q (RemoteAddr) -- a forged X-Forwarded-For/X-Real-IP header must not override it", gotIP, "203.0.113.7")
	}
}

func TestClientIPFromRemoteAddr_NoHeaders(t *testing.T) {
	var gotIP string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIP = middleware.GetClientIP(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.ClientIPFromRemoteAddr(next)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "198.51.100.9:9999"

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotIP != "198.51.100.9" {
		t.Fatalf("GetClientIP = %q, want %q", gotIP, "198.51.100.9")
	}
}
