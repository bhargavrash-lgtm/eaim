package license

import (
	"context"
	"errors"

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
