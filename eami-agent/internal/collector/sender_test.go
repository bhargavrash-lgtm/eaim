// sender_test.go — eami-agent/internal/collector
// B-072 — tests for Sender's CA-trust handling. Uses a real
// httptest.NewTLSServer (a genuine self-signed cert + genuine TLS
// handshake), not a mock -- this is specifically testing whether a real
// crypto/tls handshake succeeds or fails, which a mocked transport
// couldn't exercise meaningfully.
package collector

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeCert extracts the DER-encoded certificate a real httptest.Server
// presents, re-encodes it as PEM, and writes it to a temp file -- matching
// the shape an installer would place on disk (see appliance/README.md's
// "TLS certificates for eami-collector" section).
func writeCert(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	block := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

var acceptAnyHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestNew_NoCACertPath_UnchangedFromPreB073Behavior(t *testing.T) {
	s, err := New(Config{URL: "http://example.invalid", TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("New() with no CACertPath: unexpected error: %v", err)
	}
	if s.client.Transport != nil {
		t.Error("client.Transport should be nil (default) when CACertPath is unset")
	}
}

func TestNew_ValidCACertPath_TrustsTheServer(t *testing.T) {
	srv := httptest.NewTLSServer(acceptAnyHandler)
	defer srv.Close()

	certPath := writeCert(t, srv)
	s, err := New(Config{URL: srv.URL, TimeoutSeconds: 5, CACertPath: certPath})
	if err != nil {
		t.Fatalf("New() with a valid CA cert: unexpected error: %v", err)
	}

	resp, err := s.client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with trusted CA: unexpected TLS/transport error: %v", err)
	}
	resp.Body.Close()
}

// TestNew_WithoutCACertPath_UntrustedServer_HandshakeFails is the negative
// control: a real self-signed server, with NO CA configured, must fail its
// TLS handshake -- proving TestNew_ValidCACertPath_TrustsTheServer's
// success is genuinely due to the configured CA, not an accidentally
// permissive default.
func TestNew_WithoutCACertPath_UntrustedServer_HandshakeFails(t *testing.T) {
	srv := httptest.NewTLSServer(acceptAnyHandler)
	defer srv.Close()

	s, err := New(Config{URL: srv.URL, TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("New() with no CACertPath: unexpected error: %v", err)
	}

	_, err = s.client.Get(srv.URL)
	if err == nil {
		t.Fatal("GET against an untrusted self-signed server: want a TLS verification error, got nil (success)")
	}
}

func TestNew_MissingCACertFile_ReturnsError(t *testing.T) {
	_, err := New(Config{URL: "https://example.invalid", TimeoutSeconds: 5, CACertPath: filepath.Join(t.TempDir(), "does-not-exist.pem")})
	if err == nil {
		t.Fatal("New() with a nonexistent CACertPath: want an error, got nil")
	}
}

func TestNew_InvalidCACertContent_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(path, []byte("not a real certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(Config{URL: "https://example.invalid", TimeoutSeconds: 5, CACertPath: path})
	if err == nil {
		t.Fatal("New() with garbage PEM content: want an error, got nil")
	}
}
