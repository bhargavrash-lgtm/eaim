// dispatcher_usage_limit_test.go -- cmd/gateway
//
// Real-Postgres integration tests for B-157's Brief 2: the usage-limit
// gate added alongside Dispatch's existing module-license gate
// (dispatcher_license_test.go). Proves AC1 -- an org exceeding its
// license's usage_limits is blocked from further qualifying actions,
// verified against real usage data crossing a real configured limit --
// and that a license with no usage_limits claim at all, or usage still
// under the limit, sees zero behavioral change (no regression to Brief
// 1's own gate or normal dispatch).
//
// Reuses newDispatcherTestEnvRealLicense (dispatcher_license_test.go) --
// same real license.Store, same real test pool -- since *license.Store is
// exactly what now implements both LicenseChecker AND (via the optional
// type assertion in Dispatch) UsageLimitChecker.
//
// Run against the project's docker-compose Postgres:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgresql://eami_app:<pw>@127.0.0.1:5432/eami \
//	  go test ./cmd/gateway/... -run TestDispatch_UsageLimit -v
package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/eami/gateway/internal/license"
	policy "github.com/eami/policy"
)

// signTestLicenseWithUsageLimit is signTestLicense (dispatcher_license_
// test.go) plus a UsageLimits.MaxTokensPerMonth claim -- maxTokens nil
// means "no usage_limits claim at all" (unlimited).
func signTestLicenseWithUsageLimit(t *testing.T, orgID string, modules []string, validUntil time.Time, maxTokens *int64) string {
	t.Helper()
	now := time.Now().UTC()
	claims := license.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   orgID,
			Issuer:    license.Issuer,
			Audience:  jwt.ClaimStrings{license.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(validUntil),
		},
		Modules: modules,
	}
	if maxTokens != nil {
		claims.UsageLimits = &license.UsageLimits{MaxTokensPerMonth: maxTokens}
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(genuineTestVendorKey(t))
	if err != nil {
		t.Fatalf("sign test license: %v", err)
	}
	return signed
}

// insertUsageRow seeds one real token_usage row for orgID, recorded now
// (safely inside the current calendar month WithinUsageLimit's own
// date_trunc('month', now()) query sums over) -- reusing the SAME table/
// columns B-097/108/111/112's own FinOps aggregation queries established,
// not a new counting mechanism.
func (e *dispatcherTestEnv) insertUsageRow(t *testing.T, tokensIn, tokensOut int) {
	t.Helper()
	if _, err := e.env.pool.Exec(context.Background(), `
		INSERT INTO token_usage (org_id, agent_id, agent_name, model, tokens_in, tokens_out, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		e.env.orgID, e.agentID, e.agentName, "usage-limit-test-model", tokensIn, tokensOut,
	); err != nil {
		t.Fatalf("insert token_usage: %v", err)
	}
}

// TestDispatch_UsageLimitExceeded_Blocked (AC1) is this brief's own
// centerpiece: a real license with a real, low MaxTokensPerMonth, real
// token_usage rows already crossing it this calendar month, and a real
// Dispatch call that must be blocked -- zero downstream/provider spend --
// before tool resolution or policy evaluation, the same convergence point
// the module-license gate already uses.
func TestDispatch_UsageLimitExceeded_Blocked(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	limit := int64(100)
	raw := signTestLicenseWithUsageLimit(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour), &limit)
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}

	// 60 + 60 = 120 tokens this calendar month, already over the 100 limit.
	env.insertUsageRow(t, 40, 20)
	env.insertUsageRow(t, 40, 20)

	result, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err == nil {
		t.Fatal("expected a clean rejection for an org over its usage limit, got nil error")
	}
	if result != nil {
		t.Errorf("expected nil result, got %s", result)
	}
	if !strings.Contains(err.Error(), "exceeded its licensed usage limit") {
		t.Errorf("expected a clear usage-limit rejection message, got: %v", err)
	}

	var decision string
	if err := env.env.pool.QueryRow(context.Background(),
		`SELECT decision FROM audit_log WHERE org_id = $1 AND tool_name = 'some-tool' ORDER BY timestamp DESC LIMIT 1`,
		env.env.orgID).Scan(&decision); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if decision != "denied" {
		t.Errorf("audit_log decision = %q, want denied", decision)
	}
}

// TestDispatch_UsageLimitUnderLimit_Allowed proves usage genuinely still
// under a configured limit is NOT blocked -- this gate must fire only
// once the real total crosses the real configured number, not on the
// mere presence of a usage_limits claim.
func TestDispatch_UsageLimitUnderLimit_Allowed(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	limit := int64(1000)
	raw := signTestLicenseWithUsageLimit(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour), &limit)
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}
	env.insertUsageRow(t, 40, 20) // 60, well under 1000

	_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err != nil {
		t.Errorf("expected dispatch to succeed for usage under the limit, got: %v", err)
	}
}

// TestDispatch_NoUsageLimitClaim_Unlimited proves a license with the
// module but NO usage_limits claim at all is treated as unlimited --
// Brief 1's own migration comment's contract ("usage_limits ... NOT
// enforced ... column exists now") plus this brief's own "absence means
// no restriction" convention -- even with a large real usage total
// already recorded.
func TestDispatch_NoUsageLimitClaim_Unlimited(t *testing.T) {
	env := newDispatcherTestEnvRealLicense(t, policy.ActionAllow)

	raw := signTestLicenseWithUsageLimit(t, env.env.orgID.String(), []string{"gateway"}, time.Now().Add(365*24*time.Hour), nil)
	if _, err := env.env.pool.Exec(context.Background(),
		`INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until) VALUES ($1, $2, $3, NOW(), $4)`,
		env.env.orgID, raw, []string{"gateway"}, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("insert license: %v", err)
	}
	env.insertUsageRow(t, 1_000_000, 1_000_000)

	_, err := env.dispatcher.Dispatch(context.Background(), env.actionContext("some-tool"))
	if err != nil {
		t.Errorf("expected dispatch to succeed for a license with no usage_limits claim, got: %v", err)
	}
}
