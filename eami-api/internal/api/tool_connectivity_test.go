// tool_connectivity_test.go — eami-api/internal/api
// Unit tests for B-023: TestTool's real connectivity check.
//
// Run: go test -count=1 ./internal/api/... -run 'TestClassify|TestTestRESTTool|TestTestDatabaseTool|TestToolConnectivity'

package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/eami/api/internal/toolcreds"
)

// ─── classifyPgConnectError: pure function, no live Postgres needed ───────

func TestClassifyPgConnectError_AuthFailure(t *testing.T) {
	err := &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
	reason, _ := classifyPgConnectError(err)
	if reason != reasonAuthFailed {
		t.Errorf("SQLSTATE 28P01 (invalid_password): want reason %q, got %q", reasonAuthFailed, reason)
	}
}

func TestClassifyPgConnectError_AuthorizationSpecInvalid(t *testing.T) {
	err := &pgconn.PgError{Code: "28000", Message: "invalid authorization specification"}
	reason, _ := classifyPgConnectError(err)
	if reason != reasonAuthFailed {
		t.Errorf("SQLSTATE 28000: want reason %q, got %q", reasonAuthFailed, reason)
	}
}

func TestClassifyPgConnectError_OtherPgError_Unreachable(t *testing.T) {
	err := &pgconn.PgError{Code: "3D000", Message: "database does not exist"}
	reason, _ := classifyPgConnectError(err)
	if reason != reasonUnreachable {
		t.Errorf("non-28xxx PgError: want reason %q, got %q", reasonUnreachable, reason)
	}
}

func TestClassifyPgConnectError_DeadlineExceeded(t *testing.T) {
	reason, detail := classifyPgConnectError(context.DeadlineExceeded)
	if reason != reasonUnreachable {
		t.Errorf("want reason %q, got %q", reasonUnreachable, reason)
	}
	if !strings.Contains(detail, "timed out") {
		t.Errorf("want detail to mention timeout, got %q", detail)
	}
}

func TestClassifyPgConnectError_GenericError_Unreachable(t *testing.T) {
	reason, _ := classifyPgConnectError(errors.New("connection refused"))
	if reason != reasonUnreachable {
		t.Errorf("want reason %q, got %q", reasonUnreachable, reason)
	}
}

// ─── classifyHTTPError ──────────────────────────────────────────────────────

func TestClassifyHTTPError_DeadlineExceeded(t *testing.T) {
	got := classifyHTTPError(context.DeadlineExceeded)
	if !strings.Contains(got, "timed out") {
		t.Errorf("want timeout description, got %q", got)
	}
}

type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string   { return "fake timeout" }
func (fakeTimeoutError) Timeout() bool   { return true }
func (fakeTimeoutError) Temporary() bool { return true }

func TestClassifyHTTPError_NetTimeout(t *testing.T) {
	var _ net.Error = fakeTimeoutError{} // compile-time interface check
	got := classifyHTTPError(fakeTimeoutError{})
	if !strings.Contains(got, "timed out") {
		t.Errorf("want timeout description, got %q", got)
	}
}

func TestClassifyHTTPError_Generic(t *testing.T) {
	got := classifyHTTPError(errors.New("connection refused"))
	if got == "" {
		t.Error("want a non-empty description")
	}
}

// ─── testRESTTool: real HTTP round-trips against httptest servers ─────────
//
// These use unrestrictedDial (plain net.Dialer, no blocking) rather than
// safeDialContext, since httptest servers bind to 127.0.0.1 -- exactly what
// safeDialContext exists to block in production. safeDialContext itself is
// tested separately below (TestSafeDialContext_*/TestIsBlockedTestTarget),
// and TestTestTool_* in tools_testtool_test.go proves the production
// TestTool handler wires the real safeDialContext, not the unrestricted one.

// unrestrictedDial is a plain, unfiltered dialer for tests that
// legitimately target loopback (httptest servers, local listeners) --
// standing in for what production always replaces with safeDialContext.
func unrestrictedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func TestTestRESTTool_Success_Connected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	url := srv.URL
	outcome := testRESTTool(context.Background(), &url, "api_key", ToolCredentials{APIKey: "sk-valid"}, unrestrictedDial)
	if !outcome.Connected {
		t.Fatalf("want Connected=true, got outcome=%+v", outcome)
	}
}

func TestTestRESTTool_Unauthorized_AuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	url := srv.URL
	outcome := testRESTTool(context.Background(), &url, "api_key", ToolCredentials{APIKey: "sk-wrong"}, unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false for a 401 response")
	}
	if outcome.Reason != reasonAuthFailed {
		t.Errorf("want reason %q, got %q", reasonAuthFailed, outcome.Reason)
	}
}

func TestTestRESTTool_Forbidden_AuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	url := srv.URL
	outcome := testRESTTool(context.Background(), &url, "api_key", ToolCredentials{APIKey: "sk-wrong"}, unrestrictedDial)
	if outcome.Reason != reasonAuthFailed {
		t.Errorf("want reason %q, got %q", reasonAuthFailed, outcome.Reason)
	}
}

func TestTestRESTTool_ServerErrorStillCountsAsConnected(t *testing.T) {
	// A 500 still proves the server is reachable and responding -- the
	// connectivity test isn't asserting the endpoint is bug-free.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	url := srv.URL
	outcome := testRESTTool(context.Background(), &url, "api_key", ToolCredentials{}, unrestrictedDial)
	if !outcome.Connected {
		t.Fatalf("want Connected=true for a completed (if 500) response, got outcome=%+v", outcome)
	}
}

func TestTestRESTTool_Timeout_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	url := srv.URL
	outcome := testRESTTool(ctx, &url, "api_key", ToolCredentials{}, unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false when the request times out")
	}
	if outcome.Reason != reasonUnreachable {
		t.Errorf("want reason %q, got %q", reasonUnreachable, outcome.Reason)
	}
}

func TestTestRESTTool_ConnectionRefused_Unreachable(t *testing.T) {
	// Bind and immediately close a listener to get a URL nothing is
	// listening on -- a real, deterministic "connection refused".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	url := "http://" + addr
	outcome := testRESTTool(context.Background(), &url, "api_key", ToolCredentials{}, unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false for a refused connection")
	}
	if outcome.Reason != reasonUnreachable {
		t.Errorf("want reason %q, got %q", reasonUnreachable, outcome.Reason)
	}
}

func TestTestRESTTool_MissingBaseURL_Misconfigured(t *testing.T) {
	outcome := testRESTTool(context.Background(), nil, "api_key", ToolCredentials{APIKey: "sk-x"}, unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false when base_url is missing")
	}
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
}

func TestTestRESTTool_BasicAuthType_Misconfigured(t *testing.T) {
	url := "http://example.invalid"
	outcome := testRESTTool(context.Background(), &url, "basic", ToolCredentials{}, unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false for auth_type=basic (no username/password fields exist)")
	}
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
}

func TestTestRESTTool_NoCredentials_StillAttempted(t *testing.T) {
	// A tool with no stored credentials is still testable -- the request
	// is made without an Authorization header, and the remote's own
	// response (200 here) determines the outcome.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("expected no Authorization header, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	url := srv.URL
	outcome := testRESTTool(context.Background(), &url, "api_key", ToolCredentials{}, unrestrictedDial)
	if !outcome.Connected {
		t.Fatalf("want Connected=true, got outcome=%+v", outcome)
	}
}

// ─── testDatabaseTool ────────────────────────────────────────────────────

func TestTestDatabaseTool_EmptyConnectionString_Misconfigured(t *testing.T) {
	outcome := testDatabaseTool(context.Background(), "", unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false for an empty connection string")
	}
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
}

func TestTestDatabaseTool_UnreachableHost_Unreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// A port nothing listens on, on localhost -- fails fast with
	// connection-refused rather than a real network timeout, keeping the
	// test quick.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	outcome := testDatabaseTool(ctx, "postgres://user:pass@"+addr+"/db?connect_timeout=1", unrestrictedDial)
	if outcome.Connected {
		t.Fatal("want Connected=false for an unreachable database")
	}
	if outcome.Reason != reasonUnreachable {
		t.Errorf("want reason %q, got %q", reasonUnreachable, outcome.Reason)
	}
}

// ─── safeDialContext / isBlockedTestTarget: the SSRF guard itself ──────────

func TestIsBlockedTestTarget(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"IPv4 loopback", "127.0.0.1", true},
		{"IPv6 loopback", "::1", true},
		{"AWS/GCP metadata (link-local v4)", "169.254.169.254", true},
		{"link-local v6", "fe80::1", true},
		{"RFC1918 10.x", "10.0.0.5", true},
		{"RFC1918 172.16.x", "172.16.0.5", true},
		{"RFC1918 192.168.x", "192.168.1.5", true},
		{"IPv6 ULA (covers AWS fd00:ec2::254)", "fd00:ec2::254", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"public v4", "8.8.8.8", false},
		{"public v6", "2001:4860:4860::8888", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) returned nil", tt.ip)
			}
			if got := isBlockedTestTarget(ip); got != tt.want {
				t.Errorf("isBlockedTestTarget(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestSafeDialContext_BlocksLoopbackIPLiteral(t *testing.T) {
	_, err := safeDialContext(context.Background(), "tcp", "127.0.0.1:80")
	if err == nil {
		t.Fatal("want an error dialing a loopback IP literal, got nil")
	}
}

func TestSafeDialContext_BlocksLoopbackByHostname(t *testing.T) {
	// "localhost" resolves to 127.0.0.1/::1 -- proves the DNS-resolution
	// branch is checked too, not just IP literals.
	_, err := safeDialContext(context.Background(), "tcp", "localhost:80")
	if err == nil {
		t.Fatal("want an error dialing localhost by hostname, got nil")
	}
}

func TestSafeDialContext_BlocksLinkLocalMetadataAddress(t *testing.T) {
	_, err := safeDialContext(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("want an error dialing the cloud metadata address, got nil")
	}
}

func TestSafeDialContext_BlocksPrivateRFC1918(t *testing.T) {
	_, err := safeDialContext(context.Background(), "tcp", "10.1.2.3:5432")
	if err == nil {
		t.Fatal("want an error dialing an RFC1918 private address, got nil")
	}
}

func TestSafeDialContext_MalformedAddr_ReturnsError(t *testing.T) {
	_, err := safeDialContext(context.Background(), "tcp", "not-a-valid-addr")
	if err == nil {
		t.Fatal("want an error for an address with no port, got nil")
	}
}

// ─── testToolConnectivity: dispatch + decrypt handling ─────────────────────

func TestToolConnectivity_MCPType_Misconfigured(t *testing.T) {
	outcome := testToolConnectivity(context.Background(), "mcp", "", nil, nil, nil)
	if outcome.Connected {
		t.Fatal("want Connected=false for mcp-type tools")
	}
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
}

func TestToolConnectivity_UnknownType_Misconfigured(t *testing.T) {
	outcome := testToolConnectivity(context.Background(), "carrier_pigeon", "", nil, nil, nil)
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
}

func TestToolConnectivity_CredentialsPresentButNoCipher_Misconfigured(t *testing.T) {
	// A stored ciphertext blob but no cipher configured at test-time (e.g.
	// TOOL_CREDENTIALS_ENCRYPTION_KEY unset after tools were created) must
	// not panic and must not be treated as "no credentials".
	outcome := testToolConnectivity(context.Background(), "rest_api", "api_key", strPtr("http://example.invalid"), []byte("not-empty"), nil)
	if outcome.Connected {
		t.Fatal("want Connected=false when credentials exist but can't be decrypted")
	}
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
}

func TestToolConnectivity_DecryptFailure_WrongKey_Misconfigured_NoLeak(t *testing.T) {
	// First production caller of toolcreds.Decrypt -- confirm a wrong/
	// rotated key is a clean error path, not a panic, and that the
	// underlying decrypt error text (and definitely not any plaintext)
	// never appears in the outcome.
	c1, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := c1.Encrypt([]byte(`{"api_key":"sk-should-never-appear"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	wrongKeyHex := strings.Repeat("bb", 32)
	c2, err := toolcreds.NewCipher(wrongKeyHex)
	if err != nil {
		t.Fatalf("NewCipher (wrong key): %v", err)
	}

	outcome := testToolConnectivity(context.Background(), "rest_api", "api_key", strPtr("http://example.invalid"), sealed, c2)
	if outcome.Connected {
		t.Fatal("want Connected=false when decryption fails")
	}
	if outcome.Reason != reasonMisconfigured {
		t.Errorf("want reason %q, got %q", reasonMisconfigured, outcome.Reason)
	}
	full := outcome.Reason + " " + outcome.Detail
	if strings.Contains(full, "sk-should-never-appear") {
		t.Fatal("decrypted plaintext must never appear in the outcome")
	}
	if msg := outcome.errorMessage(); msg != nil && strings.Contains(*msg, "sk-should-never-appear") {
		t.Fatal("decrypted plaintext must never appear in the rendered error message")
	}
}

func TestToolConnectivity_ValidCredentials_RealRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-real-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cipher, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Encrypt([]byte(`{"api_key":"sk-real-key"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	url := srv.URL
	outcome := testToolConnectivityWithDialer(context.Background(), "rest_api", "api_key", &url, sealed, cipher, unrestrictedDial)
	if !outcome.Connected {
		t.Fatalf("want Connected=true with valid decrypted credentials, got outcome=%+v", outcome)
	}
}

func TestToolConnectivity_DBStatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		outcome toolTestOutcome
		want    string
	}{
		{"connected", toolTestOutcome{Connected: true}, "connected"},
		{"auth-failed", toolTestOutcome{Reason: reasonAuthFailed}, "degraded"},
		{"unreachable", toolTestOutcome{Reason: reasonUnreachable}, "disconnected"},
		{"misconfigured", toolTestOutcome{Reason: reasonMisconfigured}, "disconnected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.outcome.dbStatus(); got != tt.want {
				t.Errorf("dbStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolConnectivity_ErrorMessage_NilOnSuccess(t *testing.T) {
	o := toolTestOutcome{Connected: true}
	if msg := o.errorMessage(); msg != nil {
		t.Errorf("want nil error message on success, got %q", *msg)
	}
}

func strPtr(s string) *string { return &s }
