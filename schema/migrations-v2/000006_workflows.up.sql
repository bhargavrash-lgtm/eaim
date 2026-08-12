-- Multi-Hop Workflows (Thread B), Brief 1 (B-058): schema + CRUD
-- foundation only. workflows/workflow_steps model an admin-defined,
-- strictly LINEAR ordered chain of gateway_tools connectors -- no
-- branching/conditional columns exist or are planned for this brief, per
-- the Thread B investigation's own finding that branching is
-- architecturally unbuildable until connector output normalization
-- exists (it doesn't). Nothing executes a workflow yet: no dispatch
-- code, no approval integration, no audit-log linkage references these
-- tables in this brief -- see BACKLOG.md's Thread B entry for the full
-- epic sizing (Briefs 2-7).
--
-- workflow_steps has no org_id column: scoped transitively via
-- workflow_id -> workflows.org_id, matching policy_conditions' identical
-- no-org_id shape (it's scoped via policy_id -> policies.org_id).
--
-- gateway_tool_id uses ON DELETE SET NULL, not the default RESTRICT,
-- deliberately mirroring approval_requests.resolved_tool_id's exact
-- precedent (migration 000005): DeleteTool (eami-api/internal/api/
-- tools.go) deletes a gateway_tools row unconditionally today and is
-- out of scope to modify in this brief, so a plain RESTRICT-by-default
-- FK would turn that already-shipped, untouched delete flow into a raw
-- constraint-violation error the moment any workflow step references
-- the tool being deleted -- a behavior regression this brief must not
-- introduce. SET NULL keeps DeleteTool exactly as it is; the resulting
-- dangling reference is surfaced gracefully by GetWorkflow/ListWorkflows
-- (gateway_tool_id/tool_name come back null, action is preserved) rather
-- than hidden or left to 500 -- see eami-api/internal/api/workflows.go.
--
-- input_mapping JSONB is a genuinely inert placeholder column for a
-- later brief (output->input mapping between hops) -- this brief never
-- reads or writes anything into it beyond leaving it NULL.
CREATE TABLE IF NOT EXISTS workflows (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('active','draft','disabled')),
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_workflows_org ON workflows(org_id);

CREATE TABLE IF NOT EXISTS workflow_steps (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workflow_id     UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    step_order      INTEGER NOT NULL,
    gateway_tool_id UUID REFERENCES gateway_tools(id) ON DELETE SET NULL,
    action          TEXT NOT NULL,
    input_mapping   JSONB,
    UNIQUE (workflow_id, step_order)
);
CREATE INDEX IF NOT EXISTS idx_workflow_steps_workflow ON workflow_steps(workflow_id, step_order ASC);
