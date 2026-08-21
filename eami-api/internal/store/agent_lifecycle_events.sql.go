// Code for migration 000009 (B-087): agent_lifecycle_events. Not sqlc-
// generated (no query/*.sql source exists for it, matching e.g.
// token_usage.sql.go's own hand-written precedent) -- a single simple
// insert, no read path needed by this brief's own scope (verification is
// via direct psql, matching this codebase's established precedent for a
// new audit-style table with no UI yet, e.g. B-078's audit_log snapshot
// verification).
package store

import (
	"context"

	"github.com/google/uuid"
)

type InsertAgentLifecycleEventParams struct {
	OrgID       uuid.UUID
	AgentID     uuid.UUID
	AgentName   string
	EventType   string // 'created' | 'suspended' | 'reactivated' | 'deleted'
	PerformedBy uuid.UUID
	Detail      *string
}

const insertAgentLifecycleEventQuery = `
INSERT INTO agent_lifecycle_events (org_id, agent_id, agent_name, event_type, performed_by, detail)
VALUES ($1, $2, $3, $4, $5, $6)
`

// InsertAgentLifecycleEvent records one agent lifecycle action. Deliberately
// non-fatal to the caller by convention (see agents.go call sites): the
// lifecycle action itself (create/suspend/delete) must not fail because
// logging did, mirroring this codebase's own "_ = q.NotifyPolicyReload(...)"
// best-effort-side-effect pattern.
func (q *Queries) InsertAgentLifecycleEvent(ctx context.Context, p InsertAgentLifecycleEventParams) error {
	_, err := q.db.Exec(ctx, insertAgentLifecycleEventQuery,
		toPgtypeUUID(p.OrgID), toPgtypeUUID(p.AgentID), p.AgentName, p.EventType,
		toPgtypeUUID(p.PerformedBy), p.Detail,
	)
	return err
}
