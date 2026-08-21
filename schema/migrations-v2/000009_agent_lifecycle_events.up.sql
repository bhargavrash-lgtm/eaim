-- Agent lifecycle audit trail (B-087): a new, small, deliberately separate
-- table for "who did what to which gateway_agents row, and when" --
-- create/suspend/reactivate/delete. Explicitly NOT a write into audit_log:
-- that table is a hash-chained ledger of real dispatch-time policy
-- decisions (eami-gateway-only writer, shaped around tool_name/action/
-- decision), a fundamentally different concern from admin CRUD history.
-- Investigated before building rather than assumed: grepped every existing
-- eami-api admin-action handler (policies.go, tools.go, approvals.go,
-- agents.go) for an audit_log write -- zero exist today. Policy/tool/
-- approval admin actions do not write to audit_log either, so this isn't
-- deviating from an established pattern; it's the first real admin-action
-- history table in this codebase, scoped narrowly to agents per this
-- brief's own stated scope.
--
-- agent_id has no FK to gateway_agents and is NOT CASCADE-deleted with it
-- -- deliberately: the whole point is that a delete event survives the
-- row it describes being gone. event_type is a plain TEXT CHECK (no
-- foreign key registry needed for 4 fixed values); performed_by is
-- nullable since a future system-initiated event (none exist yet) would
-- have no human actor.
CREATE TABLE agent_lifecycle_events (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id     UUID NOT NULL,
    agent_name   TEXT NOT NULL,          -- snapshotted, survives the agent row being deleted
    event_type   TEXT NOT NULL CHECK (event_type IN ('created','suspended','reactivated','deleted')),
    performed_by UUID REFERENCES users(id),
    detail       TEXT,                   -- free text, e.g. the classified 409 reason on a blocked delete
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_agent_lifecycle_events_org_agent ON agent_lifecycle_events(org_id, agent_id, created_at DESC);
