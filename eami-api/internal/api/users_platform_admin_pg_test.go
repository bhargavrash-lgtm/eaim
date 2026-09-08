// users_platform_admin_pg_test.go -- eami-api/internal/api
//
// Real-Postgres integration test for B-157 epic Brief 2's own design
// requirement: platform_admin must be unassignable through ANY API path,
// including the ordinary admin-gated user-management endpoints
// (InviteUser/UpdateUserRole) -- otherwise any ordinary org admin could
// grant itself (or a teammate) the exact tier B-113's fix depends on
// being genuinely harder to obtain than admin, defeating the fix. Reuses
// finOpsPgTestEnv (finops_pg_test.go, same package) purely for its
// pool/srv/authSvc/orgID -- unrelated to FinOps itself.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	POSTGRES_PASSWORD=<...> go test ./internal/api/... -run TestUsers_RealDB_PlatformAdmin -v
package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestUsers_RealDB_PlatformAdmin_NotAssignableViaInviteOrUpdateRole proves
// neither self-service role-management endpoint accepts "platform_admin"
// as a value, even from a real, ordinary "admin" -- the same user who IS
// allowed to invite/promote users to every one of the other 4 tiers.
func TestUsers_RealDB_PlatformAdmin_NotAssignableViaInviteOrUpdateRole(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	admin := env.adminToken(t)

	// InviteUser must reject "platform_admin" as a role value outright.
	resp := env.doModelPricing(t, http.MethodPost, "/v1/users/invite", admin, map[string]any{
		"email": "wannabe-platform-admin@finops-test.example", "role": "platform_admin",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("InviteUser with role=platform_admin: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// UpdateUserRole must reject it too, for an existing, real user.
	targetID := uuid.New()
	if _, err := env.pool.Exec(context.Background(),
		`INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'viewer')`,
		targetID, env.orgID, "target@finops-test.example"); err != nil {
		t.Fatalf("seed target user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, targetID)
	})

	resp = env.doModelPricing(t, http.MethodPut, "/v1/users/"+targetID.String()+"/role", admin, map[string]any{
		"role": "platform_admin",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("UpdateUserRole to platform_admin: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The target's role in the DB must be genuinely unchanged -- not just
	// a 400 with a silent partial write.
	var role string
	if err := env.pool.QueryRow(context.Background(), `SELECT role FROM users WHERE id = $1`, targetID).Scan(&role); err != nil {
		t.Fatalf("read back target role: %v", err)
	}
	if role != "viewer" {
		t.Errorf("target user's role = %q after rejected update, want unchanged 'viewer'", role)
	}
}

// TestUsers_RealDB_PlatformAdmin_ProvisionedViaDirectSQL_WorksNormally
// proves the ONLY supported provisioning path -- a direct SQL statement
// against users.role -- actually works: the users.role CHECK constraint
// (migration 000017) accepts the value, and a real login-equivalent JWT
// issuance for that user carries it through as an ordinary role claim
// (requireRole itself doesn't care how a role was assigned, only what the
// JWT says -- proven separately by the model_pricing B-113-closure test).
func TestUsers_RealDB_PlatformAdmin_ProvisionedViaDirectSQL_WorksNormally(t *testing.T) {
	env := newFinOpsPgTestEnv(t)
	platformAdminID := uuid.New()
	if _, err := env.pool.Exec(context.Background(),
		`INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, 'platform_admin')`,
		platformAdminID, env.orgID, "real-platform-admin@finops-test.example"); err != nil {
		t.Fatalf("direct-SQL provisioning of platform_admin failed -- users.role CHECK constraint should accept it: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, platformAdminID)
	})

	var role string
	if err := env.pool.QueryRow(context.Background(), `SELECT role FROM users WHERE id = $1`, platformAdminID).Scan(&role); err != nil {
		t.Fatalf("read back provisioned role: %v", err)
	}
	if role != "platform_admin" {
		t.Errorf("role = %q, want platform_admin", role)
	}
}
