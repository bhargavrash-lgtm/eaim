// tools_ai_provider_test.go — eami-api/internal/api
// Handler-level unit tests for gateway_tools' ai_provider type (AI Provider
// Connector, Thread A Model 1): provider allowlist validation, audit_mode
// validation/defaulting, and API-response shape. Same convention as
// tools_action_paths_test.go: package api (white-box), fakeToolStore, no
// live Postgres needed here (see tools.sql.go's own CreateTool default and
// eami-gateway/internal/aiprovider's real-Postgres tests for the store/
// dispatch layers this module hands off to).
//
// Run: go test -count=1 ./internal/api/... -run AIProvider
package api

import (
	"net/http"
	"testing"

	"github.com/eami/api/internal/store"
)

// ─── CreateTool: provider/audit_mode validation ────────────────────────────

func TestCreateTool_AIProvider_ValidProvider_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "claude-connector",
		"type":      "ai_provider",
		"auth_type": "api_key",
		"provider":  "claude",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if !env.store.createCalled {
		t.Fatal("CreateTool must call the store when the request is valid")
	}
	if env.store.created.Provider == nil || *env.store.created.Provider != "claude" {
		t.Errorf("stored Provider = %v, want \"claude\"", env.store.created.Provider)
	}
}

func TestCreateTool_AIProvider_UnknownProvider_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "unknown-connector",
		"type":      "ai_provider",
		"auth_type": "api_key",
		"provider":  "some-provider-nobody-registered",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for an unrecognized provider, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store for an unrecognized provider")
	}
}

// TestCreateTool_AIProvider_WrongAuthType_Rejected proves a misconfigured
// connector (any auth_type other than "api_key", the only shape
// aiprovider.Credentials/ClaudeAdapter understand) fails loudly at
// creation time, not silently at every dispatch afterward (code review
// finding, this task).
func TestCreateTool_AIProvider_WrongAuthType_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "oauth-claude-connector",
		"type":      "ai_provider",
		"auth_type": "oauth2",
		"provider":  "claude",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for auth_type=oauth2 on an ai_provider connector, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store for an unsupported auth_type")
	}
}

func TestCreateTool_AIProvider_MissingProvider_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "no-provider-connector",
		"type":      "ai_provider",
		"auth_type": "api_key",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 when type=ai_provider omits provider entirely, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store when provider is missing")
	}
}

// TestCreateTool_AIProvider_DefaultAuditMode_IsStructuralMetadataOnly
// proves the fail-safe default (AC5): a connector created without an
// explicit audit_mode gets "structural_metadata_only", never "full".
func TestCreateTool_AIProvider_DefaultAuditMode_IsStructuralMetadataOnly(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "default-audit-connector",
		"type":      "ai_provider",
		"auth_type": "api_key",
		"provider":  "claude",
		// audit_mode deliberately omitted
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if env.store.created.AuditMode != "structural_metadata_only" {
		t.Errorf("stored AuditMode = %q, want structural_metadata_only (the fail-safe default)", env.store.created.AuditMode)
	}
}

func TestCreateTool_AIProvider_ExplicitFullAuditMode_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":       "full-audit-connector",
		"type":       "ai_provider",
		"auth_type":  "api_key",
		"provider":   "claude",
		"audit_mode": "full",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if env.store.created.AuditMode != "full" {
		t.Errorf("stored AuditMode = %q, want full (an admin must be able to explicitly opt in)", env.store.created.AuditMode)
	}
}

func TestCreateTool_AIProvider_InvalidAuditMode_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":       "bad-audit-mode-connector",
		"type":       "ai_provider",
		"auth_type":  "api_key",
		"provider":   "claude",
		"audit_mode": "redacted", // not implemented yet -- must not be silently accepted
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for an audit_mode value nothing implements yet, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store for an invalid audit_mode")
	}
}

// TestCreateTool_NonAIProvider_ProviderNotRequired proves the new
// validation is scoped to type=ai_provider only -- every other existing
// type is completely unaffected.
func TestCreateTool_NonAIProvider_ProviderNotRequired(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "plain-rest-tool",
		"type":      "rest_api",
		"auth_type": "api_key",
		"base_url":  "https://example.com",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
}

// TestCreateTool_NonAIProvider_ProviderRejected proves the create-side
// half of the type-consistency guard (code review finding, this task):
// a non-ai_provider type with a provider field set is rejected outright,
// not silently persisted unused.
func TestCreateTool_NonAIProvider_ProviderRejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "confused-database-tool",
		"type":      "database",
		"auth_type": "db_connection_string",
		"provider":  "claude",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for provider set on a non-ai_provider type, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store when provider is set on a non-ai_provider type")
	}
}

// ─── UpdateTool: provider/audit_mode validation ────────────────────────────

func TestUpdateTool_AIProvider_AuditModeFlip_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000001"
	env.store.getForTestRow = toolTestRow{Type: "ai_provider", AuthType: "api_key"}

	resp := env.patchTool(t, token, toolID, map[string]any{
		"audit_mode": "full",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.AuditMode == nil || *env.store.updated.AuditMode != "full" {
		t.Errorf("updated AuditMode = %v, want \"full\"", env.store.updated.AuditMode)
	}
}

func TestUpdateTool_AIProvider_InvalidAuditMode_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000002"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"audit_mode": "not-a-real-mode",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for an invalid audit_mode, got %d", resp.StatusCode)
	}
	if env.store.updateCalled {
		t.Error("UpdateTool must not call the store for an invalid audit_mode")
	}
}

func TestUpdateTool_AIProvider_UnknownProvider_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000003"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"provider": "not-a-real-provider",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for an unrecognized provider, got %d", resp.StatusCode)
	}
	if env.store.updateCalled {
		t.Error("UpdateTool must not call the store for an unrecognized provider")
	}
}

// TestUpdateTool_NonAIProviderType_ProviderRejected proves the update-side
// half of the type-consistency guard: PATCHing provider/audit_mode onto a
// tool whose actual stored type isn't ai_provider is rejected, using the
// fake store's getForTestRow to report a non-ai_provider type (code
// review finding, this task).
func TestUpdateTool_NonAIProviderType_ProviderRejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000005"
	env.store.getForTestRow = toolTestRow{Type: "database", AuthType: "db_connection_string"}

	resp := env.patchTool(t, token, toolID, map[string]any{
		"provider": "claude",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := readAllBody(resp)
		t.Fatalf("want 400 when patching provider onto a database-type tool, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updateCalled {
		t.Error("UpdateTool must not call the store when provider is rejected for the wrong type")
	}
}

func TestUpdateTool_ProviderAndAuditModeOmitted_LeavesUnchanged(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000004"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"name": "renamed-only",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.Provider != nil {
		t.Errorf("Provider should be nil (unchanged/COALESCE) when omitted, got %v", env.store.updated.Provider)
	}
	if env.store.updated.AuditMode != nil {
		t.Errorf("AuditMode should be nil (unchanged/COALESCE) when omitted, got %v", env.store.updated.AuditMode)
	}
}

// ─── CreateTool/UpdateTool: data_handling_designation validation (B-078) ────
// Mirrors this file's own audit_mode validation tests exactly -- same
// fixture, same shape, same reasoning: a visibility-only enum with a
// fail-safe default and a type-consistency guard.

func TestCreateTool_AIProvider_DefaultDataHandling_IsUnknown(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "default-data-handling-connector",
		"type":      "ai_provider",
		"auth_type": "api_key",
		"provider":  "claude",
		// data_handling_designation deliberately omitted
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if env.store.created.DataHandlingDesignation != "unknown" {
		t.Errorf("stored DataHandlingDesignation = %q, want \"unknown\" (the fail-safe default)", env.store.created.DataHandlingDesignation)
	}
}

func TestCreateTool_AIProvider_ExplicitZeroRetention_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":                      "zdr-connector",
		"type":                      "ai_provider",
		"auth_type":                 "api_key",
		"provider":                  "claude",
		"data_handling_designation": "zero_retention",
		"data_handling_note":        "Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if env.store.created.DataHandlingDesignation != "zero_retention" {
		t.Errorf("stored DataHandlingDesignation = %q, want zero_retention", env.store.created.DataHandlingDesignation)
	}
	if env.store.created.DataHandlingNote == nil || *env.store.created.DataHandlingNote != "Anthropic Enterprise Agreement dated 2026-03-01, ZDR Addendum" {
		t.Errorf("stored DataHandlingNote = %v, want the submitted citation", env.store.created.DataHandlingNote)
	}
}

func TestCreateTool_AIProvider_InvalidDataHandling_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":                      "bad-data-handling-connector",
		"type":                      "ai_provider",
		"auth_type":                 "api_key",
		"provider":                  "claude",
		"data_handling_designation": "fully_anonymized", // not a real value -- must not be silently accepted
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for an unrecognized data_handling_designation, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store for an invalid data_handling_designation")
	}
}

// TestCreateTool_NonAIProvider_DataHandlingRejected proves the
// type-consistency guard covers data_handling_* too, not just provider
// (the exact class of gap code review caught for provider/audit_mode in
// B-047, extended here on the first pass rather than found afterward).
func TestCreateTool_NonAIProvider_DataHandlingRejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":                      "confused-rest-tool",
		"type":                      "rest_api",
		"auth_type":                 "api_key",
		"base_url":                  "https://example.com",
		"data_handling_designation": "zero_retention",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for data_handling_designation set on a non-ai_provider type, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Error("CreateTool must not call the store when data_handling_designation is rejected for the wrong type")
	}
}

func TestUpdateTool_AIProvider_DataHandlingFlip_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000006"
	env.store.getForTestRow = toolTestRow{Type: "ai_provider", AuthType: "api_key"}

	resp := env.patchTool(t, token, toolID, map[string]any{
		"data_handling_designation": "standard_retention",
		"data_handling_note":        "No ZDR addendum signed as of 2026-08-20",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.DataHandlingDesignation == nil || *env.store.updated.DataHandlingDesignation != "standard_retention" {
		t.Errorf("updated DataHandlingDesignation = %v, want \"standard_retention\"", env.store.updated.DataHandlingDesignation)
	}
	if env.store.updated.DataHandlingNote == nil || *env.store.updated.DataHandlingNote != "No ZDR addendum signed as of 2026-08-20" {
		t.Errorf("updated DataHandlingNote = %v, want the submitted note", env.store.updated.DataHandlingNote)
	}
}

// TestUpdateTool_DataHandlingNote_EmptyStringClears proves the UI's own
// "clear the note" path: an explicit empty string is a real value here
// (unlike an omitted field), so it must reach the store as a non-nil
// pointer to "", not be treated the same as "not submitted".
func TestUpdateTool_DataHandlingNote_EmptyStringClears(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000007"
	env.store.getForTestRow = toolTestRow{Type: "ai_provider", AuthType: "api_key"}

	resp := env.patchTool(t, token, toolID, map[string]any{
		"data_handling_note": "",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.DataHandlingNote == nil {
		t.Fatal("DataHandlingNote should be a non-nil pointer to \"\" (an explicit clear), got nil (would be treated as \"leave unchanged\")")
	}
	if *env.store.updated.DataHandlingNote != "" {
		t.Errorf("DataHandlingNote = %q, want empty string", *env.store.updated.DataHandlingNote)
	}
}

func TestUpdateTool_AIProvider_InvalidDataHandling_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000008"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"data_handling_designation": "not-a-real-designation",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for an invalid data_handling_designation, got %d", resp.StatusCode)
	}
	if env.store.updateCalled {
		t.Error("UpdateTool must not call the store for an invalid data_handling_designation")
	}
}

func TestUpdateTool_NonAIProviderType_DataHandlingRejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000009"
	env.store.getForTestRow = toolTestRow{Type: "mcp", AuthType: "oauth2"}

	resp := env.patchTool(t, token, toolID, map[string]any{
		"data_handling_designation": "zero_retention",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := readAllBody(resp)
		t.Fatalf("want 400 when patching data_handling_designation onto an mcp-type tool, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updateCalled {
		t.Error("UpdateTool must not call the store when data_handling_designation is rejected for the wrong type")
	}
}

func TestUpdateTool_DataHandlingOmitted_LeavesUnchanged(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "22222222-0000-0000-0000-000000000010"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"name": "renamed-only-again",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.DataHandlingDesignation != nil {
		t.Errorf("DataHandlingDesignation should be nil (unchanged/COALESCE) when omitted, got %v", env.store.updated.DataHandlingDesignation)
	}
	if env.store.updated.DataHandlingNote != nil {
		t.Errorf("DataHandlingNote should be nil (unchanged/COALESCE) when omitted, got %v", env.store.updated.DataHandlingNote)
	}
}

// ─── response shape ─────────────────────────────────────────────────────────

func TestToolToResp_ProviderAndAuditMode_RoundTrip(t *testing.T) {
	provider := "claude"
	tool := store.GatewayTool{
		Type:      "ai_provider",
		AuthType:  "api_key",
		AuditMode: "structural_metadata_only",
	}
	tool.Provider.String, tool.Provider.Valid = provider, true

	resp := toolToResp(tool)
	if resp.Provider == nil || *resp.Provider != "claude" {
		t.Errorf("resp.Provider = %v, want \"claude\"", resp.Provider)
	}
	if resp.AuditMode != "structural_metadata_only" {
		t.Errorf("resp.AuditMode = %q, want structural_metadata_only", resp.AuditMode)
	}
}

func TestToolToResp_DataHandling_RoundTrip(t *testing.T) {
	tool := store.GatewayTool{
		Type:                    "ai_provider",
		AuthType:                "api_key",
		AuditMode:               "structural_metadata_only",
		DataHandlingDesignation: "zero_retention",
	}
	tool.DataHandlingNote.String, tool.DataHandlingNote.Valid = "ZDR Addendum dated 2026-03-01", true

	resp := toolToResp(tool)
	if resp.DataHandlingDesignation != "zero_retention" {
		t.Errorf("resp.DataHandlingDesignation = %q, want zero_retention", resp.DataHandlingDesignation)
	}
	if resp.DataHandlingNote == nil || *resp.DataHandlingNote != "ZDR Addendum dated 2026-03-01" {
		t.Errorf("resp.DataHandlingNote = %v, want the note", resp.DataHandlingNote)
	}
}

func TestToolToResp_DataHandling_NotSet_OmitsNote(t *testing.T) {
	tool := store.GatewayTool{
		Type:                    "ai_provider",
		AuthType:                "api_key",
		AuditMode:               "structural_metadata_only",
		DataHandlingDesignation: "unknown",
		// DataHandlingNote deliberately left zero-value (pgtype.Text{Valid: false})
	}

	resp := toolToResp(tool)
	if resp.DataHandlingDesignation != "unknown" {
		t.Errorf("resp.DataHandlingDesignation = %q, want unknown", resp.DataHandlingDesignation)
	}
	if resp.DataHandlingNote != nil {
		t.Errorf("resp.DataHandlingNote = %v, want nil (no note set)", resp.DataHandlingNote)
	}
}
