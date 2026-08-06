// tools_action_paths_test.go — eami-api/internal/api
// Handler-level unit tests for gateway_tools.action_paths (B-046): per-
// action path/method mappings for rest_api tools. Same convention as
// tools_test.go/tools_update_test.go: package api (white-box), fakeToolStore,
// no live Postgres needed -- see tools_action_paths_pg_test.go for the real-
// Postgres JSONB/COALESCE proof, and eami-gateway/internal/toolrouter's
// real-Postgres tests for AC1-AC3 at the actual dispatch layer (this module
// only owns validation, persistence, and API-response shape).
//
// Run: go test -count=1 ./internal/api/... -run ActionPaths
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/eami/api/internal/store"
)

// ─── CreateTool: validation ─────────────────────────────────────────────────

func TestCreateTool_ActionPaths_ValidMappings_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "multi-endpoint-api",
		"type":      "rest_api",
		"auth_type": "api_key",
		"action_paths": map[string]any{
			"list_contacts":  map[string]any{"path": "/contacts", "method": "get"},
			"create_contact": map[string]any{"path": "/contacts/new", "method": "PUT"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if !env.store.createCalled {
		t.Fatal("CreateTool must call the store when the request is valid")
	}

	var got map[string]ActionPathMapping
	if err := json.Unmarshal(env.store.created.ActionPaths, &got); err != nil {
		t.Fatalf("stored ActionPaths must be valid JSON: %v", err)
	}
	if got["list_contacts"].Path != "/contacts" || got["list_contacts"].Method != "GET" {
		t.Errorf("list_contacts = %+v, want {/contacts GET} (method uppercased)", got["list_contacts"])
	}
	if got["create_contact"].Path != "/contacts/new" || got["create_contact"].Method != "PUT" {
		t.Errorf("create_contact = %+v, want {/contacts/new PUT}", got["create_contact"])
	}
}

func TestCreateTool_ActionPaths_OmittedMethod_DefaultsToPOST(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "default-method-api",
		"type":      "rest_api",
		"auth_type": "api_key",
		"action_paths": map[string]any{
			"submit": map[string]any{"path": "/submit"}, // no method
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	var got map[string]ActionPathMapping
	if err := json.Unmarshal(env.store.created.ActionPaths, &got); err != nil {
		t.Fatalf("stored ActionPaths must be valid JSON: %v", err)
	}
	if got["submit"].Method != http.MethodPost {
		t.Errorf("Method = %q, want defaulted %q", got["submit"].Method, http.MethodPost)
	}
}

func TestCreateTool_ActionPaths_EmptyPath_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "bad-mapping-api",
		"type":      "rest_api",
		"auth_type": "api_key",
		"action_paths": map[string]any{
			"list": map[string]any{"path": "", "method": "GET"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an empty path, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Fatal("the store must never be called for a rejected request")
	}
}

// TestCreateTool_ActionPaths_FullURLPath_Rejected proves the security-
// review hardening: a path value shaped like a full URL is rejected at
// write time with a clear error, rather than being silently accepted and
// later appended as a literal (and confusing) path segment on base_url --
// see joinURLPath's doc comment in eami-gateway/internal/toolrouter for why
// this was never an actual host-confusion/SSRF vector (path is always
// joined into a single URL string parsed once, never resolved as a
// separate reference), just a real misconfiguration an admin could make.
func TestCreateTool_ActionPaths_FullURLPath_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "full-url-path-api",
		"type":      "rest_api",
		"auth_type": "api_key",
		"action_paths": map[string]any{
			"list": map[string]any{"path": "https://evil.example.com/steal", "method": "GET"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for a full-URL path, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Fatal("the store must never be called for a rejected request")
	}
}

func TestCreateTool_ActionPaths_UnsupportedMethod_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "bad-method-api",
		"type":      "rest_api",
		"auth_type": "api_key",
		"action_paths": map[string]any{
			"list": map[string]any{"path": "/list", "method": "TRACE"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unsupported method, got %d", resp.StatusCode)
	}
	if env.store.createCalled {
		t.Fatal("the store must never be called for a rejected request")
	}
}

func TestCreateTool_ActionPaths_Omitted_NilStored(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)

	resp := env.postTool(t, token, map[string]any{
		"name":      "no-mappings-api",
		"type":      "rest_api",
		"auth_type": "api_key",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := readAllBody(resp)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	if env.store.created.ActionPaths != nil {
		t.Errorf("ActionPaths = %s, want nil when the request omits action_paths (AC2 -- unaffected tool)", env.store.created.ActionPaths)
	}
}

// ─── UpdateTool: omitted/present/empty semantics ───────────────────────────

func TestUpdateTool_ActionPaths_Omitted_LeavesUnchanged(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "11111111-1111-1111-1111-111111111111"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"name": "renamed-only",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if env.store.updated.ActionPaths != nil {
		t.Fatalf("ActionPaths must be nil (COALESCE preserves existing mappings) when the request omits action_paths, got %s", env.store.updated.ActionPaths)
	}
}

func TestUpdateTool_ActionPaths_ExplicitEmptyObject_ClearsMappings(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "11111111-1111-1111-1111-111111111111"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"action_paths": map[string]any{},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if string(env.store.updated.ActionPaths) != "{}" {
		t.Fatalf("ActionPaths = %s, want the literal empty object \"{}\" (non-nil, so COALESCE overwrites and clears stored mappings)", env.store.updated.ActionPaths)
	}
}

func TestUpdateTool_ActionPaths_Present_Persisted(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "11111111-1111-1111-1111-111111111111"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"action_paths": map[string]any{
			"search": map[string]any{"path": "/v2/search", "method": "post"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readAllBody(resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	var got map[string]ActionPathMapping
	if err := json.Unmarshal(env.store.updated.ActionPaths, &got); err != nil {
		t.Fatalf("stored ActionPaths must be valid JSON: %v", err)
	}
	if got["search"].Path != "/v2/search" || got["search"].Method != "POST" {
		t.Errorf("search = %+v, want {/v2/search POST}", got["search"])
	}
}

func TestUpdateTool_ActionPaths_UnsupportedMethod_Rejected(t *testing.T) {
	env := newToolsTestEnv(t, testEncryptionKeyHex)
	token := env.adminToken(t)
	toolID := "11111111-1111-1111-1111-111111111111"

	resp := env.patchTool(t, token, toolID, map[string]any{
		"action_paths": map[string]any{
			"list": map[string]any{"path": "/list", "method": "CONNECT"},
		},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unsupported method, got %d", resp.StatusCode)
	}
	if env.store.updateCalled {
		t.Fatal("the store must never be called for a rejected request")
	}
}

// ─── Response shape ──────────────────────────────────────────────────────

// TestToolToResp_ActionPaths_RoundTrips proves the response shape an admin
// sees when editing a tool (Edit-panel prefill, B-046's UI requirement)
// matches exactly what was stored, and that a tool with none doesn't grow
// a spurious empty action_paths key (omitempty).
func TestToolToResp_ActionPaths_RoundTrips(t *testing.T) {
	raw, err := json.Marshal(map[string]ActionPathMapping{
		"list": {Path: "/list", Method: "GET"},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	withMappings := toolToResp(store.GatewayTool{ActionPaths: raw})
	if len(withMappings.ActionPaths) != 1 || withMappings.ActionPaths["list"].Path != "/list" {
		t.Errorf("ActionPaths = %+v, want {list: {/list GET}}", withMappings.ActionPaths)
	}

	without := toolToResp(store.GatewayTool{ActionPaths: nil})
	b, err := json.Marshal(without)
	if err != nil {
		t.Fatalf("marshal ToolResp: %v", err)
	}
	if strings.Contains(string(b), "action_paths") {
		t.Errorf("ToolResp JSON for a tool with no mappings must omit action_paths entirely (omitempty), got: %s", b)
	}
}
