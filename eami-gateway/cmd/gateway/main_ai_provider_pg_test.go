// main_ai_provider_pg_test.go -- cmd/gateway
//
// Integration tests for resolveAIProviderTool (AI Provider Connector,
// Thread A Model 1) against a REAL Postgres, mirroring main_pg_test.go's
// established pattern for resolveDynamicTool exactly -- same newMainTestEnv
// helper (reused directly, same package), same fail-open contract, same
// "cross-org must never resolve" discipline.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestResolveAIProviderTool -v
package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/eami/gateway/internal/aiprovider"
)

func (e *mainTestEnv) insertAIProviderTool(t *testing.T, orgID uuid.UUID, name, provider, auditMode string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider, audit_mode)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4, $5)
	`, uuid.New(), orgID, name, provider, auditMode); err != nil {
		t.Fatalf("insert ai_provider gateway_tools row: %v", err)
	}
}

// TestResolveAIProviderTool_AIProviderType_ReturnsRow proves AC1's routing
// decision at the resolution layer: a registered ai_provider connector
// resolves to a non-nil row carrying its provider and audit_mode.
func TestResolveAIProviderTool_AIProviderType_ReturnsRow(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertAIProviderTool(t, env.orgID, "claude", "claude", "structural_metadata_only")

	row, err := resolveAIProviderTool(context.Background(), aiprovider.New(env.pool, nil, nil), env.orgID.String(), "claude")
	if err != nil {
		t.Fatalf("resolveAIProviderTool: %v", err)
	}
	if row == nil {
		t.Fatal("expected a resolved row for a registered ai_provider connector, got nil")
	}
	if row.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", row.Provider)
	}
	if row.AuditMode != "structural_metadata_only" {
		t.Errorf("AuditMode = %q, want structural_metadata_only", row.AuditMode)
	}
}

// TestResolveAIProviderTool_DefaultAuditMode_IsStructuralMetadataOnly
// proves the fail-safe default (AC5) actually holds through the full
// insert-then-resolve path, not just at the schema level: a connector
// created without specifying audit_mode resolves with
// "structural_metadata_only", never "full".
func TestResolveAIProviderTool_DefaultAuditMode_IsStructuralMetadataOnly(t *testing.T) {
	env := newMainTestEnv(t)
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO gateway_tools (id, org_id, name, type, auth_type, provider)
		VALUES ($1, $2, $3, 'ai_provider', 'api_key', $4)
	`, uuid.New(), env.orgID, "claude-default", "claude"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	row, err := resolveAIProviderTool(context.Background(), aiprovider.New(env.pool, nil, nil), env.orgID.String(), "claude-default")
	if err != nil {
		t.Fatalf("resolveAIProviderTool: %v", err)
	}
	if row == nil {
		t.Fatal("expected a resolved row, got nil")
	}
	if row.AuditMode != "structural_metadata_only" {
		t.Errorf("AuditMode = %q, want structural_metadata_only (the fail-safe default)", row.AuditMode)
	}
}

// TestResolveAIProviderTool_UnregisteredName_ReturnsNil (B-168: no
// regression to the legitimate not-found case) proves a genuinely
// unregistered name still returns (nil, nil), not an error -- it must
// keep falling through to the existing rest_api/static-proxy resolution
// exactly as before this fix, never a rejection.
func TestResolveAIProviderTool_UnregisteredName_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)

	row, err := resolveAIProviderTool(context.Background(), aiprovider.New(env.pool, nil, nil), env.orgID.String(), "never-registered")
	if err != nil {
		t.Errorf("expected nil error for a legitimate not-found (fallback must still work), got %v", err)
	}
	if row != nil {
		t.Errorf("expected nil (fallback) for an unregistered connector name, got %+v", row)
	}
}

// TestResolveAIProviderTool_RestAPIType_ReturnsNil proves a tool name that
// resolves to a rest_api (or mcp/database) row is never mistaken for an
// ai_provider connector -- the two resolvers are independent, org+name
// scoped to type='ai_provider' specifically (aiprovider/router.go's
// Resolve query), not a post-hoc type filter over a shared lookup.
func TestResolveAIProviderTool_RestAPIType_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertTool(t, env.orgID, "my-rest-tool", "rest_api")

	row, err := resolveAIProviderTool(context.Background(), aiprovider.New(env.pool, nil, nil), env.orgID.String(), "my-rest-tool")
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if row != nil {
		t.Errorf("expected nil for a rest_api-type tool, got %+v", row)
	}
}

// TestResolveAIProviderTool_CrossOrg_ReturnsNil proves org isolation holds
// for ai_provider connectors exactly like every other type (B-042-class
// discipline): an agent from one org must never resolve a different org's
// identically-named connector.
func TestResolveAIProviderTool_CrossOrg_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	env.insertAIProviderTool(t, env.orgID, "shared-name", "claude", "full")

	otherOrgID := uuid.New()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $3)`,
		otherOrgID, "gateway-main-aiprovider-other-"+otherOrgID.String()[:8], "gateway-main-aiprovider-other-"+otherOrgID.String()); err != nil {
		t.Fatalf("insert other org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM orgs WHERE id = $1`, otherOrgID)
	})

	row, err := resolveAIProviderTool(context.Background(), aiprovider.New(env.pool, nil, nil), otherOrgID.String(), "shared-name")
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if row != nil {
		t.Errorf("expected nil for a cross-org lookup (org isolation), got %+v", row)
	}
}

// TestResolveAIProviderTool_EmptyOrgOrTool_ReturnsNil is a defensive guard,
// mirroring resolveDynamicTool's identical one.
func TestResolveAIProviderTool_EmptyOrgOrTool_ReturnsNil(t *testing.T) {
	env := newMainTestEnv(t)
	apr := aiprovider.New(env.pool, nil, nil)

	if row, err := resolveAIProviderTool(context.Background(), apr, "", "claude"); row != nil || err != nil {
		t.Errorf("expected (nil, nil) for empty orgID, got (%+v, %v)", row, err)
	}
	if row, err := resolveAIProviderTool(context.Background(), apr, env.orgID.String(), ""); row != nil || err != nil {
		t.Errorf("expected (nil, nil) for empty tool name, got (%+v, %v)", row, err)
	}
}

// TestResolveAIProviderTool_GenuineResolveError_ReturnsError (B-168) is
// the previously-untested branch this whole fix exists for: a REAL,
// non-ErrNotFound error (forced here via an already-closed pgxpool.Pool,
// the same technique the live verification's REVOKE achieves against the
// real DB) must be returned to the caller, never silently swallowed into
// the same nil the legitimate not-found case returns. Before this fix,
// this exact scenario was indistinguishable from
// TestResolveAIProviderTool_UnregisteredName_ReturnsNil above -- the bug
// this brief closes.
func TestResolveAIProviderTool_GenuineResolveError_ReturnsError(t *testing.T) {
	env := newMainTestEnv(t)
	env.pool.Close() // real, deterministic non-ErrNotFound failure -- not simulated

	row, err := resolveAIProviderTool(context.Background(), aiprovider.New(env.pool, nil, nil), env.orgID.String(), "claude")
	if err == nil {
		t.Fatal("expected a real error from a closed pool, got nil -- the resolution failure was silently swallowed")
	}
	if row != nil {
		t.Errorf("expected a nil row alongside the error, got %+v", row)
	}
}
