package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// License mirrors the licenses table (B-157 epic, migration 000015,
// built as B-169). RawLicense is the real source of truth (the signed
// JWT); every other field is a decoded, denormalized cache of its own
// claims -- see the migration's own doc comment for why.
type License struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	RawLicense  string
	Modules     []string
	ValidFrom   time.Time
	ValidUntil  time.Time
	UsageLimits []byte // raw JSONB, nil if not set -- not decoded/used by this brief (Brief 2's scope)
	CreatedAt   time.Time
}

// CreateLicenseParams holds fields for inserting a new license row.
// Append-only (schema/migrations-v2/000015_licenses.up.sql's own doc
// comment): there is no UpdateLicense -- a renewal or module change is
// always a new INSERT, never a mutation of an existing row.
type CreateLicenseParams struct {
	OrgID      uuid.UUID
	RawLicense string
	Modules    []string
	ValidFrom  time.Time
	ValidUntil time.Time
}

const createLicenseSQL = `
INSERT INTO licenses (org_id, raw_license, modules, valid_from, valid_until)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, org_id, raw_license, modules, valid_from, valid_until, usage_limits, created_at`

func (q *Queries) CreateLicense(ctx context.Context, p CreateLicenseParams) (License, error) {
	var l License
	err := q.db.QueryRow(ctx, createLicenseSQL,
		toPgtypeUUID(p.OrgID), p.RawLicense, p.Modules, p.ValidFrom, p.ValidUntil,
	).Scan(&l.ID, &l.OrgID, &l.RawLicense, &l.Modules, &l.ValidFrom, &l.ValidUntil, &l.UsageLimits, &l.CreatedAt)
	return l, err
}

const getLatestLicenseSQL = `
SELECT id, org_id, raw_license, modules, valid_from, valid_until, usage_limits, created_at
FROM licenses
WHERE org_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1`

// GetLatestLicense returns the most recently uploaded license row for an
// org, regardless of whether its own validity window has passed --
// callers that need to know whether it's CURRENTLY effective must run
// its RawLicense back through license.Verify (the real source of truth
// for validity, not this row's own valid_from/valid_until cache columns).
// Returns (License{}, pgx.ErrNoRows) if the org has never uploaded one.
func (q *Queries) GetLatestLicense(ctx context.Context, orgID uuid.UUID) (License, error) {
	var l License
	err := q.db.QueryRow(ctx, getLatestLicenseSQL, toPgtypeUUID(orgID)).
		Scan(&l.ID, &l.OrgID, &l.RawLicense, &l.Modules, &l.ValidFrom, &l.ValidUntil, &l.UsageLimits, &l.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return License{}, pgx.ErrNoRows
		}
		return License{}, err
	}
	return l, nil
}
