// tools_testtool_test.go — eami-api/internal/api
// Handler-level tests for POST /v1/gateway/tools/{toolId}/test (B-023: real
// connectivity check replacing the old synthetic "always connected" stub).
// Reuses toolsTestEnv/fakeToolStore from tools_test.go (same package).
//
// Run: go test -count=1 ./internal/api/... -run TestTestTool

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/eami/api/internal/toolcreds"
)

func (e *toolsTestEnv) postTest(t *testing.T, token, toolID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/gateway/tools/"+toolID+"/test", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

type testToolResp struct {
	Success   bool    `json:"success"`
	LatencyMs int64   `json:"latency_ms"`
	Error     *string `json:"error"`
}

func decodeTestToolResp(t *testing.T, resp *http.Response) testToolResp {
	t.Helper()
	defer resp.Body.Close()
	var out testToolResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func TestTestTool_ValidReachableCredentials_ReportsConnected(t *testing.T) {
	// Acceptance criterion 1: a real network round-trip against a live
	// (test) server with correct credentials reports connected.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()

	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	cipher, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Encrypt([]byte(`{"api_key":"sk-good"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	url := target.URL
	env.store.getForTestRow = toolTestRow{
		Type: "rest_api", AuthType: "api_key", BaseURL: &url, CredentialsEncrypted: sealed,
	}

	toolID := uuid.New().String()
	resp := env.postTest(t, token, toolID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	got := decodeTestToolResp(t, resp)
	if !got.Success {
		t.Fatalf("want success=true, got %+v", got)
	}
	if got.Error != nil {
		t.Errorf("want nil error on success, got %q", *got.Error)
	}
	if !env.store.markTestedCalled || env.store.markTestedStatus != "connected" {
		t.Errorf("want MarkToolTested called with status=connected, got called=%v status=%q",
			env.store.markTestedCalled, env.store.markTestedStatus)
	}
}

func TestTestTool_InvalidCredentials_ReportsAuthFailed(t *testing.T) {
	// Acceptance criterion 2: invalid credentials -> a distinct
	// auth-failed-type status, not a generic failure.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()

	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	cipher, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Encrypt([]byte(`{"api_key":"sk-wrong"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	url := target.URL
	env.store.getForTestRow = toolTestRow{
		Type: "rest_api", AuthType: "api_key", BaseURL: &url, CredentialsEncrypted: sealed,
	}

	resp := env.postTest(t, token, uuid.New().String())
	got := decodeTestToolResp(t, resp)
	if got.Success {
		t.Fatal("want success=false for rejected credentials")
	}
	if got.Error == nil || !strings.HasPrefix(*got.Error, "auth-failed") {
		t.Fatalf("want an auth-failed-prefixed error, got %+v", got)
	}
	if env.store.markTestedStatus != "degraded" {
		t.Errorf("want persisted status=degraded for auth-failed, got %q", env.store.markTestedStatus)
	}
}

func TestTestTool_ConnectionRefused_ReportsUnreachable_NotAHangOrCrash(t *testing.T) {
	// Acceptance criterion 3: an unreachable target reports unreachable,
	// not a hang or a crash. This specific test uses a refused connection
	// (fast, deterministic) rather than a target that actually sleeps past
	// toolTestTimeout -- the 8s timeout bound itself, as a real elapsed-time
	// mechanism, is exercised by tool_connectivity_test.go's
	// TestTestRESTTool_Timeout_Unreachable (with an artificially shortened
	// context, to keep that test fast too); nothing in this suite lets the
	// real 8s constant run to completion, which is a deliberate tradeoff
	// against test suite runtime, not an oversight.
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	unreachableURL := "http://127.0.0.1:1" // reserved/unused port, refuses immediately
	env.store.getForTestRow = toolTestRow{
		Type: "rest_api", AuthType: "api_key", BaseURL: &unreachableURL,
	}

	resp := env.postTest(t, token, uuid.New().String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 (the test itself completed, even though the target didn't), got %d", resp.StatusCode)
	}
	got := decodeTestToolResp(t, resp)
	if got.Success {
		t.Fatal("want success=false for an unreachable target")
	}
	if got.Error == nil || !strings.HasPrefix(*got.Error, "unreachable") {
		t.Fatalf("want an unreachable-prefixed error, got %+v", got)
	}
	if env.store.markTestedStatus != "disconnected" {
		t.Errorf("want persisted status=disconnected for unreachable, got %q", env.store.markTestedStatus)
	}
}

func TestTestTool_NoCredentialsConfigured_BehavesSensibly(t *testing.T) {
	// Acceptance criterion 4: a tool with no stored credentials doesn't
	// crash and reports something meaningful -- here, a real reachable
	// server with no auth requirement reports connected even though the
	// tool has never had credentials submitted.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	url := target.URL
	env.store.getForTestRow = toolTestRow{
		Type: "rest_api", AuthType: "api_key", BaseURL: &url, CredentialsEncrypted: nil,
	}

	resp := env.postTest(t, token, uuid.New().String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	got := decodeTestToolResp(t, resp)
	if !got.Success {
		t.Fatalf("want success=true for a reachable target with no auth required, got %+v", got)
	}
}

func TestTestTool_MCPType_ReportsMisconfigured_NotFabricatedConnected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	env.store.getForTestRow = toolTestRow{Type: "mcp", AuthType: "basic"}

	resp := env.postTest(t, token, uuid.New().String())
	got := decodeTestToolResp(t, resp)
	if got.Success {
		t.Fatal("want success=false for an mcp-type tool -- this must never claim a fabricated connection")
	}
	if got.Error == nil || !strings.HasPrefix(*got.Error, "misconfigured") {
		t.Fatalf("want a misconfigured-prefixed error, got %+v", got)
	}
}

func TestTestTool_ToolNotFound_Returns404(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	env.store.getForTestErr = pgx.ErrNoRows

	resp := env.postTest(t, token, uuid.New().String())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	if env.store.markTestedCalled {
		t.Error("MarkToolTested must not be called when the tool doesn't exist")
	}
}

func TestTestTool_DecryptedCredentialsNeverLeakInResponse(t *testing.T) {
	// Acceptance criterion 5: no decrypted credential material appears in
	// the response at any point, including on failure paths.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()

	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	cipher, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	const secret = "sk-must-never-leak-anywhere"
	sealed, err := cipher.Encrypt([]byte(`{"api_key":"` + secret + `"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	url := target.URL
	env.store.getForTestRow = toolTestRow{
		Type: "rest_api", AuthType: "api_key", BaseURL: &url, CredentialsEncrypted: sealed,
	}

	resp := env.postTest(t, token, uuid.New().String())
	defer resp.Body.Close()
	body, _ := readAllBody(resp)
	if strings.Contains(body, secret) {
		t.Fatalf("response must never contain the decrypted secret, got: %s", body)
	}
}
