package api

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/eami/api/internal/store"
)

// B-253: the field classifiers in rbac_fields.go, including the cases the
// HTTP tests can't reach today (a provider change is a 400 at validation
// while "claude" is the only provider) and the normalisation that keeps an
// echoing client from being rejected.

func strp(s string) *string { return &s }

func TestAgentAdminOnlyChange(t *testing.T) {
	cur := store.GatewayAgent{Status: "suspended", Scope: "read:crm", RiskTier: "low", TokenTTLSeconds: 3600}
	ttl := 7200
	same := 3600
	for _, c := range []struct {
		name       string
		req        AgentUpdateRequest
		restricted bool
	}{
		{"suspend -> active is a reactivation", AgentUpdateRequest{Status: strp("active")}, true},
		{"-> revoked is containment", AgentUpdateRequest{Status: strp("revoked")}, false},
		{"scope change", AgentUpdateRequest{Scope: strp("read:*")}, true},
		{"risk change", AgentUpdateRequest{RiskTier: strp("high")}, true},
		{"ttl change", AgentUpdateRequest{TokenTTLSeconds: &ttl}, true},
		{"echo of every current value", AgentUpdateRequest{Status: strp("suspended"), Scope: strp("read:crm"), RiskTier: strp("low"), TokenTTLSeconds: &same}, false},
	} {
		req := c.req
		if got := agentAdminOnlyChange(&req, cur); got != c.restricted {
			t.Errorf("%s: restricted=%v, want %v", c.name, got, c.restricted)
		}
	}
	// Echoed values are stripped so a non-admin never writes them (race-safe).
	req := AgentUpdateRequest{Status: strp("suspended"), Scope: strp("read:crm")}
	agentAdminOnlyChange(&req, cur)
	if req.Status != nil || req.Scope != nil {
		t.Errorf("unchanged fields not stripped: status=%v scope=%v", req.Status, req.Scope)
	}
	// An active agent echoed as active is a no-op, not a reactivation.
	req = AgentUpdateRequest{Status: strp("active")}
	if agentAdminOnlyChange(&req, store.GatewayAgent{Status: "active"}) || req.Status != nil {
		t.Errorf("active->active must be a stripped no-op")
	}
}

func TestToolAdminOnlyChange(t *testing.T) {
	cur := toolAdminFields{
		Name: "t", BaseURL: pgtype.Text{String: "https://a", Valid: true}, Provider: pgtype.Text{String: "claude", Valid: true},
		AuditMode: "structural_metadata_only", DataHandlingDesignation: "unknown",
		ActionPaths: []byte(`{"read":{"path":"/r","method":"GET"}}`), MCPCommand: pgtype.Text{String: "npx s", Valid: true}, MCPArgs: []string{"--a"},
	}
	for _, c := range []struct {
		name       string
		u          toolUpdateFields
		restricted bool
	}{
		{"provider change", toolUpdateFields{Provider: strp("other")}, true},
		{"rename", toolUpdateFields{Name: strp("t2")}, true},
		{"base_url change", toolUpdateFields{BaseURL: strp("https://b")}, true},
		{"any credential write", toolUpdateFields{HasCredentials: true}, true},
		{"audit_mode change", toolUpdateFields{AuditMode: strp("full")}, true},
		{"data handling change", toolUpdateFields{DataHandlingDesignation: strp("zero_retention")}, true},
		{"mcp_command change", toolUpdateFields{MCPCommand: strp("sh")}, true},
		{"mcp_args change", toolUpdateFields{MCPArgs: []string{"--b"}}, true},
		{"action_paths re-route", toolUpdateFields{ActionPathsJSON: []byte(`{"read":{"path":"/d","method":"DELETE"}}`)}, true},
		{"action_paths same, different key order", toolUpdateFields{ActionPathsJSON: []byte(`{"read":{"method":"GET","path":"/r"}}`)}, false},
		{"redaction disabled (loosening)", toolUpdateFields{RedactionRules: []byte(`{"enabled":false}`)}, true},
		{"redaction explicit default vs stored NULL", toolUpdateFields{RedactionRules: []byte(`{"enabled":true,"disabled_patterns":[],"custom_patterns":[]}`)}, false},
		{"redaction JSON null vs stored NULL", toolUpdateFields{RedactionRules: []byte(`null`)}, false},
		{"echo of every current value", toolUpdateFields{Name: strp("t"), BaseURL: strp("https://a"), Provider: strp("claude"), AuditMode: strp("structural_metadata_only"), DataHandlingDesignation: strp("unknown"), MCPCommand: strp("npx s"), MCPArgs: []string{"--a"}}, false},
	} {
		u := c.u
		if got := toolAdminOnlyChange(&u, cur); got != c.restricted {
			t.Errorf("%s: restricted=%v, want %v", c.name, got, c.restricted)
		}
	}
	// Echoed values are stripped so a non-admin never writes them -- the
	// property that makes the load/write window race-safe.
	echo := toolUpdateFields{Name: strp("t"), BaseURL: strp("https://a"), Provider: strp("claude"), AuditMode: strp("structural_metadata_only"),
		DataHandlingDesignation: strp("unknown"), MCPCommand: strp("npx s"), MCPArgs: []string{"--a"},
		ActionPathsJSON: []byte(`{"read":{"path":"/r","method":"GET"}}`), RedactionRules: []byte(`null`)}
	if toolAdminOnlyChange(&echo, cur) {
		t.Fatal("full echo must not be restricted")
	}
	if echo.Name != nil || echo.BaseURL != nil || echo.Provider != nil || echo.AuditMode != nil || echo.DataHandlingDesignation != nil ||
		echo.MCPCommand != nil || echo.MCPArgs != nil || echo.ActionPathsJSON != nil || echo.RedactionRules != nil {
		t.Errorf("echoed tool fields not stripped: %+v", echo)
	}
	// Stored action_paths NULL vs sent {} are both "no mappings".
	u := toolUpdateFields{ActionPathsJSON: []byte(`{}`)}
	if toolAdminOnlyChange(&u, toolAdminFields{}) {
		t.Error("{} vs NULL action_paths must be unchanged")
	}
}
