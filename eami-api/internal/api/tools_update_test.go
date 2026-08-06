// tools_update_test.go — eami-api/internal/api
// Handler-level unit tests for PATCH /v1/gateway/tools/{toolId} (B-045).
// Same convention as tools_test.go: package api (white-box), fakeToolStore,
// no live Postgres needed for these -- see internal/toolrouter's real-
// Postgres test in eami-gateway for AC4 (a dispatch after an edit uses the
// new config, not stale data), which can't be proven from this module.
//
// Run: go test -count=1 ./internal/api/... -run TestUpdateTool
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (e *toolsTestEnv) patchTool(t *testing.T, token, toolID string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, e.srv.URL+"/v1/gateway/tools/"+toolID, bytes.NewReader(b))
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

// ─── AC1: partial update of non-credential fields ─────────────────────────

func TestUpdateTool_NameAndBaseURL_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{
		"name":     "renamed-tool",
		"base_url": "https://new.example.com",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if !env.store.updateCalled {
		t.Fatal("UpdateTool must call the store when the request is valid")
	}
	if env.store.updated.Name == nil || *env.store.updated.Name != "renamed-tool" {
		t.Errorf("Name = %v, want \"renamed-tool\"", env.store.updated.Name)
	}
	if env.store.updated.BaseURL == nil || *env.store.updated.BaseURL != "https://new.example.com" {
		t.Errorf("BaseURL = %v, want the new URL", env.store.updated.BaseURL)
	}
}

// ─── AC2: editing without touching credentials preserves them ─────────────

func TestUpdateTool_CredentialsOmitted_NotReEncrypted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{
		"name": "renamed-only",
		// no "credentials" key at all
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.CredentialsEncrypted != nil {
		t.Fatalf("CredentialsEncrypted must be nil (COALESCE preserves the existing value) when the request omits credentials, got %d bytes", len(env.store.updated.CredentialsEncrypted))
	}
}

// TestUpdateTool_EmptyCredentialsObject_TreatedAsOmitted mirrors
// TestCreateTool_EmptyCredentialsObject_TreatedAsNoCredentials -- an
// explicit {} must behave the same as an omitted field, not clear the
// stored value.
func TestUpdateTool_EmptyCredentialsObject_TreatedAsOmitted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{
		"name":        "renamed-only",
		"credentials": map[string]any{},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.CredentialsEncrypted != nil {
		t.Fatalf("an empty credentials object must be treated as \"no change\", not encrypted-and-stored, got %d bytes", len(env.store.updated.CredentialsEncrypted))
	}
}

// ─── AC3: editing the credential field re-encrypts, same guarantees as creation ───

func TestUpdateTool_CredentialsProvided_ReEncrypted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{
		"credentials": map[string]any{
			"api_key": "sk-new-rotated-secret",
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	enc := env.store.updated.CredentialsEncrypted
	if len(enc) == 0 {
		t.Fatal("CredentialsEncrypted must be non-empty when credentials are submitted")
	}
	if bytes.Contains(enc, []byte("sk-new-rotated-secret")) {
		t.Fatal("stored bytes must not contain the plaintext secret")
	}
}

func TestUpdateTool_CredentialsProvided_EncryptionNotConfigured_FailsClosed(t *testing.T) {
	env := newToolsTestEnv(t, "") // no encryption key configured
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{
		"credentials": map[string]any{"api_key": "sk-x"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500 (fail closed), got %d", resp.StatusCode)
	}
	if env.store.updateCalled {
		t.Fatal("the store must never be called when encryption fails closed -- never silently drop the new secret")
	}
}

// ─── AC5: no credential material in any response, edit included ──────────

func TestUpdateTool_ResponseNeverContainsCredentials(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{
		"credentials": map[string]any{"api_key": "sk-super-secret-value"},
	})
	defer resp.Body.Close()

	body, _ := readAllBody(resp)
	if strings.Contains(body, "sk-super-secret-value") {
		t.Fatalf("UpdateTool response must never contain the submitted secret, got: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "credential") {
		t.Fatalf("UpdateTool response must not even mention credentials, got: %s", body)
	}
}

// ─── not found / cross-org (store.ErrNoRows path) ─────────────────────────

func TestUpdateTool_NotFound_Returns404(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	env.store.updateErr = pgx.ErrNoRows
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{"name": "x"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := readAllBody(resp)
		t.Fatalf("want 404, got %d: %s", resp.StatusCode, body)
	}
}

// TestUpdateTool_EmptyName_Rejected proves the code-review fix: a
// present-but-empty "name" must be rejected (400), not silently blank out
// the tool's name via COALESCE (empty string is a real, non-NULL value).
func TestUpdateTool_EmptyName_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := uuid.New().String()

	resp := env.patchTool(t, token, toolID, map[string]any{"name": ""})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := readAllBody(resp)
		t.Fatalf("want 400, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updateCalled {
		t.Fatal("the store must never be called with an empty name")
	}
}

func TestUpdateTool_InvalidToolID_Returns400(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.patchTool(t, token, "not-a-uuid", map[string]any{"name": "x"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}
