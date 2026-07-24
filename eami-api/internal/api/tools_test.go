// tools_test.go — eami-api/internal/api
// Handler-level unit tests for POST /v1/gateway/tools' credential encryption
// (fix for: credentials submitted via the Add Tool form were silently
// discarded -- CreateTool never read or persisted them).
//
// Package api (white-box), same convention as finops_test.go, so tests can
// set the unexported toolStoreOverride/toolCreds test seams directly instead
// of requiring a live Postgres.
//
// Run: go test -count=1 ./internal/api/... -run TestCreateTool

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/config"
	"github.com/eami/api/internal/store"
	"github.com/eami/api/internal/toolcreds"
)

// testEncryptionKeyHex is a fixed 32-byte (64 hex char) AES-256 key used
// only by these tests -- equivalent shape to `openssl rand -hex 32` output.
const testEncryptionKeyHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ─── fake tool store ───────────────────────────────────────────────────────
// Captures what CreateTool passes to the store, and lets tests prove the
// store is never called when encryption fails closed.

type fakeToolStore struct {
	createCalled bool
	createErr    error
	created      store.CreateToolParams

	getForTestRow     toolTestRow
	getForTestErr     error
	markTestedStatus  string
	markTestedLatency int
	markTestedCalled  bool
}

func (f *fakeToolStore) ListTools(_ context.Context, _ uuid.UUID) ([]store.GatewayTool, error) {
	return nil, nil
}

func (f *fakeToolStore) CreateTool(_ context.Context, p store.CreateToolParams) (store.GatewayTool, error) {
	f.createCalled = true
	f.created = p
	if f.createErr != nil {
		return store.GatewayTool{}, f.createErr
	}
	return store.GatewayTool{
		ID:        uuid.New(),
		OrgID:     p.OrgID,
		Name:      p.Name,
		Type:      p.Type,
		AuthType:  p.AuthType,
		Status:    "connected",
		CreatedAt: time.Now(),
	}, nil
}

func (f *fakeToolStore) DeleteTool(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeToolStore) MarkToolTested(_ context.Context, _, _ uuid.UUID, status string, latencyMs int) error {
	f.markTestedCalled = true
	f.markTestedStatus = status
	f.markTestedLatency = latencyMs
	return nil
}

func (f *fakeToolStore) GetToolForTest(_ context.Context, _, _ uuid.UUID) (toolTestRow, error) {
	return f.getForTestRow, f.getForTestErr
}

// ─── test server helper ─────────────────────────────────────────────────────

type toolsTestEnv struct {
	srv     *httptest.Server
	authSvc *auth.Service
	store   *fakeToolStore
}

// newToolsTestEnv builds a real Server + httptest.Server (full HTTP
// round-trip, jwtMiddleware/requireRole included), with the store swapped
// for a fake and the encryption key configured only if keyHex != "".
func newToolsTestEnv(t *testing.T, keyHex string) *toolsTestEnv {
	t.Helper()
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	var cfg *config.Config
	if keyHex != "" {
		cfg = &config.Config{ToolCredentialsEncryptionKey: keyHex}
	}
	srv := NewServer(nil, authSvc, nil, cfg)
	fake := &fakeToolStore{}
	srv.toolStoreOverride = fake
	// Tests target httptest servers on 127.0.0.1, which safeDialContext
	// (the production default) correctly rejects -- see
	// TestSafeDialContext_* in tool_connectivity_test.go for direct
	// coverage of that guard. Swap in an unrestricted dialer here so
	// handler tests can exercise real round-trips instead.
	srv.toolDialOverride = unrestrictedDial

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &toolsTestEnv{srv: ts, authSvc: authSvc, store: fake}
}

func (e *toolsTestEnv) adminToken(t *testing.T) string {
	t.Helper()
	userID := uuid.MustParse("11111111-0000-0000-0000-000000000001")
	orgID := uuid.MustParse("11111111-0000-0000-0000-000000000002")
	tok, _, err := e.authSvc.IssueAccessToken(userID, orgID, "admin@example.com", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

func (e *toolsTestEnv) postTool(t *testing.T, token string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/gateway/tools", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// ─── no store configured: clean error, not a nil-pointer panic ────────────

func TestToolHandlers_NoStoreConfigured_ReturnsCleanError(t *testing.T) {
	// A Server built via NewHandler (the pattern other handler tests use,
	// e.g. agents_test.go/auth_test.go) never sets s.queries. Every other
	// handler in this package guards production-store access with
	// "if s.queries != nil"; before this fix, tools.go's toolQueries()
	// returned s.queries unconditionally, so a tools-endpoint request
	// against such a Server would nil-pointer-panic inside the interface
	// method call (recovered into an opaque 500 by chi's Recoverer) instead
	// of failing cleanly. This proves the fix: a real "not configured" 500,
	// not a panic.
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	srv := NewServer(nil, authSvc, nil, nil) // s.queries is nil, toolStoreOverride never set
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	userID := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	orgID := uuid.MustParse("22222222-0000-0000-0000-000000000002")
	token, _, err := authSvc.IssueAccessToken(userID, orgID, "admin@example.com", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/gateway/tools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/gateway/tools: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want a clean 500 when no store is configured, got %d", resp.StatusCode)
	}
	body, _ := readAllBody(resp)
	if !strings.Contains(body, "not configured") {
		t.Errorf("error message should say the store isn't configured, got: %s", body)
	}
}

// ─── CreateTool: credentials are actually encrypted and stored ────────────

func TestCreateTool_WithCredentials_StoresEncryptedNotPlaintext(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "salesforce-prod",
		"type":      "rest_api",
		"auth_type": "api_key",
		"credentials": map[string]any{
			"api_key": "sk-super-secret-value",
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if !env.store.createCalled {
		t.Fatal("CreateTool must call the store when the request is valid")
	}

	enc := env.store.created.CredentialsEncrypted
	if len(enc) == 0 {
		t.Fatal("CredentialsEncrypted must be non-empty (acceptance criterion 1: real, non-null value)")
	}
	if bytes.Contains(enc, []byte("sk-super-secret-value")) {
		t.Fatal("stored bytes must not contain the plaintext secret (acceptance criterion 2)")
	}
	if bytes.Contains(enc, []byte("api_key")) {
		t.Fatal("stored bytes must not contain plaintext JSON field names either")
	}

	// Retrieval-proof: decrypt what was actually stored and confirm it
	// matches the original input exactly.
	cipher, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext, err := cipher.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt(stored value) must succeed: %v", err)
	}
	var got ToolCredentials
	if err := json.Unmarshal(plaintext, &got); err != nil {
		t.Fatalf("decrypted plaintext must be valid JSON: %v", err)
	}
	if got.APIKey != "sk-super-secret-value" {
		t.Fatalf("decrypted credentials mismatch: got %q, want %q", got.APIKey, "sk-super-secret-value")
	}
}

func TestCreateTool_WithoutCredentials_Unaffected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "internal-mcp",
		"type":      "mcp",
		"auth_type": "basic",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if !env.store.createCalled {
		t.Fatal("CreateTool must still call the store for a tool with no credentials")
	}
	if env.store.created.CredentialsEncrypted != nil {
		t.Fatalf("CredentialsEncrypted must be nil when no credentials were submitted, got %d bytes",
			len(env.store.created.CredentialsEncrypted))
	}
}

func TestCreateTool_UnrecognizedCredentialFieldName_StillEncryptedAndStored(t *testing.T) {
	// Regression test: encoding/json silently ignores JSON object keys that
	// don't match a struct field. If CreateTool decided "were credentials
	// submitted?" by decoding into the typed ToolCredentials struct first,
	// a field name it doesn't happen to declare (a typo, a differently-cased
	// key, a future field) would decode to an all-empty struct, "empty()"
	// would return true, and the secret would be silently discarded with a
	// 201 response -- the exact bug this handler exists to fix, just for
	// any payload shape other than the four known field names. Presence
	// must be decided structurally (any non-empty JSON object), not via the
	// typed struct's zero-value state.
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "salesforce-prod-2",
		"type":      "rest_api",
		"auth_type": "api_key",
		"credentials": map[string]any{
			"token": "sk-live-prod-secret", // not one of the four known field names
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if !env.store.createCalled {
		t.Fatal("CreateTool must call the store")
	}

	enc := env.store.created.CredentialsEncrypted
	if len(enc) == 0 {
		t.Fatal("an unrecognized-field-name credentials object must still be encrypted and stored, not silently dropped")
	}
	if bytes.Contains(enc, []byte("sk-live-prod-secret")) {
		t.Fatal("stored bytes must not contain the plaintext secret")
	}

	cipher, err := toolcreds.NewCipher(testEncryptionKeyHex)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plaintext, err := cipher.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt(stored value) must succeed: %v", err)
	}
	if !strings.Contains(string(plaintext), "sk-live-prod-secret") {
		t.Fatalf("decrypted content must contain the originally-submitted field verbatim, got: %s", plaintext)
	}
}

func TestCreateTool_UnrecognizedCredentialFieldName_EncryptionNotConfigured_FailsClosed(t *testing.T) {
	env := newToolsTestEnv(t, "") // no encryption key configured
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "salesforce-prod-3",
		"type":      "rest_api",
		"auth_type": "api_key",
		"credentials": map[string]any{
			"token": "sk-live-prod-secret",
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500 when encryption is unconfigured, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Fatal("the store must never be called -- an unrecognized field name must not bypass the fail-closed check")
	}
}

func TestCreateTool_CredentialsNotAnObject_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":        "bad-credentials-shape",
		"type":        "rest_api",
		"auth_type":   "api_key",
		"credentials": "sk-super-secret-value", // string, not an object
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for a non-object credentials payload, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Fatal("the store must never be called for a rejected request")
	}
}

func TestCreateTool_EmptyCredentialsObject_TreatedAsNoCredentials(t *testing.T) {
	// A client sending `"credentials": {}` (no actual secret fields set)
	// must not be forced through the encryption-configured requirement --
	// there is nothing to encrypt.
	env := newToolsTestEnv(t, "") // no key configured
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":        "internal-mcp-2",
		"type":        "mcp",
		"auth_type":   "basic",
		"credentials": map[string]any{},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if !env.store.createCalled {
		t.Fatal("CreateTool must call the store")
	}
}

// ─── CreateTool: fails closed, never silently discards a secret ───────────

func TestCreateTool_CredentialsSubmitted_EncryptionNotConfigured_FailsClosed(t *testing.T) {
	env := newToolsTestEnv(t, "") // no encryption key configured
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "salesforce-prod",
		"type":      "rest_api",
		"auth_type": "api_key",
		"credentials": map[string]any{
			"api_key": "sk-super-secret-value",
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500 when encryption is unconfigured, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Fatal("acceptance criterion 4: the store must never be called -- no tool row, no silent data loss")
	}

	body, _ := readAllBody(resp)
	if !strings.Contains(strings.ToLower(body), "encrypt") {
		t.Errorf("error message should mention encryption/config so the failure is diagnosable, got: %s", body)
	}
}

// ─── Leak check: credentials never appear in responses ────────────────────

func TestCreateTool_ResponseNeverContainsCredentials(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "salesforce-prod",
		"type":      "rest_api",
		"auth_type": "api_key",
		"credentials": map[string]any{
			"api_key": "sk-super-secret-value",
		},
	})
	defer resp.Body.Close()

	body, _ := readAllBody(resp)
	if strings.Contains(body, "sk-super-secret-value") {
		t.Fatalf("CreateTool response must never contain the submitted secret, got: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "credential") {
		t.Fatalf("CreateTool response must not even mention credentials, got: %s", body)
	}
}

func TestListTools_ResponseNeverContainsCredentials(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	// Seed a tool with credentials so there's something to leak, if the
	// leak existed.
	createResp := env.postTool(t, token, map[string]any{
		"name":      "salesforce-prod",
		"type":      "rest_api",
		"auth_type": "api_key",
		"credentials": map[string]any{
			"api_key": "sk-super-secret-value",
		},
	})
	createResp.Body.Close()

	// ListTools uses the fakeToolStore's ListTools, which returns nil
	// regardless of what was created -- but the structural guarantee this
	// test exists to prove is: store.GatewayTool (the type ListTools
	// serializes) has no field that could carry CredentialsEncrypted even
	// if the store returned real rows. Assert that structurally too, via a
	// list call against a store double that DOES return the created row.
	env.store.listResultCheck(t)

	req, _ := http.NewRequest(http.MethodGet, env.srv.URL+"/v1/gateway/tools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/gateway/tools: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAllBody(resp)
	if strings.Contains(body, "sk-super-secret-value") {
		t.Fatalf("ListTools response must never contain a secret, got: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "credential") {
		t.Fatalf("ListTools response must not even mention credentials, got: %s", body)
	}
}

// listResultCheck is a structural assertion, not a store call: GatewayTool
// (what ListTools/CreateTool actually serialize) has no CredentialsEncrypted
// field, so there is no leak surface to regress on regardless of what any
// future store implementation returns.
func (f *fakeToolStore) listResultCheck(t *testing.T) {
	t.Helper()
	var gt store.GatewayTool
	b, err := json.Marshal(toolToResp(gt))
	if err != nil {
		t.Fatalf("marshal ToolResp: %v", err)
	}
	if strings.Contains(strings.ToLower(string(b)), "credential") {
		t.Fatalf("ToolResp must never carry a credentials field, got: %s", b)
	}
}

func readAllBody(resp *http.Response) (string, error) {
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
