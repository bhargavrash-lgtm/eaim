package license

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// errOrgMismatch is returned when a licenses row's org_id column doesn't
// match the org_id signed inside its own raw_license JWT -- see
// currentClaims's own doc comment for why this check exists.
var errOrgMismatch = errors.New("license: row org_id does not match the license's own signed org_id")

// Store resolves an org's current license directly against Postgres and
// independently re-verifies it via Verify -- eami-gateway never trusts
// eami-api's already-decoded licenses.modules/valid_until columns as the
// real answer; those exist for eami-api's own display convenience. This
// is the actual behavior the B-157 investigation and this brief's own
// design both call for: a privileged direct-DB write that hand-crafted a
// licenses row with fabricated columns (bypassing eami-api's upload
// handler entirely) is still caught here, since ModuleLicensed only ever
// trusts what Verify says about the row's real raw_license.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a license Store backed by pool -- the same real Postgres
// pool eami-gateway already uses for gateway_tools/policies/audit_log,
// not a separate connection or a call to eami-api.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ModuleLicensed reports whether orgID's current, independently-
// re-verified license includes module. Fails closed on every branch: no
// license row at all, a row whose raw_license fails Verify (expired,
// tampered, wrong org, wrong issuer/audience), or a genuine DB error are
// all treated identically -- false, never true by default.
func (s *Store) ModuleLicensed(ctx context.Context, orgID, module string) bool {
	claims, err := s.currentClaims(ctx, orgID)
	if err != nil {
		return false
	}
	return claims.HasModule(module)
}

// currentClaims fetches the most recently uploaded license row for orgID
// and re-verifies it fresh -- never cached, never trusted from the row's
// own denormalized columns. Returns an error for "no license at all,"
// "the DB query itself failed," "the stored raw_license fails
// verification," and "the row's org_id column doesn't match the license's
// own signed org_id" alike -- ModuleLicensed's own fail-closed contract
// doesn't need to distinguish these for its boolean answer, but a future
// caller that DOES need to (e.g. Dispatch's own rejection message) can
// still inspect the returned error.
//
// The org_id re-check (security review finding, this brief): a genuinely
// -signed license belonging to a DIFFERENT org (e.g. read from a shared
// Postgres, a backup, or an MSP's other customer appliance) could
// otherwise be direct-DB-inserted with org_id relabeled to the attacker's
// own org -- Verify() alone has no way to know which org_id the caller
// expects, so this package, not the JWT library, is responsible for
// enforcing that binding. ORDER BY created_at DESC, id DESC (tiebreaker
// added per code review): created_at has no DB-level uniqueness
// guarantee, so two rows inserted in the same instant would otherwise
// make "most recent" ambiguous on a security-relevant gate.
func (s *Store) currentClaims(ctx context.Context, orgID string) (*Claims, error) {
	var rawLicense string
	err := s.pool.QueryRow(ctx, `
		SELECT raw_license FROM licenses
		WHERE org_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, orgID).Scan(&rawLicense)
	if err != nil {
		// pgx.ErrNoRows ("no license uploaded at all") and any other real
		// query error are both real reasons to fail closed -- neither is
		// distinguished here, matching ModuleLicensed's own contract.
		return nil, err
	}
	claims, err := Verify(rawLicense)
	if err != nil {
		return nil, err
	}
	if claims.OrgID() != orgID {
		return nil, errOrgMismatch
	}
	return claims, nil
}

// WithinUsageLimit reports whether orgID's current, real token usage this
// calendar month is still within its currently-licensed usage_limits
// (B-157 epic, Brief 2). Re-verifies the license fresh via currentClaims,
// the same trust boundary ModuleLicensed already established -- this
// package never trusts eami-api's already-decoded licenses.usage_limits
// column for an enforcement decision, only the JWT's own signed claim.
//
// A license with no usage_limits claim at all, or one whose
// MaxTokensPerMonth is unset, is unlimited -- true. A license that fails
// to resolve at all (no row, verification failure, org mismatch, DB
// error) is NOT this method's concern: Dispatch already gates on
// ModuleLicensed first, and a caller that reaches this method despite
// ModuleLicensed having failed gets the same fail-closed answer (false)
// rather than an unlimited default, since "we can't tell" must never mean
// "assume no limit."
//
// currentUsage queries the SAME token_usage table/columns
// (org_id/tokens_in/tokens_out/recorded_at) B-097/108/111/112's own
// eami-api FinOps aggregation queries already established -- not a new,
// separate counting mechanism. A rolling calendar month (first-of-month
// 00:00 UTC through now), not a rolling 30-day window: simple, matches
// how a monthly volume entitlement is conventionally understood, and
// needs no extra state to track a "period start" anywhere.
//
// Known, disclosed limitation (code-review finding, this brief, logged as
// B-173 rather than fixed here): this is a check-THEN-act read against
// token_usage, and token_usage is populated by main.go's own pre-existing
// (B-099) `go safeWriteTokenUsage(...)` -- a fire-and-forget goroutine
// that HTTP-POSTs to eami-api asynchronously AFTER a dispatch completes,
// not a synchronous write inside Dispatch itself. A burst of rapid or
// concurrent dispatch calls can each query this SUM before any of the
// PRIOR calls' own usage has actually landed, letting an org overshoot
// MaxTokensPerMonth by an unbounded amount during that lag window. This
// is a real gap beyond the inherent, unavoidable "a call's own token
// count isn't known until the provider responds, so the call that
// crosses the limit is itself never preventable" limitation every
// token-based quota system shares (already accounted for in the caller's
// own design) -- fixing it for real would mean either making token_usage
// writes synchronous (a hot-dispatch-path latency/architecture change
// well beyond this brief's own scope) or a separate real-time reservation
// counter (new counting machinery, which this brief's own brief
// explicitly said not to build). Left as a known soft limit, not a hard
// one, until B-173 is picked up.
func (s *Store) WithinUsageLimit(ctx context.Context, orgID string) (bool, error) {
	claims, err := s.currentClaims(ctx, orgID)
	if err != nil {
		return false, err
	}
	if claims.UsageLimits == nil || claims.UsageLimits.MaxTokensPerMonth == nil {
		return true, nil
	}
	limit := *claims.UsageLimits.MaxTokensPerMonth

	var used int64
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tokens_in + tokens_out), 0)
		FROM token_usage
		WHERE org_id = $1
		  AND recorded_at >= date_trunc('month', now())
	`, orgID).Scan(&used)
	if err != nil {
		return false, fmt.Errorf("license: usage limit query failed: %w", err)
	}
	return used < limit, nil
}
