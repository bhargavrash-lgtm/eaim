-- Multi-Hop Workflows (Thread B), Brief 2 (B-059): execution engine schema.
-- Brief 1 (B-058, migration 000006) built definitions only; this adds
-- what's needed to actually run one -- static per-step parameters, and a
-- durable record of each run.
--
-- workflow_step_params is deliberately a SEPARATE table from
-- workflow_steps, not a new column on it: workflow_steps.input_mapping
-- (already present, still untouched here) is explicitly reserved for a
-- LATER brief's output->input mapping mechanism (deriving a step's
-- params from a prior step's response) -- a fundamentally different
-- concept from an admin-typed static constant, and the two will likely
-- need to coexist per step once mapping ships. Keeping them as separate
-- tables/columns from day one avoids a collision or an awkward dual-
-- purpose column later. 1:1 with workflow_steps (PK = FK), so a step with
-- no configured params simply has no row here (defaults to '{}' when one
-- is inserted, but absence is the more common/expected v1 state, not
-- forced).
--
-- workflow_runs/workflow_run_steps are the audit trail for one execution
-- of a workflow -- separate from workflow_steps' own static definition.
-- workflow_id/workflow_step_id/resolved_tool_id all use ON DELETE SET
-- NULL, mirroring approval_requests' established precedent throughout
-- this schema: a run is a historical record, and deleting the definition
-- (or connector) it ran against must never be blocked by that history.
-- resolved_config_hash/projected_decision/outcome are informational --
-- the real enforcement for each step happens inside the reused,
-- unmodified dispatch() closure (eami-gateway/cmd/gateway/main.go),
-- which independently re-resolves and re-pins every step's connector
-- moments after this row is written; nothing here is ever read back to
-- make an enforcement decision.
CREATE TABLE IF NOT EXISTS workflow_step_params (
    workflow_step_id UUID PRIMARY KEY REFERENCES workflow_steps(id) ON DELETE CASCADE,
    params           JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    workflow_id   UUID REFERENCES workflows(id) ON DELETE SET NULL,
    agent_id      UUID NOT NULL REFERENCES gateway_agents(id),
    status        TEXT NOT NULL DEFAULT 'running'
                  CHECK (status IN ('running','completed','denied','failed')),
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_org ON workflow_runs(org_id, started_at DESC);

CREATE TABLE IF NOT EXISTS workflow_run_steps (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workflow_run_id      UUID NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    step_order           INTEGER NOT NULL,
    workflow_step_id     UUID REFERENCES workflow_steps(id) ON DELETE SET NULL,
    resolved_tool_id     UUID REFERENCES gateway_tools(id) ON DELETE SET NULL,
    resolved_config_hash TEXT,
    -- projected_decision: this package's own independent pre-dispatch
    -- policy.Evaluate() call, recorded purely for audit visibility (e.g.
    -- "this step escalated") since dispatch()'s own return value alone
    -- can't distinguish escalated-then-approved from always-allowed.
    projected_decision   TEXT,
    outcome              TEXT CHECK (outcome IN ('allowed','denied','blocked')),
    result               JSONB,
    error_detail         TEXT,
    started_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at          TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_workflow_run_steps_run ON workflow_run_steps(workflow_run_id, step_order ASC);
