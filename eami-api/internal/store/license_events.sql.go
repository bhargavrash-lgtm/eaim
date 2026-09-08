// Code for migration 000018 (B-157 epic, Brief 2): license_events. Not
// sqlc-generated (no query/*.sql source exists for it, matching e.g.
// agent_lifecycle_events.sql.go's own hand-written precedent) -- a single
// simple insert, no read path needed by this brief's own scope
// (verification is via direct psql, matching agent_lifecycle_events'
// established precedent for a new audit-style table with no UI yet).
package store

import (
	"context"

	"github.com/google/uuid"
)

type InsertLicenseEventParams struct {
	OrgID       uuid.UUID
	LicenseID   uuid.UUID
	EventType   string // 'created' | 'renewed' | 'expired'
	PerformedBy *uuid.UUID
	Detail      *string
}

const insertLicenseEventQuery = `
INSERT INTO license_events (org_id, license_id, event_type, performed_by, detail)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (license_id, event_type) DO NOTHING
`

// InsertLicenseEvent records one license lifecycle event (created, renewed,
// or expired). ON CONFLICT DO NOTHING makes this idempotent per
// (license_id, event_type) -- load-bearing for the 'expired' event, which
// is detected lazily on every GetLicense call and must not accumulate a
// duplicate row on every repeated status check. Deliberately non-fatal to
// the caller by convention (see license.go call sites): a license
// creation/renewal must not fail because this best-effort side-effect
// did, mirroring agents.go's identical InsertAgentLifecycleEvent
// convention.
func (q *Queries) InsertLicenseEvent(ctx context.Context, p InsertLicenseEventParams) error {
	_, err := q.db.Exec(ctx, insertLicenseEventQuery,
		toPgtypeUUID(p.OrgID), toPgtypeUUID(p.LicenseID), p.EventType,
		toPgtypeUUIDPtr(p.PerformedBy), toPgtypeText(p.Detail),
	)
	return err
}
